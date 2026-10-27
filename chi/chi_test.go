package updatepromptchi_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptchi "github.com/Update-Prompt/usage-sdk-go/chi"
	"github.com/Update-Prompt/usage-sdk-go/internal/usagetest"
	"github.com/go-chi/chi/v5"
)

// Given a chi router with the middleware, a route and a mounted router
// When both routes are called, one with query parameters
// Then each call is reported under its full route pattern, with query names but no values
func TestMiddlewareReportsTheRoutePattern(t *testing.T) {
	client, api := usagetest.NewClient(t)
	router := chi.NewRouter()
	router.Use(updatepromptchi.Middleware(client))
	router.Get("/orders/{id}", ok)
	router.Mount("/admin", adminRouter())

	serve(router, "GET", "http://api.example.com/orders/8812?expand=items")
	serve(router, "DELETE", "http://api.example.com/admin/users/7")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "DELETE", Host: "api.example.com", Route: "/admin/users/{id}", Count: 1},
		{Method: "GET", Host: "api.example.com", Route: "/orders/{id}", QueryParams: []string{"expand"}, Count: 1},
	})
}

// Given a chi router with the middleware, a mounted router and a wildcard route
// When paths with no route, at the top and in the mounted router, are called
// and get 404, and the wildcard route is called
// Then the 404s aren't counted and the wildcard route is reported under its pattern
func TestMiddlewareSkipsUnmatchedRoutes(t *testing.T) {
	client, api := usagetest.NewClient(t)
	router := chi.NewRouter()
	router.Use(updatepromptchi.Middleware(client))
	router.Get("/orders/{id}", ok)
	router.Mount("/admin", adminRouter())
	router.Get("/files/*", ok)

	serve(router, "GET", "http://api.example.com/.env")
	serve(router, "GET", "http://api.example.com/admin/.env")
	serve(router, "GET", "http://api.example.com/files/a/b.txt")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/files/*", Count: 1},
	})
}

// Given a chi router with the middleware and an authentication middleware that sets the consumer
// When a route is called
// Then the call is reported with that consumer
func TestMiddlewareTakesTheConsumer(t *testing.T) {
	client, api := usagetest.NewClient(t)
	router := chi.NewRouter()
	router.Use(updatepromptchi.Middleware(client))
	router.Use(authenticate)
	router.Get("/orders/{id}", ok)

	serve(router, "GET", "http://api.example.com/orders/1")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/orders/{id}", Consumer: "acme-mobile", Count: 1},
	})
}

// Given a chi router with the middleware and an API past its sunset
// When a route and a path with no route are called
// Then the route is refused with 410 before its handler and counted, and the
// other gets chi's 404 without signals
func TestMiddlewareAppliesSignals(t *testing.T) {
	client, api := usagetest.NewClientWithSignals(t, usagetest.Sunset)
	called := false
	router := chi.NewRouter()
	router.Use(updatepromptchi.Middleware(client))
	router.Get("/orders/{id}", func(http.ResponseWriter, *http.Request) { called = true })
	router.Mount("/admin", adminRouter())

	route := record(router, "GET", "http://api.example.com/orders/1")
	missing := record(router, "GET", "http://api.example.com/admin/.env")
	if route.Code != http.StatusGone || route.Header().Get("Deprecation") != "@1767225600" || called {
		t.Errorf("Expected 410 with Deprecation, without the handler, got %d %v, handler called %v", route.Code, route.Header(), called)
	}
	if missing.Code != http.StatusNotFound || missing.Header().Get("Deprecation") != "" {
		t.Errorf("Expected the router's 404 without Deprecation, got %d %v", missing.Code, missing.Header())
	}
	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/orders/{id}", Count: 1},
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func adminRouter() http.Handler {
	router := chi.NewRouter()
	router.Delete("/users/{id}", ok)
	return router
}

func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updateprompt.SetConsumer(r, "acme-mobile")
		next.ServeHTTP(w, r)
	})
}

func serve(handler http.Handler, method, url string) {
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, url, nil))
}

func expectReports(t *testing.T, got, expected []usagetest.Report) {
	t.Helper()
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("Expected reports %+v, got %+v", expected, got)
	}
}

// record calls handler and returns its response
func record(handler http.Handler, method, url string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, url, nil))
	return recorder
}
