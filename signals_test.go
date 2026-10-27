package updateprompt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Given the Usage API's shared test cases: a signals document, a time and a consumer
// When the signals are applied to a response at that time
// Then the headers, and the refusal and its code, are the expected ones. Cases
// that refuse new consumers are skipped: the SDK applies only entries for all
// consumers.
func TestSignalsFollowTheSharedTestCases(t *testing.T) {
	var file struct {
		Cases []struct {
			Name     string `json:"name"`
			Document struct {
				Responses []signalEntry `json:"responses"`
			} `json:"document"`
			Now      time.Time `json:"now"`
			Expected struct {
				Refuse  *int              `json:"refuse"`
				Code    *string           `json:"code"`
				Headers map[string]string `json:"headers"`
			} `json:"expected"`
		} `json:"cases"`
	}
	data, err := os.ReadFile("testdata/deprecation-signals-test-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}

	for _, test := range file.Cases {
		t.Run(test.Name, func(t *testing.T) {
			if test.Expected.Code != nil && *test.Expected.Code == "NEW_CONSUMERS_BLOCKED" {
				t.Skip("new consumers need each consumer's first call, which the Usage API doesn't list yet")
			}
			client := newClient(Options{APIKey: testAPIKey})
			client.now = func() time.Time { return test.Now }
			setSignals(client, test.Document.Responses)

			recorder := httptest.NewRecorder()
			refused := client.Signals(newRequest("GET", "http://api.example.com/orders/1")).Apply(recorder)

			for name, value := range test.Expected.Headers {
				if got := recorder.Header().Get(name); got != value {
					t.Errorf("Expected header %s %q, got %q", name, value, got)
				}
			}
			if test.Expected.Refuse == nil {
				if refused || recorder.Code != http.StatusOK {
					t.Errorf("Expected no refusal, got %d", recorder.Code)
				}
				return
			}
			var body problem
			_ = json.Unmarshal(recorder.Body.Bytes(), &body)
			if !refused || recorder.Code != *test.Expected.Refuse || body.Code != *test.Expected.Code || body.Status != *test.Expected.Refuse {
				t.Errorf("Expected refusal %d %s, got %v %d %+v", *test.Expected.Refuse, *test.Expected.Code, refused, recorder.Code, body)
			}
		})
	}
}

// Given a refusal with API_SUNSET
// When it is applied
// Then the body is the documented problem, with its content type and no caching
func TestSignalsRefuseWithProblemDetails(t *testing.T) {
	client := newClient(Options{APIKey: testAPIKey})
	client.now = func() time.Time { return testTime }
	setSignals(client, []signalEntry{{From: testTime, AppliesTo: "ALL", Status: 410, Code: "API_SUNSET",
		Headers: map[string]string{"Sunset": "Fri, 15 Jan 2027 00:00:00 GMT"}}})

	recorder := httptest.NewRecorder()
	client.Signals(newRequest("GET", "http://api.example.com/orders/1")).Apply(recorder)

	expected := `{"type":"https://docs.updateprompt.com/errors/api-sunset","title":"API sunset","status":410,` +
		`"code":"API_SUNSET","detail":"This API is no longer available. The Link header points to its migration guide."}`
	if recorder.Body.String() != expected {
		t.Errorf("Expected body %s, got %s", expected, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/problem+json" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Expected problem+json and no-store, got %v", recorder.Header())
	}
}

// Given a client whose signals were never read
// When signals are applied
// Then nothing is added or refused
func TestSignalsAreNoneBeforeTheFirstRead(t *testing.T) {
	client := newClient(Options{APIKey: testAPIKey})

	recorder := httptest.NewRecorder()
	refused := client.Signals(newRequest("GET", "http://api.example.com/orders/1")).Apply(recorder)

	if refused || len(recorder.Header()) != 0 {
		t.Errorf("Expected nothing applied, got refused %v, headers %v", refused, recorder.Header())
	}
}

// Given a Usage API that answers 200 with an ETag, then 304, then 500
// When the signals are read three times
// Then the key and the ETag are sent, and the first document stays after the 304 and the 500
func TestReadSignalsKeepsTheLastDocument(t *testing.T) {
	api := newFakeSignalsAPI(t, `{"responses": [{"from": "2026-01-01T00:00:00Z", "appliesTo": "ALL", "headers": {"Deprecation": "@1767225600"}}]}`)
	client := newClient(Options{APIKey: testAPIKey, BaseURL: api.URL})
	client.now = func() time.Time { return testTime }

	etag, _ := client.readSignals(t.Context(), "")
	api.answer(http.StatusNotModified)
	etag, _ = client.readSignals(t.Context(), etag)
	api.answer(http.StatusInternalServerError)
	etag, _ = client.readSignals(t.Context(), etag)

	for i, expectedETag := range []string{"", `"1-abc"`, `"1-abc"`} {
		request := api.requests[i]
		if request.Get("Authorization") != "Bearer "+testAPIKey || request.Get("If-None-Match") != expectedETag {
			t.Errorf("Request %d: expected the key and If-None-Match %q, got %v", i+1, expectedETag, request)
		}
	}
	if etag != `"1-abc"` {
		t.Errorf("Expected the ETag kept, got %q", etag)
	}
	recorder := httptest.NewRecorder()
	client.Signals(newRequest("GET", "http://api.example.com/orders/1")).Apply(recorder)
	if recorder.Header().Get("Deprecation") != "@1767225600" {
		t.Errorf("Expected the first document's headers, got %v", recorder.Header())
	}
}

// Given a document with entries for new consumers, an entry not yet due, and
// headers that can't be sent
// When signals are applied
// Then only the due entry for all consumers is used, without the bad headers
func TestSignalsUseTheLastDueEntryForAll(t *testing.T) {
	api := newFakeSignalsAPI(t, `{"responses": [
		{"from": "2026-01-01T00:00:00Z", "appliesTo": "ALL", "headers": {"Deprecation": "@1767225600", "Bad Name": "x", "Link": "a\r\nSet-Cookie: x"}},
		{"from": "2026-01-01T00:00:00Z", "appliesTo": "NEW", "status": 403, "code": "NEW_CONSUMERS_BLOCKED", "headers": {"Deprecation": "@1767225600"}},
		{"from": "2027-01-01T00:00:00Z", "appliesTo": "ALL", "status": 410, "code": "API_SUNSET", "headers": {"Deprecation": "@1767225600"}}
	]}`)
	client := newClient(Options{APIKey: testAPIKey, BaseURL: api.URL})
	client.now = func() time.Time { return testTime }
	client.readSignals(t.Context(), "")

	recorder := httptest.NewRecorder()
	refused := client.Signals(newRequest("GET", "http://api.example.com/orders/1")).Apply(recorder)

	expected := http.Header{"Deprecation": {"@1767225600"}}
	if refused || !reflect.DeepEqual(recorder.Header(), expected) {
		t.Errorf("Expected only %v, got refused %v, headers %v", expected, refused, recorder.Header())
	}
}

// Given a refusal due earlier and a later entry whose status isn't an error,
// with headers the response must not get from signals
// When signals are applied
// Then the later entry is used for its other headers and nothing is refused
func TestSignalsKeepEntriesWithAnInvalidStatus(t *testing.T) {
	api := newFakeSignalsAPI(t, `{"responses": [
		{"from": "2026-01-01T00:00:00Z", "appliesTo": "ALL", "status": 410, "code": "API_SUNSET"},
		{"from": "2026-02-01T00:00:00Z", "appliesTo": "ALL", "status": 200, "headers": {"Deprecation": "@1767225600", "Content-Length": "0", "transfer-encoding": "chunked", "Set-Cookie": "a=b"}}
	]}`)
	client := newClient(Options{APIKey: testAPIKey, BaseURL: api.URL})
	client.now = func() time.Time { return testTime }
	client.readSignals(t.Context(), "")

	recorder := httptest.NewRecorder()
	refused := client.Signals(newRequest("GET", "http://api.example.com/orders/1")).Apply(recorder)

	expected := http.Header{"Deprecation": {"@1767225600"}}
	if refused || !reflect.DeepEqual(recorder.Header(), expected) {
		t.Errorf("Expected only %v, got refused %v, headers %v", expected, refused, recorder.Header())
	}
}

// Given a running client
// When it is closed
// Then the signals are no longer read once Close returns
func TestCloseStopsReadingSignals(t *testing.T) {
	api := newFakeSignalsAPI(t, `{"responses": []}`)
	client := New(Options{APIKey: testAPIKey, BaseURL: api.URL})

	if err := client.Close(t.Context()); err != nil {
		t.Fatalf("Expected Close to succeed, got %v", err)
	}

	select {
	case <-client.signalsDone:
	default:
		t.Error("Expected the signals to be no longer read")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fakeSignalsAPI answers GET /v1/deprecation-signals with document and an
// ETag, or with status when set, and notes the requests' headers
type fakeSignalsAPI struct {
	*httptest.Server
	mu       sync.Mutex
	document string
	status   int
	requests []http.Header
}

func newFakeSignalsAPI(t *testing.T, document string) *fakeSignalsAPI {
	t.Helper()
	api := &fakeSignalsAPI{document: document}
	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		api.requests = append(api.requests, r.Header.Clone())
		if api.status != 0 {
			w.WriteHeader(api.status)
			return
		}
		w.Header().Set("ETag", `"1-abc"`)
		_, _ = w.Write([]byte(api.document))
	}))
	t.Cleanup(api.Close)
	return api
}

// answer makes the API answer with status from now on
func (api *fakeSignalsAPI) answer(status int) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.status = status
}

func setSignals(client *Client, entries []signalEntry) {
	valid := validEntries(entries)
	client.signals.Store(&valid)
}
