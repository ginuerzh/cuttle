package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ginuerzh/cuttle/internal/identity"
)

// fixtureID derives the id a migration assigns to a fixture name, so a
// hand-built config carries ids without hardcoding them.
func fixtureID(kind identity.Kind, name string) string {
	return identity.Derive(kind, name)
}

// withGatewayKeyIDs fills a distinct id into every fixture key that has none,
// so a validation test reaches the field it exercises.
func withGatewayKeyIDs(keys []GatewayKey) []GatewayKey {
	out := make([]GatewayKey, len(keys))
	copy(out, keys)
	for i := range out {
		if out[i].ID == "" {
			out[i].ID = fmt.Sprintf("gk_%010d", i+1)
		}
	}
	return out
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cuttle.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadExpandsEnvAndValidates(t *testing.T) {
	t.Setenv("TEST_KEY", "sk-test")

	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: default
providers:
  - name: deepseek
    type: generic
    chat: https://api.deepseek.com/v1
    keys:
      - name: default
        key: "${TEST_KEY}"
    auth:
      header: Authorization
      prefix: "Bearer "
nodes:
  - name: deepseek
    provider: deepseek
    model: deepseek-chat
routes:
  - name: default
    nodes: [deepseek]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].Keys[0].Key; got != "sk-test" {
		t.Fatalf("key = %q, want sk-test", got)
	}
	if err := Validate(c); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestProviderKeysNamedShape(t *testing.T) {
	t.Setenv("TEST_KEY", "sk-test")

	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: default
providers:
  - name: deepseek
    type: generic
    chat: https://api.deepseek.com/v1
    keys:
      - name: account-a
        key: "${TEST_KEY}"
nodes:
  - name: deepseek
    provider: deepseek
    model: deepseek-chat
routes:
  - name: default
    nodes: [deepseek]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers[0].Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(c.Providers[0].Keys))
	}
	k := c.Providers[0].Keys[0]
	if k.Name != "account-a" || k.Key != "sk-test" {
		t.Fatalf("key = %+v, want name account-a / key sk-test", k)
	}
	if !k.EnabledValue() {
		t.Fatal("EnabledValue() = false, want true")
	}
}

func TestProviderKeysScalarRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `
providers:
  - name: deepseek
    type: generic
    chat: https://api.deepseek.com/v1
    keys: ["sk-a"]
`))
	if err == nil {
		t.Fatal("scalar keys accepted, want a parse error")
	}
}

func TestProviderKeyNameValidation(t *testing.T) {
	base := func(keys []ProviderKey) *Config {
		return &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "generic", Chat: "https://a.example/v1", Keys: keys}},
			Nodes:     []Node{{ID: fixtureID(identity.Node, "n"), Name: "n", Provider: fixtureID(identity.Provider, "p"), Model: "m1"}},
			Routes:    []Route{{ID: fixtureID(identity.Route, "r"), Name: "r", Nodes: []string{fixtureID(identity.Node, "n")}}},
		}
	}
	cases := []struct {
		name string
		keys []ProviderKey
	}{
		{"empty name", []ProviderKey{{ID: "pk_0000000001", Name: "", Key: "sk-a"}}},
		{"blank name", []ProviderKey{{ID: "pk_0000000001", Name: "   ", Key: "sk-a"}}},
		{"too long", []ProviderKey{{ID: "pk_0000000001", Name: strings.Repeat("a", 65), Key: "sk-a"}}},
		{"duplicate name", []ProviderKey{{ID: "pk_0000000001", Name: "a", Key: "sk-a"}, {ID: "pk_0000000002", Name: "a", Key: "sk-b"}}},
		{"dollar name", []ProviderKey{{ID: "pk_0000000001", Name: "a$b", Key: "sk-a"}}},
		{"control name", []ProviderKey{{ID: "pk_0000000001", Name: "a\nb", Key: "sk-a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(base(tc.keys))
			if err == nil {
				t.Fatalf("%+v should not validate", tc.keys)
			}
			if !strings.Contains(err.Error(), `provider "p"`) {
				t.Fatalf("err = %v, want it to name the provider", err)
			}
		})
	}

	// A display name is free-form; "/" feeds nothing structural any more.
	if err := Validate(base([]ProviderKey{{ID: "pk_0000000001", Name: "a/b", Key: "sk-a"}})); err != nil {
		t.Fatalf("slash name should validate: %v", err)
	}
}

func TestProviderKeyValueValidation(t *testing.T) {
	base := func(key string) *Config {
		return &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "generic", Chat: "https://a.example/v1", Keys: []ProviderKey{{ID: "pk_0000000001", Name: "a", Key: key}}}},
			Nodes:     []Node{{ID: fixtureID(identity.Node, "n"), Name: "n", Provider: fixtureID(identity.Provider, "p"), Model: "m1"}},
			Routes:    []Route{{ID: fixtureID(identity.Route, "r"), Name: "r", Nodes: []string{fixtureID(identity.Node, "n")}}},
		}
	}
	cases := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"blank key", "   "},
		{"dollar key", "sk-$A"},
		{"control key", "sk-a\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(base(tc.key)); err == nil {
				t.Fatalf("key %q should not validate", tc.key)
			}
		})
	}
}

func TestProviderKeyEnabledValue(t *testing.T) {
	if !(ProviderKey{Name: "a", Key: "sk-a"}).EnabledValue() {
		t.Fatal("nil enabled should default to true")
	}
	disabled := false
	if (ProviderKey{Name: "a", Key: "sk-a", Enabled: &disabled}).EnabledValue() {
		t.Fatal("explicit false should be disabled")
	}
}

