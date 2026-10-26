package updateprompt

import (
	"encoding/json"
	"net/url"
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
	endpoint string // the route, or the path with query parameter names
	consumer string
	// queryParams are the names, escaped and joined by "&", for route reports
	queryParams string
	minute      int64 // Unix seconds, rounded down to the minute
}

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
		method:   call.Method,
		host:     call.Host,
		consumer: call.Consumer,
		minute:   at.Unix() - at.Unix()%60,
	}
	if call.Route != "" {
		key.kind = routeReport
		key.endpoint = call.Route
		key.queryParams = joinQueryParams(call.QueryParams)
	} else {
		key.kind = pathReport
		key.endpoint = call.Path
		if len(call.QueryParams) > 0 {
			// Names only, never values
			key.endpoint += "?" + joinQueryParams(call.QueryParams)
		}
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		client.dropped.Add(1)
		return
	}
	if _, ok := client.counts[key]; !ok && client.pendingReports() >= maxPendingReports {
		client.dropped.Add(1)
		client.logOnce("too many distinct calls waiting to be sent, so new ones are dropped",
			"limit", maxPendingReports)
		return
	}
	client.counts[key]++
	client.counted.Add(1)
}

// pendingReports is how many reports are in memory, counting or queued.
// The caller holds mu.
func (client *Client) pendingReports() int {
	pending := len(client.counts)
	for _, batch := range client.queue {
		pending += batch.reports
	}
	return pending
}

// queueCounts turns the counts so far into batches at the end of the queue
func (client *Client) queueCounts() {
	client.mu.Lock()
	counts := client.counts
	client.counts = map[reportKey]uint64{}
	client.mu.Unlock()
	if len(counts) == 0 {
		return
	}
	batches := makeBatches(counts)
	client.mu.Lock()
	client.queue = append(client.queue, batches...)
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
// maxBatchBytes bytes, one kind of report per batch
func makeBatches(counts map[reportKey]uint64) []*batch {
	var batches []*batch
	open := map[reportKind]*batch{}
	var bodies = map[*batch]*strings.Builder{}
	finish := func(b *batch) {
		bodies[b].WriteString("]}")
		b.body = []byte(bodies[b].String())
		batches = append(batches, b)
		delete(open, b.kind)
	}
	for key, count := range counts {
		encoded, err := json.Marshal(key.report(count))
		if err != nil || len(encoded)+len(`{"reports":[]}`) > maxBatchBytes {
			// Can't happen with strings, and a report this large would be refused
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
		current.calls += count
	}
	for _, kind := range []reportKind{routeReport, pathReport} {
		if current := open[kind]; current != nil {
			finish(current)
		}
	}
	return batches
}

// report returns the report for count calls with this key
func (key reportKey) report(count uint64) report {
	r := report{
		Method:    key.method,
		Host:      key.host,
		Consumer:  key.consumer,
		Timestamp: time.Unix(key.minute, 0).UTC().Format(time.RFC3339),
		Count:     count,
	}
	if key.kind == routeReport {
		r.Route = key.endpoint
		if key.queryParams != "" {
			for _, name := range strings.Split(key.queryParams, "&") {
				// Escaped by joinQueryParams, so this can't fail
				unescaped, _ := url.QueryUnescape(name)
				r.QueryParams = append(r.QueryParams, unescaped)
			}
		}
	} else {
		r.Path = key.endpoint
	}
	return r
}

// joinQueryParams escapes each name and joins them with "&", so names that
// hold "&" or "=" stay apart and can be read back
func joinQueryParams(names []string) string {
	escaped := make([]string, len(names))
	for i, name := range names {
		escaped[i] = url.QueryEscape(name)
	}
	return strings.Join(escaped, "&")
}
