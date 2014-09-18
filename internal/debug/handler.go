// The debug package holds various functions that may
// be used for debugging but should not be included
// in production code.
package debug

import (
	"log"
	"net/http"
)

// Handler returns a new handler that wraps h
// and logs the given message with the URL path
// every time the request is invoked.
func Handler(msg string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		log.Printf("%s got request at URL %q; headers %q", msg, req.URL, req.Header)
		h.ServeHTTP(w, req)
	})
}
