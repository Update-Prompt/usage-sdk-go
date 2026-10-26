package updateprompt

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Call is one call to an endpoint of your API, for [Client.Record]
type Call struct {
	// Method is the HTTP method, such as GET
	Method string
	// Host is the host that was called, such as api.example.com
	Host string
	// Route is the route template, such as /orders/{id}. Set Route or Path.
	Route string
	// Path is the raw path, such as /orders/8812, used when there is no
	// Route. Update Prompt turns it into a route. Leave the query string out:
	// put its parameter names in QueryParams.
	Path string
	// Consumer is the id of the consumer that called. Empty means no consumer
	// id.
	Consumer string
	// QueryParams are the names of the query parameters sent, never their
	// values
	QueryParams []string
	// Time is when the call was made. Zero means now.
	Time time.Time
}

// reportKind is the Usage API endpoint a report is sent to
type reportKind int

const (
	routeReport reportKind = iota
	pathReport
)

// reportKey is what calls are counted by: calls with the same key in the same
// minute become one report
type reportKey struct {
	kind     reportKind
	method   string
	host     string
	endpoint string // the route, or the raw path
	consumer string
	minute   int64 // Unix seconds, rounded down to the minute
}

// tally is what a report adds up: its calls and the query parameter names they
// sent. Names are a set, so they never split a report.
type tally struct {
	calls       uint64
	queryParams []string
}

// Limits that keep memory bounded whatever the requests hold
const (
	// maxKeyBytes caps the method, host, route or path and consumer of a call
	// together. The API refuses far shorter values, so only junk is dropped.
	maxKeyBytes = 8 << 10
	// maxQueryParams is how many names one report keeps, as many as the API does
	maxQueryParams = 50
	// maxQueryParamBytes skips longer names: the API keeps names of up to 128
	// characters, which is at most 512 bytes
	maxQueryParamBytes = 512
)

// Record counts one call. It never blocks on the network. A call without a
// Route or Path is ignored.
func (client *Client) Record(call Call) {
	if client.apiKey == "" || (call.Route == "" && call.Path == "") {
		return
	}
	at := call.Time
	if at.IsZero() {
		at = client.now()
	}
	key := reportKey{
		kind:     routeReport,
		method:   call.Method,
		host:     call.Host,
		endpoint: call.Route,
		consumer: call.Consumer,
		minute:   at.Unix() - at.Unix()%60,
	}
	if call.Route == "" {
		key.kind = pathReport
		key.endpoint = call.Path
	}
	if len(key.method)+len(key.host)+len(key.endpoint)+len(key.consumer) > maxKeyBytes {
		client.dropped.Add(1)
		client.logOnce("a call's host, route, path or consumer is too long to report, so it is dropped",
			"limit_bytes", maxKeyBytes)
		return
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		client.dropped.Add(1)
		return
	}
	current, ok := client.counts[key]
	if !ok {
		if len(client.counts)+client.queuedReports >= maxPendingReports {
			client.dropped.Add(1)
			client.logOnce("too many distinct calls waiting to be sent, so new ones are dropped",
				"limit", maxPendingReports)
			return
		}
		current = &tally{}
		client.counts[key] = current
	}
	current.calls++
	current.addQueryParams(call.QueryParams)
	client.counted.Add(1)
}

// addQueryParams adds names the tally doesn't have yet, up to maxQueryParams
func (t *tally) addQueryParams(names []string) {
	for _, name := range names {
		if len(t.queryParams) == maxQueryParams {
			return
		}
		if name != "" && len(name) <= maxQueryParamBytes && !slices.Contains(t.queryParams, name) {
			t.queryParams = append(t.queryParams, name)
		}
	}
}

// queueCounts turns the counts so far into batches at the end of the queue
func (client *Client) queueCounts() {
	client.mu.Lock()
	counts := client.counts
	client.counts = map[reportKey]*tally{}
	// Still in memory while they are encoded, so they count against the limit
	client.queuedReports += len(counts)
	client.mu.Unlock()
	if len(counts) == 0 {
		return
	}
	batches, skipped, skippedCalls := makeBatches(counts)
	if skippedCalls > 0 {
		client.dropped.Add(skippedCalls)
		client.logOnce("a report is too large to send, so its calls are dropped")
	}
	client.mu.Lock()
	client.queue = append(client.queue, batches...)
	client.queuedReports -= skipped
	client.mu.Unlock()
}

// Limits on one batch, from the Usage API
const (
	maxBatchReports = 1000
	// maxBatchBytes stays under the API's 1 MB with room to spare
	maxBatchBytes = 1<<20 - 4096
)

// batch is one request's worth of reports, ready to send
type batch struct {
	kind    reportKind
	body    []byte
	reports int
	calls   uint64
	// idempotencyKey is set on the first try and kept for every retry
	idempotencyKey string
	// tries counts failed tries, for the backoff
	tries int
}

// report is one report as the Usage API takes it
type report struct {
	Method      string   `json:"method"`
	Host        string   `json:"host"`
	Route       string   `json:"route,omitempty"`
	Path        string   `json:"path,omitempty"`
	Consumer    string   `json:"consumer,omitempty"`
	QueryParams []string `json:"queryParams,omitempty"`
	Timestamp   string   `json:"timestamp"`
	Count       uint64   `json:"count"`
}

// makeBatches turns counts into batches of at most maxBatchReports reports and
// maxBatchBytes bytes, one kind of report per batch. It also returns the
// reports, and their calls, too large for any batch.
func makeBatches(counts map[reportKey]*tally) (batches []*batch, skipped int, skippedCalls uint64) {
	open := map[reportKind]*batch{}
	var bodies = map[*batch]*strings.Builder{}
	finish := func(b *batch) {
		bodies[b].WriteString("]}")
		b.body = []byte(bodies[b].String())
		batches = append(batches, b)
		delete(open, b.kind)
	}
	for key, t := range counts {
		encoded, err := json.Marshal(key.report(t))
		if err != nil || len(encoded)+len(`{"reports":[]}`) > maxBatchBytes {
			// Only possible with a path of nearly 1 MB, which the API would refuse
			skipped++
			skippedCalls += t.calls
			continue
		}
		current := open[key.kind]
		if current != nil && (current.reports == maxBatchReports ||
			bodies[current].Len()+1+len(encoded)+len("]}") > maxBatchBytes) {
			finish(current)
			current = nil
		}
		if current == nil {
			current = &batch{kind: key.kind}
			bodies[current] = &strings.Builder{}
			bodies[current].WriteString(`{"reports":[`)
			open[key.kind] = current
		} else {
			bodies[current].WriteByte(',')
		}
		bodies[current].Write(encoded)
		current.reports++
		current.calls += t.calls
	}
	for _, kind := range []reportKind{routeReport, pathReport} {
		if current := open[kind]; current != nil {
			finish(current)
		}
	}
	return batches, skipped, skippedCalls
}

// report returns the report for the tally's calls with this key
func (key reportKey) report(t *tally) report {
	r := report{
		Method:    key.method,
		Host:      key.host,
		Consumer:  key.consumer,
		Timestamp: time.Unix(key.minute, 0).UTC().Format(time.RFC3339),
		Count:     t.calls,
	}
	if key.kind == routeReport {
		r.Route = key.endpoint
		r.QueryParams = t.queryParams
		return r
	}
	// The paths endpoint reads names from the query string: names only,
	// escaped, never values
	r.Path = key.endpoint
	for i, name := range t.queryParams {
		if i == 0 {
			r.Path += "?"
		} else {
			r.Path += "&"
		}
		r.Path += url.QueryEscape(name)
	}
	return r
}
