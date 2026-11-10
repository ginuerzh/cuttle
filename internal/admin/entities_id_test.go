package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
	"github.com/ginuerzh/cuttle/internal/identity"
)

// listNodes returns every node the API lists.
func listNodes(t *testing.T, client *http.Client, base string) []config.Node {
	t.Helper()

	resp := doJSON(t, client, http.MethodGet, base+"/api/nodes", "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("list nodes = %d: %s", resp.StatusCode, b)
	}
	var out []config.Node
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// nodeID returns the id of the node with the display name.
func nodeID(t *testing.T, client *http.Client, base, name string) string {
	t.Helper()

	for _, n := range listNodes(t, client, base) {
		if n.Name == name {
			return n.ID
		}
	}
	t.Fatalf("node %q is not listed", name)
	return ""
}

// listRoutes returns every route the API lists.
func listRoutes(t *testing.T, client *http.Client, base string) []config.Route {
	t.Helper()

	resp := doJSON(t, client, http.MethodGet, base+"/api/routes", "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("list routes = %d: %s", resp.StatusCode, b)
	}
	var out []config.Route
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// routeID returns the id of the route with the display name.
func routeID(t *testing.T, client *http.Client, base, name string) string {
	t.Helper()

	for _, r := range listRoutes(t, client, base) {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("route %q is not listed", name)
	return ""
}

// listEntrypoints returns the ordered entrypoint list.
func listEntrypoints(t *testing.T, client *http.Client, base string) []config.Entrypoint {
	t.Helper()

	resp := doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("list entrypoints = %d: %s", resp.StatusCode, b)
	}
	var out []config.Entrypoint
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// mustCreate posts a body and fails unless the endpoint answers 201.
func mustCreate(t *testing.T, client *http.Client, url, body, csrf string) {
	t.Helper()

	if resp := doJSON(t, client, http.MethodPost, url, body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create %s = %d: %s", url, resp.StatusCode, b)
	}
}

// createNodeTest creates a node on the provider id and returns the node id.
func createNodeTest(t *testing.T, client *http.Client, base, csrf, providerID, name string) string {
	t.Helper()

	mustCreate(t, client, base+"/api/nodes",
		`{"name":"`+name+`","provider":"`+providerID+`","model":"m1"}`, csrf)
	return nodeID(t, client, base, name)
}

func TestNodeCRUDByIDAndRename(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, base)
	pid := createProviderTest(t, client, base, csrf, providerBody("p", "https://a.example"), "p")

	// The server owns the id: a client-supplied one is ignored.
	mustCreate(t, client, base+"/api/nodes",
		`{"id":"n_0000000000","name":"alpha","provider":"`+pid+`","model":"m1"}`, csrf)
	nodes := listNodes(t, client, base)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want one", nodes)
	}
	id := nodes[0].ID
	if !identity.Valid(identity.Node, id) || id == "n_0000000000" {
		t.Fatalf("id = %q, want a generated node id", id)
	}
	if nodes[0].Name != "alpha" || nodes[0].Provider != pid {
		t.Fatalf("node = %+v, want alpha on provider %q", nodes[0], pid)
	}

	// PUT by id renames the node and keeps the id and the provider id.
	rename := `{"name":"beta","provider":"` + pid + `","model":"m1"}`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/nodes/"+id, rename, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update by id = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Nodes; len(got) != 1 || got[0].ID != id || got[0].Name != "beta" || got[0].Provider != pid {
		t.Fatalf("stored nodes = %+v, want the same id renamed to beta", got)
	}

	// A body id must match the path id.
	other := identity.Derive(identity.Node, "another-node")
	mismatch := `{"id":"` + other + `","name":"beta","provider":"` + pid + `","model":"m1"}`
	resp := doJSON(t, client, http.MethodPut, base+"/api/nodes/"+id, mismatch, csrf)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched id = %d: %s, want 400", resp.StatusCode, raw)
	}
	if !jsonErrorContains(raw, "does not match the path") {
		t.Fatalf("error = %s, want it to name the path mismatch", raw)
	}

	// Writes are addressed by id; the display name is not a handle.
	if resp := doJSON(t, client, http.MethodPut, base+"/api/nodes/beta", rename, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update by name = %d, want 400", resp.StatusCode)
	}

	// A node referenced by a route cannot be deleted; the error names both
	// the node id and label and the referencing route.
	rid := createNodeBackedRoute(t, client, base, csrf, "fast", id)

	// A second rename after the route exists keeps the id and the reference.
	if resp := doJSON(t, client, http.MethodPut, base+"/api/nodes/"+id,
		`{"name":"gamma","provider":"`+pid+`","model":"m1"}`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("second rename = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Nodes; len(got) != 1 || got[0].ID != id || got[0].Name != "gamma" {
		t.Fatalf("nodes after the second rename = %+v", got)
	}
	if got := rel.cfg.Routes; len(got) != 1 || len(got[0].Nodes) != 1 || got[0].Nodes[0] != id {
		t.Fatalf("routes after the node rename = %+v, want the node id kept", got)
	}

	resp = doJSON(t, client, http.MethodDelete, base+"/api/nodes/"+id, "", csrf)
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete referenced node = %d, want 400: %s", resp.StatusCode, raw)
	}
	for _, want := range []string{id, "gamma", rid, "fast"} {
		if !jsonErrorContains(raw, want) {
			t.Fatalf("error = %s, want it to name %q", raw, want)
		}
	}
	if got := len(rel.cfg.Nodes); got != 1 {
		t.Fatalf("nodes after a rejected delete = %d, want 1", got)
	}

	if resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/"+rid, "", csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete route = %d: %s", resp.StatusCode, b)
	}
	if resp := doJSON(t, client, http.MethodDelete, base+"/api/nodes/"+id, "", csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete node = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Nodes) != 0 {
		t.Fatalf("nodes after delete = %+v", rel.cfg.Nodes)
	}
	if resp := doJSON(t, client, http.MethodGet, base+"/api/nodes/"+id, "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted node = %d, want 404", resp.StatusCode)
	}
}

