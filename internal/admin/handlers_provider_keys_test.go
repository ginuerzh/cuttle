package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
)

func createNamedProvider(t *testing.T, client *http.Client, base, csrf, body string) string {
	t.Helper()
	if resp := doJSON(t, client, http.MethodPost, base+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create provider = %d: %s", resp.StatusCode, b)
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatalf("decode provider body: %v", err)
	}
	return providerID(t, client, base, in.Name)
}

func seedNamedProvider(t *testing.T, client *http.Client, base, csrf string) string {
	t.Helper()
	return createNamedProvider(t, client, base, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"a","key":"sk-a"},{"name":"b","key":"sk-b"}]}`)
}

func updateNamedKeys(t *testing.T, client *http.Client, base, csrf, updates string) *http.Response {
	t.Helper()
	body := `{"name":"p","type":"generic","chat":"https://a.example","keyUpdates":` + updates + `}`
	return doJSON(t, client, http.MethodPut, providerURL(base, providerID(t, client, base, "p")), body, csrf)
}

// providerKeyIDs maps the key names of one provider to their ids.
func providerKeyIDs(t *testing.T, client *http.Client, base, providerIDValue string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, k := range getProvider(t, client, base, providerIDValue).Keys {
		out[k.Name] = k.ID
	}
	return out
}

func TestProviderViewReturnsNamedMaskedKeys(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	pid := createNamedProvider(t, client, ts.URL, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"account-a","key":"sk-aaaa1111"},{"name":"account-b","key":"sk-bbbb2222","enabled":false}]}`)

	resp := doJSON(t, client, http.MethodGet, providerURL(ts.URL, pid), "", "")
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
	var view providerJSON
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatalf("decode provider: %v (%s)", err, b)
	}
	if view.ID != pid || view.Name != "p" {
		t.Fatalf("view = %+v, want id %q and name p", view, pid)
	}
	if len(view.Keys) != 2 ||
		view.Keys[0].Name != "account-a" || !view.Keys[0].Enabled ||
		view.Keys[1].Name != "account-b" || view.Keys[1].Enabled {
		t.Fatalf("keys = %+v", view.Keys)
	}
	for _, k := range view.Keys {
		if !identity.Valid(identity.ProviderKey, k.ID) {
			t.Fatalf("key %q has id %q, want a generated pk_ id", k.Name, k.ID)
		}
	}
	if view.Keys[0].ID == view.Keys[1].ID {
		t.Fatalf("keys share the id %q", view.Keys[0].ID)
	}

	cfgResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	cfgBody, _ := io.ReadAll(cfgResp.Body)
	if strings.Contains(string(cfgBody), "sk-aaaa1111") {
		t.Fatalf("config leaked a raw key: %s", cfgBody)
	}
	if !strings.Contains(string(cfgBody), `"name":"account-a"`) {
		t.Fatalf("config view missing the named key: %s", cfgBody)
	}
	if !strings.Contains(string(cfgBody), `"id":"`+pid+`"`) {
		t.Fatalf("config view missing the provider id: %s", cfgBody)
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
	if !identity.Valid(identity.ProviderKey, got[2].ID) {
		t.Fatalf("appended key id = %q, want a generated pk_ id", got[2].ID)
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

func TestKeyUpdatesDeleteByID(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	pid := seedNamedProvider(t, client, ts.URL, csrf)
	keys := providerKeyIDs(t, client, ts.URL, pid)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"id":"`+keys["a"]+`","delete":true}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 1 || got[0].Name != "b" || got[0].ID != keys["b"] {
		t.Fatalf("keys = %+v, want [b] with the stored id", got)
	}
}

func TestKeyUpdatesDeletePinnedKeyRejected(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	pid := seedNamedProvider(t, client, ts.URL, csrf)
	keys := providerKeyIDs(t, client, ts.URL, pid)
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"n","provider":"`+pid+`","model":"m1","key":"`+keys["a"]+`"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}

	resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"id":"`+keys["a"]+`","delete":true}]`)
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
	pid := seedNamedProvider(t, client, ts.URL, csrf)
	keys := providerKeyIDs(t, client, ts.URL, pid)
	qid := createNamedProvider(t, client, ts.URL, csrf,
		`{"name":"q","type":"generic","chat":"https://b.example","keys":[{"name":"a","key":"sk-q"}]}`)
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"nq","provider":"`+qid+`","model":"m1","key":"a"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"id":"`+keys["a"]+`","delete":true}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete = %d, want 204: %s", resp.StatusCode, b)
	}
}

func TestKeyUpdatesToggleEnabled(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	pid := seedNamedProvider(t, client, ts.URL, csrf)
	keys := providerKeyIDs(t, client, ts.URL, pid)

	if resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"id":"`+keys["a"]+`","enabled":false}]`); resp.StatusCode != http.StatusNoContent {
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
	pid := seedNamedProvider(t, client, ts.URL, csrf)
	keys := providerKeyIDs(t, client, ts.URL, pid)

	if resp := updateNamedKeys(t, client, ts.URL, csrf,
		`[{"id":"`+keys["a"]+`","delete":true},{"name":"a","key":"sk-new"}]`); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rotate = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 2 {
		t.Fatalf("keys = %+v, want two keys", got)
	}
	byName := make(map[string]config.ProviderKey, len(got))
	for _, k := range got {
		byName[k.Name] = k
	}
	if byName["a"].Key != "sk-new" || byName["b"].Key != "sk-b" {
		t.Fatalf("keys = %+v, want a=sk-new and b=sk-b", got)
	}
	// The replacement is a new identity: the pinned-node protection and the
	// stats never conflate it with the deleted key.
	if byName["a"].ID == keys["a"] || !identity.Valid(identity.ProviderKey, byName["a"].ID) {
		t.Fatalf("replacement key id = %q, want a fresh pk_ id", byName["a"].ID)
	}
}

