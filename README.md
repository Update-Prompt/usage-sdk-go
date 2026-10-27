# Update Prompt usage SDK for Go

Counts how often each consumer calls each endpoint of your Go HTTP API and reports it to [Update Prompt](https://updateprompt.com), so you can see what is used and by whom before you change or retire an endpoint.

- **One line to add.** Wrap your router; endpoints are discovered from the calls.
- **Never in your way.** Counting is a memory update. Nothing blocks, slows or fails a request, and if Update Prompt can't be reached, counts are kept within a fixed limit and the rest are dropped.
- **Little data.** Calls are counted per endpoint, consumer and minute, and sent every 10 seconds. No bodies, header values, query values or IP addresses ever leave your server.
- **Tells your consumers.** Deprecation headers and sunset refusals you set in Update Prompt are added to your responses.
- **Standard library only.** Adapters for chi, gin, echo and gorilla/mux are separate modules, so you only download the router you use.

## Install

```sh
go get github.com/Update-Prompt/usage-sdk-go
```

Requires Go 1.24 or later. For a router adapter, also get its module (see [Routers](#routers)).

## Quick start

```go
import updateprompt "github.com/Update-Prompt/usage-sdk-go"

client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
defer client.Close(context.Background())           // sends what's left on shutdown

mux := http.NewServeMux()
mux.HandleFunc("GET /orders/{id}", getOrder)
http.ListenAndServe(":8080", client.Middleware(mux))
```

Get the API key from your API's page in Update Prompt and set it as `UPDATE_PROMPT_API_KEY`, or pass it as `Options.APIKey`. Without a key the middleware does nothing.

## Routers

Wrap `http.ServeMux` with `client.Middleware`, as above. For another router, get its adapter and add it as the router's first middleware, so it sees the route the router matched:

| Router | Add | Go | Docs |
|---|---|---|---|
| [chi](https://github.com/go-chi/chi) | `router.Use(updatepromptchi.Middleware(client))` | 1.24 | [docs/chi.md](docs/chi.md) |
| [gin](https://github.com/gin-gonic/gin) | `engine.Use(updatepromptgin.Middleware(client))` | 1.26 | [docs/gin.md](docs/gin.md) |
| [echo](https://github.com/labstack/echo) v5 | `e.Use(updatepromptecho.Middleware(client))` | 1.25 | [docs/echo.md](docs/echo.md) |
| [gorilla/mux](https://github.com/gorilla/mux) | `router.Use(updatepromptmux.Middleware(client))` | 1.24 | [docs/gorilla-mux.md](docs/gorilla-mux.md) |

Each adapter is a module of its own, such as `github.com/Update-Prompt/usage-sdk-go/chi`, so you only download the router you use. Its package is named `updatepromptchi` and so on, so it doesn't clash with the router's own package.

Another router can use `client.MiddlewareWithRoute` with a function that reads its route, and a framework whose handlers aren't `http.Handler`s can use `client.Begin`, the way the adapters do.

## Consumers

Update Prompt shows which consumer made each call. Only your code knows who is calling, so tell the SDK where your authentication finds out:

```go
func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := verify(r)                    // your own authentication
		updateprompt.SetConsumer(r, clientID)
		next.ServeHTTP(w, r)
	})
}

http.ListenAndServe(":8080", client.Middleware(authenticate(mux)))
```

With gin and echo, see [docs/gin.md](docs/gin.md#consumers) and [docs/echo.md](docs/echo.md#consumers).

Or, when a header is enough, set `Options.Consumer`:

```go
updateprompt.New(updateprompt.Options{Consumer: updateprompt.ConsumerFromHeader("X-Client-Id")})
```

Use a stable id that isn't secret, such as a client id or an app name. **Never use API keys, tokens or email addresses**: the id is stored and shown as it is. Calls without a consumer id are still counted.

## Routes

Calls are grouped by route template, such as `/orders/{id}`, not by raw path. The middleware takes the route from, in order:

| Source | When |
|---|---|
| `Options.Route` | You set it, for a router the SDK can't read |
| The router's route | The `http.ServeMux` pattern when the middleware wraps the mux itself (method and host are left out), or the route an adapter reads, such as `/orders/{id}` in chi or `/orders/:id` in gin. It's sent as written |
| The raw path | Neither of the above. Update Prompt turns numbers, UUIDs and long hex ids into `{id}` |

A call with no route that answers 404 isn't counted, so requests for paths your API doesn't have, such as scanners probing `/.env`, don't become endpoints. A `"/"` pattern matches every path, so calls it serves are reported as `/`.

Query parameters are reported by name only, never by value. Names don't split an endpoint: each report lists the names its calls sent, up to 50.

## Deprecation signals

When you deprecate or sunset an API in Update Prompt, the SDK tells its consumers in your responses. It reads the API's deprecation signals once a minute and, for each call:

| Signal | What the response gets |
|---|---|
| Deprecated | The signal's headers, such as `Deprecation`, `Sunset` and `Link`. Your handler answers as usual |
| Sunset | The signal's status, such as `410 Gone`, with a `application/problem+json` body. Your handler isn't called |

Signals apply to every call to a route your API has, including routes you add later. Only the router's own 404 and 405 answers, for paths and methods your API doesn't have, are left as they are, so every Update Prompt SDK answers the same. Wrap the router itself: when other handlers sit between the middleware and the mux, it can't tell which paths have a route, so every call gets the signals.

Refused calls are still counted, but the code below the middleware doesn't run for them, so [`SetConsumer`](#consumers) isn't called. To see which consumers still call after the sunset, also set `Options.Consumer`.

Until the first read succeeds, no signals apply. If a read fails, the last signals read stay in use. Signals for new consumers only aren't applied yet.

## Reporting calls by hand

For a router without middleware support, another protocol, or a proxy, call `Record`:

```go
client.Record(updateprompt.Call{
	Method:   "GET",
	Host:     "api.example.com",
	Route:    "/orders/{id}",
	Consumer: "acme-mobile",
})
```

## Serverless

A frozen or stopped process loses the counts it hasn't sent. In AWS Lambda and similar, call `client.Flush(ctx)` before the invocation ends.

## How it behaves

| | |
|---|---|
| Sending | Every 10 seconds, in batches of up to 1,000 reports, one request at a time |
| Retries | 429, 5xx and network errors, after `Retry-After` or a backoff from 1 s to 60 s, with the same `Idempotency-Key`, so nothing is counted twice |
| Refused batches | Any other 4xx (such as a revoked key): dropped, not retried |
| Memory | At most 10,000 distinct reports of at most 8 KB each. When full, calls already counted keep counting and new ones are dropped |
| Signals | Read once a minute with `If-None-Match`, or later when the API asks with `Retry-After`. A failed read keeps the last signals |
| Shutdown | `Close(ctx)` sends what's left until `ctx` is done |
| Health | `client.Stats()` returns calls counted, sent and dropped. Pass `Options.Logger` for one log line per problem |

The [usage SDK specification](https://github.com/Update-Prompt/usage-sdk-spec) describes this behaviour for every Update Prompt usage SDK, and how to build your own.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities as described in [SECURITY.md](SECURITY.md), not in public issues.

## License

[Apache License 2.0](LICENSE)