// TestAdminNodeRejectsNameReference proves the admin API no longer accepts a
// display name where an entity id belongs: the payload is rejected and the
// error names the id the request should carry.
func TestAdminNodeRejectsNameReference(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, base)
	pid := createProviderTest(t, client, base, csrf, providerBody("p", "https://a.example"), "p")
	nid := createNodeTest(t, client, base, csrf, pid, "n")

	resp := doJSON(t, client, http.MethodPut, base+"/api/nodes/"+nid,
		`{"name":"n","provider":"p","model":"m1"}`, csrf)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update with a provider name = %d: %s, want 400", resp.StatusCode, raw)
	}
	if !jsonErrorContains(raw, "provider id") || !jsonErrorContains(raw, pid) {
		t.Fatalf("error = %s, want it to name the expected provider id %q", raw, pid)
	}
	if got := rel.cfg.Nodes; len(got) != 1 || got[0].Provider != pid || got[0].Name != "n" {
		t.Fatalf("nodes after a rejected update = %+v, want the stored provider id kept", got)
	}
}

// createNodeBackedRoute creates a route that references the node id.
func createNodeBackedRoute(t *testing.T, client *http.Client, base, csrf, name, nodeID string) string {
	t.Helper()

	mustCreate(t, client, base+"/api/routes",
		`{"name":"`+name+`","nodes":["`+nodeID+`"],"strategy":"round"}`, csrf)
	return routeID(t, client, base, name)
}

