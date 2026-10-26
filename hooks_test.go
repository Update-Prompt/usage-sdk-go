package updateprompt

import (
	"net/http"
	"testing"
)

// Given MiddlewareWithRoute with a route function, and one that finds no route
// When calls are served, one of them a 404
// Then the function's route is used, an empty one falls back to the raw path,
// and the 404 without a route isn't counted
func TestMiddlewareWithRouteUsesTheRouteFunction(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	withRoute := client.MiddlewareWithRoute(func(*http.Request) string { return "/orders/{id}" })
	withoutRoute := client.MiddlewareWithRoute(func(*http.Request) string { return "" })

	serve(withRoute(http.HandlerFunc(ok)), "GET", "http://api.example.com/orders/1?page=2")
	serve(withoutRoute(http.HandlerFunc(ok)), "GET", "http://api.example.com/items/2")
	serve(withoutRoute(http.NotFoundHandler()), "GET", "http://api.example.com/.env")
	flush(t, client)

	expectReports(t, gateway.reports("/v1/usage"), []report{{Method: "GET", Host: "api.example.com",
		Route: "/orders/{id}", QueryParams: []string{"page"}, Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
	expectReports(t, gateway.reports("/v1/usage/paths"), []report{{Method: "GET", Host: "api.example.com",
		Path: "/items/2", Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
}

// Given MiddlewareWithRoute and Options.Route both set
// When a call is served
// Then Options.Route wins
func TestMiddlewareWithRoutePutsTheRouteOptionFirst(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)
	client.route = func(*http.Request) string { return "/custom" }
	middleware := client.MiddlewareWithRoute(func(*http.Request) string { return "/orders/{id}" })

	serve(middleware(http.HandlerFunc(ok)), "GET", "http://api.example.com/orders/1")
	flush(t, client)

	if got := gateway.reports("/v1/usage"); len(got) != 1 || got[0].Route != "/custom" {
		t.Errorf("Expected one report for /custom, got %+v", got)
	}
}

// Given a call started with Begin, whose handler sets the consumer on the
// request Begin returned
// When end is called twice with a route and a status, and another with no route and 404
// Then the first is reported once with its route and consumer, and the 404 isn't counted
func TestBeginCountsWhenEnded(t *testing.T) {
	gateway := newFakeGateway(t)
	client := newTestClient(gateway)

	request, end := client.Begin(newRequest("GET", "http://api.example.com/orders/1?expand=items"))
	SetConsumer(request, "acme-mobile")
	end("/orders/:id", http.StatusOK)
	end("/orders/:id", http.StatusOK)
	_, end = client.Begin(newRequest("GET", "http://api.example.com/.env"))
	end("", http.StatusNotFound)
	flush(t, client)

	expectReports(t, gateway.reports("/v1/usage"), []report{{Method: "GET", Host: "api.example.com",
		Route: "/orders/:id", Consumer: "acme-mobile", QueryParams: []string{"expand"},
		Timestamp: "2026-10-26T12:30:00Z", Count: 1}})
	if got := gateway.reports("/v1/usage/paths"); len(got) != 0 {
		t.Errorf("Expected no path reports, got %+v", got)
	}
}

// Given a client without an API key
// When Begin is called and ended
// Then the request is passed on as it was and nothing is counted
func TestBeginWithoutAPIKeyDoesNothing(t *testing.T) {
	t.Setenv("UPDATE_PROMPT_API_KEY", "")
	client := New(Options{})
	original := newRequest("GET", "http://api.example.com/orders/1")

	request, end := client.Begin(original)
	end("/orders/{id}", http.StatusOK)

	if request != original {
		t.Error("Expected the same request back")
	}
	expectStats(t, client, Stats{})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newRequest(method, url string) *http.Request {
	request, _ := http.NewRequest(method, url, nil)
	return request
}
