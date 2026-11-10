package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/logging"
)

// legacyConfig is an id-less configuration whose references are all still
// entity names. Every migration test starts from a copy of it.
const legacyConfig = `
gateway:
  listen: "127.0.0.1:0"
  keys:
    - name: client
      key: sk-gw-client
entrypoints:
  - name: default
    match: ["*"]
    route: default
providers:
  - name: deepseek
    type: generic
    chat: https://api.deepseek.com/v1
    keys:
      - name: primary
        key: "${MIGRATE_KEY}"
nodes:
  - name: deepseek-chat
    provider: deepseek
    key: primary
    model: deepseek-chat
routes:
  - name: default
    nodes: [deepseek-chat]
`

// decodeConfig parses a YAML body without migration, expansion or defaults, so
// a test can migrate it explicitly.
func decodeConfig(t *testing.T, body string) *Config {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(body))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &c
}

func writeMigrateConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "cuttle.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateIdentitiesDerivesAndTracksChanges(t *testing.T) {
	c := decodeConfig(t, legacyConfig)

	changed, err := MigrateIdentities(c)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for an id-less config")
	}

	provider := &c.Providers[0]
	if !identity.Valid(identity.Provider, provider.ID) {
		t.Fatalf("provider id = %q, want a valid provider id", provider.ID)
	}
	key := &provider.Keys[0]
	if !identity.Valid(identity.ProviderKey, key.ID) {
		t.Fatalf("provider key id = %q, want a valid provider key id", key.ID)
	}
	node := &c.Nodes[0]
	if !identity.Valid(identity.Node, node.ID) {
		t.Fatalf("node id = %q, want a valid node id", node.ID)
	}
	route := &c.Routes[0]
	if !identity.Valid(identity.Route, route.ID) {
		t.Fatalf("route id = %q, want a valid route id", route.ID)
	}
	entrypoint := &c.Entrypoints[0]
	if !identity.Valid(identity.Entrypoint, entrypoint.ID) {
		t.Fatalf("entrypoint id = %q, want a valid entrypoint id", entrypoint.ID)
	}
	if got := c.Gateway.Keys[0].ID; !identity.Valid(identity.GatewayKey, got) {
		t.Fatalf("gateway key id = %q, want a valid gateway key id", got)
	}

	if node.Provider != provider.ID {
		t.Fatalf("node.provider = %q, want the provider id %q", node.Provider, provider.ID)
	}
	if node.Key != key.ID {
		t.Fatalf("node.key = %q, want the provider key id %q", node.Key, key.ID)
	}
	if len(route.Nodes) != 1 || route.Nodes[0] != node.ID {
		t.Fatalf("route.nodes = %#v, want [%q]", route.Nodes, node.ID)
	}
	if entrypoint.Route != route.ID {
		t.Fatalf("entrypoint.route = %q, want the route id %q", entrypoint.Route, route.ID)
	}

	changed, err = MigrateIdentities(c)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if changed {
		t.Fatal("changed = true on a migrated config, want false")
	}
}

func TestMigrateProviderKeyIDScopedToProvider(t *testing.T) {
	c := decodeConfig(t, `
providers:
  - name: alpha
    type: generic
    chat: https://a.example/v1
    keys:
      - name: default
        key: sk-a
  - name: beta
    type: generic
    chat: https://b.example/v1
    keys:
      - name: default
        key: sk-b
`)
	if _, err := MigrateIdentities(c); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	a := c.Providers[0]
	b := c.Providers[1]
	if a.Keys[0].ID == b.Keys[0].ID {
		t.Fatalf("both keys named default share id %q", a.Keys[0].ID)
	}
	if want := identity.Derive(identity.ProviderKey, a.ID+"\x00"+"default"); a.Keys[0].ID != want {
		t.Fatalf("alpha key id = %q, want %q", a.Keys[0].ID, want)
	}
	if want := identity.Derive(identity.ProviderKey, b.ID+"\x00"+"default"); b.Keys[0].ID != want {
		t.Fatalf("beta key id = %q, want %q", b.Keys[0].ID, want)
	}
}

