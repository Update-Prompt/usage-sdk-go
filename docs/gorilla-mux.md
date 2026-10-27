# gorilla/mux

Counts the calls to a [gorilla/mux](https://github.com/gorilla/mux) router by the path template of the route that matched, such as `/orders/{id}`.

## Install

```sh
go get github.com/Update-Prompt/usage-sdk-go/gorillamux
```

Requires Go 1.24 or later.

## Set up

Add the middleware with `Use` on the top router:

```go
import (
	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptmux "github.com/Update-Prompt/usage-sdk-go/gorillamux"
	"github.com/gorilla/mux"
)

client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
defer client.Close(context.Background())           // sends what's left on shutdown

router := mux.NewRouter()
router.Use(updatepromptmux.Middleware(client))
router.HandleFunc("/orders/{id}", getOrder).Methods("GET")
```

## Routes

| Call | Reported as |
|---|---|
| `GET /orders/8812` | `/orders/{id}` |
| A route in a subrouter, such as `/admin` + `/users/{id}` | `/admin/users/{id}` |
| A path with no route (404), or a route called with a method it doesn't have (405) | Not counted: gorilla/mux runs middleware only for calls that match a route |

Templates are sent as written, so a template with a regular expression, such as `/orders/{id:[0-9]+}`, is reported that way.

## Deprecation signals

gorilla/mux runs middleware only for calls that match a route, so every call the middleware sees can get signals, and a sunset route is refused without calling its handler. gorilla/mux's own 404 and 405 answers get no signals. See [Deprecation signals](../README.md#deprecation-signals).

## Consumers

Call `updateprompt.SetConsumer(r, clientID)` in your authentication middleware, added with `Use` after the Update Prompt one, or set `Options.Consumer`. See [Consumers](../README.md#consumers).