func TestRouteCRUDByIDAndRename(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, base)
	pid := createProviderTest(t, client, base, csrf, providerBody("p", "https://a.example"), "p")
	nid := createNodeTest(t, client, base, csrf, pid, "n")

	// The server owns the id: a client-supplied one is ignored and the node
	// reference is the node id.
	mustCreate(t, client, base+"/api/routes",
		`{"id":"r_0000000000","name":"fast","nodes":["`+nid+`"],"strategy":"round"}`, csrf)
	routes := listRoutes(t, client, base)
	if len(routes) != 1 {
		t.Fatalf("routes = %+v, want one", routes)
	}
	id := routes[0].ID
	if !identity.Valid(identity.Route, id) || id == "r_0000000000" {
		t.Fatalf("id = %q, want a generated route id", id)
	}
	if len(routes[0].Nodes) != 1 || routes[0].Nodes[0] != nid {
		t.Fatalf("route nodes = %+v, want the node id %q", routes[0].Nodes, nid)
	}

	// An entrypoint references the route by id before the rename.
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"default","match":["*"],"route":"`+id+`"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put entrypoints = %d: %s", resp.StatusCode, b)
	}
	eid := listEntrypoints(t, client, base)[0].ID

	// PUT by id renames the route and keeps every reference.
	rename := `{"name":"renamed","nodes":["` + nid + `"],"strategy":"round"}`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/routes/"+id, rename, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update by id = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Routes; len(got) != 1 || got[0].ID != id || got[0].Name != "renamed" ||
		len(got[0].Nodes) != 1 || got[0].Nodes[0] != nid {
		t.Fatalf("stored routes = %+v, want the same id renamed to renamed", got)
	}
	if got := rel.cfg.Entrypoints; len(got) != 1 || got[0].Route != id {
		t.Fatalf("entrypoints after the route rename = %+v, want the route id kept", got)
	}

	// A body id must match the path id.
	other := identity.Derive(identity.Route, "another-route")
	mismatch := `{"id":"` + other + `","name":"renamed","nodes":["` + nid + `"]}`
	resp := doJSON(t, client, http.MethodPut, base+"/api/routes/"+id, mismatch, csrf)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !jsonErrorContains(raw, "does not match the path") {
		t.Fatalf("mismatched id = %d: %s, want 400 naming the path mismatch", resp.StatusCode, raw)
	}

	// Writes are addressed by id; the display name is not a handle.
	if resp := doJSON(t, client, http.MethodPut, base+"/api/routes/renamed", rename, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update by name = %d, want 400", resp.StatusCode)
	}

	// A route referenced by an entrypoint cannot be deleted; the error names
	// both the route id and label and the referencing entrypoint.
	resp = doJSON(t, client, http.MethodDelete, base+"/api/routes/"+id, "", csrf)
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete referenced route = %d, want 400: %s", resp.StatusCode, raw)
	}
	for _, want := range []string{id, "renamed", eid, "default"} {
		if !jsonErrorContains(raw, want) {
			t.Fatalf("error = %s, want it to name %q", raw, want)
		}
	}
	if got := len(rel.cfg.Routes); got != 1 {
		t.Fatalf("routes after a rejected delete = %d, want 1", got)
	}

	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", `[]`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear entrypoints = %d", resp.StatusCode)
	}
	if resp := doJSON(t, client, http.MethodDelete, base+"/api/routes/"+id, "", csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete route = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Routes) != 0 {
		t.Fatalf("routes after delete = %+v", rel.cfg.Routes)
	}
}

func TestPutEntrypointsAssignsIDsAndPreservesThem(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, base)
	pid := createProviderTest(t, client, base, csrf, providerBody("p", "https://a.example"), "p")
	nid := createNodeTest(t, client, base, csrf, pid, "n")
	fastID := createNodeBackedRoute(t, client, base, csrf, "fast", nid)
	slowID := createNodeBackedRoute(t, client, base, csrf, "slow", nid)

	// A fresh entry receives a generated entrypoint id.
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"fast","match":["gpt-4.5*"],"route":"`+fastID+`"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put entrypoints = %d: %s", resp.StatusCode, b)
	}
	list := listEntrypoints(t, client, base)
	if len(list) != 1 {
		t.Fatalf("entrypoints = %+v, want one", list)
	}
	eid := list[0].ID
	if !identity.Valid(identity.Entrypoint, eid) {
		t.Fatalf("entrypoint id = %q, want a generated e_ id", eid)
	}
	if list[0].Route != fastID {
		t.Fatalf("entrypoint route = %q, want the route id %q", list[0].Route, fastID)
	}

	// A later PUT keeps an id that is sent, mints one for a new entry and
	// keeps declaration order.
	body := `[{"name":"slow","match":["slow*"],"route":"` + slowID + `"},` +
		`{"id":"` + eid + `","name":"fast","match":["gpt-4.5*"],"route":"` + fastID + `"}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", body, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("second put entrypoints = %d: %s", resp.StatusCode, b)
	}
	list = listEntrypoints(t, client, base)
	if len(list) != 2 || list[0].Name != "slow" || list[1].Name != "fast" {
		t.Fatalf("entrypoints = %+v, want slow then fast", list)
	}
	if list[1].ID != eid {
		t.Fatalf("kept id = %q, want %q", list[1].ID, eid)
	}
	if list[0].ID == "" || !identity.Valid(identity.Entrypoint, list[0].ID) || list[0].ID == eid {
		t.Fatalf("new entry id = %q, want a fresh e_ id", list[0].ID)
	}
	slowEID := list[0].ID

	// An id-less entry matched by name adopts the stored id, so a rename of
	// that entry keeps its identity and its position.
	renamed := `[{"name":"slowest","match":["slow*"],"route":"` + slowID + `"},` +
		`{"name":"fast","match":["gpt-4.5*"],"route":"` + fastID + `"}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", renamed, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("third put entrypoints = %d: %s", resp.StatusCode, b)
	}
	list = listEntrypoints(t, client, base)
	if len(list) != 2 || list[0].Name != "slowest" || list[1].Name != "fast" {
		t.Fatalf("entrypoints = %+v, want slowest then fast", list)
	}
	if list[1].ID != eid {
		t.Fatalf("id after an id-less replacement = %q, want %q", list[1].ID, eid)
	}
	if list[0].ID == slowEID || !identity.Valid(identity.Entrypoint, list[0].ID) {
		t.Fatalf("renamed id-less entry id = %q, want a fresh id rather than %q", list[0].ID, slowEID)
	}
	if got := rel.cfg.Entrypoints; len(got) != 2 || got[1].ID != eid {
		t.Fatalf("stored entrypoints = %+v, want the kept id", got)
	}
}

