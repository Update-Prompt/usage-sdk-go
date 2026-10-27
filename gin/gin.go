// Package updatepromptgin counts the calls to a [gin] engine with the Update
// Prompt usage SDK, by the route path gin matched.
//
//	client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
//	defer client.Close(context.Background())
//
//	engine := gin.New()
//	engine.Use(updatepromptgin.Middleware(client))
//	engine.GET("/orders/:id", getOrder)
//
// Set the consumer id with [updateprompt.SetConsumer] on c.Request.
//
// [gin]: https://github.com/gin-gonic/gin
package updatepromptgin

import (
	"net/http"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	"github.com/gin-gonic/gin"
)

// Middleware counts every call to the engine, by the route path it matched,
// such as /orders/:id, and applies the deprecation signals to calls that match
// a route. Add it with Use on the engine, before any route.
func Middleware(client *updateprompt.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		request, end := client.Begin(c.Request)
		c.Request = request
		if route := c.FullPath(); route != "" && client.Signals(request).Apply(c.Writer) {
			c.Abort()
			end(route, c.Writer.Status())
			return
		}
		returned := false
		// Deferred, so a call that panics is counted too, without recovering the
		// panic: gin's recovery middleware, if any, still sees it
		defer func() {
			status := c.Writer.Status()
			if !returned && !c.Writer.Written() {
				status = http.StatusInternalServerError
			}
			end(c.FullPath(), status)
		}()
		c.Next()
		returned = true
	}
}
