package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

func TestDraftNodeAndEntrypointAreAccepted(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	route := routeID(t, client, base, "fast")

	// A node with no provider and no model is a draft.
	if resp := doJSON(t, client, http.MethodPost, base+"/api/nodes", `{"name":"draft-node"}`, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create draft node = %d, want 201", resp.StatusCode)
	}

	// An entrypoint with no route is a draft, in declaration order.
	body := `[{"name":"draft-entry","match":["mystery*"]},{"name":"fast","match":["gpt-4.5*"],"route":"` + route + `"}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", body, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put draft entrypoint = %d, want 204", resp.StatusCode)
	}

	// GET /api/config must show the drafts and round-trip through PUT unchanged.
	cfgResp := doJSON(t, client, http.MethodGet, base+"/api/config", "", "")
	cfgBody, err := io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, client, http.MethodPut, base+"/api/config", string(cfgBody), csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put config with drafts = %d, want 204", resp.StatusCode)
	}

	cfgResp = doJSON(t, client, http.MethodGet, base+"/api/config", "", "")
	var view struct {
		Entrypoints []config.Entrypoint `json:"entrypoints"`
		Nodes       []config.Node       `json:"nodes"`
	}
	if err := json.NewDecoder(cfgResp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	var draftNode *config.Node
	for i := range view.Nodes {
		if view.Nodes[i].Name == "draft-node" {
			draftNode = &view.Nodes[i]
		}
	}
	if draftNode == nil || draftNode.Provider != "" || draftNode.Model != "" {
		t.Fatalf("draft node = %+v, want an empty provider and model", draftNode)
	}
	if len(view.Entrypoints) == 0 || view.Entrypoints[0].Name != "draft-entry" || view.Entrypoints[0].Route != "" {
		t.Fatalf("entrypoints = %+v, want an unconnected draft first", view.Entrypoints)
	}
}
