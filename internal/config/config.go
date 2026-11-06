// Package config defines the llm-gw YAML configuration and its validation.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// Config is the top-level llm-gw configuration.
type Config struct {
	Gateway     Gateway      `yaml:"gateway" json:"gateway"`
	Admin       Admin        `yaml:"admin" json:"admin"`
	Stats       Stats        `yaml:"stats" json:"stats"`
	Log         Log          `yaml:"log" json:"log"`
	Entrypoints []Entrypoint `yaml:"entrypoints" json:"entrypoints"`
	Providers   []Provider   `yaml:"providers" json:"providers"`
	Nodes       []Node       `yaml:"nodes" json:"nodes"`
	Routes      []Route      `yaml:"routes" json:"routes"`
}

// Gateway holds data-plane settings.
type Gateway struct {
	Listen string `yaml:"listen" json:"listen"`
}

// Admin holds the management API settings.
type Admin struct {
	Listen       string   `yaml:"listen" json:"listen"`
	PasswordHash string   `yaml:"passwordHash" json:"-"`
	SessionTTL   Duration `yaml:"sessionTTL" json:"sessionTTL"`
	// RememberTTL bounds a session created with "remember me"; it is
	// persisted across restarts, unlike a normal session.
	RememberTTL Duration `yaml:"rememberTTL" json:"rememberTTL"`
}

// Stats holds statistics storage settings (used from P4).
type Stats struct {
	Path           string   `yaml:"path" json:"path"`
	RetainRequests Duration `yaml:"retainRequests" json:"retainRequests"`
	RetainRollups  Duration `yaml:"retainRollups" json:"retainRollups"`
	Capture        Capture  `yaml:"capture" json:"capture"`
}

// Capture configures bounded request/response body capture.
type Capture struct {
	Mode       string   `yaml:"mode" json:"mode"`
	SampleRate *float64 `yaml:"sampleRate" json:"sampleRate,omitempty"`
	MaxBytes   int      `yaml:"maxBytes" json:"maxBytes"`
	Retain     Duration `yaml:"retain" json:"retain"`
}

// Log controls the gateway logger. Level can change at runtime; format is
// fixed when the process starts.
type Log struct {
	Level  string `yaml:"level" json:"level"`
	Format string `yaml:"format" json:"format"`
}

// Entrypoint is the client-facing ingress: it owns the model globs that select
// it and exactly one route.
type Entrypoint struct {
	Name  string   `yaml:"name" json:"name"`
	Match []string `yaml:"match" json:"match"`
	Route string   `yaml:"route" json:"route"`
}

// Provider is an upstream LLM endpoint: which APIs it serves, how to connect
// and how to authenticate. It carries no model mapping and no routing policy;
// those belong to a Node.
//
// Type selects the adapter. Each non-empty API base URL advertises one wire
// API; an empty field means the provider does not serve that API.
type Provider struct {
	Name      string   `yaml:"name" json:"name"`
	Type      string   `yaml:"type" json:"type"`
	Chat      string   `yaml:"chat,omitempty" json:"chat,omitempty"`
	Responses string   `yaml:"responses,omitempty" json:"responses,omitempty"`
	Anthropic string   `yaml:"anthropic,omitempty" json:"anthropic,omitempty"`
	Gemini    string   `yaml:"gemini,omitempty" json:"gemini,omitempty"`
	Keys      []string `yaml:"keys,omitempty" json:"keys,omitempty"`
	Auth      Auth     `yaml:"auth,omitempty" json:"auth,omitempty"`
	Health    Health   `yaml:"health,omitempty" json:"health,omitempty"`
	TLS       TLS      `yaml:"tls,omitempty" json:"tls,omitempty"`
	Enabled   *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Models holds local per-model overrides keyed by model ID. The upstream
	// catalog stays authoritative; overrides only add local policy such as
	// disabling a model. Unknown model IDs are allowed because the catalog can
	// change at any time.
	Models map[string]ModelOverride `yaml:"models,omitempty" json:"models,omitempty"`
}