func TestKeyUpdatesMissingIDRejected(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	seedNamedProvider(t, client, ts.URL, csrf)

	resp := updateNamedKeys(t, client, ts.URL, csrf, `[{"name":"nope","delete":true}]`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing id = %d, want 400", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "key id is required") {
		t.Fatalf("error = %s, want it to ask for the key id", b)
	}
}

func TestMergeProviderKeys(t *testing.T) {
	old := []config.ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-a"}}

	if got, err := mergeProviderKeys(old, nil); err != nil || len(got) != 1 || got[0].Key != "sk-a" {
		t.Fatalf("nil = %+v/%v, want old preserved", got, err)
	}
	if got, err := mergeProviderKeys(old, []config.ProviderKey{}); err != nil || len(got) != 0 {
		t.Fatalf("empty = %+v/%v, want cleared", got, err)
	}

	// A stored id keeps its value and takes the new name.
	got, err := mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000001", Name: "renamed"}})
	if err != nil || len(got) != 1 || got[0].ID != "pk_0000000001" || got[0].Name != "renamed" || got[0].Key != "sk-a" {
		t.Fatalf("keep-by-id = %+v/%v, want the stored value under the new name", got, err)
	}

	// A stored id with a value replaces the value.
	got, err = mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-new"}})
	if err != nil || len(got) != 1 || got[0].Key != "sk-new" {
		t.Fatalf("replace = %+v/%v", got, err)
	}

	// An unknown id is a stale handle, not a new key.
	if _, err := mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000009", Name: "a", Key: "sk-x"}}); err == nil {
		t.Fatal("an unknown key id should fail")
	}

	// An entry without an id is a new key with a generated id.
	got, err = mergeProviderKeys(old, []config.ProviderKey{{Name: "b", Key: "sk-b"}})
	if err != nil || len(got) != 1 || got[0].Name != "b" || got[0].Key != "sk-b" {
		t.Fatalf("new key = %+v/%v", got, err)
	}
	if !identity.Valid(identity.ProviderKey, got[0].ID) || got[0].ID == "pk_0000000001" {
		t.Fatalf("new key id = %q, want a generated pk_ id", got[0].ID)
	}

	// A new entry needs a value, and names stay unique.
	if _, err := mergeProviderKeys(old, []config.ProviderKey{{Name: "b"}}); err == nil {
		t.Fatal("a new key with an empty value should fail")
	}
	if _, err := mergeProviderKeys(old, []config.ProviderKey{{Name: "b", Key: "1"}, {Name: "b", Key: "2"}}); err == nil {
		t.Fatal("duplicate names should fail")
	}
}

