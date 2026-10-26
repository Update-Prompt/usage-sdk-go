package updateprompt_test

import (
	"context"
	"net/http"
	"time"

	updateprompt "github.com/Update-Prompt/usage-sdk-go"
)

// Count every call to a ServeMux. The API key comes from UPDATE_PROMPT_API_KEY.
func ExampleClient_Middleware() {
	client := updateprompt.New(updateprompt.Options{})
	defer client.Close(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {})

	server := &http.Server{Addr: ":8080", Handler: client.Middleware(mux), ReadHeaderTimeout: 10 * time.Second}
	_ = server.ListenAndServe()
}

// Set the consumer id where your authentication finds out who is calling.
func ExampleSetConsumer() {
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientID := r.Header.Get("X-Client-Id") // or a claim from a verified token
			updateprompt.SetConsumer(r, clientID)
			next.ServeHTTP(w, r)
		})
	}

	client := updateprompt.New(updateprompt.Options{})
	mux := http.NewServeMux()
	_ = client.Middleware(authenticate(mux))
}

// Report calls without the middleware, such as from a proxy or another protocol.
func ExampleClient_Record() {
	client := updateprompt.New(updateprompt.Options{})
	defer client.Close(context.Background())

	client.Record(updateprompt.Call{
		Method:   "GET",
		Host:     "api.example.com",
		Route:    "/orders/{id}",
		Consumer: "acme-mobile",
	})
}