func TestRouteHealthIDsExposed(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()
	routeID := identity.Derive(identity.Route, "deepseek-flash")
	nodeID := identity.Derive(identity.Node, "cc-dsf")
	providerID := identity.Derive(identity.Provider, "commandcode")
	rel.health = []health.Route{{
		ID:          routeID,
		Name:        "deepseek-flash",
		MaxFails:    2,
		FailTimeout: "30s",
		Nodes: []health.Node{{
			ID:          nodeID,
			Name:        "cc-dsf",
			ProviderID:  providerID,
			FailCount:   1,
			LastFailure: &now,
			Dead:        true,
			DeadUntil:   &now,
		}},
	}}

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/routes/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Nodes []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			ProviderID  string `json:"providerId"`
			FailCount   int64  `json:"failCount"`
			LastFailure string `json:"lastFailure"`
			Dead        bool   `json:"dead"`
			DeadUntil   string `json:"deadUntil"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != routeID || got[0].Name != "deepseek-flash" {
		t.Fatalf("health = %+v, want route id %q labelled deepseek-flash", got, routeID)
	}
	if len(got[0].Nodes) != 1 {
		t.Fatalf("health nodes = %+v", got[0].Nodes)
	}
	node := got[0].Nodes[0]
	if node.ID != nodeID || node.Name != "cc-dsf" || node.ProviderID != providerID {
		t.Fatalf("node health = %+v, want id/providerId exposed beside the label", node)
	}
	if !node.Dead || node.FailCount != 1 || node.LastFailure == "" || node.DeadUntil == "" {
		t.Fatalf("node health = %+v, want the marker fields preserved", node)
	}
}

func TestCaptureRuleUsesRouteID(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, base)
	pid := createProviderTest(t, client, base, csrf, providerBody("p", "https://a.example"), "p")
	nid := createNodeTest(t, client, base, csrf, pid, "n")
	rid := createNodeBackedRoute(t, client, base, csrf, "fast", nid)

	body := `{"route":"` + rid + `","clientModel":"claude-*","reason":"investigate","limit":5,"ttlSeconds":60}`
	resp := doJSON(t, client, http.MethodPost, base+"/api/captures", body, csrf)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create rule with the route id = %d: %s", resp.StatusCode, b)
	}
	var rule struct {
		ID    string `json:"id"`
		Route string `json:"route"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if rule.Route != rid {
		t.Fatalf("rule route = %q, want the route id %q", rule.Route, rid)
	}

	// The recorder records route ids, so the display name is not a rule key.
	resp = doJSON(t, client, http.MethodPost, base+"/api/captures",
		`{"route":"fast","reason":"bad","limit":1,"ttlSeconds":60}`, csrf)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create rule with a route name = %d, want 400: %s", resp.StatusCode, raw)
	}
	if !jsonErrorContains(raw, "does not exist") {
		t.Fatalf("error = %s, want it to say the route does not exist", raw)
	}

	resp = doJSON(t, client, http.MethodPost, base+"/api/captures",
		`{"route":"r_0000000000","reason":"bad","limit":1,"ttlSeconds":60}`, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create rule with an unknown route id = %d, want 400", resp.StatusCode)
	}
}

// jsonErrorContains reports whether a JSON error body contains want.
func jsonErrorContains(raw []byte, want string) bool {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	return strings.Contains(body.Error, want)
}

