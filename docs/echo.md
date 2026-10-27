# echo

Counts the calls to an [echo](https://github.com/labstack/echo) v5 server by the route path echo matched, such as `/orders/:id`.

## Install

```sh
go get github.com/Update-Prompt/usage-sdk-go/echo
```

Requires Go 1.25 or later, like echo v5. echo v4 isn't supported.

## Set up

Add the middleware with `Use` on the server. Put it after your recover middleware, so a call that panics is counted too:

```go
import (
	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	updatepromptecho "github.com/Update-Prompt/usage-sdk-go/echo"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
defer client.Close(context.Background())           // sends what's left on shutdown

e := echo.New()
e.Use(middleware.Recover(), updatepromptecho.Middleware(client))
e.GET("/orders/:id", getOrder)
```

## Routes

| Call | Reported as |
|---|---|
| `GET /orders/8812` | `/orders/:id` |
| A route in a group, such as `/admin` + `/users/:id` | `/admin/users/:id` |
| A route whose handler returns an error, such as `echo.ErrNotFound` | Its route |
| A path with no route (404), including the not-found routes echo adds for groups with middleware, and `RouteNotFound` routes | Not counted |
| A route called with a method it doesn't have (405) | The raw path, which Update Prompt turns into a route |

Routes are sent as echo writes them, with `:id` and `*`.

## Consumers

Call `updateprompt.SetConsumer(c.Request(), clientID)` in your authentication middleware, after the Update Prompt one, or set `Options.Consumer`. See [Consumers](../README.md#consumers).

```go
func authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		clientID := verify(c.Request()) // your own authentication
		updateprompt.SetConsumer(c.Request(), clientID)
		return next(c)
	}
}
```
