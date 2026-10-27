package updateprompt

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Given a ServeMux behind the middleware and an API deprecated with headers only
// When a route, a path with no pattern (404) and a method with no pattern (405) are called
// Then only the route's response gets the headers, and the others are left as they are
func TestMiddlewareAddsSignalsToMatchedRoutes(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	setSignals(client, []signalEntry{{From: testTime, AppliesTo: "ALL", Headers: map[string]string{"Deprecation": "@1767225600"}}})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", ok)
	handler := client.Middleware(mux)

	route := record(handler, "GET", "http://api.example.com/orders/1")
	missing := record(handler, "GET", "http://api.example.com/.env")
	wrongMethod := record(handler, "POST", "http://api.example.com/orders/1")

	if route.Code != http.StatusOK || route.Header().Get("Deprecation") != "@1767225600" {
		t.Errorf("Expected 200 with Deprecation, got %d %v", route.Code, route.Header())
	}
	for _, response := range []*httptest.ResponseRecorder{missing, wrongMethod} {
		if response.Header().Get("Deprecation") != "" {
			t.Errorf("Expected no Deprecation on %d, got %v", response.Code, response.Header())
		}
	}
}

// Given a ServeMux behind the middleware and an API past its sunset
// When a route and a path with no pattern are called
// Then the route is refused with 410 without calling its handler and counted
// under its pattern, and the other gets the mux's 404 and isn't counted
func TestMiddlewareRefusesMatchedRoutes(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	setSignals(client, []signalEntry{{From: testTime, AppliesTo: "ALL", Status: 410, Code: "API_SUNSET"}})
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(http.ResponseWriter, *http.Request) { called = true })
	handler := client.Middleware(mux)

	route := record(handler, "GET", "http://api.example.com/orders/1")
	missing := record(handler, "GET", "http://api.example.com/.env")
	flush(t, client)

	if route.Code != http.StatusGone || called {
		t.Errorf("Expected 410 without the handler, got %d, handler called %v", route.Code, called)
	}
	if missing.Code != http.StatusNotFound || missing.Header().Get("Content-Type") == "application/problem+json" {
		t.Errorf("Expected the mux's 404, got %d %v", missing.Code, missing.Header())
	}
	expectReports(t, gateway.reports("/v1/usage"), []report{{Method: "GET", Host: "api.example.com",
		Route: "/orders/{id}", Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
	if got := gateway.reports("/v1/usage/paths"); len(got) != 0 {
		t.Errorf("Expected no path reports, got %+v", got)
	}
}

// Given a plain handler, no router, behind the middleware and an API refused with 404
// When it is called
// Then the call is refused with 404 and still counted, by raw path
func TestMiddlewareRefusesWithoutRouter(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	setSignals(client, []signalEntry{{From: testTime, AppliesTo: "ALL", Status: 404, Code: "API_SUNSET"}})

	response := record(client.Middleware(http.HandlerFunc(ok)), "GET", "http://api.example.com/orders/1")
	flush(t, client)

	if response.Code != http.StatusNotFound || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("Expected the 404 refusal, got %d %v", response.Code, response.Header())
	}
	expectReports(t, gateway.reports("/v1/usage/paths"), []report{{Method: "GET", Host: "api.example.com",
		Path: "/orders/1", Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
}

// Given MiddlewareWithRoute with a route function that finds a route for /orders
// only, and an API past its sunset
// When /orders/1 and /items/1 are called
// Then /orders/1 is refused before its handler, and /items/1 reaches it
func TestMiddlewareWithRouteRefusesMatchedRoutes(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	setSignals(client, []signalEntry{{From: testTime, AppliesTo: "ALL", Status: 410, Code: "API_SUNSET"}})
	middleware := client.MiddlewareWithRoute(func(r *http.Request) string {
		if r.URL.Path == "/orders/1" {
			return "/orders/{id}"
		}
		return ""
	})
	handler := middleware(http.HandlerFunc(ok))

	orders := record(handler, "GET", "http://api.example.com/orders/1")
	items := record(handler, "GET", "http://api.example.com/items/1")

	if orders.Code != http.StatusGone || items.Code != http.StatusOK {
		t.Errorf("Expected 410 and 200, got %d and %d", orders.Code, items.Code)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// record calls handler and returns its response
func record(handler http.Handler, method, url string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, url, nil))
	return recorder
}
