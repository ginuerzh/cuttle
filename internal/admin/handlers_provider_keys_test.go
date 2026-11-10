package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

func createNamedProvider(t *testing.T, client *http.Client, base, csrf, body string) {
	t.Helper()
	if resp := doJSON(t, client, http.MethodPost, base+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
}

func seedNamedProvider(t *testing.T, client *http.Client, base, csrf string) {
	t.Helper()
	createNamedProvider(t, client, base, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"a","key":"sk-a"},{"name":"b","key":"sk-b"}]}`)
}

func updateNamedKeys(t *testing.T, client *http.Client, base, csrf, updates string) *http.Response {
	t.Helper()
	body := `{"name":"p","type":"generic","chat":"https://a.example","keyUpdates":` + updates + `}`
	return doJSON(t, client, http.MethodPut, base+"/api/providers/p", body, csrf)
}

func TestProviderViewReturnsNamedMaskedKeys(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	createNamedProvider(t, client, ts.URL, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"account-a","key":"sk-aaaa1111"},{"name":"account-b","key":"sk-bbbb2222","enabled":false}]}`)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/providers/p", "", "")
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get provider = %d: %s", resp.StatusCode, b)
	}
	if strings.Contains(string(b), "sk-aaaa1111") || strings.Contains(string(b), "sk-bbbb2222") {
		t.Fatalf("provider leaked a raw key: %s", b)
	}
	var view struct {
		Keys []struct {
			Name       string `json:"name"`
			KeyPreview string `json:"keyPreview"`
			Enabled    bool   `json:"enabled"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatalf("decode provider: %v (%s)", err, b)
	}
	if len(view.Keys) != 2 ||
		view.Keys[0].Name != "account-a" || !view.Keys[0].Enabled ||
		view.Keys[1].Name != "account-b" || view.Keys[1].Enabled {
		t.Fatalf("keys = %+v", view.Keys)
	}

	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, _ := io.ReadAll(cfgResp.Body)
	if strings.Contains(string(cfgBody), "sk-aaaa1111") {
		t.Fatalf("config leaked a raw key: %s", cfgBody)
	}
	if !strings.Contains(string(cfgBody), `"name":"account-a"`) {
		t.Fatalf("config view missing the named key: %s", cfgBody)
	}
}

func TestKeyUpdatesAppendByName(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"c","key":"sk-c"}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 3 || got[0].Name != "a" || got[1].Name != "b" || got[2].Name != "c" {
		t.Fatalf("keys = %+v, want names a,b,c", got)
	}
}

func TestKeyUpdatesDuplicateNameRejected(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"a","key":"sk-new"}]`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate name = %d, want 400", resp.StatusCode)
	}
}

func TestKeyUpdatesDeleteByName(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"a","delete":true}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("keys = %+v, want [b]", got)
	}
}

func TestKeyUpdatesDeletePinnedKeyRejected(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"n","provider":"p","model":"m1","key":"a"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}

	resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"a","delete":true}]`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete pinned key = %d, want 400", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "is used by node") {
		t.Fatalf("error = %s, want it to name the node", b)
	}
}

func TestKeyUpdatesDeletePinnedKeyOtherProviderAllowed(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)
	createNamedProvider(t, client, ts.URL, csrf,
		`{"name":"q","type":"generic","chat":"https://b.example","keys":[{"name":"a","key":"sk-q"}]}`)
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"nq","provider":"q","model":"m1","key":"a"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"a","delete":true}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete = %d, want 204: %s", resp.StatusCode, b)
	}
}

func TestKeyUpdatesToggleEnabled(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"a","enabled":false}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("toggle = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 2 || got[0].EnabledValue() || !got[1].EnabledValue() {
		t.Fatalf("keys = %+v, want a disabled and b enabled", got)
	}
}

func TestKeyUpdatesDeleteAndReaddSameName(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	if resp := updateNamedKeys(t, client, ts.URL, csrf,
		`[{"name":"a","delete":true},{"name":"a","key":"sk-new"}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rotate = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 2 {
		t.Fatalf("keys = %+v, want two keys", got)
	}
	byName := make(map[string]string, len(got))
	for _, k := range got {
		byName[k.Name] = k.Key
	}
	if byName["a"] != "sk-new" || byName["b"] != "sk-b" {
		t.Fatalf("keys = %+v, want a=sk-new and b=sk-b", got)
	}
}

func TestKeyUpdatesMissingNameRejected(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"nope","delete":true}]`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing name = %d, want 400", resp.StatusCode)
	}
}

func TestMergeProviderKeys(t *testing.T) {
	old := []config.ProviderKey{{Name: "a", Key: "sk-a"}}

	if got, err := mergeProviderKeys(old, nil); err != nil || len(got) != 1 || got[0].Key != "sk-a" {
		t.Fatalf("nil = %+v/%v, want old preserved", got, err)
	}
	if got, err := mergeProviderKeys(old, []config.ProviderKey{}); err != nil || len(got) != 0 {
		t.Fatalf("empty = %+v/%v, want cleared", got, err)
	}
	got, err := mergeProviderKeys(old, []config.ProviderKey{{Name: "a", Key: "sk-new"}})
	if err != nil || len(got) != 1 || got[0].Key != "sk-new" {
		t.Fatalf("replace = %+v/%v", got, err)
	}
	got, err = mergeProviderKeys(old, []config.ProviderKey{{Name: "a"}})
	if err != nil || len(got) != 1 || got[0].Key != "sk-a" {
		t.Fatalf("keep-by-name = %+v/%v, want the stored value", got, err)
	}
	if _, err := mergeProviderKeys(old, []config.ProviderKey{{Name: "b"}}); err == nil {
		t.Fatal("new name with an empty key should fail")
	}
}

func TestPutConfigPreservesProviderKeys(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	createNamedProvider(t, client, ts.URL, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"account-a","key":"sk-keep"}]}`)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sk-keep") {
		t.Fatalf("config leaked the provider key: %s", b)
	}
	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", string(b), csrf); resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("put config = %d: %s", resp.StatusCode, body)
	}
	if len(rel.cfg.Providers) != 1 || len(rel.cfg.Providers[0].Keys) != 1 ||
		rel.cfg.Providers[0].Keys[0].Key != "sk-keep" {
		t.Fatalf("keys after round-trip = %+v", rel.cfg.Providers)
	}
}

func TestUsageViewIncludesNameAndEnabled(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	disabled := false
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{
			{Name: "account-a", Key: "sk-a"},
			{Name: "account-b", Key: "sk-b", Enabled: &disabled},
		},
	})

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got, _ := decodeUsage(t, resp)
	if len(got.Providers) != 1 || len(got.Providers[0].Accounts) != 2 {
		t.Fatalf("usage = %+v", got.Providers)
	}
	accounts := got.Providers[0].Accounts
	if accounts[0].Name != "account-a" || !accounts[0].Enabled ||
		accounts[1].Name != "account-b" || accounts[1].Enabled {
		t.Fatalf("accounts = %+v", accounts)
	}
}
