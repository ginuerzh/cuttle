// Package config defines the llm-gw YAML configuration and its validation.
package config

import (
	"encoding/json"
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
	Gateway   Gateway    `yaml:"gateway" json:"gateway"`
	Admin     Admin      `yaml:"admin" json:"admin"`
	Stats     Stats      `yaml:"stats" json:"stats"`
	Effort    Effort     `yaml:"effort" json:"effort"`
	Aliases   []Alias    `yaml:"aliases" json:"aliases"`
	Providers []Provider `yaml:"providers" json:"providers"`
	Routes    []Route    `yaml:"routes" json:"routes"`
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
}

// Stats holds statistics storage settings (used from P4).
type Stats struct {
	Path           string   `yaml:"path" json:"path"`
	RetainRequests Duration `yaml:"retainRequests" json:"retainRequests"`
	RetainRollups  Duration `yaml:"retainRollups" json:"retainRollups"`
	StoreBody      bool     `yaml:"storeBody" json:"storeBody"`
}

// Effort holds the thinking-budget thresholds.
type Effort struct {
	LowThreshold    int `yaml:"lowThreshold" json:"lowThreshold"`
	MediumThreshold int `yaml:"mediumThreshold" json:"mediumThreshold"`
	HighThreshold   int `yaml:"highThreshold" json:"highThreshold"`
}

// Alias maps a client-facing model glob to a logical model.
type Alias struct {
	Match string `yaml:"match" json:"match"`
	Model string `yaml:"model" json:"model"`
}

