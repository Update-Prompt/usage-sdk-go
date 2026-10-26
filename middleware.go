package updateprompt

import (
	"bufio"
	"context"
	"net"
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
func (client *Client) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if client.apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		start := client.now()
		info := &requestInfo{}
		r = r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, info))
		recorder := &statusRecorder{ResponseWriter: w}
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
			client.recordRequest(r, info, status, start)
		}()
		next.ServeHTTP(recorder, r)
		returned = true
	})
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

// recordRequest counts a request the middleware saw
func (client *Client) recordRequest(r *http.Request, info *requestInfo, status int, start time.Time) {
	call := Call{Method: r.Method, Host: r.Host, Time: start}

	if client.route != nil {
		call.Route = client.route(r)
	}
	if call.Route == "" {
		call.Route = patternRoute(r.Pattern)
	}
	if call.Route == "" {
		if status == http.StatusNotFound {
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

// statusRecorder notes the status a handler answers with
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	// 1xx are informational; the final status comes later
	if recorder.status == 0 && status >= 200 {
		recorder.status = status
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(data)
}

// Unwrap lets http.ResponseController reach the writer below
func (recorder *statusRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

// Flush keeps streaming responses working for handlers that check for http.Flusher
func (recorder *statusRecorder) Flush() {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	_ = http.NewResponseController(recorder.ResponseWriter).Flush()
}

// Hijack keeps WebSocket upgrades working for handlers that check for http.Hijacker
func (recorder *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusSwitchingProtocols
	}
	return http.NewResponseController(recorder.ResponseWriter).Hijack()
}