func TestNodeKeyPinResolves(t *testing.T) {
	providerID := fixtureID(identity.Provider, "p")
	keyID := fixtureID(identity.ProviderKey, "account-a")
	c := &Config{
		Gateway: Gateway{Listen: ":8787"},
		Providers: []Provider{{
			ID: providerID, Name: "p", Type: "generic", Chat: "https://a.example/v1",
			Keys: []ProviderKey{{ID: keyID, Name: "account-a", Key: "sk-a"}},
		}},
		Nodes:  []Node{{ID: fixtureID(identity.Node, "n"), Name: "n", Provider: providerID, Model: "m1", Key: keyID}},
		Routes: []Route{{ID: fixtureID(identity.Route, "r"), Name: "r", Nodes: []string{fixtureID(identity.Node, "n")}}},
	}
	if err := Validate(c); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestNodeKeyPinUnknownFails(t *testing.T) {
	providerID := fixtureID(identity.Provider, "p")
	c := &Config{
		Gateway: Gateway{Listen: ":8787"},
		Providers: []Provider{{
			ID: providerID, Name: "p", Type: "generic", Chat: "https://a.example/v1",
			Keys: []ProviderKey{{ID: fixtureID(identity.ProviderKey, "account-a"), Name: "account-a", Key: "sk-a"}},
		}},
		Nodes:  []Node{{ID: fixtureID(identity.Node, "n"), Name: "n", Provider: providerID, Model: "m1", Key: "nope"}},
		Routes: []Route{{ID: fixtureID(identity.Route, "r"), Name: "r", Nodes: []string{fixtureID(identity.Node, "n")}}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want it to name the unknown key", err)
	}
	if !strings.Contains(err.Error(), "key id") {
		t.Fatalf("err = %v, want it to name the expected key id", err)
	}
}

func TestNodeKeyPinOtherProviderFails(t *testing.T) {
	providerA := fixtureID(identity.Provider, "a")
	providerB := fixtureID(identity.Provider, "b")
	c := &Config{
		Gateway: Gateway{Listen: ":8787"},
		Providers: []Provider{
			{ID: providerA, Name: "a", Type: "generic", Chat: "https://a.example/v1",
				Keys: []ProviderKey{{ID: fixtureID(identity.ProviderKey, "account-a"), Name: "account-a", Key: "sk-a"}}},
			{ID: providerB, Name: "b", Type: "generic", Chat: "https://b.example/v1",
				Keys: []ProviderKey{{ID: fixtureID(identity.ProviderKey, "account-b"), Name: "account-b", Key: "sk-b"}}},
		},
		Nodes:  []Node{{ID: fixtureID(identity.Node, "n"), Name: "n", Provider: providerB, Model: "m1", Key: fixtureID(identity.ProviderKey, "account-a")}},
		Routes: []Route{{ID: fixtureID(identity.Route, "r"), Name: "r", Nodes: []string{fixtureID(identity.Node, "n")}}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "key id") {
		t.Fatalf("err = %v, want the other provider's key id rejected", err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: "  default  "
    match: ["  *  ", "", "*"]
    routes:
      - route: "  r  "
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: "  m1  "
routes:
  - name: "  r  "
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway.Listen != ":8787" {
		t.Fatalf("listen = %q, want :8787", c.Gateway.Listen)
	}
	if c.Providers[0].Auth.Header != "" || c.Providers[0].Auth.Prefix != "" {
		t.Fatalf("auth = %+v, want empty so the adapter supplies the default", c.Providers[0].Auth)
	}
	if c.Providers[0].TLS.Secure == nil || !*c.Providers[0].TLS.Secure {
		t.Fatal("tls.secure should default to true")
	}
	if c.Nodes[0].Model != "m1" || c.Nodes[0].Weight != 1 {
		t.Fatalf("node defaults = %+v, want model m1 and weight 1", c.Nodes[0])
	}
	if c.Routes[0].Name != "r" || c.Routes[0].Strategy != "round" {
		t.Fatalf("route defaults = %+v, want name r and strategy round", c.Routes[0])
	}
	if c.Entrypoints[0].Name != "default" || c.Entrypoints[0].Routes[0].Route != c.Routes[0].ID {
		t.Fatalf("entrypoint defaults = %+v", c.Entrypoints[0])
	}
	if got := c.Entrypoints[0].Match; len(got) != 1 || got[0] != "*" {
		t.Fatalf("match = %#v, want deduplicated [*]", got)
	}
}

func TestNodeAPIAcceptedAndNormalized(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: auto
    provider: a
    model: m1
    api: AUTO
  - name: mixed
    provider: a
    model: m1
    api: Anthropic
  - name: absent
    provider: a
    model: m1
routes:
  - name: r
    nodes: [auto, mixed, absent]
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "anthropic", ""}
	for i, api := range want {
		if got := c.Nodes[i].API; got != api {
			t.Fatalf("nodes[%d].api = %q, want %q", i, got, api)
		}
	}
}

func TestNodeAPIRejectsUnknown(t *testing.T) {
	_, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
    api: grpc
routes:
  - name: r
    nodes: [n]
`))
	if err == nil {
		t.Fatal("Load accepted api: grpc")
	}
	if !strings.Contains(err.Error(), `node "n"`) || !strings.Contains(err.Error(), "api") {
		t.Fatalf("error = %v, want it to name the node and api", err)
	}
}

func TestLoadKeepsCustomAuthHeaderWithoutPrefix(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    auth:
      header: x-api-key
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].Auth.Header != "x-api-key" || c.Providers[0].Auth.Prefix != "" {
		t.Fatalf("auth = %+v, want x-api-key with empty prefix", c.Providers[0].Auth)
	}
}

func TestResolveEntrypointOrder(t *testing.T) {
	c := &Config{Entrypoints: []Entrypoint{
		{ID: "e_3k9x2m4q7a", Name: "specific", Match: []string{"gpt-5*"}},
		{ID: "e_8m2xq7a4k9", Name: "fallback", Match: []string{"*"}},
	}}

	ep, ok := c.ResolveEntrypoint("GPT-5.5")
	if !ok || ep.Name != "specific" {
		t.Fatalf("specific = %+v, %v", ep, ok)
	}
	ep, ok = c.ResolveEntrypoint("other")
	if !ok || ep.Name != "fallback" {
		t.Fatalf("fallback = %+v, %v", ep, ok)
	}
	if got := c.ResolveEntrypointID("gpt-5.5"); got != "e_3k9x2m4q7a" {
		t.Fatalf("ResolveEntrypointID = %q, want the specific entrypoint id", got)
	}
	if got := c.ResolveEntrypointID(""); got != "e_8m2xq7a4k9" {
		t.Fatalf("ResolveEntrypointID(empty) = %q, want the fallback entrypoint id", got)
	}
	noCatchAll := &Config{Entrypoints: []Entrypoint{
		{ID: "e_3k9x2m4q7a", Name: "specific", Match: []string{"gpt-5*"}},
	}}
	if got := noCatchAll.ResolveEntrypointID("unmatched"); got != "" {
		t.Fatalf("ResolveEntrypointID(unmatched) = %q, want empty", got)
	}
	if got := noCatchAll.ResolveEntrypointID(""); got != "" {
		t.Fatalf("ResolveEntrypointID(empty) = %q, want empty", got)
	}
}

func TestResolveEntrypointMultipleGlobsAndCase(t *testing.T) {
	c := &Config{Entrypoints: []Entrypoint{
		{Name: "claude", Match: []string{"claude-*", "claude*"}},
	}}
	ep, ok := c.ResolveEntrypoint("CLAUDE-OPUS-4-5")
	if !ok || ep.Name != "claude" {
		t.Fatalf("ResolveEntrypoint = %+v, %v", ep, ok)
	}
}

// topologyFixture is the smallest valid topology: one provider with one key,
// one node, one route and one entrypoint, all addressed by id.
func topologyFixture() *Config {
	return &Config{
		Gateway: Gateway{Listen: ":8787"},
		Entrypoints: []Entrypoint{{
			ID: fixtureID(identity.Entrypoint, "entry"), Name: "entry", Match: []string{"*"},
			Routes: []RouteBinding{{Route: fixtureID(identity.Route, "route")}},
		}},
		Providers: []Provider{{
			ID: fixtureID(identity.Provider, "provider"), Name: "provider", Type: "generic", Chat: "https://a.example/v1",
			Keys: []ProviderKey{{ID: fixtureID(identity.ProviderKey, "key"), Name: "key", Key: "sk-a"}},
		}},
		Nodes: []Node{{
			ID: fixtureID(identity.Node, "node"), Name: "node", Provider: fixtureID(identity.Provider, "provider"), Model: "m1",
		}},
		Routes: []Route{{
			ID: fixtureID(identity.Route, "route"), Name: "route", Nodes: []string{fixtureID(identity.Node, "node")},
		}},
	}
}

func TestValidateEntrypoints(t *testing.T) {
	routeID := fixtureID(identity.Route, "route")
	base := func() *Config {
		c := topologyFixture()
		c.Entrypoints = nil
		return c
	}
	cases := []struct {
		name string
		ep   Entrypoint
	}{
		{"missing id", Entrypoint{Name: "e", Match: []string{"*"}, Routes: []RouteBinding{{Route: routeID}}}},
		{"empty name", Entrypoint{ID: "e_0000000001", Match: []string{"*"}, Routes: []RouteBinding{{Route: routeID}}}},
		{"empty match", Entrypoint{ID: "e_0000000001", Name: "e", Routes: []RouteBinding{{Route: routeID}}}},
		{"empty match entries", Entrypoint{ID: "e_0000000001", Name: "e", Match: []string{"", "  "}, Routes: []RouteBinding{{Route: routeID}}}},
		{"unknown route", Entrypoint{ID: "e_0000000001", Name: "e", Match: []string{"*"}, Routes: []RouteBinding{{Route: "missing"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			c.Entrypoints = []Entrypoint{tc.ep}
			if err := Validate(c); err == nil {
				t.Fatalf("%+v should not validate", tc.ep)
			}
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		c := base()
		c.Entrypoints = []Entrypoint{
			{ID: "e_0000000001", Name: "e", Match: []string{"a*"}, Routes: []RouteBinding{{Route: routeID}}},
			{ID: "e_0000000002", Name: "e", Match: []string{"b*"}, Routes: []RouteBinding{{Route: routeID}}},
		}
		if err := Validate(c); err == nil {
			t.Fatal("duplicate entrypoint names should not validate")
		}
	})

	t.Run("unknown route by name", func(t *testing.T) {
		c := base()
		c.Entrypoints = []Entrypoint{{ID: "e_0000000001", Name: "e", Match: []string{"*"}, Routes: []RouteBinding{{Route: "route"}}}}
		err := Validate(c)
		if err == nil || !strings.Contains(err.Error(), `use the route id "`+routeID+`"`) {
			t.Fatalf("err = %v, want the display name rejected with the expected route id", err)
		}
	})

	t.Run("slash name accepted", func(t *testing.T) {
		c := base()
		c.Entrypoints = []Entrypoint{{ID: "e_0000000001", Name: "cli/desktop", Match: []string{"*"}, Routes: []RouteBinding{{Route: routeID}}}}
		if err := Validate(c); err != nil {
			t.Fatalf("slash in an entrypoint name should validate: %v", err)
		}
	})
}

func TestValidateAllowsDraftEntrypoint(t *testing.T) {
	c := topologyFixture()
	c.Entrypoints[0].Routes = nil
	if err := Validate(c); err != nil {
		t.Fatalf("an entrypoint without a route should validate as a draft: %v", err)
	}
}

func TestValidateEntrypointRouteBindings(t *testing.T) {
	routeA := fixtureID(identity.Route, "a")
	routeB := fixtureID(identity.Route, "b")
	nodeID := fixtureID(identity.Node, "node")
	probe := func() *Probe {
		return &Probe{Command: "true", Interval: Duration(30 * time.Second), Timeout: Duration(5 * time.Second)}
	}
	base := func(routes ...RouteBinding) *Config {
		c := topologyFixture()
		c.Routes = []Route{
			{ID: routeA, Name: "a", Nodes: []string{nodeID}},
			{ID: routeB, Name: "b", Nodes: []string{nodeID}},
		}
		c.Entrypoints = []Entrypoint{{
			ID: "e_0000000001", Name: "e", Match: []string{"*"}, Routes: routes,
		}}
		return c
	}

	cases := []struct {
		name   string
		routes []RouteBinding
		// wantErr lists substrings the error must contain; empty means the
		// config must validate.
		wantErr []string
	}{
		{name: "draft", routes: nil},
		{name: "two bindings with probes", routes: []RouteBinding{
			{Route: routeA, Probe: probe()},
			{Route: routeB, Probe: probe()},
		}},
		{name: "probe-less last", routes: []RouteBinding{
			{Route: routeA, Probe: probe()},
			{Route: routeB},
		}},
		{name: "blank route", routes: []RouteBinding{{Route: "  "}},
			wantErr: []string{`entrypoint "e"`, "routes[0]", "route is required"}},
		{name: "duplicate route", routes: []RouteBinding{
			{Route: routeA, Probe: probe()},
			{Route: routeA, Probe: probe()},
		}, wantErr: []string{`entrypoint "e"`, "routes[1]", "duplicate route"}},
		{name: "unknown route", routes: []RouteBinding{{Route: "missing"}},
			wantErr: []string{`entrypoint "e"`, "routes[0]", "unknown route id"}},
		{name: "probe-less first", routes: []RouteBinding{
			{Route: routeA},
			{Route: routeB, Probe: probe()},
		}, wantErr: []string{`entrypoint "e"`, "routes[0]", "must be last"}},
		{name: "two probe-less", routes: []RouteBinding{
			{Route: routeA},
			{Route: routeB},
		}, wantErr: []string{`entrypoint "e"`, "routes[0]", "must be last"}},
		{name: "blank command", routes: []RouteBinding{
			{Route: routeA, Probe: &Probe{Command: "   ", Interval: Duration(30 * time.Second), Timeout: Duration(5 * time.Second)}},
		}, wantErr: []string{`entrypoint "e"`, "routes[0]", "command is required"}},
		{name: "timeout equals interval", routes: []RouteBinding{
			{Route: routeA, Probe: &Probe{Command: "true", Interval: Duration(5 * time.Second), Timeout: Duration(5 * time.Second)}},
		}, wantErr: []string{`entrypoint "e"`, "routes[0]", "timeout"}},
		{name: "interval below a second", routes: []RouteBinding{
			{Route: routeA, Probe: &Probe{Command: "true", Interval: Duration(500 * time.Millisecond), Timeout: Duration(100 * time.Millisecond)}},
		}, wantErr: []string{`entrypoint "e"`, "routes[0]", "interval"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(base(tc.routes...))
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("config should not validate")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestLoadAppliesProbeDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
        probe:
          command: "  true  "
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entrypoints) != 1 || len(c.Entrypoints[0].Routes) != 1 {
		t.Fatalf("entrypoint routes = %+v, want one binding", c.Entrypoints)
	}
	b := c.Entrypoints[0].Routes[0]
	if want := fixtureID(identity.Route, "r"); b.Route != want {
		t.Fatalf("binding route = %q, want %q", b.Route, want)
	}
	if b.Probe == nil {
		t.Fatal("probe = nil, want the configured probe")
	}
	if b.Probe.Command != "true" {
		t.Fatalf("probe.command = %q, want the trimmed command", b.Probe.Command)
	}
	if b.Probe.Interval != Duration(30*time.Second) {
		t.Fatalf("probe.interval = %v, want 30s", time.Duration(b.Probe.Interval))
	}
	if b.Probe.Timeout != Duration(5*time.Second) {
		t.Fatalf("probe.timeout = %v, want 5s", time.Duration(b.Probe.Timeout))
	}
}

func TestLoadRejectsLegacyEntrypointRoute(t *testing.T) {
	_, err := Load(writeConfig(t, `
entrypoints:
  - name: e
    match: ["*"]
    route: r_0000000001
`))
	if err == nil {
		t.Fatal("a config with entrypoint.route should not load")
	}
	if !strings.Contains(err.Error(), "field route not found") {
		t.Fatalf("err = %v, want it to name the unknown route field", err)
	}
}

func TestProbeCommandIsNotEnvExpanded(t *testing.T) {
	t.Setenv("CUTTLE_PROBE_TOKEN", "secret")
	const command = `echo "$CUTTLE_PROBE_TOKEN" | grep -q x`

	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
        probe:
          command: 'echo "$CUTTLE_PROBE_TOKEN" | grep -q x'
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Entrypoints[0].Routes[0].Probe.Command; got != command {
		t.Fatalf("probe.command = %q, want it byte-identical (%q)", got, command)
	}
}

func TestEntrypointRouteBindingExpandsEnv(t *testing.T) {
	t.Setenv("R_ID", "r_0000000001")
	t.Setenv("CUTTLE_PROBE_TOKEN", "secret")

	c := &Config{Entrypoints: []Entrypoint{{
		ID: "e_0000000001", Name: "e", Match: []string{"*"},
		Routes: []RouteBinding{{
			Route: "${R_ID}",
			Probe: &Probe{Command: `echo "$CUTTLE_PROBE_TOKEN"`},
		}},
	}}}
	c.Normalize()

	b := c.Entrypoints[0].Routes[0]
	if b.Route != "r_0000000001" {
		t.Fatalf("binding route = %q, want the expanded id", b.Route)
	}
	if b.Probe.Command != `echo "$CUTTLE_PROBE_TOKEN"` {
		t.Fatalf("probe.command = %q, want it left for the shell", b.Probe.Command)
	}
}

func TestValidateAllowsProviderlessNode(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	c.Nodes[0].Model = ""
	if err := Validate(c); err != nil {
		t.Fatalf("a node without a provider should validate as a draft: %v", err)
	}
}

func TestValidateDraftNodeKeepsModelHint(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	if err := Validate(c); err != nil {
		t.Fatalf("a providerless node may keep its model text: %v", err)
	}
}

func TestValidateProviderlessNodeRejectsKey(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	c.Nodes[0].Key = fixtureID(identity.ProviderKey, "key")
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "node") || !strings.Contains(err.Error(), "key") {
		t.Fatalf("err = %v, want the node rejected for a pinned key without a provider", err)
	}
}

func TestValidateProviderlessNodeRejectsAPI(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = ""
	c.Nodes[0].Model = ""
	c.Nodes[0].API = "chat"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "node") || !strings.Contains(err.Error(), "api") {
		t.Fatalf("err = %v, want the node rejected for a pinned api without a provider", err)
	}
}

func TestValidateNodeRequiresModel(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Model = ""
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("err = %v, want missing model error", err)
	}
}

func TestValidateRejectsDuplicateRouteNode(t *testing.T) {
	c := topologyFixture()
	c.Routes[0].Nodes = []string{c.Nodes[0].ID, c.Nodes[0].ID}
	if err := Validate(c); err == nil {
		t.Fatal("duplicate route node references should not validate")
	}
}

func TestValidateRejectsUnknownProvider(t *testing.T) {
	c := topologyFixture()
	c.Nodes[0].Provider = "p_0000000001"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "provider id") {
		t.Fatalf("err = %v, want an unknown provider id rejected", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	_, err := Load(writeConfig(t, `
gateway:
  lstien: ":8787"
providers: []
routes: []
`))
	if err == nil || !strings.Contains(err.Error(), "lstien") {
		t.Fatalf("err = %v, want unknown field error", err)
	}
}

func TestLoadRejectsLegacyAliasConfig(t *testing.T) {
	_, err := Load(writeConfig(t, `
gateway:
  listen: ":8787"
aliases:
  - match: "*"
    model: fast
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err == nil || !strings.Contains(err.Error(), "aliases") {
		t.Fatalf("err = %v, want an aliases unknown-field error", err)
	}
}

func TestNodeModelExpandsEnv(t *testing.T) {
	t.Setenv("MODEL", "deepseek-v4-flash")
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: "${MODEL}"
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Nodes[0].Model; got != "deepseek-v4-flash" {
		t.Fatalf("model = %q, want expanded value", got)
	}
}

func TestLoadRejectsEmptyKey(t *testing.T) {
	_, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    keys:
      - name: default
        key: "${THIS_ENV_DOES_NOT_EXIST}"
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err == nil || !strings.Contains(err.Error(), "keys[0]") {
		t.Fatalf("err = %v, want empty key error", err)
	}
}

func TestLoadRejectsLegacyProviderFields(t *testing.T) {
	cases := []struct {
		name  string
		field string
		body  string
	}{
		{
			name:  "baseURL",
			field: "baseURL",
			body: `
providers:
  - name: a
    type: generic
    baseURL: https://a.example
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`,
		},
		{
			name:  "protocol",
			field: "protocol",
			body: `
providers:
  - name: a
    type: generic
    protocol: openai
    chat: https://a.example/v1
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`,
		},
		{
			name:  "pathMap",
			field: "pathMap",
			body: `
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    pathMap:
      /v1/chat/completions: /chat/completions
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("err = %v, want unknown field %q", err, tt.field)
			}
		})
	}
}

func TestDurationAndWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cuttle.yaml")
	c, err := Load(writeConfig(t, `
admin:
  listen: "127.0.0.1:8788"
  sessionTTL: 30m
stats:
  path: "stats.sqlite"
  retainRequests: 168h
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: a
    type: generic
    chat: https://a.example/v1
    models:
      m1:
        disabled: true
nodes:
  - name: n
    provider: a
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(c.Admin.SessionTTL); got != 30*time.Minute {
		t.Fatalf("sessionTTL = %v, want 30m", got)
	}
	if got := time.Duration(c.Stats.RetainRequests); got != 168*time.Hour {
		t.Fatalf("retainRequests = %v, want 168h", got)
	}

	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Providers[0].Name != "a" || again.Routes[0].Name != "r" {
		t.Fatalf("round trip mismatch: %+v", again)
	}
	if again.Entrypoints[0].Name != "default" || again.Nodes[0].Model != "m1" {
		t.Fatalf("entrypoint/model lost in round trip: %+v", again)
	}
	if !again.Providers[0].ModelDisabled("m1") {
		t.Fatalf("model override lost in round trip: %+v", again.Providers[0].Models)
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "s3cret") {
		t.Fatal("verify failed for the correct password")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("verify succeeded for a wrong password")
	}
	if VerifyPassword("not-a-hash", "s3cret") {
		t.Fatal("verify succeeded for a malformed hash")
	}
}

func TestProviderAPIRequiresType(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{ID: fixtureID(identity.Provider, "new"), Name: "new", Chat: "https://chat.example/v1"}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("err = %v, want missing type error", err)
	}
}

func TestProviderAPIHealthAPI(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := &Config{
			Gateway: Gateway{Listen: ":8787"},
			Providers: []Provider{{
				ID:        fixtureID(identity.Provider, "p"),
				Name:      "p",
				Type:      "deepseek",
				Chat:      "https://api.deepseek.com/v1",
				Anthropic: "https://api.deepseek.com/anthropic",
				Health:    Health{API: "anthropic"},
			}},
		}
		if err := Validate(c); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})

	t.Run("unknown api", func(t *testing.T) {
		c := &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "deepseek", Chat: "https://api.deepseek.com/v1", Health: Health{API: "grpc"}}},
		}
		err := Validate(c)
		if err == nil || !strings.Contains(err.Error(), "health.api") {
			t.Fatalf("err = %v, want health.api error", err)
		}
	})

	t.Run("missing base", func(t *testing.T) {
		c := &Config{
			Gateway:   Gateway{Listen: ":8787"},
			Providers: []Provider{{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "deepseek", Chat: "https://api.deepseek.com/v1", Health: Health{API: "anthropic"}}},
		}
		err := Validate(c)
		if err == nil || !strings.Contains(err.Error(), "health.api") {
			t.Fatalf("err = %v, want health.api base error", err)
		}
	})
}

