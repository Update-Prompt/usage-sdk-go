// Package updatepromptchi counts the calls to a [chi] router with the Update
// Prompt usage SDK, by the route pattern chi matched.
//
//	client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
//	defer client.Close(context.Background())
//
//	router := chi.NewRouter()
//	router.Use(updatepromptchi.Middleware(client))
//	router.Get("/orders/{id}", getOrder)
//
// [chi]: https://github.com/go-chi/chi
package updatepromptchi

import (
	"net/http"
	"strings"
	"sync"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	"github.com/go-chi/chi/v5"
)

// Middleware counts every call to the router, by the route pattern it
// matched, such as /orders/{id}. Add it with Use on the top router, before
// any route, so it sees the full pattern of routes in mounted routers too.
func Middleware(client *updateprompt.Client) func(http.Handler) http.Handler {
	var mounts sync.Map // route patterns → whether they end at a mounted router
	return client.MiddlewareWithRoute(func(r *http.Request) string {
		routeContext := chi.RouteContext(r.Context())
		if routeContext == nil {
			return ""
		}
		pattern := routeContext.RoutePattern()
		// A mounted router that matched nothing leaves its mount pattern, such
		// as /admin/*, which isn't a route
		if strings.HasSuffix(pattern, "*") && routeContext.Routes != nil {
			key := strings.Join(routeContext.RoutePatterns, " ")
			mount, ok := mounts.Load(key)
			if !ok {
				mount, _ = mounts.LoadOrStore(key, endsAtMount(routeContext.Routes, routeContext.RoutePatterns))
			}
			if mount.(bool) {
				return ""
			}
		}
		return pattern
	})
}

// endsAtMount tells whether the last of patterns, followed down from routes,
// is where a router is mounted
func endsAtMount(routes chi.Routes, patterns []string) bool {
	for i, pattern := range patterns {
		var subRoutes chi.Routes
		for _, route := range routes.Routes() {
			if route.Pattern == pattern {
				subRoutes = route.SubRoutes
				break
			}
		}
		if subRoutes == nil {
			return false
		}
		if i == len(patterns)-1 {
			return true
		}
		routes = subRoutes
	}
	return false
}
