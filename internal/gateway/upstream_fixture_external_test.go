package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// testUpstream is the gateway_test counterpart of the internal test helper:
// it keeps build-time catalog lookups out of data-plane hit assertions.
func testUpstream(handler http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[]}`)
			return
		}
		handler.ServeHTTP(w, r)
	}))
}
