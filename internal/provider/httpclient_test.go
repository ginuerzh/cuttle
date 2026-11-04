package provider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
)

func TestModelsHonorsTLSConfig(t *testing.T) {
	t.Run("secure false accepts a self-signed certificate", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"data":[{"id":"chat-model"}]}`)
		}))
		t.Cleanup(srv.Close)

		// Control: default verification must reject the test certificate.
		verified := mustProvider(t, config.Provider{
			Name: "p",
			Type: "generic",
			Chat: srv.URL + "/v1",
		})
		if _, err := verified.Models(context.Background()); err == nil {
			t.Fatal("Models should reject a self-signed certificate without a TLS override")
		}

		secure := false
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "generic",
			Chat: srv.URL + "/v1",
			TLS:  config.TLS{Secure: &secure},
		})
		models, err := p.Models(context.Background())
		if err != nil {
			t.Fatalf("Models with tls.secure=false: %v", err)
		}
		if len(models) != 1 || models[0].ID != "chat-model" {
			t.Fatalf("models = %+v", models)
		}
	})

	t.Run("custom server name", func(t *testing.T) {
		const name = "provider.example"
		sni := make(chan string, 1)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"data":[{"id":"chat-model"}]}`)
		}))
		srv.TLS = &tls.Config{
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				select {
				case sni <- hello.ServerName:
				default:
				}
				return nil, nil
			},
		}
		srv.StartTLS()
		t.Cleanup(srv.Close)

		secure := false
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "generic",
			Chat: srv.URL + "/v1",
			TLS:  config.TLS{Secure: &secure, ServerName: name},
		})
		if _, err := p.Models(context.Background()); err != nil {
			t.Fatalf("Models with tls.serverName=%q: %v", name, err)
		}
		select {
		case got := <-sni:
			if got != name {
				t.Fatalf("SNI = %q, want %q", got, name)
			}
		default:
			t.Fatal("TLS handshake did not reach the test server")
		}
	})
}
