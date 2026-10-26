package updateprompt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Given a ServeMux with patterns, with and without method and host
// When they are called
// Then each call is reported under the pattern's path, with query names but no values
func TestMiddlewareReportsTheServeMuxPattern(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", ok)
	mux.HandleFunc("api.example.com/items/{id}", ok)
	handler := client.Middleware(mux)

	serve(handler, "GET", "http://api.example.com/orders/8812?expand=items&page=2")
	serve(handler, "GET", "http://api.example.com/orders/8813")
	serve(handler, "POST", "http://api.example.com/items/5")
	flush(t, client)

	reports := gateway.reports("/v1/usage")
	expected := map[string]report{
		"/orders/{id}?": {Method: "GET", Host: "api.example.com", Route: "/orders/{id}",
			Timestamp: "2026-10-26T12:30:00Z", Count: 1},
		"/orders/{id}?expand,page": {Method: "GET", Host: "api.example.com", Route: "/orders/{id}",
			QueryParams: []string{"expand", "page"}, Timestamp: "2026-10-26T12:30:00Z", Count: 1},
		"/items/{id}?": {Method: "POST", Host: "api.example.com", Route: "/items/{id}",
			Timestamp: "2026-10-26T12:30:00Z", Count: 1},
	}
	expectReportSet(t, reports, expected)
}

// Given a ServeMux without a pattern for the path, and a handler without any router
// When a missing path gets 404 and the plain handler answers 200
// Then the 404 isn't counted and the other is reported by raw path, without query values
func TestMiddlewareSkips404sWithoutRoute(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", ok)

	serve(client.Middleware(mux), "GET", "http://api.example.com/.env")
	serve(client.Middleware(http.HandlerFunc(ok)), "GET", "http://api.example.com/orders/8812?token=secret")
	flush(t, client)

	expectReports(t, gateway.reports("/v1/usage/paths"), []report{{Method: "GET", Host: "api.example.com",
		Path: "/orders/8812?token", Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
	if got := gateway.reports("/v1/usage"); len(got) != 0 {
		t.Errorf("Expected no route reports, got %+v", got)
	}
}

// Given a route that answers 404 itself, such as a missing order
// When it is called
// Then it is counted under its route
func TestMiddlewareCounts404sWithRoute(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", http.NotFound)

	serve(client.Middleware(mux), "GET", "http://api.example.com/orders/1")
	flush(t, client)

	if got := gateway.reports("/v1/usage"); len(got) != 1 || got[0].Route != "/orders/{id}" {
		t.Errorf("Expected one report for /orders/{id}, got %+v", got)
	}
}

// Given Options.Route
// When a call is served
// Then its result is the route, ahead of the ServeMux pattern
func TestMiddlewareUsesTheRouteOption(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	client.route = func(*http.Request) string { return "/custom/{id}" }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", ok)

	serve(client.Middleware(mux), "GET", "http://api.example.com/orders/1")
	flush(t, client)

	if got := gateway.reports("/v1/usage"); len(got) != 1 || got[0].Route != "/custom/{id}" {
		t.Errorf("Expected one report for /custom/{id}, got %+v", got)
	}
}

// Given a handler that calls SetConsumer on a request derived from the one it got,
// and a client with Options.Consumer reading a header
// When calls with and without SetConsumer are served
// Then SetConsumer wins, and the option is used otherwise
func TestMiddlewareTakesTheConsumer(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	client.consumer = ConsumerFromHeader("X-Client-Id")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /set", func(w http.ResponseWriter, r *http.Request) {
		type key struct{}
		derived := r.WithContext(context.WithValue(r.Context(), key{}, "user"))
		SetConsumer(derived, "from-auth")
	})
	mux.HandleFunc("GET /header", ok)

	serve(client.Middleware(mux), "GET", "http://h/set", "X-Client-Id", "from-header")
	serve(client.Middleware(mux), "GET", "http://h/header", "X-Client-Id", "from-header")
	flush(t, client)

	consumers := map[string]string{}
	for _, r := range gateway.reports("/v1/usage") {
		consumers[r.Route] = r.Consumer
	}
	if consumers["/set"] != "from-auth" || consumers["/header"] != "from-header" {
		t.Errorf("Expected from-auth and from-header, got %v", consumers)
	}
}

// Given a handler that panics before writing
// When it is called
// Then the panic still reaches the caller and the call is counted
func TestMiddlewareCountsPanics(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	func() {
		defer func() {
			if recover() != "boom" {
				t.Errorf("Expected the panic to reach the caller")
			}
		}()
		serve(client.Middleware(mux), "GET", "http://h/boom")
	}()
	flush(t, client)

	if got := gateway.reports("/v1/usage"); len(got) != 1 || got[0].Route != "/boom" {
		t.Errorf("Expected one report for /boom, got %+v", got)
	}
}

// Given a handler that streams and checks for http.Flusher
// When it is called through the middleware
// Then the writer still flushes
func TestMiddlewareKeepsFlushing(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	handler := client.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("Expected an http.Flusher")
		}
		flusher.Flush()
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "http://h/stream", nil))

	if !recorder.Flushed {
		t.Error("Expected the response to be flushed")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// serve calls handler, with header name and value pairs
func serve(handler http.Handler, method, url string, headers ...string) {
	request := httptest.NewRequest(method, url, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

// expectReportSet compares reports by route and query names, in any order
func expectReportSet(t *testing.T, got []report, expected map[string]report) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("Expected %d reports, got %+v", len(expected), got)
	}
	for _, r := range got {
		key := r.Route + "?"
		for i, name := range r.QueryParams {
			if i > 0 {
				key += ","
			}
			key += name
		}
		expectReports(t, []report{r}, []report{expected[key]})
	}
}
