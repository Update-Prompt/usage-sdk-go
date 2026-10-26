// Package usagetest is a fake Usage API for the router adapters' tests
package usagetest

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
)

// Report is one report the fake Usage API got, without its timestamp
type Report struct {
	Method      string   `json:"method"`
	Host        string   `json:"host"`
	Route       string   `json:"route,omitempty"`
	Path        string   `json:"path,omitempty"`
	Consumer    string   `json:"consumer,omitempty"`
	QueryParams []string `json:"queryParams,omitempty"`
	Count       uint64   `json:"count"`
}

// API is a fake Usage API that accepts every batch
type API struct {
	server  *httptest.Server
	mu      sync.Mutex
	reports []Report
}

// NewClient starts a fake Usage API and returns a client that sends to it.
// Both stop when the test ends.
func NewClient(t testing.TB) (*updateprompt.Client, *API) {
	t.Helper()
	api := &API{}
	api.server = httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.server.Close)
	client := updateprompt.New(updateprompt.Options{APIKey: "up_usage_test", BaseURL: api.server.URL})
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	return client, api
}

// Reports sends what client counted and returns every report the API got,
// sorted by route or path, then method
func (api *API) Reports(t testing.TB, client *updateprompt.Client) []Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Flush(ctx); err != nil {
		t.Fatalf("Expected Flush to succeed, got %v", err)
	}
	api.mu.Lock()
	reports := append([]Report(nil), api.reports...)
	api.mu.Unlock()
	slices.SortFunc(reports, func(a, b Report) int {
		return cmp.Or(cmp.Compare(a.Route+a.Path, b.Route+b.Path), cmp.Compare(a.Method, b.Method))
	})
	return reports
}

func (api *API) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reports []Report `json:"reports"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	api.mu.Lock()
	api.reports = append(api.reports, body.Reports...)
	api.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}