func TestProviderAPIRequiresBaseURL(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "generic"}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "API base") {
		t.Fatalf("err = %v, want missing API base error", err)
	}
}

func TestProviderAPIRejectsBadBaseURLs(t *testing.T) {
	cases := []struct {
		name string
		p    Provider
	}{
		{"chat scheme", Provider{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "generic", Chat: "ftp://chat.example"}},
		{"anthropic url", Provider{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "generic", Anthropic: "://bad"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Gateway: Gateway{Listen: ":8787"}, Providers: []Provider{tt.p}}
			if err := Validate(c); err == nil {
				t.Fatalf("provider %+v should not validate", tt.p)
			}
		})
	}
}

func TestProviderAPIExpandsEnv(t *testing.T) {
	t.Setenv("TEST_CHAT_BASE", "https://chat.example/v1")
	t.Setenv("TEST_GEMINI_BASE", "https://gemini.example/v1beta")
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: "${TEST_CHAT_BASE}"
    gemini: "${TEST_GEMINI_BASE}"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].Chat; got != "https://chat.example/v1" {
		t.Fatalf("chat = %q, want expanded base URL", got)
	}
	if got := c.Providers[0].Gemini; got != "https://gemini.example/v1beta" {
		t.Fatalf("gemini = %q, want expanded base URL", got)
	}
}

