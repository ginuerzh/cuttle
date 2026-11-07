package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/llm-gw/internal/stats"
)

// The test-local shapes mirror the documented JSON API without depending on
// the implementation types, so a missing route fails as a 404 rather than a
// compile error.

type gwKeyTest struct {
	Name       string `json:"name"`
	KeyPreview string `json:"keyPreview"`
	Enabled    bool   `json:"enabled"`
}

type gwKeySecretTest struct {
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

	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/gateway-keys/claude-code", `{"enabled":false}`, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable status = %d", resp.StatusCode)
	}
	keys, _ = getGatewayKeysTest(t, client, ts.URL)
	if len(keys) != 1 || keys[0].Enabled {
		t.Fatalf("after disable keys = %+v", keys)
	}

	rotateResp := doJSON(t, client, http.MethodPost, ts.URL+"/api/gateway-keys/claude-code/rotate", "", csrf)
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

	if resp := doJSON(t, client, http.MethodDelete, ts.URL+"/api/gateway-keys/claude-code", "", csrf); resp.StatusCode != http.StatusNoContent {
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

	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, err := io.ReadAll(cfgResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", string(cfgBody), csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("put config status = %d: %s", resp.StatusCode, b)
	}
	if len(rel.cfg.Gateway.Keys) != 1 {
		t.Fatalf("keys after read-modify-write = %+v, want preserved", rel.cfg.Gateway.Keys)
	}
	if got := rel.cfg.Gateway.Keys[0]; got.Name != "claude-code" || got.Key != created.Key {
		t.Fatalf("preserved key = %+v, want name claude-code and the original value", got)
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

func TestRequestsFilterByGatewayKey(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()
	for _, row := range []stats.Request{
		{ID: "a1", SID: "a1", TS: now, Provider: "p", Model: "m", GatewayKey: "alpha", Status: 200},
		{ID: "b1", SID: "b1", TS: now, Provider: "p", Model: "m", GatewayKey: "beta", Status: 200},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests?key=alpha", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var rows []stats.Request
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "a1" || rows[0].GatewayKey != "alpha" {
		t.Fatalf("rows = %+v, want only the alpha row", rows)
	}
}

func TestGatewayKeyUsageMergesConfiguredAndDeleted(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()
	for _, row := range []stats.Request{
		{ID: "a1", SID: "a1", TS: now.Add(-2 * time.Minute), Provider: "p", Model: "m", GatewayKey: "alpha", Status: 200, InputTokens: 10, OutputTokens: 2},
		{ID: "a2", SID: "a2", TS: now.Add(-time.Minute), Provider: "p", Model: "m", GatewayKey: "alpha", Status: 500, InputTokens: 20, OutputTokens: 4},
		{ID: "o1", SID: "o1", TS: now.Add(-3 * time.Minute), Provider: "p", Model: "m", GatewayKey: "old", Status: 200, InputTokens: 10, OutputTokens: 2},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	client, csrf := login(t, ts.URL)
	created, _ := createGatewayKeyTest(t, client, ts.URL, csrf, `{"name":"alpha"}`)

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
	if alpha.Name != "alpha" || !alpha.Configured || !alpha.Enabled {
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
	if old.Name != "old" || old.Configured || old.Enabled || old.KeyPreview != "" {
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