func TestPutConfigPreservesProviderKeys(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	pid := createNamedProvider(t, client, ts.URL, csrf,
		`{"name":"p","type":"generic","chat":"https://a.example","keys":[{"name":"account-a","key":"sk-keep"}]}`)
	keyID := providerKeyIDs(t, client, ts.URL, pid)["account-a"]

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
	providers := rel.cfg.Providers
	if len(providers) != 1 || len(providers[0].Keys) != 1 || providers[0].Keys[0].Key != "sk-keep" {
		t.Fatalf("keys after round-trip = %+v", providers)
	}
	if providers[0].ID != pid || providers[0].Keys[0].ID != keyID {
		t.Fatalf("ids after round-trip = %+v, want provider %q and key %q", providers, pid, keyID)
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
	srv.mu.Lock()
	pid := srv.cfg.Providers[0].ID
	srv.mu.Unlock()

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage?refresh=1", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got, _ := decodeUsage(t, resp)
	if len(got.Providers) != 1 || len(got.Providers[0].Accounts) != 2 {
		t.Fatalf("usage = %+v", got.Providers)
	}
	if got.Providers[0].ID != pid {
		t.Fatalf("provider id = %q, want %q", got.Providers[0].ID, pid)
	}
	accounts := got.Providers[0].Accounts
	if accounts[0].Name != "account-a" || !accounts[0].Enabled ||
		accounts[1].Name != "account-b" || accounts[1].Enabled {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestMergeProviderKeysPreservesEnabled(t *testing.T) {
	disabled := false
	old := []config.ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-a", Enabled: &disabled}}

	got, err := mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000001", Name: "a"}})
	if err != nil || len(got) != 1 || got[0].EnabledValue() {
		t.Fatalf("empty key = %+v/%v, want the stored value and disabled", got, err)
	}
	got, err = mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-new"}})
	if err != nil || len(got) != 1 || got[0].EnabledValue() {
		t.Fatalf("new value = %+v/%v, want disabled", got, err)
	}
	enabled := true
	got, err = mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-new", Enabled: &enabled}})
	if err != nil || len(got) != 1 || !got[0].EnabledValue() {
		t.Fatalf("explicit enabled = %+v/%v, want enabled", got, err)
	}
}

func TestMergeProviderKeysTrimsNames(t *testing.T) {
	old := []config.ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-a"}}
	got, err := mergeProviderKeys(old, []config.ProviderKey{{ID: "pk_0000000001", Name: " a ", Key: "sk-new"}})
	if err != nil || len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("merged = %+v/%v, want the trimmed name a", got, err)
	}
}

func TestProviderFullKeysReplacementPinned(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	pid := seedNamedProvider(t, client, ts.URL, csrf)
	keys := providerKeyIDs(t, client, ts.URL, pid)
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/nodes",
		`{"name":"n","provider":"`+pid+`","model":"m1","key":"`+keys["a"]+`"}`, csrf); resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create node = %d: %s", resp.StatusCode, b)
	}

	keep := `{"name":"p","type":"generic","chat":"https://a.example","keys":[{"id":"` + keys["a"] + `","name":"a","key":"sk-new"}]}`
	if resp := doJSON(t, client, http.MethodPut, providerURL(ts.URL, pid), keep, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("replace keys = %d: %s", resp.StatusCode, b)
	}
	got := rel.cfg.Providers[0].Keys
	if len(got) != 1 || got[0].ID != keys["a"] || got[0].Name != "a" || got[0].Key != "sk-new" {
		t.Fatalf("keys = %+v, want the pinned id kept with the new value", got)
	}

	clear := `{"name":"p","type":"generic","chat":"https://a.example","keys":[]}`
	if resp := doJSON(t, client, http.MethodPut, providerURL(ts.URL, pid), clear, csrf); resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("clear pinned keys = %d, want 400: %s", resp.StatusCode, b)
	}
}
