package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// The test-local shapes mirror the documented JSON API without depending on
// the implementation types, so a missing route fails as a 404 rather than a
// compile error.

type gwKeyTest struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	KeyPreview string `json:"keyPreview"`
	Enabled    bool   `json:"enabled"`
}

type gwKeySecretTest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

type gwKeyCountsTest struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CachedTokens int64 `json:"cachedTokens"`
}

type gwKeyUsageRowTest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Configured   bool   `json:"configured"`
	Enabled      bool   `json:"enabled"`
	KeyPreview   string `json:"keyPreview"`
	Requests     int64  `json:"requests"`
	Errors       int64  `json:"errors"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedTokens int64  `json:"cachedTokens"`
}

type gwKeyUsageTest struct {
	From   string              `json:"from"`
	To     string              `json:"to"`
	Totals gwKeyCountsTest     `json:"totals"`
	Keys   []gwKeyUsageRowTest `json:"keys"`
}

func createGatewayKeyTest(t *testing.T, client *http.Client, base, csrf, body string) (gwKeySecretTest, string) {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, base+"/api/gateway-keys", body, csrf)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d: %s", resp.StatusCode, raw)
	}
	var out gwKeySecretTest
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out, string(raw)
}

func getGatewayKeysTest(t *testing.T, client *http.Client, base string) ([]gwKeyTest, string) {
	t.Helper()
	resp := doJSON(t, client, http.MethodGet, base+"/api/gateway-keys", "", "")
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d: %s", resp.StatusCode, raw)
	}
	var out []gwKeyTest
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out, string(raw)
}

func TestGatewayKeyCRUD(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"claude-code"}`)
	if created.Name != "claude-code" {
		t.Fatalf("name = %q, want claude-code", created.Name)
	}
	if !strings.HasPrefix(created.Key, "sk-gw-") || len(created.Key) <= 40 {
		t.Fatalf("generated key = %q, want sk-gw- prefix and length > 40", created.Key)
	}
	raw := created.Key
	if len(rel.cfg.Gateway.Keys) != 1 || rel.cfg.Gateway.Keys[0].Key != raw {
		t.Fatalf("reloaded keys = %+v, want the created key", rel.cfg.Gateway.Keys)
	}

	keys, listRaw := getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[0].Name != "claude-code" || !keys[0].Enabled {
		t.Fatalf("key = %+v", keys[0])
	}
	if keys[0].KeyPreview == raw || !strings.Contains(keys[0].KeyPreview, "...") {
		t.Fatalf("preview = %q, raw = %q", keys[0].KeyPreview, raw)
	}
	if strings.Contains(listRaw, raw) {
		t.Fatalf("list leaked the raw key: %s", listRaw)
	}
	if keys[0].ID != created.ID {
		t.Fatalf("view id = %q, want the created id %q", keys[0].ID, created.ID)
	}

	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, err := io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgBody), raw) {
		t.Fatalf("config leaked the raw key: %s", cfgBody)
	}
	var cfg struct {
		Gateway struct {
			KeysSet      int  `json:"keysSet"`
			AuthRequired bool `json:"authRequired"`
		} `json:"gateway"`
	}
	if err := json.Unmarshal(cfgBody, &cfg); err != nil {
		t.Fatalf("decode %s: %v", cfgBody, err)
	}
	if cfg.Gateway.KeysSet != 1 || !cfg.Gateway.AuthRequired {
		t.Fatalf("gateway config = %+v, want keysSet 1 and authRequired true", cfg.Gateway)
	}

	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/gateway-keys/"+created.ID, `{"enabled":false}`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable status = %d", resp.StatusCode)
	}
	keys, _ = getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 || keys[0].Enabled {
		t.Fatalf("after disable keys = %+v", keys)
	}

	rotateResp := doJSON(t, client, http.MethodPost, ts.URL+"/api/gateway-keys/"+created.ID+"/rotate", "", csrf)
	rotateBody, err := io.ReadAll(rotateResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if rotateResp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d: %s", rotateResp.StatusCode, rotateBody)
	}
	var rotated gwKeySecretTest
	if err := json.Unmarshal(rotateBody, &rotated); err != nil {
		t.Fatalf("decode %s: %v", rotateBody, err)
	}
	if rotated.Name != "claude-code" || !strings.HasPrefix(rotated.Key, "sk-gw-") {
		t.Fatalf("rotated = %+v", rotated)
	}
	if rotated.ID != created.ID {
		t.Fatalf("rotated id = %q, want the created id %q", rotated.ID, created.ID)
	}
	if rotated.Key == raw {
		t.Fatal("rotate returned the old key")
	}
	if len(rel.cfg.Gateway.Keys) != 1 || rel.cfg.Gateway.Keys[0].Key != rotated.Key {
		t.Fatalf("reloaded keys after rotate = %+v", rel.cfg.Gateway.Keys)
	}
	keys, listRaw = getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 || keys[0].KeyPreview == rotated.Key {
		t.Fatalf("after rotate keys = %+v", keys)
	}
	if strings.Contains(listRaw, rotated.Key) {
		t.Fatalf("list leaked the rotated key: %s", listRaw)
	}

	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/gateway-keys/"+created.ID, "", csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	keys, listRaw = getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 0 {
		t.Fatalf("after delete keys = %+v", keys)
	}
	if got := strings.TrimSpace(listRaw); got != "[]" {
		t.Fatalf("empty list body = %q, want []", got)
	}
	if len(rel.cfg.Gateway.Keys) != 0 {
		t.Fatalf("reloaded keys after delete = %+v", rel.cfg.Gateway.Keys)
	}
}