func TestMigrateRejectsDanglingAndAmbiguousReferences(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "dangling provider",
			body: `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: nope
    model: m1
`,
			want: []string{`node "n"`, `"nope"`, "unique provider name"},
		},
		{
			name: "ambiguous provider name",
			body: `
providers:
  - name: dup
    type: generic
    chat: https://a.example/v1
  - name: dup
    type: generic
    chat: https://b.example/v1
nodes:
  - name: n
    provider: dup
    model: m1
`,
			want: []string{`node "n"`, `"dup"`, "unique provider name"},
		},
		{
			name: "dangling node",
			body: `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: p
    model: m1
routes:
  - name: r
    nodes: [nope]
`,
			want: []string{`route "r"`, `"nope"`, "unique node name"},
		},
		{
			name: "dangling route",
			body: `
entrypoints:
  - name: e
    match: ["*"]
    route: nope
`,
			want: []string{`entrypoint "e"`, `"nope"`, "unique route name"},
		},
		{
			name: "dangling key",
			body: `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    keys:
      - name: primary
        key: sk-a
nodes:
  - name: n
    provider: p
    key: nope
    model: m1
`,
			want: []string{`node "n"`, `"nope"`, "unique key name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := decodeConfig(t, tc.body)
			_, err := MigrateIdentities(c)
			if err == nil {
				t.Fatal("migrate accepted an unresolvable reference")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestLoadReadOnlyConfigDerivesStableIDs(t *testing.T) {
	t.Setenv("MIGRATE_KEY", "sk-migrated")

	t.Run("read-only directory keeps the file untouched", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMigrateConfig(t, dir, legacyConfig)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root user: root bypasses directory permissions")
		}

		first, err := Load(path)
		if err != nil {
			t.Fatalf("first load: %v", err)
		}
		second, err := Load(path)
		if err != nil {
			t.Fatalf("second load: %v", err)
		}
		if first.Providers[0].ID != second.Providers[0].ID {
			t.Fatalf("provider id changed across loads: %q then %q",
				first.Providers[0].ID, second.Providers[0].ID)
		}
		if first.Nodes[0].Provider != second.Nodes[0].Provider {
			t.Fatalf("node.provider changed across loads: %q then %q",
				first.Nodes[0].Provider, second.Nodes[0].Provider)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("read-only config was rewritten:\n%s", after)
		}
		if bak, err := filepath.Glob(path + ".bak-*"); err != nil || len(bak) != 0 {
			t.Fatalf("backup files = %v, %v; want none", bak, err)
		}
	})

	t.Run("identical content derives identical ids", func(t *testing.T) {
		first, err := Load(writeMigrateConfig(t, t.TempDir(), legacyConfig))
		if err != nil {
			t.Fatalf("first load: %v", err)
		}
		second, err := Load(writeMigrateConfig(t, t.TempDir(), legacyConfig))
		if err != nil {
			t.Fatalf("second load: %v", err)
		}
		if first.Providers[0].ID != second.Providers[0].ID ||
			first.Routes[0].ID != second.Routes[0].ID ||
			first.Entrypoints[0].ID != second.Entrypoints[0].ID {
			t.Fatalf("ids differ between identical configs: %+v / %+v", first, second)
		}
	})
}

func TestLoadWritesBackMigratedConfigAndBackup(t *testing.T) {
	t.Setenv("MIGRATE_KEY", "sk-migrated")
	dir := t.TempDir()
	path := writeMigrateConfig(t, dir, legacyConfig)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Providers[0].Keys[0].Key; got != "sk-migrated" {
		t.Fatalf("key = %q, want the expanded value", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: p_", "id: pk_", "id: n_", "id: r_", "id: e_", "id: gk_"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("written config lacks %q:\n%s", want, raw)
		}
	}
	if !strings.Contains(string(raw), "${MIGRATE_KEY}") {
		t.Fatalf("write-back expanded the environment:\n%s", raw)
	}
	if strings.Contains(string(raw), "sk-migrated") {
		t.Fatalf("written config contains the expanded key:\n%s", raw)
	}

	baks, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(baks) != 1 {
		t.Fatalf("backups = %v, want one", baks)
	}
	saved, err := os.ReadFile(baks[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != legacyConfig {
		t.Fatalf("backup is not the original file:\n%s", saved)
	}
	info, err := os.Stat(baks[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}

	// A migrated file needs no second write-back.
	if _, err := Load(path); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if baks, err := filepath.Glob(path + ".bak-*"); err != nil || len(baks) != 1 {
		t.Fatalf("backups after reload = %v, %v; want still one", baks, err)
	}
}

// capturedLogger returns a text logger writing into a buffer.
func capturedLogger(t *testing.T) (*logging.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := logging.NewWithWriter("info", "text", &buf)
	if err != nil {
		t.Fatal(err)
	}
	return l, &buf
}

// TestLoadLogsWriteBackOutcome pins spec §3: the upgrade that rewrites
// cuttle.yaml is best-effort but never silent.
func TestLoadLogsWriteBackOutcome(t *testing.T) {
	t.Setenv("MIGRATE_KEY", "sk-migrated")

	t.Run("success names the backup", func(t *testing.T) {
		path := writeMigrateConfig(t, t.TempDir(), legacyConfig)
		l, buf := capturedLogger(t)
		if _, err := Load(path, WithLogger(l)); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "level=info") || !strings.Contains(out, "entity ids written") {
			t.Fatalf("log = %q, want an info line for the write-back", out)
		}
		if !strings.Contains(out, path+".bak-") {
			t.Fatalf("log = %q, want the backup path", out)
		}
	})

	t.Run("backup failure warns", func(t *testing.T) {
		path := writeMigrateConfig(t, t.TempDir(), legacyConfig)
		old := writeBackupFile
		writeBackupFile = func(string, []byte, os.FileMode) error {
			return errors.New("disk full")
		}
		defer func() { writeBackupFile = old }()

		l, buf := capturedLogger(t)
		if _, err := Load(path, WithLogger(l)); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "level=warn") || !strings.Contains(out, "disk full") {
			t.Fatalf("log = %q, want a warning for the failed write-back", out)
		}
	})
}

// TestLoadSymlinkedConfigIsNotRewritten pins the link guard: replacing the
// link with a regular file would stop the dotfiles source from receiving
// changes.
func TestLoadSymlinkedConfigIsNotRewritten(t *testing.T) {
	t.Setenv("MIGRATE_KEY", "sk-migrated")
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-cuttle.yaml")
	if err := os.WriteFile(target, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cuttle.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	l, buf := capturedLogger(t)
	if _, err := Load(link, WithLogger(l)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=warn") || !strings.Contains(out, "symlink") {
		t.Fatalf("log = %q, want the symlink skip warning", out)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("mode = %v, want the config path to stay a symlink", info.Mode())
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != legacyConfig {
		t.Fatalf("symlink target was rewritten:\n%s", raw)
	}
	if baks, err := filepath.Glob(link + ".bak-*"); err != nil || len(baks) != 0 {
		t.Fatalf("backups = %v, %v; want none", baks, err)
	}
}

func TestLoadRenameWithoutIDChangesIdentity(t *testing.T) {
	t.Setenv("MIGRATE_KEY", "sk-migrated")
	dir := t.TempDir()
	path := writeMigrateConfig(t, dir, legacyConfig)

	first, err := Load(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	oldID := first.Providers[0].ID

	// A hand edit that renames the provider and drops the id line starts a new
	// identity; the id is the handle, the name is only a label.
	renamed := strings.Replace(legacyConfig, "name: deepseek\n", "name: deepseek-next\n", 1)
	renamed = strings.Replace(renamed, "provider: deepseek\n", "provider: deepseek-next\n", 1)
	if err := os.WriteFile(path, []byte(renamed), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Load(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	newID := second.Providers[0].ID
	if newID == oldID {
		t.Fatalf("id %q survived a name-only edit without an id line, want a new identity", newID)
	}
	if !identity.Valid(identity.Provider, newID) {
		t.Fatalf("id = %q, want a valid provider id", newID)
	}
	if got := second.Nodes[0].Provider; got != newID {
		t.Fatalf("node.provider = %q, want the renamed provider id %q", got, newID)
	}
}

func TestValidateNameReferencesRejected(t *testing.T) {
	c := &Config{
		Gateway: Gateway{Listen: ":8787"},
		Providers: []Provider{{
			ID:   identity.Derive(identity.Provider, "p"),
			Name: "p", Type: "generic", Chat: "https://a.example/v1",
		}},
		Nodes:  []Node{{ID: identity.Derive(identity.Node, "n"), Name: "n", Provider: "p", Model: "m1"}},
		Routes: []Route{{ID: identity.Derive(identity.Route, "r"), Name: "r", Nodes: []string{"n"}}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "provider id") {
		t.Fatalf("err = %v, want a name reference rejected with the expected id", err)
	}
}

// TestEntityLookupsUseIDsOnly pins the handles: every lookup resolves an id,
// and a display name never matches even when it is unique.
func TestEntityLookupsUseIDsOnly(t *testing.T) {
	c := decodeConfig(t, legacyConfig)
	if _, err := MigrateIdentities(c); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if provider, ok := c.Provider(c.Providers[0].ID); !ok || provider != &c.Providers[0] {
		t.Fatalf("Provider(id) = %+v, %v", provider, ok)
	}
	for _, ref := range []string{" deepseek ", "deepseek", "missing", ""} {
		if _, ok := c.Provider(ref); ok {
			t.Fatalf("Provider(%q) reported a match, want id-only lookups", ref)
		}
	}

	if node, ok := c.Node(c.Nodes[0].ID); !ok || node != &c.Nodes[0] {
		t.Fatalf("Node(id) = %+v, %v", node, ok)
	}
	for _, ref := range []string{"deepseek-chat", "missing"} {
		if _, ok := c.Node(ref); ok {
			t.Fatalf("Node(%q) reported a match, want id-only lookups", ref)
		}
	}

	if route, ok := c.Route(c.Routes[0].ID); !ok || route != &c.Routes[0] {
		t.Fatalf("Route(id) = %+v, %v", route, ok)
	}
	if _, ok := c.Route("default"); ok {
		t.Fatal("Route(name) reported a match, want id-only lookups")
	}

	if ep, ok := c.Entrypoint(c.Entrypoints[0].ID); !ok || ep != &c.Entrypoints[0] {
		t.Fatalf("Entrypoint(id) = %+v, %v", ep, ok)
	}
	if _, ok := c.Entrypoint("default"); ok {
		t.Fatal("Entrypoint(name) reported a match, want id-only lookups")
	}

	if key, ok := c.GatewayKey(c.Gateway.Keys[0].ID); !ok || key != &c.Gateway.Keys[0] {
		t.Fatalf("GatewayKey(id) = %+v, %v", key, ok)
	}
	if _, ok := c.GatewayKey("client"); ok {
		t.Fatal("GatewayKey(name) reported a match, want id-only lookups")
	}
}

// sharedKeyNameConfig has two providers that both name a key "primary", the
// case that makes a provider key name ambiguous across the config.
const sharedKeyNameConfig = `
gateway:
  listen: "127.0.0.1:0"
  keys:
    - name: client
      key: sk-gw-client
entrypoints:
  - name: default
    match: ["*"]
    route: default
providers:
  - name: alpha
    type: generic
    chat: https://a.example/v1
    keys:
      - name: primary
        key: sk-a
  - name: beta
    type: generic
    chat: https://b.example/v1
    keys:
      - name: primary
        key: sk-b
nodes:
  - name: alpha-chat
    provider: alpha
    model: m1
routes:
  - name: default
    nodes: [alpha-chat]
`

func TestNameToIDsAndLabelsByID(t *testing.T) {
	c := decodeConfig(t, sharedKeyNameConfig)
	if _, err := MigrateIdentities(c); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids := c.NameToIDs()
	if got, want := ids[string(identity.Provider)]["alpha"], c.Providers[0].ID; got != want {
		t.Fatalf("NameToIDs provider alpha = %q, want %q", got, want)
	}
	if got, want := ids[string(identity.Node)]["alpha-chat"], c.Nodes[0].ID; got != want {
		t.Fatalf("NameToIDs node = %q, want %q", got, want)
	}
	if got, want := ids[string(identity.Route)]["default"], c.Routes[0].ID; got != want {
		t.Fatalf("NameToIDs route = %q, want %q", got, want)
	}
	if got, want := ids[string(identity.Entrypoint)]["default"], c.Entrypoints[0].ID; got != want {
		t.Fatalf("NameToIDs entrypoint = %q, want %q", got, want)
	}
	if got, want := ids[string(identity.GatewayKey)]["client"], c.Gateway.Keys[0].ID; got != want {
		t.Fatalf("NameToIDs gateway key = %q, want %q", got, want)
	}
	// Provider key names are only unique per provider, so a shared name is
	// ambiguous and must not resolve.
	if _, ok := ids[string(identity.ProviderKey)]["primary"]; ok {
		t.Fatal("NameToIDs resolved an ambiguous provider key name")
	}

	labels := c.LabelsByID()
	if got := labels[string(identity.Provider)][c.Providers[1].ID]; got != "beta" {
		t.Fatalf("LabelsByID provider = %q, want beta", got)
	}
	if got := labels[string(identity.ProviderKey)][c.Providers[0].Keys[0].ID]; got != "primary" {
		t.Fatalf("LabelsByID provider key = %q, want primary", got)
	}
	if got := labels[string(identity.GatewayKey)][c.Gateway.Keys[0].ID]; got != "client" {
		t.Fatalf("LabelsByID gateway key = %q, want client", got)
	}
}
