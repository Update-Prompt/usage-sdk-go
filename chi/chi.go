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
// matched, such as /orders/{id}, and applies the deprecation signals to calls
// that match a route. Add it with Use on the top router, before any route, so
// it sees the full pattern of routes in mounted routers too.
func Middleware(client *updateprompt.Client) func(http.Handler) http.Handler {
	var known sync.Map // routers → the method and pattern of each of their routes
	return client.MiddlewareWithRoute(func(r *http.Request) string {
		routeContext := chi.RouteContext(r.Context())
		if routeContext == nil || routeContext.Routes == nil {
			return ""
		}
		method := method(r, routeContext)
		var pattern string
		if len(routeContext.RoutePatterns) == 0 {
			// Before routing: ask the router which route will match
			pattern = find(routeContext.Routes, method, path(r, routeContext))
		} else {
			pattern = routeContext.RoutePattern()
		}
		if pattern == "" {
			return ""
		}
		return route(&known, routeContext.Routes, method, pattern)
	})
}

// findContexts are reused by find, which runs for every call while there are
// deprecation signals
var findContexts = sync.Pool{New: func() any { return chi.NewRouteContext() }}

// find returns the pattern router will match for method and path, or empty
func find(router chi.Routes, method, path string) string {
	routeContext := findContexts.Get().(*chi.Context)
	routeContext.Reset()
	pattern := router.Find(routeContext, method, path)
	findContexts.Put(routeContext)
	return pattern
}

// route returns the route of router that pattern names, or empty when it
// names none. chi matches patterns that aren't routes: the mount pattern of a
// mounted router that matched nothing, such as /admin/*, and the stubs Mount
// adds for the mount path itself, such as /admin, which chi passes on to the
// mounted router's / route.
func route(known *sync.Map, router chi.Routes, method, pattern string) string {
	routes, ok := known.Load(router)
	if !ok {
		// Read once: chi routers are set up before they serve
		routes, _ = known.LoadOrStore(router, walk(router))
	}
	for _, candidate := range []string{pattern, strings.TrimSuffix(pattern, "/") + "/"} {
		if routes.(map[string]bool)[method+" "+candidate] {
			return candidate
		}
	}
	return ""
}

// walk returns the method and full pattern of every route of router
func walk(router chi.Routes) map[string]bool {
	routes := map[string]bool{}
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	})
	return routes
}

// method returns the method chi routes r by
func method(r *http.Request, routeContext *chi.Context) string {
	if routeContext.RouteMethod != "" {
		return routeContext.RouteMethod
	}
	return r.Method
}

// path returns the path chi routes r by
func path(r *http.Request, routeContext *chi.Context) string {
	switch {
	case routeContext.RoutePath != "":
		return routeContext.RoutePath
	case r.URL.RawPath != "":
		return r.URL.RawPath
	case r.URL.Path != "":
		return r.URL.Path
	default:
		return "/"
	}
}