// ModelOverride holds local policy for one model from a provider's catalog.
type ModelOverride struct {
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// providerTypes is the canonical set of provider adapter names accepted by
// the configuration. internal/provider keeps its factory in sync through
// IsProviderType so validation and construction cannot diverge.
var providerTypes = map[string]struct{}{
	"deepseek":     {},
	"opencode-go":  {},
	"opencode-zen": {},
	"commandcode":  {},
	"generic":      {},
}

// IsProviderType reports whether typ, ignoring case and surrounding space,
// names a registered provider type.
func IsProviderType(typ string) bool {
	_, ok := providerTypes[strings.ToLower(strings.TrimSpace(typ))]
	return ok
}

// Node is one concrete use of a provider: exactly one provider model plus the
// local routing policy (backup, weight). The same provider can back several
// nodes with different models.
type Node struct {
	Name     string `yaml:"name" json:"name"`
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
	Backup   bool   `yaml:"backup,omitempty" json:"backup,omitempty"`
	Weight   int    `yaml:"weight,omitempty" json:"weight,omitempty"`
	Enabled  *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// Auth describes how the provider key is injected into upstream requests.
type Auth struct {
	Header     string            `yaml:"header,omitempty" json:"header,omitempty"`
	Prefix     string            `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	KeyHeaders []KeyHeader       `yaml:"keyHeaders,omitempty" json:"keyHeaders,omitempty"`
	Extra      map[string]string `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// KeyHeader injects the provider key into an additional header.
type KeyHeader struct {
	Header string `yaml:"header" json:"header"`
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
}

// Health describes the provider health probe (used from P5).
type Health struct {
	// API is the API whose base URL the probe targets. Empty means the first
	// configured API in chat, responses, anthropic, gemini order.
	API      string   `yaml:"api,omitempty" json:"api,omitempty"`
	Path     string   `yaml:"path,omitempty" json:"path,omitempty"`
	Interval Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout  Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// TLS overrides the TLS settings derived from the API base URL.
type TLS struct {
	ServerName string `yaml:"serverName,omitempty" json:"serverName,omitempty"`
	Secure     *bool  `yaml:"secure,omitempty" json:"secure,omitempty"`
}

// Route maps client requests to a set of nodes. The entrypoint selects the
// route; the route only decides which node serves it.
type Route struct {
	Name     string   `yaml:"name" json:"name"`
	Nodes    []string `yaml:"nodes" json:"nodes"`
	Strategy string   `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}

// Duration is a time.Duration that unmarshals from strings like "12h".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Value == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(value.Value)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Load reads, expands, defaults and validates a configuration file.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	c.expandEnv()
	c.applyDefaults()
	if err := Validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ErrConfigExists is returned by WriteNew when the target already exists.
var ErrConfigExists = errors.New("config file already exists")

// Write atomically writes the configuration with 0600 permissions, replacing
// an existing file.
func Write(path string, c *Config) error {
	return write(path, c, false)
}

// WriteNew atomically creates the configuration with 0600 permissions. It
// returns ErrConfigExists instead of replacing an existing file.
func WriteNew(path string, c *Config) error {
	return write(path, c, true)
}

func write(path string, c *Config, exclusive bool) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".llm-gw-*.yaml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if exclusive {
		if err := os.Link(tmp, path); err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrConfigExists
			}
			return err
		}
	} else if err := os.Rename(tmp, path); err != nil {
		return err
	}

	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// HashPassword returns a bcrypt hash for the admin password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword reports whether password matches the bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ExpandEnv expands ${VAR} and $VAR using the process environment.
func ExpandEnv(s string) string {
	return os.Expand(s, os.Getenv)
}

// ApplyDefaults fills zero values without expanding the environment. Used by
// the admin API, which must not re-expand literal "$" in keys.
func ApplyDefaults(c *Config) {
	c.applyDefaults()
}

// Normalize applies environment expansion and defaults in place.
func (c *Config) Normalize() {
	c.expandEnv()
	c.applyDefaults()
}

func (c *Config) expandEnv() {
	c.Gateway.Listen = ExpandEnv(c.Gateway.Listen)
	c.Admin.Listen = ExpandEnv(c.Admin.Listen)
	c.Stats.Path = ExpandEnv(c.Stats.Path)
	c.Log.Level = ExpandEnv(c.Log.Level)
	c.Log.Format = ExpandEnv(c.Log.Format)
	for i := range c.Entrypoints {
		e := &c.Entrypoints[i]
		e.Name = ExpandEnv(e.Name)
		e.Route = ExpandEnv(e.Route)
		for j := range e.Match {
			e.Match[j] = ExpandEnv(e.Match[j])
		}
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		p.Name = ExpandEnv(p.Name)
		p.Type = ExpandEnv(p.Type)
		p.Chat = ExpandEnv(p.Chat)
		p.Responses = ExpandEnv(p.Responses)
		p.Anthropic = ExpandEnv(p.Anthropic)
		p.Gemini = ExpandEnv(p.Gemini)
		p.Health.API = ExpandEnv(p.Health.API)
		p.Health.Path = ExpandEnv(p.Health.Path)
		for j := range p.Keys {
			p.Keys[j] = ExpandEnv(p.Keys[j])
		}
		p.Auth.Header = ExpandEnv(p.Auth.Header)
		p.Auth.Prefix = ExpandEnv(p.Auth.Prefix)
		for k, v := range p.Auth.Extra {
			p.Auth.Extra[k] = ExpandEnv(v)
		}
		for j := range p.Auth.KeyHeaders {
			p.Auth.KeyHeaders[j].Header = ExpandEnv(p.Auth.KeyHeaders[j].Header)
			p.Auth.KeyHeaders[j].Prefix = ExpandEnv(p.Auth.KeyHeaders[j].Prefix)
		}
		if len(p.Models) > 0 {
			models := make(map[string]ModelOverride, len(p.Models))
			for id, override := range p.Models {
				id = strings.TrimSpace(ExpandEnv(id))
				// Two YAML keys can expand to the same model ID. Merge them
				// so disabled:true survives regardless of map iteration order.
				if prev, ok := models[id]; ok {
					override.Disabled = prev.Disabled || override.Disabled
				}
				models[id] = override
			}
			p.Models = models
		}
	}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		n.Name = ExpandEnv(n.Name)
		n.Provider = ExpandEnv(n.Provider)
		n.Model = ExpandEnv(n.Model)
	}
	for i := range c.Routes {
		r := &c.Routes[i]
		r.Name = ExpandEnv(r.Name)
		for j := range r.Nodes {
			r.Nodes[j] = ExpandEnv(r.Nodes[j])
		}
	}
}

func (c *Config) applyDefaults() {
	if c.Gateway.Listen == "" {
		c.Gateway.Listen = ":8787"
	}
	if c.Admin.Listen == "" {
		c.Admin.Listen = "127.0.0.1:8788"
	}
	if c.Admin.SessionTTL == 0 {
		c.Admin.SessionTTL = Duration(12 * time.Hour)
	}
	if c.Admin.RememberTTL == 0 {
		c.Admin.RememberTTL = Duration(720 * time.Hour)
	}
	if c.Stats.Path == "" {
		c.Stats.Path = "stats.sqlite"
	}
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	c.Stats.Capture.Mode = strings.ToLower(strings.TrimSpace(c.Stats.Capture.Mode))
	if c.Stats.Capture.Mode == "" {
		c.Stats.Capture.Mode = "off"
	}
	if c.Stats.Capture.SampleRate == nil {
		rate := 0.01
		c.Stats.Capture.SampleRate = &rate
	}
	if c.Stats.Capture.MaxBytes == 0 {
		c.Stats.Capture.MaxBytes = 32768
	}
	if c.Stats.Capture.Retain == 0 {
		c.Stats.Capture.Retain = Duration(24 * time.Hour)
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		p.Health.API = strings.ToLower(strings.TrimSpace(p.Health.API))
		if p.TLS.Secure == nil {
			secure := true
			p.TLS.Secure = &secure
		}
		if p.Health.Interval == 0 {
			p.Health.Interval = Duration(30 * time.Second)
		}
		if p.Health.Timeout == 0 {
			p.Health.Timeout = Duration(5 * time.Second)
		}
		if len(p.Models) > 0 {
			models := make(map[string]ModelOverride, len(p.Models))
			for id, override := range p.Models {
				id = strings.TrimSpace(id)
				// A false override carries no information; drop it so the
				// config file stays minimal and the map only lists disabled
				// models.
				if id == "" || !override.Disabled {
					continue
				}
				models[id] = override
			}
			if len(models) == 0 {
				p.Models = nil
			} else {
				p.Models = models
			}
		}
	}
	for i := range c.Entrypoints {
		e := &c.Entrypoints[i]
		e.Name = strings.TrimSpace(e.Name)
		e.Route = strings.TrimSpace(e.Route)
		seen := make(map[string]struct{}, len(e.Match))
		match := make([]string, 0, len(e.Match))
		for _, m := range e.Match {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			key := strings.ToLower(m)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			match = append(match, m)
		}
		if len(match) == 0 {
			e.Match = nil
		} else {
			e.Match = match
		}
	}
	for i := range c.Nodes {
		c.Nodes[i].Model = strings.TrimSpace(c.Nodes[i].Model)
		if c.Nodes[i].Weight == 0 {
			c.Nodes[i].Weight = 1
		}
	}
	for i := range c.Routes {
		c.Routes[i].Name = strings.TrimSpace(c.Routes[i].Name)
		for j := range c.Routes[i].Nodes {
			c.Routes[i].Nodes[j] = strings.TrimSpace(c.Routes[i].Nodes[j])
		}
		if c.Routes[i].Strategy == "" {
			c.Routes[i].Strategy = "round"
		}
	}
}

type apiBaseField struct {
	api   string
	value string
}

func providerAPIBaseURLs(p *Provider) []apiBaseField {
	return []apiBaseField{
		{api: "chat", value: p.Chat},
		{api: "responses", value: p.Responses},
		{api: "anthropic", value: p.Anthropic},
		{api: "gemini", value: p.Gemini},
	}
}

func providerAPIBase(p *Provider, api string) string {
	switch strings.ToLower(strings.TrimSpace(api)) {
	case "chat":
		return p.Chat
	case "responses":
		return p.Responses
	case "anthropic":
		return p.Anthropic
	case "gemini":
		return p.Gemini
	}
	return ""
}

func validateProviderBaseURL(name, field, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("provider %q: invalid %s %q", name, field, raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("provider %q: %s scheme must be http or https", name, field)
	}
	return nil
}

// Validate checks structural and reference errors.
func Validate(c *Config) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.Gateway.Listen) == "" {
		return fmt.Errorf("gateway.listen is required")
	}
	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "", "trace", "debug", "info", "warn", "error", "fatal":
	default:
		return fmt.Errorf("log.level must be one of trace, debug, info, warn, error, fatal")
	}
	switch strings.ToLower(strings.TrimSpace(c.Log.Format)) {
	case "", "json", "text":
	default:
		return fmt.Errorf("log.format must be json or text")
	}
	switch c.Stats.Capture.Mode {
	case "", "off", "errors", "sample":
	default:
		return fmt.Errorf("stats.capture.mode must be off, errors or sample")
	}
	if c.Stats.Capture.SampleRate != nil &&
		(*c.Stats.Capture.SampleRate < 0 || *c.Stats.Capture.SampleRate > 1) {
		return fmt.Errorf("stats.capture.sampleRate must be between 0 and 1")
	}
	if c.Stats.Capture.MaxBytes != 0 && (c.Stats.Capture.MaxBytes < 1024 || c.Stats.Capture.MaxBytes > 1048576) {
		return fmt.Errorf("stats.capture.maxBytes must be between 1024 and 1048576")
	}
	if c.Stats.Capture.Retain < 0 {
		return fmt.Errorf("stats.capture.retain must be positive")
	}

	seen := make(map[string]bool, len(c.Providers))
	for i := range c.Providers {
		p := &c.Providers[i]
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return fmt.Errorf("providers[%d]: name is required", i)
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("provider %q: name must not contain '/'", name)
		}
		if seen[name] {
			return fmt.Errorf("providers[%d]: duplicate name %q", i, name)
		}
		seen[name] = true

		if typ := strings.TrimSpace(p.Type); typ == "" {
			return fmt.Errorf("provider %q: type is required", name)
		} else if !IsProviderType(typ) {
			return fmt.Errorf("provider %q: unknown type %q", name, typ)
		}
		var hasAPI bool
		for _, b := range providerAPIBaseURLs(p) {
			if strings.TrimSpace(b.value) == "" {
				continue
			}
			hasAPI = true
			if err := validateProviderBaseURL(name, b.api, b.value); err != nil {
				return err
			}
		}
		if !hasAPI {
			return fmt.Errorf("provider %q: at least one API base URL is required", name)
		}
		if api := strings.TrimSpace(p.Health.API); api != "" {
			switch strings.ToLower(api) {
			case "chat", "responses", "anthropic", "gemini":
			default:
				return fmt.Errorf("provider %q: health.api must be chat, responses, anthropic or gemini", name)
			}
			if strings.TrimSpace(providerAPIBase(p, api)) == "" {
				return fmt.Errorf("provider %q: health.api %q has no matching base URL", name, api)
			}
		}
		if p.Health.Path != "" && !strings.HasPrefix(p.Health.Path, "/") {
			return fmt.Errorf("provider %q: health.path must start with /", name)
		}
		for j, k := range p.Keys {
			if strings.TrimSpace(k) == "" {
				return fmt.Errorf("provider %q: keys[%d] is empty (check the environment variable)", name, j)
			}
		}
	}

	nodeNames := make(map[string]bool, len(c.Nodes))
	for i := range c.Nodes {
		n := &c.Nodes[i]
		name := strings.TrimSpace(n.Name)
		if name == "" {
			return fmt.Errorf("nodes[%d]: name is required", i)
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("node %q: name must not contain '/'", name)
		}
		if nodeNames[name] {
			return fmt.Errorf("nodes[%d]: duplicate name %q", i, name)
		}
		nodeNames[name] = true

		provider := strings.TrimSpace(n.Provider)
		if provider == "" {
			return fmt.Errorf("node %q: provider is required", name)
		}
		if !seen[provider] {
			return fmt.Errorf("node %q: unknown provider %q", name, provider)
		}
		if strings.TrimSpace(n.Model) == "" {
			return fmt.Errorf("node %q: model is required", name)
		}
		if n.Weight < 0 {
			return fmt.Errorf("node %q: weight must be >= 0", name)
		}
	}

	routes := make(map[string]bool, len(c.Routes))
	for i := range c.Routes {
		r := &c.Routes[i]
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("routes[%d]: name is required", i)
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("route %q: name must not contain '/'", name)
		}
		if routes[name] {
			return fmt.Errorf("routes[%d]: duplicate name %q", i, name)
		}
		routes[name] = true
		if len(r.Nodes) == 0 {
			return fmt.Errorf("route %q: nodes is required", name)
		}
		switch strings.ToLower(r.Strategy) {
		case "", "fifo", "round", "random":
		default:
			return fmt.Errorf("route %q: strategy must be fifo, round or random", name)
		}
		used := make(map[string]bool, len(r.Nodes))
		for _, ref := range r.Nodes {
			ref = strings.TrimSpace(ref)
			if !nodeNames[ref] {
				return fmt.Errorf("route %q: unknown node %q", name, ref)
			}
			if used[ref] {
				return fmt.Errorf("route %q: duplicate node %q", name, ref)
			}
			used[ref] = true
		}
	}

	entrypointNames := make(map[string]bool, len(c.Entrypoints))
	for i := range c.Entrypoints {
		e := &c.Entrypoints[i]
		name := strings.TrimSpace(e.Name)
		if name == "" {
			return fmt.Errorf("entrypoints[%d]: name is required", i)
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("entrypoint %q: name must not contain '/'", name)
		}
		if entrypointNames[name] {
			return fmt.Errorf("entrypoints[%d]: duplicate name %q", i, name)
		}
		entrypointNames[name] = true

		hasMatch := false
		for _, m := range e.Match {
			if strings.TrimSpace(m) != "" {
				hasMatch = true
				break
			}
		}
		if !hasMatch {
			return fmt.Errorf("entrypoint %q: match is required", name)
		}
		route := strings.TrimSpace(e.Route)
		if route == "" {
			return fmt.Errorf("entrypoint %q: route is required", name)
		}
		if !routes[route] {
			return fmt.Errorf("entrypoint %q: unknown route %q", name, route)
		}
	}
	return nil
}

// ResolveEntrypoint returns the first entrypoint, in declaration order, with a
// match glob for clientModel.
func (c *Config) ResolveEntrypoint(clientModel string) (*Entrypoint, bool) {
	for i := range c.Entrypoints {
		for _, pattern := range c.Entrypoints[i].Match {
			if globMatch(pattern, clientModel) {
				return &c.Entrypoints[i], true
			}
		}
	}
	return nil, false
}

// ResolveEntrypointName returns the name of the entrypoint that handles
// clientModel, or an empty string when none matches.
func (c *Config) ResolveEntrypointName(clientModel string) string {
	if ep, ok := c.ResolveEntrypoint(clientModel); ok {
		return ep.Name
	}
	return ""
}

// TopologyIncludes reports whether a stats row belongs to the current route
// topology: the route must exist, reference the node, and the node's provider
// must match. Unrouted rows are always kept.
func (c *Config) TopologyIncludes(route, node, provider string) bool {
	route = strings.TrimSpace(route)
	if route == "" {
		return true
	}
	var target *Route
	for i := range c.Routes {
		if c.Routes[i].Name == route {
			target = &c.Routes[i]
			break
		}
	}
	if target == nil {
		return false
	}
	node = strings.TrimSpace(node)
	if node == "" {
		return true
	}
	referenced := false
	for _, ref := range target.Nodes {
		if strings.TrimSpace(ref) == node {
			referenced = true
			break
		}
	}
	if !referenced {
		return false
	}
	for i := range c.Nodes {
		if c.Nodes[i].Name != node {
			continue
		}
		return provider == "" || strings.EqualFold(strings.TrimSpace(c.Nodes[i].Provider), strings.TrimSpace(provider))
	}
	return false
}

// EnabledValue reports whether the node is enabled (default true).
func (n Node) EnabledValue() bool {
	return n.Enabled == nil || *n.Enabled
}

// EnabledValue reports whether the provider is enabled (default true).
func (p Provider) EnabledValue() bool {
	return p.Enabled == nil || *p.Enabled
}

// ModelDisabled reports whether the provider locally disables the model with
// the given ID. Surrounding space is ignored; unknown IDs are not disabled.
func (p Provider) ModelDisabled(id string) bool {
	return p.Models[strings.TrimSpace(id)].Disabled
}

// globMatch matches s against a pattern with "*" wildcards, case-insensitively.
func globMatch(pattern, s string) bool {
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)

	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	rest := s[len(parts[0]):]
	for i := 1; i < len(parts); i++ {
		if i == len(parts)-1 {
			return strings.HasSuffix(rest, parts[i])
		}
		idx := strings.Index(rest, parts[i])
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(parts[i]):]
	}
	return true
}