// Provider is an upstream LLM endpoint.
type Provider struct {
	Name         string                       `yaml:"name" json:"name"`
	BaseURL      string                       `yaml:"baseURL" json:"baseURL"`
	Protocol     string                       `yaml:"protocol" json:"protocol"`
	Keys         []string                     `yaml:"keys,omitempty" json:"keys,omitempty"`
	Auth         Auth                         `yaml:"auth,omitempty" json:"auth,omitempty"`
	PathMap      map[string]string            `yaml:"pathMap,omitempty" json:"pathMap,omitempty"`
	Models       map[string]string            `yaml:"models,omitempty" json:"models,omitempty"`
	EffortModels map[string]map[string]string `yaml:"effortModels,omitempty" json:"effortModels,omitempty"`
	Health       Health                       `yaml:"health,omitempty" json:"health,omitempty"`
	TLS          TLS                          `yaml:"tls,omitempty" json:"tls,omitempty"`
	Backup       bool                         `yaml:"backup,omitempty" json:"backup,omitempty"`
	Weight       int                          `yaml:"weight,omitempty" json:"weight,omitempty"`
	Enabled      *bool                        `yaml:"enabled,omitempty" json:"enabled,omitempty"`
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
	Path     string   `yaml:"path,omitempty" json:"path,omitempty"`
	Interval Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout  Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// TLS overrides the TLS settings derived from BaseURL.
type TLS struct {
	ServerName string `yaml:"serverName,omitempty" json:"serverName,omitempty"`
	Secure     *bool  `yaml:"secure,omitempty" json:"secure,omitempty"`
}

// Route maps client requests to a set of providers.
type Route struct {
	Name      string              `yaml:"name" json:"name"`
	Providers []string            `yaml:"providers" json:"providers"`
	Strategy  string              `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	Effort    map[string][]string `yaml:"effort,omitempty" json:"effort,omitempty"`
	Match     Match               `yaml:"match" json:"match"`
}

// Match filters requests. An empty Match is a catch-all.
type Match struct {
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	Path  string `yaml:"path,omitempty" json:"path,omitempty"`
	Host  string `yaml:"host,omitempty" json:"host,omitempty"`
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

// Write atomically writes the configuration with 0600 permissions.
func Write(path string, c *Config) error {
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
	if err := os.Rename(tmp, path); err != nil {
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
	for i := range c.Aliases {
		c.Aliases[i].Match = ExpandEnv(c.Aliases[i].Match)
		c.Aliases[i].Model = ExpandEnv(c.Aliases[i].Model)
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		p.Name = ExpandEnv(p.Name)
		p.BaseURL = ExpandEnv(p.BaseURL)
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
		for k, v := range p.PathMap {
			p.PathMap[ExpandEnv(k)] = ExpandEnv(v)
		}
		for k, v := range p.Models {
			p.Models[ExpandEnv(k)] = ExpandEnv(v)
		}
		for logical, byEffort := range p.EffortModels {
			for effort, model := range byEffort {
				byEffort[effort] = ExpandEnv(model)
			}
			p.EffortModels[logical] = byEffort
		}
	}
	for i := range c.Routes {
		r := &c.Routes[i]
		r.Name = ExpandEnv(r.Name)
		for j := range r.Providers {
			r.Providers[j] = ExpandEnv(r.Providers[j])
		}
		r.Match.Model = ExpandEnv(r.Match.Model)
		r.Match.Path = ExpandEnv(r.Match.Path)
		r.Match.Host = ExpandEnv(r.Match.Host)
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
	if c.Stats.Path == "" {
		c.Stats.Path = "stats.sqlite"
	}
	if c.Effort.LowThreshold == 0 {
		c.Effort.LowThreshold = 1024
	}
	if c.Effort.MediumThreshold == 0 {
		c.Effort.MediumThreshold = 4096
	}
	if c.Effort.HighThreshold == 0 {
		c.Effort.HighThreshold = 16384
	}

	for i := range c.Providers {
		p := &c.Providers[i]
		if p.Auth.Header == "" {
			p.Auth.Header = "Authorization"
			if p.Auth.Prefix == "" {
				p.Auth.Prefix = "Bearer "
			}
		}
		if p.Protocol == "" {
			p.Protocol = "openai"
		}
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
		if p.Weight == 0 {
			p.Weight = 1
		}
	}
	for i := range c.Routes {
		if c.Routes[i].Strategy == "" {
			c.Routes[i].Strategy = "round"
		}
	}
}

// Validate checks structural and reference errors.
func Validate(c *Config) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.Gateway.Listen) == "" {
		return fmt.Errorf("gateway.listen is required")
	}
	if c.Effort.LowThreshold >= c.Effort.MediumThreshold ||
		c.Effort.MediumThreshold >= c.Effort.HighThreshold {
		return fmt.Errorf("effort thresholds must satisfy low < medium < high")
	}

	for i, a := range c.Aliases {
		if strings.TrimSpace(a.Match) == "" || strings.TrimSpace(a.Model) == "" {
			return fmt.Errorf("aliases[%d]: match and model are required", i)
		}
	}

	seen := make(map[string]bool, len(c.Providers))
	for i := range c.Providers {
		p := &c.Providers[i]
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return fmt.Errorf("providers[%d]: name is required", i)
		}
		if seen[name] {
			return fmt.Errorf("providers[%d]: duplicate name %q", i, name)
		}
		seen[name] = true

		u, err := url.Parse(strings.TrimSpace(p.BaseURL))
		if err != nil || u.Host == "" {
			return fmt.Errorf("provider %q: invalid baseURL %q", name, p.BaseURL)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("provider %q: baseURL scheme must be http or https", name)
		}
		switch strings.ToLower(strings.TrimSpace(p.Protocol)) {
		case "", "openai", "anthropic", "gemini":
		default:
			return fmt.Errorf("provider %q: protocol must be openai, anthropic or gemini", name)
		}
		if p.Weight < 0 {
			return fmt.Errorf("provider %q: weight must be >= 0", name)
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

	routes := make(map[string]bool, len(c.Routes))
	for i := range c.Routes {
		r := &c.Routes[i]
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("routes[%d]: name is required", i)
		}
		if routes[name] {
			return fmt.Errorf("routes[%d]: duplicate name %q", i, name)
		}
		routes[name] = true
		if len(r.Providers) == 0 {
			return fmt.Errorf("route %q: providers is required", name)
		}
		switch strings.ToLower(r.Strategy) {
		case "", "fifo", "round", "random":
		default:
			return fmt.Errorf("route %q: strategy must be fifo, round or random", name)
		}
		for tier, refs := range r.Effort {
			switch tier {
			case "high", "medium", "low":
			default:
				return fmt.Errorf("route %q: effort tier %q must be high, medium or low", name, tier)
			}
			if len(refs) == 0 {
				return fmt.Errorf("route %q: effort.%s is empty", name, tier)
			}
			for _, ref := range refs {
				if !seen[strings.TrimSpace(ref)] {
					return fmt.Errorf("route %q: unknown provider %q in effort.%s", name, ref, tier)
				}
			}
		}
		for _, ref := range r.Providers {
			if !seen[strings.TrimSpace(ref)] {
				return fmt.Errorf("route %q: unknown provider %q", name, ref)
			}
		}
	}
	return nil
}

// ResolveAlias maps a client-facing model name to a logical model using the
// ordered alias list. An empty result means no alias matched.
func (c *Config) ResolveAlias(clientModel string) string {
	for _, a := range c.Aliases {
		if globMatch(a.Match, clientModel) {
			return a.Model
		}
	}
	return ""
}

// EffectiveModel resolves the provider model for a logical model and effort:
// effort-specific override, then logical model, then the "*" fallback.
func (p Provider) EffectiveModel(logical, effort string) string {
	if byEffort, ok := p.EffortModels[logical]; ok {
		if model := strings.TrimSpace(byEffort[effort]); model != "" {
			return model
		}
	}
	if model := strings.TrimSpace(p.Models[logical]); model != "" {
		return model
	}
	return strings.TrimSpace(p.Models["*"])
}

// EnabledValue reports whether the provider is enabled (default true).
func (p Provider) EnabledValue() bool {
	return p.Enabled == nil || *p.Enabled
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
