package gateway

import (
	"testing"

	"github.com/go-gost/core/hop"
	xlogger "github.com/go-gost/x/logger"
	mdutil "github.com/go-gost/x/metadata/util"

	"github.com/ginuerzh/cuttle/internal/capture"
	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// buildRetryRouteHop compiles one route whose provider has two enabled keys and
// whose nodes are a normal node plus a backup node, so four x nodes must carry
// the policy. The nodes pin the chat API to keep target resolution offline.
func buildRetryRouteHop(t *testing.T, route gwconfig.Route) hop.NodeList {
	t.Helper()
	cfg := &gwconfig.Config{
		Providers: []gwconfig.Provider{{
			ID:   "p_0000000001",
			Name: "p",
			Type: "generic",
			Chat: "https://upstream.test/v1",
			Keys: []gwconfig.ProviderKey{
				{ID: "pk_0000000001", Name: "k1", Key: "sk-1"},
				{ID: "pk_0000000002", Name: "k2", Key: "sk-2"},
			},
		}},
		Nodes: []gwconfig.Node{
			{ID: "n_0000000001", Name: "n1", Provider: "p_0000000001", Model: "model", API: "chat"},
			{ID: "n_0000000002", Name: "n2", Provider: "p_0000000001", Model: "model", API: "chat", Backup: true},
		},
	}

	plan, err := buildRoutePlan(cfg, "retry-build", route, xlogger.Nop(), stats.NewFacts(), capture.NewManager(capture.Policy{Mode: "off"}))
	if err != nil {
		t.Fatal(err)
	}
	h, ok := plan.hops[""]
	if !ok {
		t.Fatalf("default hop missing: %+v", plan.hops)
	}
	nl, ok := h.(hop.NodeList)
	if !ok {
		t.Fatal("default hop does not expose its nodes")
	}
	// Two keys for each of the two nodes, backup included.
	if got := len(nl.Nodes()); got != 4 {
		t.Fatalf("default hop has %d nodes, want 4", got)
	}
	return nl
}

func TestRouteRetryMetadata(t *testing.T) {
	t.Run("stamps every per-key and backup node", func(t *testing.T) {
		nl := buildRetryRouteHop(t, gwconfig.Route{
			ID:         "r_0000000001",
			Name:       "r",
			Nodes:      []string{"n_0000000001", "n_0000000002"},
			Retries:    2,
			RetryCodes: "429,502",
		})

		for _, n := range nl.Nodes() {
			md := n.Options().Metadata
			if got := mdutil.GetInt(md, "retry.max"); got != 2 {
				t.Fatalf("node %s retry.max = %d, want 2", n.Name, got)
			}
			if got := mdutil.GetString(md, "retry.codes"); got != "429,502" {
				t.Fatalf("node %s retry.codes = %q, want 429,502", n.Name, got)
			}
		}
	})

	t.Run("retries disabled leaves both keys absent", func(t *testing.T) {
		nl := buildRetryRouteHop(t, gwconfig.Route{
			ID:         "r_0000000001",
			Name:       "r",
			Nodes:      []string{"n_0000000001", "n_0000000002"},
			Retries:    0,
			RetryCodes: "429,502",
		})

		for _, n := range nl.Nodes() {
			md := n.Options().Metadata
			if got := mdutil.GetInt(md, "retry.max"); got != 0 {
				t.Fatalf("node %s retry.max = %d, want 0", n.Name, got)
			}
			if got := mdutil.GetString(md, "retry.codes"); got != "" {
				t.Fatalf("node %s retry.codes = %q, want empty", n.Name, got)
			}
			if md.IsExists("retry.max") || md.IsExists("retry.codes") {
				t.Fatalf("node %s carries retry metadata with retries disabled", n.Name)
			}
		}
	})

	t.Run("empty retryCodes gets the build default", func(t *testing.T) {
		nl := buildRetryRouteHop(t, gwconfig.Route{
			ID:      "r_0000000001",
			Name:    "r",
			Nodes:   []string{"n_0000000001", "n_0000000002"},
			Retries: 1,
		})

		for _, n := range nl.Nodes() {
			md := n.Options().Metadata
			if got := mdutil.GetInt(md, "retry.max"); got != 1 {
				t.Fatalf("node %s retry.max = %d, want 1", n.Name, got)
			}
			if got := mdutil.GetString(md, "retry.codes"); got != "429,5xx" {
				t.Fatalf("node %s retry.codes = %q, want 429,5xx", n.Name, got)
			}
		}
	})
}
