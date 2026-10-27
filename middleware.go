package updateprompt

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Middleware counts every call to next. Wrap your router itself, so the route
// it matched can be read:
//
//	http.ListenAndServe(":8080", client.Middleware(mux))
//
// The route is, in order: [Options.Route], the [http.ServeMux] pattern, or
// else the raw path, which Update Prompt turns into a route. A call with no
// route that got 404 isn't counted, so requests for paths your API doesn't
// have don't become endpoints.
//
// When your API is deprecated, the middleware adds the deprecation headers to
// the responses and, once the API is no longer available, refuses calls
// instead of calling next (see [Client.Signals]). A ServeMux's own 404 and 405
// answers, for paths and methods it has no pattern for, are left as they are.
func (client *Client) Middleware(next http.Handler) http.Handler {
	return client.middleware(next, nil)
}

// MiddlewareWithRoute returns a middleware like [Client.Middleware] for a
// router that puts the route it matched on the request, such as chi or
// gorilla/mux. Add it inside the router, where that route can be seen. route
// returns the route template the router matches for the request, or empty
// when it matches none; the ServeMux pattern isn't read. It runs after the
// handler, and also before it when there are deprecation signals to apply,
// so it must work at both times. Calls with no route get no signals.
// [Options.Route] still comes first.
//
// The adapters in this repository use it; you need it only for a router
// without one.
func (client *Client) MiddlewareWithRoute(route func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return client.middleware(next, route)
	}
}

// Begin starts counting one call, for frameworks whose handlers aren't
// [http.Handler]s, such as gin and echo. Pass the request it returns on to
// your handlers, so [SetConsumer] works there. When the handler is done, call
// end with the route template the router matched, or empty when it matched
// none, and the status sent. Only the first call to end counts.
// [Options.Route] still comes first.
//
// The adapters in this repository use it; you need it only for a framework
// without one.
//
// To apply deprecation signals, call [Client.Signals] with the request it
// returns before your handler, for calls the router matched to a route.
func (client *Client) Begin(r *http.Request) (_ *http.Request, end func(route string, status int)) {
	if client.apiKey == "" {
		return r, func(string, int) {}
	}
	r, c := client.begin(r)
	return r, func(route string, status int) { c.end(route, status, false) }
}

// call is one call being counted
type call struct {
	client *Client
	r      *http.Request
	info   *requestInfo
	start  time.Time
	once   sync.Once
}

// begin starts counting a call and returns the request to pass on
func (client *Client) begin(r *http.Request) (*http.Request, *call) {
	info := &requestInfo{}
	r = r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, info))
	return r, &call{client: client, r: r, info: info, start: client.now()}
}

// end counts the call once. matched means a route matched even if route is
// empty, so a 404 is counted.
func (c *call) end(route string, status int, matched bool) {
	c.once.Do(func() { c.client.recordRequest(c.r, c.info, route, status, matched, c.start) })
}

// middleware counts every call to next, taking the route from route when
// given, else from the ServeMux pattern. Routers such as chi set the request's
// pattern too, so route alone decides when it is given.
func (client *Client) middleware(next http.Handler, route func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if client.apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		r, c := client.begin(r)
		recorder := &statusRecorder{ResponseWriter: w}
		writer := recorder.wrap()
		if signals := client.Signals(r); signals.entry != nil {
			if matched, ok := matchBefore(next, route, r); ok && signals.Apply(writer) {
				c.end(matched, recorder.status, true)
				return
			}
		}
		returned := false
		// Deferred, so a call that panics is counted too, without recovering the
		// panic: net/http still sees it with its stack
		defer func() {
			status := recorder.status
			switch {
			case status != 0:
			case returned:
				// Nothing written: an empty 200
				status = http.StatusOK
			default:
				status = http.StatusInternalServerError
			}
			var matched string
			if route != nil {
				matched = route(r)
			} else {
				matched = patternRoute(r.Pattern)
			}
			c.end(matched, status, false)
		}()
		next.ServeHTTP(writer, r)
		returned = true
	})
}

// matchBefore returns the route the router will match for r, before it runs,
// and whether it matches one. Without a router to ask, every call matches.
func matchBefore(next http.Handler, route func(*http.Request) string, r *http.Request) (string, bool) {
	if route != nil {
		matched := route(r)
		return matched, matched != ""
	}
	if mux, ok := next.(*http.ServeMux); ok {
		_, pattern := mux.Handler(r)
		return patternRoute(pattern), pattern != ""
	}
	return "", true
}

// SetConsumer sets the id of the consumer making r, such as a client id from
// your authentication. Call it anywhere below the middleware, with the request
// or one derived from it. It does nothing for a request the middleware didn't
// see.
func SetConsumer(r *http.Request, consumer string) {
	if info, ok := r.Context().Value(requestInfoKey{}).(*requestInfo); ok {
		info.setConsumer(consumer)
	}
}

// ConsumerFromHeader returns an [Options.Consumer] that takes the consumer id
// from a request header, such as X-Client-Id
func ConsumerFromHeader(name string) func(*http.Request) string {
	return func(r *http.Request) string {
		return r.Header.Get(name)
	}
}

// recordRequest counts a request the middleware or Begin saw. route is the
// router's template, used when Options.Route gives none. matched means a
// route matched even without a template, so a 404 is counted.
func (client *Client) recordRequest(r *http.Request, info *requestInfo, route string, status int, matched bool, start time.Time) {
	call := Call{Method: r.Method, Host: r.Host, Time: start}

	if client.route != nil {
		call.Route = client.route(r)
	}
	if call.Route == "" {
		call.Route = route
	}
	if call.Route == "" {
		if status == http.StatusNotFound && !matched {
			return
		}
		call.Path = r.URL.EscapedPath()
	}
	call.QueryParams = queryParamNames(r.URL.RawQuery)

	if consumer, ok := info.getConsumer(); ok {
		call.Consumer = consumer
	} else if client.consumer != nil {
		call.Consumer = client.consumer(r)
	}
	client.Record(call)
}

// patternRoute returns the path of a ServeMux pattern such as
// "GET example.com/orders/{id}", without its method and host
func patternRoute(pattern string) string {
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		pattern = strings.TrimLeft(rest, " \t")
	}
	if i := strings.Index(pattern, "/"); i >= 0 {
		return pattern[i:]
	}
	return ""
}

// queryParamNames returns the names in a raw query string, decoded, in order,
// without values
func queryParamNames(rawQuery string) []string {
	var names []string
	for part := range strings.SplitSeq(rawQuery, "&") {
		name, _, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// requestInfoKey is the context key of a request's requestInfo
type requestInfoKey struct{}

// requestInfo is what the code below the middleware tells it about a request.
// A handler may set it from another goroutine, so it has a lock.
type requestInfo struct {
	mu          sync.Mutex
	consumer    string
	consumerSet bool
}

func (info *requestInfo) setConsumer(consumer string) {
	info.mu.Lock()
	defer info.mu.Unlock()
	info.consumer, info.consumerSet = consumer, true
}

func (info *requestInfo) getConsumer() (string, bool) {
	info.mu.Lock()
	defer info.mu.Unlock()
	return info.consumer, info.consumerSet
}
