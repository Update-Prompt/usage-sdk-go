package updateprompt

import (
	"context"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 10, 26, 12, 30, 15, 0, time.UTC)

// Given calls with the same method, host, route, consumer, query names and minute
// When they are flushed
// Then they arrive as one report with their count, at the minute's start
func TestRecordAddsUpTheSameCall(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	for range 3 {
		client.Record(Call{Method: "GET", Host: "api.example.com", Route: "/orders/{id}",
			Consumer: "acme", QueryParams: []string{"expand"}, Time: testTime})
	}
	flush(t, client)

	reports := gateway.reports("/v1/usage")
	expectReports(t, reports, []report{{Method: "GET", Host: "api.example.com", Route: "/orders/{id}",
		Consumer: "acme", QueryParams: []string{"expand"}, Timestamp: "2026-10-26T12:30:00Z", Count: 3}})
	expectStats(t, client, Stats{Counted: 3, Sent: 3})
}

// Given calls that differ in one field each
// When they are flushed
// Then each becomes its own report
func TestRecordKeepsDifferentCallsApart(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	base := Call{Method: "GET", Host: "api.example.com", Route: "/orders", Time: testTime}
	calls := []Call{base}
	for _, change := range []func(*Call){
		func(c *Call) { c.Method = "POST" },
		func(c *Call) { c.Host = "eu.api.example.com" },
		func(c *Call) { c.Route = "/orders/{id}" },
		func(c *Call) { c.Consumer = "acme" },
		func(c *Call) { c.Time = testTime.Add(time.Minute) },
	} {
		call := base
		change(&call)
		calls = append(calls, call)
	}
	for _, call := range calls {
		client.Record(call)
	}
	flush(t, client)

	if got := len(gateway.reports("/v1/usage")); got != len(calls) {
		t.Fatalf("Expected %d reports, got %d", len(calls), got)
	}
}

