package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

func TestProbeHTTPHonorsTLSOverrides(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL + "/v1",
	}
	if res := probeHTTP(context.Background(), p); res.OK {
		t.Fatalf("probe without a TLS override = %+v, want certificate verification to fail", res)
	}

	secure := false
	p.TLS.Secure = &secure
	if res := probeHTTP(context.Background(), p); !res.OK || res.Error != "" {
		t.Fatalf("probe with tls.secure=false = %+v", res)
	}
}
