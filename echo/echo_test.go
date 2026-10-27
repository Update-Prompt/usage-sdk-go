package updatepromptecho_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptecho "github.com/Update-Prompt/usage-sdk-go/echo"
	"github.com/Update-Prompt/usage-sdk-go/internal/usagetest"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// Given an echo server with the middleware, a route and a route in a group
// When both routes are called, one with query parameters
// Then each call is reported under its full route path, with query names but no values
func TestMiddlewareReportsTheRoutePath(t *testing.T) {
	client, api := usagetest.NewClient(t)
	e := echo.New()
	e.Use(updatepromptecho.Middleware(client))
	e.GET("/orders/:id", ok)
	e.Group("/admin").DELETE("/users/:id", ok)

	serve(e, "GET", "http://api.example.com/orders/8812?expand=items")
	serve(e, "DELETE", "http://api.example.com/admin/users/7")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "DELETE", Host: "api.example.com", Route: "/admin/users/:id", Count: 1},
		{Method: "GET", Host: "api.example.com", Route: "/orders/:id", QueryParams: []string{"expand"}, Count: 1},
	})
}

// Given an echo server with the middleware, and a group with middleware, for
// which echo adds a not-found route /admin/*
// When paths with no route, at the top and in the group, are called and get
// 404, and a route is called with a method it doesn't have and gets 405
// Then the 404s aren't counted and the 405 is reported by raw path
func TestMiddlewareSkipsUnmatchedRoutes(t *testing.T) {
	client, api := usagetest.NewClient(t)
	e := echo.New()
	e.Use(updatepromptecho.Middleware(client))
	e.GET("/orders/:id", ok)
	e.Group("/admin", authenticate).GET("/users/:id", ok)

	for _, url := range []string{"http://api.example.com/.env", "http://api.example.com/admin/.env"} {
		if status := serve(e, "GET", url); status != http.StatusNotFound {
			t.Fatalf("Expected 404 for %s, got %d", url, status)
		}
	}
	if status := serve(e, "PUT", "http://api.example.com/orders/1"); status != http.StatusMethodNotAllowed {
		t.Fatalf("Expected 405, got %d", status)
	}

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "PUT", Host: "api.example.com", Path: "/orders/1", Count: 1},
	})
}

// Given an echo server with the middleware and routes that return errors without answering
// When a route returns a 404 error, and another a plain error
// Then both are counted under their routes, since they matched one
func TestMiddlewareCountsErrors(t *testing.T) {
	client, api := usagetest.NewClient(t)
	e := echo.New()
	e.Use(updatepromptecho.Middleware(client))
	e.GET("/orders/:id", func(*echo.Context) error { return echo.ErrNotFound })
	e.GET("/items/:id", func(*echo.Context) error { return errors.New("broken") })

	serve(e, "GET", "http://api.example.com/orders/1")
	if status := serve(e, "GET", "http://api.example.com/items/1"); status != http.StatusInternalServerError {
		t.Fatalf("Expected 500, got %d", status)
	}

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/items/:id", Count: 1},
		{Method: "GET", Host: "api.example.com", Route: "/orders/:id", Count: 1},
	})
}

// Given an echo server with the middleware and an authentication middleware that sets the consumer
// When a route is called
// Then the call is reported with that consumer
func TestMiddlewareTakesTheConsumer(t *testing.T) {
	client, api := usagetest.NewClient(t)
	e := echo.New()
	e.Use(updatepromptecho.Middleware(client), authenticate)
	e.GET("/orders/:id", ok)

	serve(e, "GET", "http://api.example.com/orders/1")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/orders/:id", Consumer: "acme-mobile", Count: 1},
	})
}

// Given an echo server with recover, the middleware and a handler that panics
// When the route is called
// Then recover answers 500 and the call is counted
func TestMiddlewareCountsPanics(t *testing.T) {
	client, api := usagetest.NewClient(t)
	e := echo.New()
	e.Use(middleware.Recover(), updatepromptecho.Middleware(client))
	e.GET("/boom", func(*echo.Context) error { panic("boom") })

	if status := serve(e, "GET", "http://api.example.com/boom"); status != http.StatusInternalServerError {
		t.Fatalf("Expected 500, got %d", status)
	}

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/boom", Count: 1},
	})
}

// Given an echo server with the middleware, a group with middleware, and an
// API past its sunset
// When a route and a path with no route in the group are called
// Then the route is refused with 410 before its handler and counted, and the
// other gets echo's 404 without signals
func TestMiddlewareAppliesSignals(t *testing.T) {
	client, api := usagetest.NewClientWithSignals(t, usagetest.Sunset)
	called := false
	e := echo.New()
	e.Use(updatepromptecho.Middleware(client))
	e.GET("/orders/:id", func(*echo.Context) error { called = true; return nil })
	e.Group("/admin", authenticate).GET("/users/:id", ok)

	route := record(e, "GET", "http://api.example.com/orders/1")
	missing := record(e, "GET", "http://api.example.com/admin/.env")
	if route.Code != http.StatusGone || route.Header().Get("Deprecation") != "@1767225600" || called {
		t.Errorf("Expected 410 with Deprecation, without the handler, got %d %v, handler called %v", route.Code, route.Header(), called)
	}
	if missing.Code != http.StatusNotFound || missing.Header().Get("Deprecation") != "" {
		t.Errorf("Expected the router's 404 without Deprecation, got %d %v", missing.Code, missing.Header())
	}
	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/orders/:id", Count: 1},
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ok(c *echo.Context) error {
	return c.NoContent(http.StatusOK)
}

func authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		updateprompt.SetConsumer(c.Request(), "acme-mobile")
		return next(c)
	}
}

// serve calls handler and returns the status it answered with
func serve(handler http.Handler, method, url string) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, url, nil))
	return recorder.Code
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