func TestProviderAPINewStyleUsesAdapterAuthDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: "https://chat.example/v1"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].Auth.Header; got != "" {
		t.Fatalf("auth.header = %q, want empty so the adapter supplies the default", got)
	}
}

func TestProviderAPIHealthAPINormalized(t *testing.T) {
	for _, raw := range []string{"CHAT", "Chat", " chat "} {
		t.Run(raw, func(t *testing.T) {
			c, err := Load(writeConfig(t, fmt.Sprintf(`
providers:
  - name: p
    type: deepseek
    chat: "https://chat.example/v1"
    health:
      api: %q
`, raw)))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := c.Providers[0].Health.API; got != "chat" {
				t.Fatalf("health.api = %q, want canonical %q", got, "chat")
			}
		})
	}
}

func TestProviderAPIHealthAPIRejectsInvalidValue(t *testing.T) {
	_, err := Load(writeConfig(t, `
providers:
  - name: p
    type: deepseek
    chat: "https://chat.example/v1"
    health:
      api: " GRPC "
`))
	if err == nil || !strings.Contains(err.Error(), "health.api") {
		t.Fatalf("err = %v, want health.api error", err)
	}
}

func TestProviderAPIRejectsUnknownType(t *testing.T) {
	c := &Config{
		Gateway:   Gateway{Listen: ":8787"},
		Providers: []Provider{{ID: fixtureID(identity.Provider, "p"), Name: "p", Type: "bogus", Chat: "https://chat.example/v1"}},
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v, want unknown type error", err)
	}
}

func TestProviderModelOverrides(t *testing.T) {
	t.Setenv("MODEL_ID", "glm-5.3")
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      " ${MODEL_ID} ":
        disabled: true
      empty:
        disabled: false
nodes:
  - name: n
    provider: p
    model: m1
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Providers[0]
	if !p.ModelDisabled("glm-5.3") {
		t.Fatalf("models = %+v", p.Models)
	}
	if _, ok := p.Models["empty"]; ok {
		t.Fatalf("disabled:false should be dropped: %+v", p.Models)
	}
	if p.ModelDisabled("missing") {
		t.Fatal("unknown model must not be disabled")
	}
}

