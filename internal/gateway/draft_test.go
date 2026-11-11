package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// waitRow polls the store until the recorder has written the request row.
func waitRow(t *testing.T, gw *Gateway) stats.Request {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := gw.Stats().Query(context.Background(), stats.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			return rows[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("no request row recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDraftEntrypointDoesNotFallThrough(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	cfg.Entrypoints = []gwconfig.Entrypoint{
		{ID: "e_0000000003", Name: "draft", Match: []string{"*"}},
		{ID: "e_0000000004", Name: "fallback", Match: []string{"*"}, Route: testRouteID},
	}
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[]}`); code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if hit.path != "" {
		t.Fatalf("a draft entrypoint leaked to the later entrypoint's upstream: %q", hit.path)
	}
	row := waitRow(t, gw)
	if row.Route != "" || row.Status != http.StatusBadGateway {
		t.Fatalf("row route/status = %q/%d, want empty/502 (unrouted)", row.Route, row.Status)
	}
}

func TestDraftNodeRouteFails(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Nodes[0].Provider = ""
	cfg.Nodes[0].Model = ""
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[]}`); code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if hit.path != "" {
		t.Fatalf("a providerless node reached an upstream: %q", hit.path)
	}
}

func TestDraftEntrypointCountTokensFails(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Entrypoints[0].Route = ""
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/messages/count_tokens", `{"model":"anything","messages":[]}`); code != http.StatusBadGateway {
		t.Fatalf("count_tokens status = %d, want a 5xx instead of a local estimate", code)
	}
	if hit.path != "" {
		t.Fatalf("count_tokens reached an upstream: %q", hit.path)
	}
}

func TestReloadAcceptsDraftConfig(t *testing.T) {
	up, _ := captureUpstream(t)
	gw := startGateway(t, testConfig(up.URL))

	draft := testConfig(up.URL)
	draft.Entrypoints = append(draft.Entrypoints, gwconfig.Entrypoint{
		ID: "e_0000000003", Name: "draft", Match: []string{"mystery*"},
	})
	draft.Nodes[0].Provider = ""
	draft.Nodes[0].Model = ""
	if err := gw.Reload(draft); err != nil {
		t.Fatalf("reload with drafts = %v, want success", err)
	}
}
