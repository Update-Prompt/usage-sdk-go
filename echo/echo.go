// Package updatepromptecho counts the calls to an [echo] v5 server with the
// Update Prompt usage SDK, by the route path echo matched.
//
//	client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
//	defer client.Close(context.Background())
//
//	e := echo.New()
//	e.Use(updatepromptecho.Middleware(client))
//	e.GET("/orders/:id", getOrder)
//
// Set the consumer id with [updateprompt.SetConsumer] on c.Request().
//
// [echo]: https://github.com/labstack/echo
package updatepromptecho

import (
	"errors"
	"net/http"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	"github.com/labstack/echo/v5"
)

// Middleware counts every call to the server, by the route path it matched,
// such as /orders/:id. Add it with Use on the server.
func Middleware(client *updateprompt.Client) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			request, end := client.Begin(c.Request())
			c.SetRequest(request)
			returned := false
			// Deferred, so a call that panics is counted too, without recovering
			// the panic: echo's recover middleware, if any, still sees it
			defer func() {
				end(routePath(c), status(c, err, returned))
			}()
			err = next(c)
			returned = true
			return err
		}
	}
}

// routePath returns the path of the route echo matched, or empty when it
// matched none. Not-found routes, which echo also adds for every group with
// middleware, such as /api/*, count as none.
func routePath(c *echo.Context) string {
	route := c.RouteInfo()
	if route.Method == echo.RouteNotFound {
		return ""
	}
	return route.Path
}

// status returns the status of a call. When the handler returned an error
// without answering, echo's error handler answers after every middleware is
// done, so the status is taken from the error the way echo's default error
// handler does.
func status(c *echo.Context, err error, returned bool) int {
	response, unwrapErr := echo.UnwrapResponse(c.Response())
	switch {
	case unwrapErr == nil && response.Committed:
		return response.Status
	case err != nil:
		var coder echo.HTTPStatusCoder
		if errors.As(err, &coder) && coder.StatusCode() != 0 {
			return coder.StatusCode()
		}
		return http.StatusInternalServerError
	case !returned:
		return http.StatusInternalServerError
	case unwrapErr == nil:
		return response.Status
	default:
		return http.StatusOK
	}
}
