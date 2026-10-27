package updatepromptgin_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptgin "github.com/Update-Prompt/usage-sdk-go/gin"
	"github.com/Update-Prompt/usage-sdk-go/internal/usagetest"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// Given a gin engine with the middleware, a route and a route in a group
// When both routes are called, one with query parameters
// Then each call is reported under its full route path, with query names but no values
func TestMiddlewareReportsTheRoutePath(t *testing.T) {
	client, api := usagetest.NewClient(t)
	engine := gin.New()
	engine.Use(updatepromptgin.Middleware(client))
	engine.GET("/orders/:id", ok)
	engine.Group("/admin").DELETE("/users/:id", ok)

	serve(engine, "GET", "http://api.example.com/orders/8812?expand=items")
	serve(engine, "DELETE", "http://api.example.com/admin/users/7")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "DELETE", Host: "api.example.com", Route: "/admin/users/:id", Count: 1},
		{Method: "GET", Host: "api.example.com", Route: "/orders/:id", QueryParams: []string{"expand"}, Count: 1},
	})
}

// Given a gin engine with the middleware
// When a path with no route is called and gets 404
// Then it isn't counted
func TestMiddlewareSkipsUnmatchedRoutes(t *testing.T) {
	client, api := usagetest.NewClient(t)
	engine := gin.New()
	engine.Use(updatepromptgin.Middleware(client))
	engine.GET("/orders/:id", ok)

	if status := serve(engine, "GET", "http://api.example.com/.env"); status != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d", status)
	}

	expectReports(t, api.Reports(t, client), nil)
}

// Given a gin engine with the middleware and an authentication middleware that sets the consumer
// When a route is called
// Then the call is reported with that consumer
func TestMiddlewareTakesTheConsumer(t *testing.T) {
	client, api := usagetest.NewClient(t)
	engine := gin.New()
	engine.Use(updatepromptgin.Middleware(client), authenticate)
	engine.GET("/orders/:id", ok)

	serve(engine, "GET", "http://api.example.com/orders/1")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/orders/:id", Consumer: "acme-mobile", Count: 1},
	})
}

// Given a gin engine with recovery, the middleware and a handler that panics
// When the route is called
// Then recovery answers 500 and the call is counted
func TestMiddlewareCountsPanics(t *testing.T) {
	client, api := usagetest.NewClient(t)
	engine := gin.New()
	engine.Use(gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	engine.Use(updatepromptgin.Middleware(client))
	engine.GET("/boom", func(*gin.Context) { panic("boom") })

	if status := serve(engine, "GET", "http://api.example.com/boom"); status != http.StatusInternalServerError {
		t.Fatalf("Expected 500, got %d", status)
	}

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/boom", Count: 1},
	})
}

// Given a gin engine with the middleware and an API past its sunset
// When a route and a path with no route are called
// Then the route is refused with 410 before its handler and counted, and the
// other gets gin's 404 without signals
func TestMiddlewareAppliesSignals(t *testing.T) {
	client, api := usagetest.NewClientWithSignals(t, usagetest.Sunset)
	called := false
	engine := gin.New()
	engine.Use(updatepromptgin.Middleware(client))
	engine.GET("/orders/:id", func(*gin.Context) { called = true })

	route := record(engine, "GET", "http://api.example.com/orders/1")
	missing := record(engine, "GET", "http://api.example.com/.env")
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

func ok(c *gin.Context) {
	c.Status(http.StatusOK)
}

func authenticate(c *gin.Context) {
	updateprompt.SetConsumer(c.Request, "acme-mobile")
	c.Next()
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