// Given a call with a raw path and query parameter names, including one with "&"
// When it is flushed
// Then it is sent to /v1/usage/paths with the names escaped and no values
func TestRecordSendsPathsToThePathsEndpoint(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	client.Record(Call{Method: "GET", Host: "api.example.com", Path: "/orders/8812",
		QueryParams: []string{"expand", "a&b"}, Time: testTime})
	flush(t, client)

	expectReports(t, gateway.reports("/v1/usage/paths"), []report{{Method: "GET", Host: "api.example.com",
		Path: "/orders/8812?expand&a%26b", Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
	if got := gateway.reports("/v1/usage"); len(got) != 0 {
		t.Errorf("Expected no route reports, got %v", got)
	}
}

// Given calls to one endpoint in one minute with different query parameter names,
// more than 50 in all, with repeats, empty and overly long names
// When they are flushed
// Then they make one report with each name once, in order seen, at most 50
func TestRecordCollectsQueryParamNames(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	call := Call{Method: "GET", Host: "h", Route: "/r", Time: testTime}
	call.QueryParams = []string{"page", "", strings.Repeat("x", maxQueryParamBytes+1), "page", "sort"}
	client.Record(call)
	for i := range 60 {
		call.QueryParams = []string{consumerID(i)}
		client.Record(call)
	}
	flush(t, client)

	reports := gateway.reports("/v1/usage")
	if len(reports) != 1 || reports[0].Count != 61 {
		t.Fatalf("Expected one report of 61 calls, got %+v", reports)
	}
	names := reports[0].QueryParams
	if len(names) != maxQueryParams || names[0] != "page" || names[1] != "sort" || names[2] != consumerID(0) {
		t.Errorf("Expected page, sort, then names up to %d in all, got %v", maxQueryParams, names)
	}
}

// Given a call whose path is longer than 8 KB
// When it is recorded
// Then it is dropped, not kept in memory
func TestRecordDropsHugeCalls(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	client.Record(Call{Method: "GET", Host: "h", Path: "/" + strings.Repeat("x", maxKeyBytes), Time: testTime})
	flush(t, client)

	expectStats(t, client, Stats{Dropped: 1})
	if got := gateway.requestCount(); got != 0 {
		t.Errorf("Expected no requests, got %d", got)
	}
}

// Given a route call with query parameter names that hold "&" and "="
// When it is flushed
// Then the names arrive exactly as given
func TestRecordKeepsQueryParamNamesIntact(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	names := []string{"a&b", "c=d", "filter[status]"}
	client.Record(Call{Method: "GET", Host: "h", Route: "/r", QueryParams: names, Time: testTime})
	flush(t, client)

	reports := gateway.reports("/v1/usage")
	if len(reports) != 1 || strings.Join(reports[0].QueryParams, "|") != strings.Join(names, "|") {
		t.Errorf("Expected query params %v, got %v", names, reports)
	}
}

// Given a call with neither route nor path, and a call without a time
// When they are recorded
// Then the first is ignored and the second counts at the current minute
func TestRecordIgnoresCallsWithoutEndpoint(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	client.Record(Call{Method: "GET", Host: "h"})
	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
	flush(t, client)

	expectReports(t, gateway.reports("/v1/usage"), []report{{Method: "GET", Host: "h", Route: "/r",
		Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
	expectStats(t, client, Stats{Counted: 1, Sent: 1})
}

// Given as many distinct calls in memory as the limit allows
// When another distinct call and a repeat of a known call are recorded
// Then the new one is dropped and the known one still counts
func TestRecordDropsNewCallsWhenFull(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	for i := range maxPendingReports {
		client.Record(Call{Method: "GET", Host: "h", Route: "/r", Consumer: consumerID(i), Time: testTime})
	}
	client.Record(Call{Method: "GET", Host: "h", Route: "/new", Time: testTime})
	client.Record(Call{Method: "GET", Host: "h", Route: "/r", Consumer: consumerID(0), Time: testTime})

	expectStats(t, client, Stats{Counted: maxPendingReports + 1, Dropped: 1})
}

// Given more than 1,000 distinct calls, and calls with very long routes
// When they are flushed
// Then every batch holds at most 1,000 reports and is under 1 MB
func TestRecordSplitsBatches(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	for i := range 2500 {
		client.Record(Call{Method: "GET", Host: "h", Route: "/r", Consumer: consumerID(i), Time: testTime})
	}
	long := "/" + strings.Repeat("x", 2000)
	for i := range 900 {
		client.Record(Call{Method: "GET", Host: "h", Route: long, Consumer: consumerID(i), Time: testTime})
	}
	flush(t, client)

	batches := gateway.batches("/v1/usage")
	total := 0
	for _, b := range batches {
		if b.reports > maxBatchReports || b.bytes >= 1<<20 {
			t.Errorf("Expected at most %d reports under 1 MB, got %d reports, %d bytes",
				maxBatchReports, b.reports, b.bytes)
		}
		total += b.reports
	}
	if total != 3400 || len(batches) < 4 {
		t.Errorf("Expected 3,400 reports in at least 4 batches, got %d in %d", total, len(batches))
	}
}

// Given a closed client
// When a call is recorded
// Then it is dropped
func TestRecordAfterCloseIsDropped(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Expected Close to succeed, got %v", err)
	}

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})

	expectStats(t, client, Stats{Dropped: 1})
}

// Given a client without an API key
// When a call is recorded and flushed
// Then nothing is counted or sent
func TestRecordWithoutAPIKeyDoesNothing(t *testing.T) {
	t.Setenv("UPDATE_PROMPT_API_KEY", "")
	gateway := newFakeGateway(t)
	client := New(Options{BaseURL: gateway.URL})

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
	flush(t, client)
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Expected Close to succeed, got %v", err)
	}

	expectStats(t, client, Stats{})
	if got := gateway.requestCount(); got != 0 {
		t.Errorf("Expected no requests, got %d", got)
	}
}