func TestGatewayKeyCreateRejectsDuplicateName(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"claude-code"}`)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/gateway-keys", `{"name":"claude-code"}`, csrf)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "already exists") {
		t.Fatalf("duplicate error = %s", body)
	}
	if strings.Contains(string(body), created.Key) {
		t.Fatalf("duplicate error leaked a key: %s", body)
	}
}

func TestGatewayKeyCRUDByID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	// The server owns the id: a client-supplied one is ignored.
	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf,
		`{"id":"gk_0000000000","name":"claude-code"}`)
	id := created.ID
	if !identity.Valid(identity.GatewayKey, id) || id == "gk_0000000000" {
		t.Fatalf("id = %q, want a generated gateway key id", id)
	}
	if created.Name != "claude-code" {
		t.Fatalf("name = %q, want claude-code", created.Name)
	}

	// PUT by id renames the display label and keeps the identity.
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/gateway-keys/"+id,
		`{"name":"laptop","enabled":true}`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rename status = %d: %s", resp.StatusCode, b)
	}
	keys, _ := getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 || keys[0].ID != id || keys[0].Name != "laptop" {
		t.Fatalf("keys after rename = %+v, want the same id labelled laptop", keys)
	}
	if got := rel.cfg.Gateway.Keys; len(got) != 1 || got[0].ID != id || got[0].Name != "laptop" {
		t.Fatalf("stored keys after rename = %+v", got)
	}

	// The display name is not a handle.
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/gateway-keys/claude-code",
		`{"enabled":false}`, csrf); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update by name = %d, want 400", resp.StatusCode)
	}

	// A body id must match the path id.
	other := identity.Derive(identity.GatewayKey, "another-key")
	resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/gateway-keys/"+id,
		`{"id":"`+other+`","enabled":false}`, csrf)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !jsonErrorContains(raw, "does not match the path") {
		t.Fatalf("mismatched id = %d: %s, want 400 naming the path mismatch", resp.StatusCode, raw)
	}

	// PUT by id disables the key and keeps its identity.
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/gateway-keys/"+id,
		`{"enabled":false}`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("disable status = %d: %s", resp.StatusCode, b)
	}
	keys, _ = getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 || keys[0].ID != id || keys[0].Name != "laptop" || keys[0].Enabled {
		t.Fatalf("keys after disable = %+v, want the same id disabled", keys)
	}

	// Rotate by id mints a new secret and keeps the id, the label and the
	// disabled state.
	rotateResp := doJSON(t, client, http.MethodPost, ts.URL+"/api/gateway-keys/"+id+"/rotate", "", csrf)
	body, err := io.ReadAll(rotateResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if rotateResp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d: %s", rotateResp.StatusCode, body)
	}
	var rotated gwKeySecretTest
	if err := json.Unmarshal(body, &rotated); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if rotated.ID != id || rotated.Name != "laptop" {
		t.Fatalf("rotated = %+v, want id %q labelled laptop", rotated, id)
	}
	if rotated.Key == created.Key || !strings.HasPrefix(rotated.Key, "sk-gw-") {
		t.Fatalf("rotated key = %q, want a fresh sk-gw- secret", rotated.Key)
	}
	if got := rel.cfg.Gateway.Keys; len(got) != 1 || got[0].ID != id ||
		got[0].Name != "laptop" || got[0].Key != rotated.Key || gwKeyEnabled(got[0]) {
		t.Fatalf("stored keys after rotate = %+v", got)
	}

	// Delete by id removes only the configured entry.
	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/gateway-keys/"+id, "", csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete status = %d: %s", resp.StatusCode, b)
	}
	if got := rel.cfg.Gateway.Keys; len(got) != 0 {
		t.Fatalf("keys after delete = %+v, want none", got)
	}
	keys, listRaw := getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 0 || strings.TrimSpace(listRaw) != "[]" {
		t.Fatalf("keys after delete = %+v (%s)", keys, listRaw)
	}
}

func TestGatewayKeyUsageJoinsByID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()

	client, csrf := login(t, ts.URL)
	alpha, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"alpha"}`)
	beta, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"beta"}`)
	retired := identity.Derive(identity.GatewayKey, "retired")
	batch := identity.Derive(identity.GatewayKey, "old-batch")

	for _, row := range []stats.Request{
		{ID: "a1", SID: "a1", TS: now.Add(-3 * time.Minute), Provider: "p", Model: "m", GatewayKey: alpha.ID, Status: 200, InputTokens: 10, OutputTokens: 2},
		{ID: "r1", SID: "r1", TS: now.Add(-2 * time.Minute), Provider: "p", Model: "m", GatewayKey: retired, Status: 200, InputTokens: 5, OutputTokens: 1},
		{ID: "b1", SID: "b1", TS: now.Add(-time.Minute), Provider: "p", Model: "m", GatewayKey: batch, Status: 200, InputTokens: 7, OutputTokens: 3},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	// The store keeps every label the config ever synced, including the keys
	// that were deleted since. The configured key's stored label is stale on
	// purpose: the current config wins.
	if err := rel.store.SyncLabels(context.Background(), map[string]map[string]string{
		"gateway_key": {
			alpha.ID: "alpha-renamed-elsewhere",
			retired:  "retired-laptop",
			batch:    "batch-one",
		},
	}); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/gateway-key-usage", "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got gwKeyUsageTest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	// Configured keys keep configuration order with their current names; the
	// deleted keys follow alphabetically by their last stored label.
	if len(got.Keys) != 4 {
		t.Fatalf("keys = %+v, want two configured and two deleted rows", got.Keys)
	}
	if k := got.Keys[0]; k.ID != alpha.ID || k.Name != "alpha" || !k.Configured {
		t.Fatalf("first key = %+v, want the configured alpha %q", k, alpha.ID)
	}
	if k := got.Keys[1]; k.ID != beta.ID || k.Name != "beta" || !k.Configured || k.Requests != 0 {
		t.Fatalf("second key = %+v, want the configured, unused beta %q", k, beta.ID)
	}
	if k := got.Keys[2]; k.ID != batch || k.Name != "batch-one" || k.Configured || k.Requests != 1 {
		t.Fatalf("third key = %+v, want the deleted batch-one %q", k, batch)
	}
	if k := got.Keys[3]; k.ID != retired || k.Name != "retired-laptop" || k.Configured || k.Requests != 1 {
		t.Fatalf("fourth key = %+v, want the deleted retired-laptop %q", k, retired)
	}
	if got.Totals.Requests != 3 || got.Totals.InputTokens != 22 || got.Totals.OutputTokens != 6 {
		t.Fatalf("totals = %+v", got.Totals)
	}
}

func TestGatewayKeyCreateGeneratesUniqueKeys(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	first, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"first"}`)
	second, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"second"}`)
	if first.Key == second.Key {
		t.Fatalf("generated the same key twice: %q", first.Key)
	}
}

func TestPutConfigPreservesGatewayKeys(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"claude-code"}`)

	// Seed a provider key. GET /api/config must not echo it and PUT must
	// preserve it when the decoded field is absent.
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers",
		`{"name":"upstream","type":"generic","chat":"https://example.test/v1","keys":[{"name":"key-1","key":"sk-provider-secret"}]}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider status = %d: %s", resp.StatusCode, b)
	}

	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, err := io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgBody), "sk-provider-secret") {
		t.Fatalf("config leaked the provider key: %s", cfgBody)
	}

	// Round-trip the GET body with custom gateway fields the view exposes.
	var roundTrip config.Config
	if err := json.Unmarshal(cfgBody, &roundTrip); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	roundTrip.Gateway.ReadTimeout = config.Duration(90 * time.Second)
	roundTrip.Gateway.UserAgent = "cuttle-test-agent/1.0"
	putBody, err := json.Marshal(&roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", string(putBody), csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put config status = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Gateway.Keys) != 1 {
		t.Fatalf("keys after read-modify-write = %+v, want preserved", rel.cfg.Gateway.Keys)
	}
	if got := rel.cfg.Gateway.Keys[0]; got.Name != "claude-code" || got.Key != created.Key {
		t.Fatalf("preserved key = %+v, want name claude-code and the original value", got)
	}
	if len(rel.cfg.Providers) != 1 || len(rel.cfg.Providers[0].Keys) != 1 ||
		rel.cfg.Providers[0].Keys[0].Key != "sk-provider-secret" {
		t.Fatalf("providers after read-modify-write = %+v, want the provider key preserved", rel.cfg.Providers)
	}
	if rel.cfg.Gateway.ReadTimeout != config.Duration(90*time.Second) || rel.cfg.Gateway.UserAgent != "cuttle-test-agent/1.0" {
		t.Fatalf("gateway after read-modify-write = %+v, want readTimeout 90s and the custom userAgent", rel.cfg.Gateway)
	}

	// The view must expose the custom fields and still hide the key.
	cfgResp = doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, err = io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"readTimeout":"1m30s"`, `"userAgent":"cuttle-test-agent/1.0"`, `"keysSet":1`, `"authRequired":true`} {
		if !strings.Contains(string(cfgBody), want) {
			t.Fatalf("config = %s, want it to contain %s", cfgBody, want)
		}
	}
	if strings.Contains(string(cfgBody), "sk-provider-secret") {
		t.Fatalf("config leaked the provider key: %s", cfgBody)
	}
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", string(cfgBody), csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("second put config status = %d: %s", resp.StatusCode, b)
	}
	if rel.cfg.Gateway.ReadTimeout != config.Duration(90*time.Second) || rel.cfg.Gateway.UserAgent != "cuttle-test-agent/1.0" {
		t.Fatalf("gateway after second round-trip = %+v", rel.cfg.Gateway)
	}
	if len(rel.cfg.Providers) != 1 || len(rel.cfg.Providers[0].Keys) != 1 ||
		rel.cfg.Providers[0].Keys[0].Key != "sk-provider-secret" {
		t.Fatalf("providers after second round-trip = %+v", rel.cfg.Providers)
	}

	clearBody := `{"gateway":{"listen":"127.0.0.1:0","keys":[]}}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", clearBody, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("clear config status = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Gateway.Keys) != 0 {
		t.Fatalf("keys after explicit [] = %+v, want none", rel.cfg.Gateway.Keys)
	}

	replaceBody := `{"gateway":{"listen":"127.0.0.1:0","keys":[{"name":"claude-code","key":"replacement-secret","enabled":true}]}}`
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", replaceBody, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("replace config status = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Gateway.Keys) != 1 || rel.cfg.Gateway.Keys[0].Key != "replacement-secret" {
		t.Fatalf("keys after replacement = %+v", rel.cfg.Gateway.Keys)
	}

	cfgResp = doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, err = io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgBody), "replacement-secret") {
		t.Fatalf("config leaked the replacement key: %s", cfgBody)
	}
	keys, listRaw := getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 || keys[0].KeyPreview == "replacement-secret" {
		t.Fatalf("keys = %+v", keys)
	}
	if strings.Contains(listRaw, "replacement-secret") {
		t.Fatalf("list leaked the replacement key: %s", listRaw)
	}
}

// TestPutConfigAdoptsStoredGatewayKeyID pins the identity rule for a full
// config replacement: an id-less gateway key matched by name keeps the stored
// id, so its usage history stays attached, and only a new name is minted.
func TestPutConfigAdoptsStoredGatewayKeyID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"claude-code"}`)
	putConfig(t, client, ts.URL, csrf, `{"gateway":{"listen":"127.0.0.1:0","keys":[`+
		`{"name":"claude-code","key":"`+created.Key+`"},`+
		`{"name":"second","key":"another-secret-value"}]}}`)

	keys := rel.cfg.Gateway.Keys
	if len(keys) != 2 {
		t.Fatalf("keys after the replacement = %+v, want two", keys)
	}
	if keys[0].ID != created.ID || keys[0].Name != "claude-code" || keys[0].Key != created.Key {
		t.Fatalf("adopted key = %+v, want the stored id %q kept", keys[0], created.ID)
	}
	if !identity.Valid(identity.GatewayKey, keys[1].ID) || keys[1].ID == created.ID {
		t.Fatalf("new key id = %q, want a freshly minted gateway key id", keys[1].ID)
	}
}