func TestPutConfigAdoptsStoredEntityIDs(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	base := ts.URL
	client, csrf := login(t, base)
	pid := createProviderTest(t, client, base, csrf, providerBody("p", "https://a.example"), "p")
	nid := createNodeTest(t, client, base, csrf, pid, "n")
	rid := createNodeBackedRoute(t, client, base, csrf, "fast", nid)
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints",
		`[{"name":"default","match":["*"],"route":"`+rid+`"}]`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put entrypoints = %d: %s", resp.StatusCode, b)
	}
	eid := listEntrypoints(t, client, base)[0].ID

	// The payload carries no ids at all. Every stored identity must survive,
	// matched by display name, so a rename cannot detach history.
	provider := `{"name":"p","type":"generic","chat":"https://a.example"}`
	cfg := `{"providers":[` + provider + `],"nodes":[` +
		`{"name":"n","provider":"` + pid + `","model":"m1"}],` +
		`"routes":[{"name":"fast","nodes":["` + nid + `"]}],` +
		`"entrypoints":[{"name":"default","match":["*"],"route":"` + rid + `"}]}`
	putConfig(t, client, base, csrf, cfg)

	if got := rel.cfg.Nodes; len(got) != 1 || got[0].ID != nid {
		t.Fatalf("nodes after an id-less PUT /api/config = %+v, want the stored id %q", got, nid)
	}
	if got := rel.cfg.Routes; len(got) != 1 || got[0].ID != rid {
		t.Fatalf("routes after an id-less PUT /api/config = %+v, want the stored id %q", got, rid)
	}
	if got := rel.cfg.Entrypoints; len(got) != 1 || got[0].ID != eid {
		t.Fatalf("entrypoints after an id-less PUT /api/config = %+v, want the stored id %q", got, eid)
	}

	// The adopted identities are addressable for writes.
	if resp := doJSON(t, client, http.MethodPut, base+"/api/nodes/"+nid,
		`{"name":"n2","provider":"`+pid+`","model":"m1"}`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rename the adopted node = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Nodes[0]; got.ID != nid || got.Name != "n2" {
		t.Fatalf("node after the rename = %+v, want the same id renamed to n2", got)
	}

	// A genuinely new entity receives a minted id. Names are labels, so the
	// payload keeps every reference an id: each new entity is added beside
	// the stored topology it references.
	cfg = `{"providers":[` + provider + `,` +
		`{"name":"fresh","type":"generic","chat":"https://b.example"}],` +
		`"nodes":[{"name":"n2","provider":"` + pid + `","model":"m1"},` +
		`{"name":"fresh","provider":"` + pid + `","model":"m2"}],` +
		`"routes":[{"name":"fast","nodes":["` + nid + `"]},` +
		`{"name":"fresh","nodes":["` + nid + `"]}],` +
		`"entrypoints":[{"name":"default","match":["*"],"route":"` + rid + `"},` +
		`{"name":"fresh","match":["fresh-*"],"route":"` + rid + `"}]}`
	putConfig(t, client, base, csrf, cfg)

	if got := rel.cfg.Providers; len(got) != 2 || got[0].ID != pid {
		t.Fatalf("providers after the second PUT = %+v, want the stored id %q kept", got, pid)
	}
	if got := rel.cfg.Nodes; len(got) != 2 || got[0].ID != nid {
		t.Fatalf("nodes after the second PUT = %+v, want the stored id %q kept", got, nid)
	}
	if got := rel.cfg.Routes; len(got) != 2 || got[0].ID != rid {
		t.Fatalf("routes after the second PUT = %+v, want the stored id %q kept", got, rid)
	}
	if got := rel.cfg.Entrypoints; len(got) != 2 || got[0].ID != eid {
		t.Fatalf("entrypoints after the second PUT = %+v, want the stored id %q kept", got, eid)
	}

	fresh := []struct {
		kind string
		want identity.Kind
		id   string
	}{
		{"provider", identity.Provider, rel.cfg.Providers[1].ID},
		{"node", identity.Node, rel.cfg.Nodes[1].ID},
		{"route", identity.Route, rel.cfg.Routes[1].ID},
		{"entrypoint", identity.Entrypoint, rel.cfg.Entrypoints[1].ID},
	}
	for _, got := range fresh {
		if got.id == "" || !identity.Valid(got.want, got.id) {
			t.Fatalf("%s id = %q, want a minted %s id", got.kind, got.id, got.kind)
		}
		for _, stored := range []string{pid, nid, rid, eid} {
			if got.id == stored {
				t.Fatalf("%s reused a stored id %q for a new entity", got.kind, got.id)
			}
		}
	}
}