func TestProviderModelOverridesMergeExpandedDuplicates(t *testing.T) {
	t.Setenv("DUP_MODEL_ID", "shared-model")
	path := writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      "${DUP_MODEL_ID}":
        disabled: false
      "$DUP_MODEL_ID":
        disabled: true
`)
	// Map iteration made the winning entry nondeterministic before the merge;
	// repeat so a regression cannot pass by chance.
	for i := 0; i < 20; i++ {
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		p := c.Providers[0]
		if len(p.Models) != 1 {
			t.Fatalf("models = %+v, want one merged entry", p.Models)
		}
		if !p.ModelDisabled("shared-model") {
			t.Fatalf("models = %+v, want disabled:true to win the merge", p.Models)
		}
	}
}

func TestProviderModelOverrideUnknownField(t *testing.T) {
	_, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      m1:
        enabled: true
`))
	if err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("err = %v, want unknown field error", err)
	}
}

func TestProviderModelOverridesDropEmptyID(t *testing.T) {
	t.Setenv("MODEL_ID_NOT_SET", "")
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
    models:
      " ${MODEL_ID_NOT_SET} ":
        disabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if models := c.Providers[0].Models; len(models) != 0 {
		t.Fatalf("models = %+v, want empty after dropping empty IDs", models)
	}
}

func TestTopologyIncludes(t *testing.T) {
	c := &Config{
		Routes: []Route{{ID: "r_f3k9x2m4qa", Name: "fast", Nodes: []string{"n_4q7a9x2m3k", "n_7a4m2xk9d3"}}},
		Nodes: []Node{
			{ID: "n_4q7a9x2m3k", Name: "n1", Provider: "p_3k9x2m4q7a", Model: "m1"},
			{ID: "n_7a4m2xk9d3", Name: "n2", Provider: "p_8m2xq7a4k9", Model: "m2"},
		},
	}

	cases := []struct {
		route, node, provider string
		want                  bool
	}{
		{"r_f3k9x2m4qa", "n_4q7a9x2m3k", "p_3k9x2m4q7a", true},
		{"r_f3k9x2m4qa", "n_7a4m2xk9d3", "p_8m2xq7a4k9", true},
		{"", "n_orphan", "p_3k9x2m4q7a", true},
		{"r_f3k9x2m4qa", "", "", true},
		{"r_missing", "n_4q7a9x2m3k", "p_3k9x2m4q7a", false},
		{"r_f3k9x2m4qa", "n_orphan", "p_3k9x2m4q7a", false},
		{"r_f3k9x2m4qa", "n_4q7a9x2m3k", "p_8m2xq7a4k9", false},
	}
	for _, tc := range cases {
		if got := c.TopologyIncludes(tc.route, tc.node, tc.provider); got != tc.want {
			t.Fatalf("TopologyIncludes(%q,%q,%q) = %v, want %v",
				tc.route, tc.node, tc.provider, got, tc.want)
		}
	}
}

// TestTopologyIncludesIDKeyedRows pins the filter to ids: a migrated config
// references nodes by id, and a stats row that still spelled a display name is
// dropped instead of being reattributed.
func TestTopologyIncludesIDKeyedRows(t *testing.T) {
	c, err := Load(writeConfig(t, `
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
  - name: p2
    type: generic
    chat: https://b.example/v1
nodes:
  - name: n
    provider: p
    model: m1
  - name: n2
    provider: p2
    model: m2
routes:
  - name: r
    nodes: [n]
entrypoints:
  - name: e
    match: ["*"]
    routes:
      - route: r
`))
	if err != nil {
		t.Fatal(err)
	}
	routeID, nodeID, providerID := c.Routes[0].ID, c.Nodes[0].ID, c.Providers[0].ID
	if routeID == "" || nodeID == "" || providerID == "" {
		t.Fatalf("migration did not assign ids: %q %q %q", routeID, nodeID, providerID)
	}
	if c.Routes[0].Nodes[0] != nodeID {
		t.Fatalf("route nodes = %v, want the node id %q", c.Routes[0].Nodes, nodeID)
	}

	kept := []struct {
		name              string
		route, node, prov string
	}{
		{"ids", routeID, nodeID, providerID},
		{"route id only", routeID, nodeID, ""},
		{"node id only", routeID, nodeID, providerID},
		{"orphan node", "", nodeID, providerID},
	}
	for _, tc := range kept {
		if !c.TopologyIncludes(tc.route, tc.node, tc.prov) {
			t.Fatalf("%s: TopologyIncludes(%q,%q,%q) = false, want true",
				tc.name, tc.route, tc.node, tc.prov)
		}
	}

	dropped := []struct {
		name              string
		route, node, prov string
	}{
		{"route name", "r", nodeID, providerID},
		{"node name", routeID, "n", providerID},
		{"provider name", routeID, nodeID, "p"},
		{"unknown node name", routeID, "ghost", providerID},
		{"unknown node id", routeID, "n_4q7a9x2m3k", providerID},
		{"node of another route", routeID, c.Nodes[1].ID, c.Providers[1].ID},
		{"provider mismatch", routeID, nodeID, c.Providers[1].ID},
		{"unknown route", "r_4q7a9x2m3k", nodeID, providerID},
	}
	for _, tc := range dropped {
		if c.TopologyIncludes(tc.route, tc.node, tc.prov) {
			t.Fatalf("%s: TopologyIncludes(%q,%q,%q) = true, want false",
				tc.name, tc.route, tc.node, tc.prov)
		}
	}
}

func TestRouteFailSettingsDefaultsAndValidation(t *testing.T) {
	c, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: p
    model: m
routes:
  - name: r
    nodes: [n]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Routes[0].MaxFails; got != 1 {
		t.Fatalf("maxFails = %d, want 1", got)
	}
	if got := time.Duration(c.Routes[0].FailTimeout); got != 10*time.Second {
		t.Fatalf("failTimeout = %v, want 10s", got)
	}

	c, err = Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: p
    model: m
routes:
  - name: r
    nodes: [n]
    maxFails: 3
    failTimeout: 30s
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Routes[0].MaxFails != 3 || time.Duration(c.Routes[0].FailTimeout) != 30*time.Second {
		t.Fatalf("route settings = %+v", c.Routes[0])
	}

	c, err = Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: p
    model: m
routes:
  - name: r
    nodes: [n]
    strategy: sticky
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Routes[0].Strategy != "sticky" {
		t.Fatalf("strategy = %q, want sticky", c.Routes[0].Strategy)
	}

	for _, body := range []string{
		`maxFails: -1`,
		`failTimeout: -1s`,
		`strategy: bogus`,
	} {
		_, err := Load(writeConfig(t, `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: p
    model: m
routes:
  - name: r
    nodes: [n]
    `+body+`
`))
		if err == nil {
			t.Fatalf("%s: want validation error", body)
		}
	}
}

func routeRetryConfig(extra string) string {
	return `
entrypoints:
  - name: default
    match: ["*"]
    routes:
      - route: r
providers:
  - name: p
    type: generic
    chat: https://a.example/v1
nodes:
  - name: n
    provider: p
    model: m
routes:
  - name: r
    nodes: [n]
` + extra
}

func TestRouteRetryDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, routeRetryConfig("")))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Routes[0].Retries; got != 0 {
		t.Fatalf("retries = %d, want 0", got)
	}
	if got := c.Routes[0].RetryCodes; got != "429,5xx" {
		t.Fatalf("retryCodes = %q, want %q", got, "429,5xx")
	}
}

func TestRouteRetryValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"negative retries", "    retries: -1\n", `route "r": retries must be between 0 and 10`},
		{"too many retries", "    retries: 11\n", `route "r": retries must be between 0 and 10`},
		{"token not a number", "    retryCodes: \"abc\"\n", `route "r": retryCodes:`},
		{"short wildcard", "    retryCodes: \"5x\"\n", `route "r": retryCodes:`},
		{"code below 100", "    retryCodes: \"99\"\n", `route "r": retryCodes:`},
		{"empty token", "    retryCodes: \"429,,502\"\n", `route "r": retryCodes:`},
	}
	for _, tc := range cases {
		_, err := Load(writeConfig(t, routeRetryConfig(tc.body)))
		if err == nil {
			t.Fatalf("%s: want validation error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %q, want it to contain %q", tc.name, err, tc.want)
		}
	}

	c, err := Load(writeConfig(t, routeRetryConfig("    retryCodes: \"429,5xx,502\"\n")))
	if err != nil {
		t.Fatalf("valid retryCodes rejected: %v", err)
	}
	if got := c.Routes[0].RetryCodes; got != "429,5xx,502" {
		t.Fatalf("retryCodes = %q, want %q", got, "429,5xx,502")
	}
}

