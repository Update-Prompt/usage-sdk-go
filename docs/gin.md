# gin

Counts the calls to a [gin](https://github.com/gin-gonic/gin) engine by the route path gin matched, such as `/orders/:id`.

## Install

```sh
go get github.com/Update-Prompt/usage-sdk-go/updatepromptgin
```

Requires Go 1.26 or later: the `golang.org/x/net` release with the security fixes gin needs does.

## Set up

Add the middleware with `Use` on the engine, before any route. Put it after your recovery middleware, so a call that panics is counted too:

```go
import (
	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	"github.com/Update-Prompt/usage-sdk-go/updatepromptgin"
	"github.com/gin-gonic/gin"
)

client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
defer client.Close(context.Background())           // sends what's left on shutdown

engine := gin.New()
engine.Use(gin.Recovery(), updatepromptgin.Middleware(client))
engine.GET("/orders/:id", getOrder)
```

## Routes

| Call | Reported as |
|---|---|
| `GET /orders/8812` | `/orders/:id` |
| A route in a group, such as `/admin` + `/users/:id` | `/admin/users/:id` |
| A path with no route, answered with 404 | Not counted |

Routes are sent as gin writes them, with `:id` and `*path`.

## Consumers

Call `updateprompt.SetConsumer(c.Request, clientID)` in your authentication middleware, after the Update Prompt one, or set `Options.Consumer`. See [Consumers](../README.md#consumers).

```go
func authenticate(c *gin.Context) {
	clientID := verify(c.Request) // your own authentication
	updateprompt.SetConsumer(c.Request, clientID)
	c.Next()
}
```
