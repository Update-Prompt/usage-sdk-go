// Package updateprompt reports how often each consumer calls each endpoint of
// your HTTP API to Update Prompt.
//
// Add the middleware to your router and every call is counted in memory, per
// endpoint, consumer and minute, and sent in batches every few seconds. The
// SDK never blocks, slows or fails a request: if Update Prompt can't be
// reached, counts are kept within a fixed limit and the rest are dropped.
//
//	client := updateprompt.New(updateprompt.Options{}) // reads UPDATE_PROMPT_API_KEY
//	defer client.Close(context.Background())
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("GET /orders/{id}", getOrder)
//	http.ListenAndServe(":8080", client.Middleware(mux))
//
// For chi, gin, echo and gorilla/mux, add the adapter module for the router
// instead, such as github.com/Update-Prompt/usage-sdk-go/chi.
//
// Tell the SDK who is calling with [SetConsumer], usually where your own
// authentication finds out, or with [Options.Consumer]. To report calls
// without the middleware, use [Client.Record].
package updateprompt
