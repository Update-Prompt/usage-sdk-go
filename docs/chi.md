# chi

Counts the calls to a [chi](https://github.com/go-chi/chi) v5 router by the route pattern chi matched, such as `/orders/{id}`.

## Install

```sh
go get github.com/Update-Prompt/usage-sdk-go/chi
```

Requires Go 1.24 or later.

## Set up

Add the middleware with `Use` on the top router, before any route, so it also sees the full pattern of routes in mounted routers:

```go
import (
	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptchi "github.com/Update-Prompt/usage-sdk-go/chi"
	"github.com/go-chi/chi/v5"
)

client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
defer client.Close(context.Background())           // sends what's left on shutdown

router := chi.NewRouter()
router.Use(updatepromptchi.Middleware(client))
router.Get("/orders/{id}", getOrder)
router.Mount("/admin", adminRouter)
```

## Routes

| Call | Reported as |
|---|---|
| `GET /orders/8812` | `/orders/{id}` |
| `DELETE /admin/users/7`, a route in a mounted router | `/admin/users/{id}` |
| A wildcard route such as `/files/*` | `/files/*` |
| A path with no route, at the top or in a mounted router, answered with 404 | Not counted |
| A route called with a method it doesn't have (405) | The raw path, which Update Prompt turns into a route |

Patterns are sent as written, so a pattern with a regular expression, such as `/orders/{id:[0-9]+}`, is reported that way.

## Consumers

Call `updateprompt.SetConsumer(r, clientID)` in your authentication middleware, below the Update Prompt one, or set `Options.Consumer`. See [Consumers](../README.md#consumers).
