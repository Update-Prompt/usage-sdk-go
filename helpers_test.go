package updateprompt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// testAPIKey is an obviously fake key
const testAPIKey = "up_usage_test"

// fakeGateway records the batches it gets and answers with the statuses it is
// given, then 202
type fakeGateway struct {
	*httptest.Server
	mu       sync.Mutex
	received []receivedBatch
	answers  []fakeAnswer
}

type fakeAnswer struct {
	status     int
	retryAfter string
	// dropConnection closes the connection without an answer
	dropConnection bool
	// delay holds the answer back, or until the client gives up
	delay time.Duration
}

type receivedBatch struct {
	path    string
	header  http.Header
	reports int
	bytes   int
	body    struct {
		Reports []report `json:"reports"`
	}
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	gateway := &fakeGateway{}
	gateway.Server = httptest.NewServer(http.HandlerFunc(gateway.serve))
	t.Cleanup(gateway.Close)
	return gateway
}

func (gateway *fakeGateway) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	gateway.mu.Lock()
	answer := fakeAnswer{status: http.StatusAccepted}
	if len(gateway.answers) > 0 {
		answer, gateway.answers = gateway.answers[0], gateway.answers[1:]
	}
	batch := receivedBatch{path: r.URL.Path, header: r.Header.Clone(), bytes: len(body)}
	_ = json.Unmarshal(body, &batch.body)
	batch.reports = len(batch.body.Reports)
	gateway.received = append(gateway.received, batch)
	gateway.mu.Unlock()

	if answer.delay > 0 {
		select {
		case <-time.After(answer.delay):
		case <-r.Context().Done():
			return
		}
	}
	if answer.dropConnection {
		connection, _, _ := http.NewResponseController(w).Hijack()
		_ = connection.Close()
		return
	}
	if answer.retryAfter != "" {
		w.Header().Set("Retry-After", answer.retryAfter)
	}
	w.WriteHeader(answer.status)
}

// answer queues statuses for the next requests
func (gateway *fakeGateway) answer(answers ...fakeAnswer) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.answers = append(gateway.answers, answers...)
}

func (gateway *fakeGateway) batches(path string) []receivedBatch {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	var batches []receivedBatch
	for _, batch := range gateway.received {
		if batch.path == path {
			batches = append(batches, batch)
		}
	}
	return batches
}

func (gateway *fakeGateway) allBatches() []receivedBatch {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return append([]receivedBatch(nil), gateway.received...)
}

func (gateway *fakeGateway) requestCount() int {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return len(gateway.received)
}

// reports returns the reports of the batches the gateway answered with 2xx,
// once each, in the order received
func (gateway *fakeGateway) reports(path string) []report {
	var reports []report
	seen := map[string]bool{}
	for _, batch := range gateway.batches(path) {
		key := batch.header.Get("Idempotency-Key")
		if seen[key] {
			continue
		}
		seen[key] = true
		reports = append(reports, batch.body.Reports...)
	}
	return reports
}

// newTestClient returns a started client for the gateway, at testTime, with
// short backoffs
func newTestClient(gateway *fakeGateway) *Client {
	client := newClient(Options{APIKey: testAPIKey, BaseURL: gateway.URL})
	client.now = func() time.Time { return testTime }
	client.minBackoff = 5 * time.Millisecond
	client.maxBackoff = 20 * time.Millisecond
	client.interval = time.Hour
	go client.run()
	return client
}

func flush(t *testing.T, client *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Flush(ctx); err != nil {
		t.Fatalf("Expected Flush to succeed, got %v", err)
	}
}

func expectReports(t *testing.T, got, expected []report) {
	t.Helper()
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("Expected reports %+v, got %+v", expected, got)
	}
}

func expectStats(t *testing.T, client *Client, expected Stats) {
	t.Helper()
	if got := client.Stats(); got != expected {
		t.Errorf("Expected stats %+v, got %+v", expected, got)
	}
}

func consumerID(i int) string {
	return fmt.Sprintf("consumer-%d", i)
}

// waitFor waits up to 5 s for done to be true
func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("Timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
