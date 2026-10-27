package updatepromptmux_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptmux "github.com/Update-Prompt/usage-sdk-go/gorillamux"
	"github.com/Update-Prompt/usage-sdk-go/internal/usagetest"
	"github.com/gorilla/mux"
)

// Given a gorilla/mux router with the middleware, a route and a subrouter
// When both routes are called, one with query parameters
// Then each call is reported under its full path template, with query names but no values
func TestMiddlewareReportsThePathTemplate(t *testing.T) {
	client, api := usagetest.NewClient(t)
	router := mux.NewRouter()
	router.Use(updatepromptmux.Middleware(client))
	router.HandleFunc("/orders/{id}", ok).Methods("GET")
	router.PathPrefix("/admin").Subrouter().HandleFunc("/users/{id}", ok).Methods("DELETE")

	serve(router, "GET", "http://api.example.com/orders/8812?expand=items")
	serve(router, "DELETE", "http://api.example.com/admin/users/7")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "DELETE", Host: "api.example.com", Route: "/admin/users/{id}", Count: 1},
		{Method: "GET", Host: "api.example.com", Route: "/orders/{id}", QueryParams: []string{"expand"}, Count: 1},
	})
}

// Given a gorilla/mux router with the middleware
// When a path with no route is called and gets 404
// Then it isn't counted
func TestMiddlewareSkipsUnmatchedRoutes(t *testing.T) {
	client, api := usagetest.NewClient(t)
	router := mux.NewRouter()
	router.Use(updatepromptmux.Middleware(client))
	router.HandleFunc("/orders/{id}", ok)

	serve(router, "GET", "http://api.example.com/.env")

	expectReports(t, api.Reports(t, client), nil)
}

// Given a gorilla/mux router with the middleware and an authentication middleware that sets the consumer
// When a route is called
// Then the call is reported with that consumer
func TestMiddlewareTakesTheConsumer(t *testing.T) {
	client, api := usagetest.NewClient(t)
	router := mux.NewRouter()
	router.Use(updatepromptmux.Middleware(client), authenticate)
	router.HandleFunc("/orders/{id}", ok)

	serve(router, "GET", "http://api.example.com/orders/1")

	expectReports(t, api.Reports(t, client), []usagetest.Report{
		{Method: "GET", Host: "api.example.com", Route: "/orders/{id}", Consumer: "acme-mobile", Count: 1},
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
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