func TestPutConfigRejectsDollarInGatewayKey(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"keep"}`)

	body := `{"gateway":{"listen":"127.0.0.1:0","keys":[{"name":"env","key":"${MISSING}"}]}}`
	resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", body, csrf)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "must not contain '$'") {
		t.Fatalf("error = %s, want the '$' restriction", raw)
	}
	if strings.Contains(string(raw), "${MISSING}") {
		t.Fatalf("error leaked the gateway key value: %s", raw)
	}
	if len(rel.cfg.Gateway.Keys) != 1 || rel.cfg.Gateway.Keys[0].Key != created.Key {
		t.Fatalf("keys after rejected update = %+v, want the original key", rel.cfg.Gateway.Keys)
	}
}

func TestRequestsFilterByGatewayKey(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()
	alphaID := identity.Derive(identity.GatewayKey, "alpha")
	betaID := identity.Derive(identity.GatewayKey, "beta")
	for _, row := range []stats.Request{
		{ID: "a1", SID: "a1", TS: now, Provider: "p", Model: "m", GatewayKey: alphaID, Status: 200},
		{ID: "b1", SID: "b1", TS: now, Provider: "p", Model: "m", GatewayKey: betaID, Status: 200},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?key="+alphaID, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var rows []stats.Request
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "a1" || rows[0].GatewayKey != alphaID {
		t.Fatalf("rows = %+v, want only the alpha row", rows)
	}
}

func TestGatewayKeyUsageMergesConfiguredAndDeleted(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()

	client, csrf := login(t, ts.URL)
	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"alpha"}`)
	retired := identity.Derive(identity.GatewayKey, "old")
	for _, row := range []stats.Request{
		{ID: "a1", SID: "a1", TS: now.Add(-2 * time.Minute), Provider: "p", Model: "m", GatewayKey: created.ID, Status: 200, InputTokens: 10, OutputTokens: 2},
		{ID: "a2", SID: "a2", TS: now.Add(-time.Minute), Provider: "p", Model: "m", GatewayKey: created.ID, Status: 500, InputTokens: 20, OutputTokens: 4},
		{ID: "o1", SID: "o1", TS: now.Add(-3 * time.Minute), Provider: "p", Model: "m", GatewayKey: retired, Status: 200, InputTokens: 10, OutputTokens: 2},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	// The store keeps the last known label of a key the config no longer lists.
	if err := rel.store.SyncLabels(context.Background(), map[string]map[string]string{
		"gateway_key": {retired: "old"},
	}); err != nil {
		t.Fatal(err)
	}

	from := now.Add(-time.Hour).Format(time.RFC3339)
	to := now.Add(time.Hour).Format(time.RFC3339)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/gateway-key-usage?from="+from+"&to="+to, "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got gwKeyUsageTest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.Keys) != 2 {
		t.Fatalf("keys = %+v", got.Keys)
	}
	alpha := got.Keys[0]
	if alpha.ID != created.ID || alpha.Name != "alpha" || !alpha.Configured || !alpha.Enabled {
		t.Fatalf("alpha = %+v", alpha)
	}
	if alpha.KeyPreview == "" || alpha.KeyPreview == created.Key {
		t.Fatalf("alpha preview = %q", alpha.KeyPreview)
	}
	if alpha.Requests != 2 || alpha.Errors != 1 || alpha.InputTokens != 30 ||
		alpha.OutputTokens != 6 || alpha.CachedTokens != 0 {
		t.Fatalf("alpha counters = %+v", alpha)
	}
	old := got.Keys[1]
	if old.ID != retired || old.Name != "old" || old.Configured || old.Enabled || old.KeyPreview != "" {
		t.Fatalf("old = %+v", old)
	}
	if old.Requests != 1 || old.Errors != 0 || old.InputTokens != 10 ||
		old.OutputTokens != 2 || old.CachedTokens != 0 {
		t.Fatalf("old counters = %+v", old)
	}
	if got.Totals.Requests != 3 || got.Totals.Errors != 1 || got.Totals.InputTokens != 40 ||
		got.Totals.OutputTokens != 8 || got.Totals.CachedTokens != 0 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if got.From == "" || got.To == "" {
		t.Fatalf("range = %q..%q", got.From, got.To)
	}
}

func TestGatewayKeyUsageConfiguredZeroUsage(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"alpha"}`)
	createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"idle","enabled":false}`)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/gateway-key-usage", "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got gwKeyUsageTest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.Keys) != 2 {
		t.Fatalf("keys = %+v", got.Keys)
	}
	if got.Keys[0].Name != "alpha" || !got.Keys[0].Configured || !got.Keys[0].Enabled {
		t.Fatalf("alpha = %+v", got.Keys[0])
	}
	idle := got.Keys[1]
	if idle.Name != "idle" || !idle.Configured || idle.Enabled {
		t.Fatalf("idle = %+v", idle)
	}
	if idle.Requests != 0 || idle.Errors != 0 || idle.InputTokens != 0 ||
		idle.OutputTokens != 0 || idle.CachedTokens != 0 {
		t.Fatalf("idle counters = %+v", idle)
	}
	if got.Totals.Requests != 0 {
		t.Fatalf("totals = %+v", got.Totals)
	}
}

func TestGatewayKeyUsageStatsDisabled(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	srv.stats = nil

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/gateway-key-usage", "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if got["error"] != "stats disabled" {
		t.Fatalf("error = %q", got["error"])
	}
}