func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test")
	body, err := os.ReadFile("../../example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Load migrates a legacy config and writes it back, so never point it at
	// the repository file.
	path := writeConfig(t, string(body))

	// Decode the raw copy without migration: the committed example must carry
	// ids and id references on its own, so a first load never rewrites it.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var raw Config
	decodeErr := dec.Decode(&raw)
	f.Close()
	if decodeErr != nil {
		t.Fatalf("decode example: %v", decodeErr)
	}

	providerIDs := make(map[string]bool, len(raw.Providers))
	keyIDs := make(map[string]bool)
	for _, p := range raw.Providers {
		if !identity.Valid(identity.Provider, p.ID) {
			t.Fatalf("provider %q: id = %q, want a provider id", p.Name, p.ID)
		}
		providerIDs[p.ID] = true
		for _, k := range p.Keys {
			if !identity.Valid(identity.ProviderKey, k.ID) {
				t.Fatalf("provider %q: key %q: id = %q, want a provider key id", p.Name, k.Name, k.ID)
			}
			keyIDs[p.ID+"\x00"+k.ID] = true
		}
	}

	nodeIDs := make(map[string]bool, len(raw.Nodes))
	for _, n := range raw.Nodes {
		if !identity.Valid(identity.Node, n.ID) {
			t.Fatalf("node %q: id = %q, want a node id", n.Name, n.ID)
		}
		nodeIDs[n.ID] = true
		if !providerIDs[n.Provider] {
			t.Fatalf("node %q: provider = %q, want a provider id", n.Name, n.Provider)
		}
		if n.Key != "" && !keyIDs[n.Provider+"\x00"+n.Key] {
			t.Fatalf("node %q: key = %q, want a provider key id of that provider", n.Name, n.Key)
		}
	}

	routeIDs := make(map[string]bool, len(raw.Routes))
	for _, r := range raw.Routes {
		if !identity.Valid(identity.Route, r.ID) {
			t.Fatalf("route %q: id = %q, want a route id", r.Name, r.ID)
		}
		routeIDs[r.ID] = true
		for _, ref := range r.Nodes {
			if !nodeIDs[ref] {
				t.Fatalf("route %q: node reference = %q, want a node id", r.Name, ref)
			}
		}
	}

	for _, e := range raw.Entrypoints {
		if !identity.Valid(identity.Entrypoint, e.ID) {
			t.Fatalf("entrypoint %q: id = %q, want an entrypoint id", e.Name, e.ID)
		}
		for _, b := range e.Routes {
			if !routeIDs[b.Route] {
				t.Fatalf("entrypoint %q: routes[].route = %q, want a route id", e.Name, b.Route)
			}
		}
	}

	for _, k := range raw.Gateway.Keys {
		if !identity.Valid(identity.GatewayKey, k.ID) {
			t.Fatalf("gateway key %q: id = %q, want a gateway key id", k.Name, k.ID)
		}
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	if len(c.Entrypoints) == 0 {
		t.Fatal("example has no entrypoints")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("load rewrote the example config; it must already carry ids")
	}
	if backups, err := filepath.Glob(path + ".bak-*"); err != nil {
		t.Fatal(err)
	} else if len(backups) != 0 {
		t.Fatalf("load created backup %v; it must not migrate the example", backups)
	}
}

func TestLogDefaultsAndValidation(t *testing.T) {
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Level != "info" || c.Log.Format != "json" {
		t.Fatalf("log defaults = %+v", c.Log)
	}

	for _, body := range []string{
		"log:\n  level: loud\n",
		"log:\n  format: yaml\n",
	} {
		if _, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"+body)); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestLogExpandsEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
log:
  level: ${LOG_LEVEL}
  format: ${LOG_FORMAT}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Level != "debug" || c.Log.Format != "text" {
		t.Fatalf("expanded log = %+v", c.Log)
	}
}

func TestCaptureDefaultsAndValidation(t *testing.T) {
	c, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stats.Capture.Mode != "off" || c.Stats.Capture.MaxBytes != 1048576 ||
		c.Stats.Capture.Retain != Duration(24*time.Hour) {
		t.Fatalf("capture defaults = %+v", c.Stats.Capture)
	}
	for _, body := range []string{
		"stats:\n  capture:\n    mode: always\n",
		"stats:\n  capture:\n    mode: sample\n    sampleRate: 2\n",
		"stats:\n  capture:\n    mode: errors\n    maxBytes: 1\n",
		"stats:\n  capture:\n    mode: errors\n    maxBytes: 1048577\n",
	} {
		if _, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"+body)); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestGatewayReadTimeoutDefaultsAndValidation(t *testing.T) {
	c, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway.ReadTimeout != Duration(60*time.Second) {
		t.Fatalf("readTimeout = %v, want 60s", c.Gateway.ReadTimeout)
	}
	for _, body := range []string{
		"gateway:\n  listen: \"127.0.0.1:0\"\n  readTimeout: 500ms\n",
		"gateway:\n  listen: \"127.0.0.1:0\"\n  readTimeout: 301s\n",
	} {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestUserAgentAndHeaderValidation(t *testing.T) {
	c, err := Load(writeConfig(t, "gateway:\n  listen: \"127.0.0.1:0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Gateway.UserAgent, "cuttle/") {
		t.Fatalf("gateway.userAgent = %q", c.Gateway.UserAgent)
	}
	for _, body := range []string{
		"gateway:\n  listen: \"127.0.0.1:0\"\nproviders:\n  - name: p\n    type: generic\n    chat: https://example.com/v1\n    headers:\n      Host: evil\n",
		"gateway:\n  listen: \"127.0.0.1:0\"\nproviders:\n  - name: p\n    type: generic\n    chat: https://example.com/v1\n    headers:\n      Accept-Encoding: gzip\n",
		"gateway:\n  listen: \"127.0.0.1:0\"\nproviders:\n  - name: p\n    type: generic\n    chat: https://example.com/v1\n    headers:\n      Bad Header: x\n",
		"gateway:\n  listen: \"127.0.0.1:0\"\nproviders:\n  - name: p\n    type: generic\n    chat: https://example.com/v1\n    userAgent: \"bad\\nvalue\"\n",
	} {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestCaptureExplicitZeroSampleRate(t *testing.T) {
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
stats:
  capture:
    mode: sample
    sampleRate: 0
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stats.Capture.SampleRate == nil || *c.Stats.Capture.SampleRate != 0 {
		t.Fatalf("sampleRate = %v, want explicit 0", c.Stats.Capture.SampleRate)
	}
}

func TestLoadGatewayKeys(t *testing.T) {
	t.Setenv("GW_KEY", "sk-gw-secret")
	t.Setenv("GW_NAME", "claude-code")
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
  keys:
    - name: " ${GW_NAME} "
      key: " ${GW_KEY} "
    - name: disabled
      key: sk-gw-disabled
      enabled: false
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Gateway.Keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(c.Gateway.Keys))
	}
	first := c.Gateway.Keys[0]
	if first.Name != "claude-code" {
		t.Fatalf("keys[0].name = %q, want claude-code", first.Name)
	}
	if first.Key != "sk-gw-secret" {
		t.Fatalf("keys[0].key = %q, want expanded value", first.Key)
	}
	if first.Enabled != nil {
		t.Fatalf("keys[0].enabled = %v, want nil (default true)", *first.Enabled)
	}
	second := c.Gateway.Keys[1]
	if second.Enabled == nil || *second.Enabled {
		t.Fatalf("keys[1].enabled = %v, want explicit false", second.Enabled)
	}
}

func TestLoadGatewayKeysEmptyIsOpen(t *testing.T) {
	c, err := Load(writeConfig(t, `
gateway:
  listen: "127.0.0.1:0"
  keys: []
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Gateway.Keys) != 0 {
		t.Fatalf("keys = %#v, want none", c.Gateway.Keys)
	}
}

func TestValidateGatewayKeys(t *testing.T) {
	tests := []struct {
		name string
		keys []GatewayKey
		want string
	}{
		{
			name: "duplicate name",
			keys: []GatewayKey{
				{Name: "dup", Key: "sk-gateway-key-1"},
				{Name: "dup", Key: "sk-gateway-key-2"},
			},
			want: `gateway.keys[1]: duplicate name "dup"`,
		},
		{
			name: "empty name",
			keys: []GatewayKey{{Name: "  ", Key: "sk-gateway-key-1"}},
			want: "gateway.keys[0]: name is required",
		},
		{
			name: "dollar in name",
			keys: []GatewayKey{{Name: "bad$name", Key: "sk-gateway-key-1"}},
			want: `gateway.keys[0]: name "bad$name" must not contain '$'`,
		},
		{
			name: "control character in name",
			keys: []GatewayKey{{Name: "bad\x01name", Key: "sk-gateway-key-1"}},
			want: `gateway.keys[0]: name "bad\x01name" contains control characters`,
		},
		{
			name: "name too long",
			keys: []GatewayKey{{Name: strings.Repeat("n", 65), Key: "sk-gateway-key-1"}},
			want: fmt.Sprintf(`gateway.keys[0]: name %q must be at most 64 characters`, strings.Repeat("n", 65)),
		},
		{
			name: "empty key",
			keys: []GatewayKey{{Name: "a", Key: ""}},
			want: "gateway.keys[0]: key is required",
		},
		{
			name: "key too short",
			keys: []GatewayKey{{Name: "a", Key: "abc1234"}},
			want: "gateway.keys[0]: key is too short",
		},
		{
			name: "key too long",
			keys: []GatewayKey{{Name: "a", Key: strings.Repeat("k", 513)}},
			want: "gateway.keys[0]: key is too long",
		},
		{
			name: "dollar in key",
			keys: []GatewayKey{{Name: "a", Key: "sk-gateway-$key"}},
			want: "gateway.keys[0]: key must not contain '$'",
		},
		{
			name: "control character in key",
			keys: []GatewayKey{{Name: "a", Key: "sk-gateway\nkey"}},
			want: "gateway.keys[0]: key contains control characters",
		},
		{
			name: "NUL in key",
			keys: []GatewayKey{{Name: "a", Key: "sk-gateway\x00key"}},
			want: "gateway.keys[0]: key contains control characters",
		},
		{
			name: "duplicate key",
			keys: []GatewayKey{
				{Name: "a", Key: "sk-gateway-key-1"},
				{Name: "b", Key: "sk-gateway-key-1"},
			},
			want: "gateway.keys[1]: duplicate key value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Gateway: Gateway{Keys: withGatewayKeyIDs(tt.keys)}}
			ApplyDefaults(cfg)
			err := Validate(cfg)
			if err == nil {
				t.Fatal("want validation error")
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.want) {
				t.Fatalf("err = %q, want it to contain %q", msg, tt.want)
			}
			for _, k := range tt.keys {
				if k.Key != "" && strings.Contains(msg, k.Key) {
					t.Fatalf("err = %q leaks key value", msg)
				}
			}
		})
	}
}

func TestValidateGatewayKeysLengthBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"minimum", "12345678", false},
		{"trimmed minimum", "  12345678  ", false},
		{"maximum", strings.Repeat("k", 512), false},
		{"too short", "1234567", true},
		{"too long", strings.Repeat("k", 513), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Gateway: Gateway{Keys: withGatewayKeyIDs([]GatewayKey{{Name: "a", Key: tc.key}})}}
			ApplyDefaults(cfg)
			err := Validate(cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestWriteNewDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cuttle.yaml")

	first := &Config{Gateway: Gateway{Listen: ":1111"}}
	ApplyDefaults(first)
	if err := Write(path, first); err != nil {
		t.Fatal(err)
	}

	second := &Config{Gateway: Gateway{Listen: ":2222"}}
	ApplyDefaults(second)
	if err := WriteNew(path, second); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("WriteNew err = %v, want ErrConfigExists", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gateway.Listen != ":1111" {
		t.Fatalf("listen = %q, want :1111", got.Gateway.Listen)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, second); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gateway.Listen != ":2222" {
		t.Fatalf("listen = %q, want :2222", got.Gateway.Listen)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

// validEntityConfig is the shared valid fixture: every entity carries an id
// and every reference is one.
func validEntityConfig() *Config {
	return topologyFixture()
}

func TestValidateNameRules(t *testing.T) {
	for _, name := range []string{"", "  ", "a$b", "ctl\x01", strings.Repeat("x", 65)} {
		c := validEntityConfig()
		c.Providers[0].Name = name
		err := Validate(c)
		if err == nil {
			t.Fatalf("provider name %q should not validate", name)
		}
		if !strings.Contains(err.Error(), "providers[0]") {
			t.Fatalf("name %q: err = %v, want it to name the provider", name, err)
		}
	}
	for _, name := range []string{"DeepSeek 官方", "a/b", "x y", strings.Repeat("x", 64)} {
		c := validEntityConfig()
		c.Providers[0].Name = name
		if err := Validate(c); err != nil {
			t.Fatalf("provider name %q should validate: %v", name, err)
		}
	}
}

func TestValidateNameRulesPerKind(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"provider", func(c *Config) { c.Providers[0].Name = "a$b" }, "providers[0]"},
		{"provider key", func(c *Config) { c.Providers[0].Keys[0].Name = "a\x01b" }, `provider "provider"`},
		{"node", func(c *Config) { c.Nodes[0].Name = strings.Repeat("n", 65) }, "nodes[0]"},
		{"route", func(c *Config) { c.Routes[0].Name = "a$b" }, "routes[0]"},
		{"entrypoint", func(c *Config) { c.Entrypoints[0].Name = "a\x01b" }, "entrypoints[0]"},
		{"gateway key", func(c *Config) {
			c.Gateway.Keys = []GatewayKey{{ID: "gk_0000000001", Name: "a\x01b", Key: "sk-gateway-key"}}
		}, "gateway.keys[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validEntityConfig()
			tc.mutate(c)
			err := Validate(c)
			if err == nil {
				t.Fatal("want a name error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateSlashInKeyNamesAllowed(t *testing.T) {
	c := validEntityConfig()
	c.Providers[0].Keys[0].Name = "team/primary"
	c.Gateway.Keys = []GatewayKey{{ID: "gk_0000000001", Name: "laptop/home", Key: "sk-gateway-key"}}
	c.Nodes[0].Key = c.Providers[0].Keys[0].ID
	if err := Validate(c); err != nil {
		t.Fatalf("slash in key names should validate: %v", err)
	}
}

func TestValidateDuplicateNamesAcrossKinds(t *testing.T) {
	c := validEntityConfig()
	c.Providers[0].Name = "shared"
	c.Nodes[0].Name = "shared"
	c.Routes[0].Name = "shared"
	c.Entrypoints[0].Name = "shared"
	if err := Validate(c); err != nil {
		t.Fatalf("duplicate names across kinds should validate: %v", err)
	}

	c2 := validEntityConfig()
	c2.Providers = append(c2.Providers, Provider{
		ID: "p_0000000002", Name: "provider", Type: "generic", Chat: "https://b.example/v1",
	})
	if err := Validate(c2); err == nil {
		t.Fatal("duplicate provider names should not validate")
	}
}

func TestLoadAcceptsEntityIDs(t *testing.T) {
	c, err := Load(writeConfig(t, `
gateway:
  listen: ":8787"
  keys:
    - id: gk_9x2m4q7a3k
      name: laptop
      key: sk-gateway-key
entrypoints:
  - id: e_q7a4m2xk9d
    name: entry
    match: ["*"]
    routes:
      - route: route
providers:
  - id: p_3k9x2m4q7a
    name: provider
    type: generic
    chat: https://a.example/v1
    keys:
      - id: pk_8m2xq7a4k9
        name: key
        key: sk-a
nodes:
  - id: n_4q7a9x2m3k
    name: node
    provider: provider
    model: m1
routes:
  - id: r_f3k9x2m4qa
    name: route
    nodes: [node]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Gateway.Keys[0].ID; got != "gk_9x2m4q7a3k" {
		t.Fatalf("gateway key id = %q", got)
	}
	if got := c.Entrypoints[0].ID; got != "e_q7a4m2xk9d" {
		t.Fatalf("entrypoint id = %q", got)
	}
	if got := c.Providers[0].ID; got != "p_3k9x2m4q7a" {
		t.Fatalf("provider id = %q", got)
	}
	if got := c.Providers[0].Keys[0].ID; got != "pk_8m2xq7a4k9" {
		t.Fatalf("provider key id = %q", got)
	}
	if got := c.Nodes[0].ID; got != "n_4q7a9x2m3k" {
		t.Fatalf("node id = %q", got)
	}
	if got := c.Routes[0].ID; got != "r_f3k9x2m4qa" {
		t.Fatalf("route id = %q", got)
	}
}

func TestEntityIDsExpandEnv(t *testing.T) {
	t.Setenv("PROVIDER_ID", "p_3k9x2m4q7a")
	c, err := Load(writeConfig(t, `
gateway:
  listen: ":8787"
entrypoints:
  - name: entry
    match: ["*"]
    routes:
      - route: route
providers:
  - id: "${PROVIDER_ID}"
    name: provider
    type: generic
    chat: https://a.example/v1
nodes:
  - name: node
    provider: provider
    model: m1
routes:
  - name: route
    nodes: [node]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].ID; got != "p_3k9x2m4q7a" {
		t.Fatalf("provider id = %q, want expanded value", got)
	}
}

// TestValidateRequiresIDs pins the mandatory id: only a migrated or
// admin-written config validates, and every kind names the entity that lost its
// id.
func TestValidateRequiresIDs(t *testing.T) {
	withIDs := validEntityConfig()
	withIDs.Gateway.Keys = []GatewayKey{{ID: "gk_9x2m4q7a3k", Name: "laptop", Key: "sk-gateway-key"}}
	if err := Validate(withIDs); err != nil {
		t.Fatalf("valid ids should validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"provider missing id", func(c *Config) {
			c.Providers[0].ID = ""
		}, `provider "provider": id is required`},
		{"provider key missing id", func(c *Config) {
			c.Providers[0].Keys[0].ID = ""
		}, `provider "provider": keys[0]: id is required`},
		{"node missing id", func(c *Config) {
			c.Nodes[0].ID = ""
		}, `node "node": id is required`},
		{"route missing id", func(c *Config) {
			c.Routes[0].ID = ""
		}, `route "route": id is required`},
		{"entrypoint missing id", func(c *Config) {
			c.Entrypoints[0].ID = ""
		}, `entrypoint "entry": id is required`},
		{"gateway key missing id", func(c *Config) {
			c.Gateway.Keys = []GatewayKey{{Name: "laptop", Key: "sk-gateway-key"}}
		}, "gateway.keys[0]: id is required"},
		{"provider wrong shape", func(c *Config) { c.Providers[0].ID = "p_short" }, `provider "provider"`},
		{"provider padded id", func(c *Config) { c.Providers[0].ID = " p_3k9x2m4q7a" }, "surrounding whitespace"},
		{"provider wrong prefix", func(c *Config) { c.Providers[0].ID = "n_4q7a9x2m3k" }, `provider "provider"`},
		{"provider key wrong kind", func(c *Config) { c.Providers[0].Keys[0].ID = "p_3k9x2m4q7a" }, `provider "provider"`},
		{"node wrong kind", func(c *Config) { c.Nodes[0].ID = "r_f3k9x2m4qa" }, `node "node"`},
		{"route wrong shape", func(c *Config) { c.Routes[0].ID = "r_3K9X2M4Q7A" }, `route "route"`},
		{"entrypoint wrong kind", func(c *Config) { c.Entrypoints[0].ID = "e_q7a4m2xk9" }, `entrypoint "entry"`},
		{"gateway key wrong kind", func(c *Config) {
			c.Gateway.Keys = []GatewayKey{{ID: "p_3k9x2m4q7a", Name: "laptop", Key: "sk-gateway-key"}}
		}, "gateway.keys[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				Gateway:     withIDs.Gateway,
				Entrypoints: append([]Entrypoint(nil), withIDs.Entrypoints...),
				Providers:   append([]Provider(nil), withIDs.Providers...),
				Nodes:       append([]Node(nil), withIDs.Nodes...),
				Routes:      append([]Route(nil), withIDs.Routes...),
			}
			c.Providers[0].Keys = append([]ProviderKey(nil), withIDs.Providers[0].Keys...)
			c.Gateway.Keys = append([]GatewayKey(nil), withIDs.Gateway.Keys...)
			tc.mutate(c)
			err := Validate(c)
			if err == nil {
				t.Fatal("want an id error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestValidateDuplicateNameBeatsDuplicateID keeps the error actionable: two
// entries that share a name would also share the id migration derives from it,
// and the name is the real problem.
func TestValidateDuplicateNameBeatsDuplicateID(t *testing.T) {
	c := validEntityConfig()
	c.Providers[0].ID = "p_3k9x2m4q7a"
	dup := c.Providers[0]
	c.Providers = append(c.Providers, dup)

	err := Validate(c)
	if err == nil {
		t.Fatal("duplicate providers should not validate")
	}
	if !strings.Contains(err.Error(), `duplicate name "provider"`) {
		t.Fatalf("err = %v, want the duplicate name to win over the duplicate id", err)
	}
}

// TestValidateRejectsDuplicateIDs covers the global id uniqueness rule: a
// hand-edited or merged config that reuses an id must fail validation with both
// entities named, not silently resolve to the first or last one.
func TestValidateRejectsDuplicateIDs(t *testing.T) {
	base := func() *Config {
		c := validEntityConfig()
		c.Providers[0].ID = "p_3k9x2m4q7a"
		c.Providers[0].Keys[0].ID = "pk_8m2xq7a4k9"
		c.Nodes[0].ID = "n_4q7a9x2m3k"
		c.Nodes[0].Provider = "p_3k9x2m4q7a"
		c.Routes[0].ID = "r_f3k9x2m4qa"
		c.Routes[0].Nodes = []string{"n_4q7a9x2m3k"}
		c.Entrypoints[0].ID = "e_q7a4m2xk9d"
		c.Entrypoints[0].Routes = []RouteBinding{{Route: "r_f3k9x2m4qa"}}
		c.Gateway.Keys = []GatewayKey{{ID: "gk_9x2m4q7a3k", Name: "laptop", Key: "sk-gateway-key"}}
		return c
	}
	if err := Validate(base()); err != nil {
		t.Fatalf("distinct ids should validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		want   []string
	}{
		{
			"providers",
			func(c *Config) {
				c.Providers = append(c.Providers, Provider{
					ID: "p_3k9x2m4q7a", Name: "provider2", Type: "generic",
					Chat: "https://b.example/v1", Keys: []ProviderKey{{ID: "pk_0000000002", Name: "key", Key: "sk-b"}},
				})
			},
			[]string{"duplicate id", "p_3k9x2m4q7a", `provider "provider2"`, `provider "provider"`},
		},
		{
			"provider keys",
			func(c *Config) {
				c.Providers[0].Keys = append(c.Providers[0].Keys,
					ProviderKey{ID: "pk_8m2xq7a4k9", Name: "key2", Key: "sk-b"})
			},
			[]string{"duplicate id", "pk_8m2xq7a4k9", "keys[1]", "keys[0]"},
		},
		{
			"nodes",
			func(c *Config) {
				c.Nodes = append(c.Nodes, Node{
					ID: "n_4q7a9x2m3k", Name: "node2", Provider: "p_3k9x2m4q7a", Model: "m2",
				})
			},
			[]string{"duplicate id", "n_4q7a9x2m3k", `node "node2"`, `node "node"`},
		},
		{
			"routes",
			func(c *Config) {
				c.Routes = append(c.Routes, Route{
					ID: "r_f3k9x2m4qa", Name: "route2", Nodes: []string{"n_4q7a9x2m3k"},
				})
			},
			[]string{"duplicate id", "r_f3k9x2m4qa", `route "route2"`, `route "route"`},
		},
		{
			"entrypoints",
			func(c *Config) {
				c.Entrypoints = append(c.Entrypoints, Entrypoint{
					ID: "e_q7a4m2xk9d", Name: "entry2", Match: []string{"*"}, Routes: []RouteBinding{{Route: "r_f3k9x2m4qa"}},
				})
			},
			[]string{"duplicate id", "e_q7a4m2xk9d", `entrypoint "entry2"`, `entrypoint "entry"`},
		},
		{
			"gateway keys",
			func(c *Config) {
				c.Gateway.Keys = append(c.Gateway.Keys,
					GatewayKey{ID: "gk_9x2m4q7a3k", Name: "phone", Key: "sk-gateway-key-2"})
			},
			[]string{"duplicate id", "gk_9x2m4q7a3k", "gateway.keys[1]", "gateway.keys[0]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := Validate(c)
			if err == nil {
				t.Fatal("duplicate ids should not validate")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}
