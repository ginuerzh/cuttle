package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// testUpstream wraps a fake upstream so build-time catalog lookups do not
// count as routed requests. Tests that exercise catalog parsing use plain
// httptest servers and keep control of the /models response.
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
