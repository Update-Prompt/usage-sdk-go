package updateprompt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// signalsInterval is how often the deprecation signals are read, give or
// take jitter
const signalsInterval = time.Minute

// maxSignalsBytes caps the signals document read, so a wrong answer can't
// fill memory
const maxSignalsBytes = 1 << 20

// signalEntry is one dated response of the deprecation signals document
type signalEntry struct {
	From      time.Time         `json:"from"`
	AppliesTo string            `json:"appliesTo"`
	Status    int               `json:"status"`
	Code      string            `json:"code"`
	Headers   map[string]string `json:"headers"`
}

// Signals are the deprecation signals for one call: headers to add to the
// response and, once the API is no longer available, a refusal instead of
// the response. Get them with [Client.Signals].
type Signals struct {
	entry *signalEntry
}

// Signals returns the deprecation signals for r, a call to your API that
// your router matched to a route. Calls it matched to no route get none.
// Until the signals have been read once, there are none. The middleware and
// the adapters in this repository call it; you need it only for a framework
// without one.
//
// Only signals for every consumer are applied. Signals for new consumers
// need each consumer's first call, which the Usage API doesn't list yet.
func (client *Client) Signals(r *http.Request) Signals {
	entries := client.signals.Load()
	if entries == nil {
		return Signals{}
	}
	now := client.now()
	var picked *signalEntry
	for i := range *entries {
		entry := &(*entries)[i]
		if entry.AppliesTo == "ALL" && !entry.From.After(now) {
			picked = entry
		}
	}
	return Signals{entry: picked}
}

// Apply adds the signals' headers to w. When the signals refuse the call, it
// also writes the refusal, an application/problem+json body, and returns
// true: then don't call your handler. Call it before your handler writes
// anything.
func (signals Signals) Apply(w http.ResponseWriter) (refused bool) {
	entry := signals.entry
	if entry == nil {
		return false
	}
	header := w.Header()
	for name, value := range entry.Headers {
		header.Set(name, value)
	}
	if entry.Status == 0 {
		return false
	}
	body, _ := json.Marshal(refusal(entry.Status, entry.Code))
	header.Set("Content-Type", "application/problem+json")
	header.Set("Cache-Control", "no-store")
	header.Del("Content-Length")
	w.WriteHeader(entry.Status)
	_, _ = w.Write(body)
	return true
}

// problem is a refusal's body (RFC 9457)
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// refusal returns the body of a refusal with status and code, as the Usage
// API's documentation gives it
func refusal(status int, code string) problem {
	body := problem{Title: http.StatusText(status), Status: status, Code: code, Type: "about:blank"}
	if code != "" {
		body.Type = "https://docs.updateprompt.com/errors/" + strings.ReplaceAll(strings.ToLower(code), "_", "-")
	}
	if code == "API_SUNSET" {
		body.Title = "API sunset"
		body.Detail = "This API is no longer available. The Link header points to its migration guide."
	}
	return body
}

// runSignals reads the deprecation signals now and then every interval until
// Close
func (client *Client) runSignals() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-client.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	etag := ""
	for {
		var retryAfter time.Duration
		etag, retryAfter = client.readSignals(ctx, etag)
		timer := time.NewTimer(max(jitter(client.signalsInterval), retryAfter))
		select {
		case <-client.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// readSignals reads the deprecation signals document, unless it hasn't
// changed since etag. It returns the ETag to send next time and how long the
// API asked to wait. When the document can't be read, the last one stays.
func (client *Client) readSignals(ctx context.Context, etag string) (string, time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/v1/deprecation-signals", nil)
	if err != nil {
		client.logOnce("can't read deprecation signals: invalid base URL", "error", err)
		return etag, 0
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", client.userAgent)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			// The error names the URL, never the key
			client.logOnce("can't read deprecation signals", "error", err)
		}
		return etag, 0
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var document struct {
			Responses []signalEntry `json:"responses"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, maxSignalsBytes)).Decode(&document); err != nil {
			client.logOnce("can't read deprecation signals: invalid document", "error", err)
			return etag, 0
		}
		entries := validEntries(document.Responses)
		client.signals.Store(&entries)
		return response.Header.Get("ETag"), 0
	case http.StatusNotModified:
		return etag, 0
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		client.logOnce(fmt.Sprintf("can't read deprecation signals: the Usage API answered %d", response.StatusCode))
		return etag, parseRetryAfter(response.Header.Get("Retry-After"), client.now())
	}
}

// validEntries leaves out headers that can't be sent and refusals with a
// status that isn't an error
func validEntries(entries []signalEntry) []signalEntry {
	valid := make([]signalEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Status != 0 && (entry.Status < 400 || entry.Status > 599) {
			continue
		}
		headers := make(map[string]string, len(entry.Headers))
		for name, value := range entry.Headers {
			if validHeaderName(name) && validHeaderValue(value) {
				headers[name] = value
			}
		}
		entry.Headers = headers
		valid = append(valid, entry)
	}
	return valid
}

// validHeaderName tells whether name is an HTTP token
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range []byte(name) {
		if c <= ' ' || c >= 0x7f || strings.IndexByte("\"(),/:;<=>?@[\\]{}", c) >= 0 {
			return false
		}
	}
	return true
}

// validHeaderValue tells whether value has no control characters
func validHeaderValue(value string) bool {
	for _, c := range []byte(value) {
		if (c < ' ' && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}
