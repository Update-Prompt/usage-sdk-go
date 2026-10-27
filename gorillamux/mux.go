// Package updatepromptmux counts the calls to a [gorilla/mux] router with the
// Update Prompt usage SDK, by the path template of the route that matched.
//
//	client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
//	defer client.Close(context.Background())
//
//	router := mux.NewRouter()
//	router.Use(updatepromptmux.Middleware(client))
//	router.HandleFunc("/orders/{id}", getOrder).Methods("GET")
//
// [gorilla/mux]: https://github.com/gorilla/mux
package updatepromptmux

import (
	"net/http"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
	"github.com/gorilla/mux"
)

// Middleware counts every call to a route of the router, by the route's path
// template, such as /orders/{id}. Add it with Use on the top router. gorilla/mux
// runs middleware only for calls that match a route, so calls that get its 404
// or 405 aren't counted.
func Middleware(client *updateprompt.Client) mux.MiddlewareFunc {
	return client.MiddlewareWithRoute(pathTemplate)
}

// pathTemplate returns the path template of the route that matched r, or empty
func pathTemplate(r *http.Request) string {
	route := mux.CurrentRoute(r)
	if route == nil {
		return ""
	}
	template, err := route.GetPathTemplate()
	if err != nil {
		return ""
	}
	return template
}
