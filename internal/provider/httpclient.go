package provider

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
)

// NewHTTPClient returns an HTTP client for provider control-plane requests
// (catalog listing and health probes). It honors the provider's tls.secure and
// tls.serverName overrides, and timeout bounds the whole request, including
// the TLS handshake.
func NewHTTPClient(tlsCfg config.TLS, timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg := newTLSConfig(tlsCfg); cfg != nil {
		tr.TLSClientConfig = cfg
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func newTLSConfig(cfg config.TLS) *tls.Config {
	serverName := strings.TrimSpace(cfg.ServerName)
	if cfg.Secure == nil && serverName == "" {
		return nil
	}
	out := &tls.Config{ServerName: serverName}
	if cfg.Secure != nil {
		// tls.secure is an explicit per-provider opt-out for private gateways
		// with self-signed certificates.
		out.InsecureSkipVerify = !*cfg.Secure
	}
	return out
}
