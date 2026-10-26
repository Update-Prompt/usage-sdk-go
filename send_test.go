package updateprompt

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Given a recorded call
// When it is flushed
// Then the request carries the key, JSON, an Idempotency-Key and the User-Agent
func TestSendSetsHeaders(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
	flush(t, client)

	batches := gateway.allBatches()
	if len(batches) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(batches))
	}
	header := batches[0].header
	if got := header.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("Expected the API key as bearer token, got %q", got)
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Expected JSON, got %q", got)
	}
	if got := header.Get("Idempotency-Key"); !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(got) {
		t.Errorf("Expected a 22 character base64url Idempotency-Key, got %q", got)
	}
	if got := header.Get("User-Agent"); !strings.HasPrefix(got, "usage-sdk-go/"+Version+" (go") {
		t.Errorf("Expected the SDK's User-Agent, got %q", got)
	}
}

// Given a gateway that answers 429, 503, 502 and drops a connection before 202
// When a call is flushed
// Then the batch is retried with the same Idempotency-Key until it is accepted
func TestSendRetriesWithTheSameKey(t *testing.T) {
	gateway := newFakeGateway(t)
	gateway.answer(
		fakeAnswer{status: http.StatusTooManyRequests, retryAfter: "0"},
		fakeAnswer{status: http.StatusServiceUnavailable},
		fakeAnswer{status: http.StatusBadGateway},
		fakeAnswer{dropConnection: true},
	)
	client := newTestClient(gateway)

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
	flush(t, client)

	batches := gateway.allBatches()
	if len(batches) != 5 {
		t.Fatalf("Expected 5 tries, got %d", len(batches))
	}
	for _, batch := range batches[1:] {
		if batch.header.Get("Idempotency-Key") != batches[0].header.Get("Idempotency-Key") {
			t.Errorf("Expected the same Idempotency-Key on every try")
		}
	}
	expectStats(t, client, Stats{Counted: 1, Sent: 1})
}

// Given a gateway that answers 207 for one batch and 400, 401 and 413 for others
// When they are flushed
// Then the 207 counts as sent, the others are dropped, and none is retried
func TestSendDropsRefusedBatches(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusRequestEntityTooLarge} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			gateway := newFakeGateway(t)
			gateway.answer(fakeAnswer{status: http.StatusMultiStatus}, fakeAnswer{status: status})
			client := newTestClient(gateway)

			client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
			flush(t, client)
			client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
			client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
			flush(t, client)

			if got := gateway.requestCount(); got != 2 {
				t.Errorf("Expected 2 requests, got %d", got)
			}
			expectStats(t, client, Stats{Counted: 3, Sent: 1, Dropped: 2})
		})
	}
}

// Given a gateway that keeps answering 503
// When Close runs out of time
// Then it returns ErrNotSent and the unsent calls are dropped
func TestCloseDropsWhatItCannotSend(t *testing.T) {
	gateway := newFakeGateway(t)
	unavailable := make([]fakeAnswer, 1000)
	for i := range unavailable {
		unavailable[i] = fakeAnswer{status: http.StatusServiceUnavailable}
	}
	gateway.answer(unavailable...)
	client := newTestClient(gateway)

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})
	client.Record(Call{Method: "GET", Host: "h", Route: "/other"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := client.Close(ctx)

	if !errors.Is(err, ErrNotSent) {
		t.Errorf("Expected ErrNotSent, got %v", err)
	}
	expectStats(t, client, Stats{Counted: 2, Dropped: 2})
}

// Given a client that sends every 20 ms
// When a call is recorded and nobody flushes
// Then it is sent in the background
func TestRunSendsInTheBackground(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newClient(Options{APIKey: testAPIKey, BaseURL: gateway.URL})
	client.interval = 20 * time.Millisecond
	go client.run()
	defer client.Close(context.Background())

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})

	deadline := time.Now().Add(5 * time.Second)
	for client.Stats().Sent == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	expectStats(t, client, Stats{Counted: 1, Sent: 1})
}

// Given a client sending in the background to a gateway that answers 503 once
// When a call is recorded
// Then the background loop retries it after the backoff
func TestRunRetriesInTheBackground(t *testing.T) {
	gateway := newFakeGateway(t)
	gateway.answer(fakeAnswer{status: http.StatusServiceUnavailable})
	client := newClient(Options{APIKey: testAPIKey, BaseURL: gateway.URL})
	client.interval = 20 * time.Millisecond
	client.minBackoff = 10 * time.Millisecond
	go client.run()
	defer client.Close(context.Background())

	client.Record(Call{Method: "GET", Host: "h", Route: "/r"})

	deadline := time.Now().Add(5 * time.Second)
	for client.Stats().Sent == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	expectStats(t, client, Stats{Counted: 1, Sent: 1})
	if got := gateway.requestCount(); got != 2 {
		t.Errorf("Expected 2 requests, got %d", got)
	}
}

// Given Retry-After values in seconds, as a date, and invalid
// When they are parsed
// Then seconds and dates give the wait, anything else gives zero
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"7":                             7 * time.Second,
		"-3":                            0,
		"soon":                          0,
		"Mon, 26 Oct 2026 12:00:30 GMT": 30 * time.Second,
		"Mon, 26 Oct 2026 11:59:00 GMT": 0,
	}
	for value, expected := range cases {
		if got := parseRetryAfter(value, now); got != expected {
			t.Errorf("Expected %q to wait %v, got %v", value, expected, got)
		}
	}
}

// Given a batch that failed several times, with and without Retry-After
// When the next delay is computed
// Then it doubles from the minimum up to the maximum, within 20%, and never undercuts Retry-After
func TestRetryDelay(t *testing.T) {
	client := newClient(Options{APIKey: testAPIKey})
	for tries, expected := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second,
		4: 8 * time.Second, 20: time.Minute} {
		got := client.retryDelay(tries, 0)
		if got < expected*8/10 || got > expected*12/10 {
			t.Errorf("Expected about %v after %d tries, got %v", expected, tries, got)
		}
	}
	if got := client.retryDelay(1, 5*time.Second); got < 5*time.Second || got > 6*time.Second {
		t.Errorf("Expected 5 to 6 s for Retry-After 5, got %v", got)
	}
}
