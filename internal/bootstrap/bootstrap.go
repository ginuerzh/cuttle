// Package bootstrap resolves the llm-gw config location and creates a
// zero-config first-run configuration when none exists.
package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gost/llm-gw/internal/config"
)

const (
	// DefaultDataDir is the default LLM_GW_DATA directory.
	DefaultDataDir = "/data"
	// ConfigFileName is the config file name inside the data directory.
	ConfigFileName = "llm-gw.yaml"
	// StatsFileName is the stats database name inside the data directory.
	StatsFileName = "stats.sqlite"
)

// DefaultLegacyPaths are checked after the data-directory config and before a
// new config is generated.
var DefaultLegacyPaths = []string{
	"/etc/llm-gw/llm-gw.yaml",
	"llm-gw.yaml",
}

// Paths is the resolved config location.
type Paths struct {
	DataDir    string
	ConfigPath string
	Existing   bool
}

// Resolve determines where the config lives. An explicit path must exist. With
// no explicit path it checks $LLM_GW_DATA/llm-gw.yaml, then legacyPaths, and
// finally returns the path where a new config should be generated.
//
// A nil legacyPaths uses DefaultLegacyPaths; an empty non-nil slice disables
// legacy candidates.
func Resolve(explicitPath string, getenv func(string) string, legacyPaths []string) (Paths, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	dataDir := strings.TrimSpace(getenv("LLM_GW_DATA"))
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return Paths{}, fmt.Errorf("LLM_GW_DATA %q: %w", dataDir, err)
	}

	paths := Paths{DataDir: absDataDir}

	if explicitPath != "" {
		ok, err := fileExists(explicitPath)
		if err != nil {
			return Paths{}, fmt.Errorf("config file %s: %w", explicitPath, err)
		}
		if !ok {
			return Paths{}, fmt.Errorf("config file %s: %w", explicitPath, os.ErrNotExist)
		}
		paths.ConfigPath = explicitPath
		paths.Existing = true
		return paths, nil
	}

	dataConfig := filepath.Join(absDataDir, ConfigFileName)
	ok, err := fileExists(dataConfig)
	if err != nil {
		return Paths{}, fmt.Errorf("LLM_GW_DATA %s: %w", absDataDir, err)
	}
	if ok {
		paths.ConfigPath = dataConfig
		paths.Existing = true
		return paths, nil
	}

	if legacyPaths == nil {
		legacyPaths = DefaultLegacyPaths
	}
	for _, path := range legacyPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		ok, err := fileExists(path)
		if err != nil {
			return Paths{}, fmt.Errorf("config file %s: %w", path, err)
		}
		if ok {
			paths.ConfigPath = path
			paths.Existing = true
			return paths, nil
		}
	}

	paths.ConfigPath = dataConfig
	return paths, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// loadConfig is a test seam for the final config round-trip.
var loadConfig = config.Load

// Options configures Prepare.
type Options struct {
	// ExplicitPath is the -c/--config value. When non-empty it must exist.
	ExplicitPath string
	// LegacyPaths overrides DefaultLegacyPaths. A nil slice uses the defaults;
	// an empty non-nil slice disables legacy candidates.
	LegacyPaths []string
	// Getenv overrides os.Getenv in tests. A nil function uses os.Getenv.
	Getenv func(string) string
	// Stderr receives the one-time generated password. A nil writer uses
	// os.Stderr.
	Stderr io.Writer
}

// Result is the resolved config and first-run metadata.
type Result struct {
	Config     *config.Config
	ConfigPath string
	Generated  bool
	// GeneratedPassword is set only for tests. Never log it.
	GeneratedPassword string
	Warnings          []string
}

// Prepare resolves and loads the config, or generates a first-run config when
// none exists. A generated config is written before the generated password is
// printed, so a later startup failure cannot leave an unreported password.
func Prepare(opts Options) (*Result, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	paths, err := Resolve(opts.ExplicitPath, getenv, opts.LegacyPaths)
	if err != nil {
		return nil, err
	}
	if paths.Existing {
		cfg, err := loadConfig(paths.ConfigPath)
		if err != nil {
			return nil, err
		}
		return &Result{
			Config:     cfg,
			ConfigPath: paths.ConfigPath,
			Warnings:   ignoredEnvWarnings(cfg, getenv),
		}, nil
	}

	return generate(paths, getenv, stderr)
}

func generate(paths Paths, getenv func(string) string, stderr io.Writer) (*Result, error) {
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("LLM_GW_DATA %s: %w", paths.DataDir, err)
	}

	listen := envOrDefault(getenv, "LLM_GW_LISTEN", ":8787")
	adminListen := envOrDefault(getenv, "LLM_GW_ADMIN_LISTEN", "127.0.0.1:8788")

	var password string
	var hash string
	var err error
	if envPassword := getenv("LLM_GW_ADMIN_PASSWORD"); envPassword != "" {
		if err := validatePassword(envPassword); err != nil {
			return nil, err
		}
		hash, err = config.HashPassword(envPassword)
		if err != nil {
			return nil, err
		}
	} else {
		password, err = GeneratePassword()
		if err != nil {
			return nil, err
		}
		hash, err = config.HashPassword(password)
		if err != nil {
			return nil, err
		}
	}

	cfg := &config.Config{
		Gateway: config.Gateway{Listen: listen},
		Admin:   config.Admin{Listen: adminListen, PasswordHash: hash},
		Stats:   config.Stats{Path: filepath.Join(paths.DataDir, StatsFileName)},
	}
	config.ApplyDefaults(cfg)
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	if err := config.Write(paths.ConfigPath, cfg); err != nil {
		return nil, fmt.Errorf("write config %s: %w", paths.ConfigPath, err)
	}

	if password != "" {
		printGeneratedPassword(stderr, password, adminListen)
	}

	loaded, err := loadConfig(paths.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &Result{
		Config:            loaded,
		ConfigPath:        paths.ConfigPath,
		Generated:         true,
		GeneratedPassword: password,
	}, nil
}

// GeneratePassword returns 24 random bytes encoded as unpadded base64url.
func GeneratePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("LLM_GW_ADMIN_PASSWORD must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("LLM_GW_ADMIN_PASSWORD must be at most 72 bytes")
	}
	return nil
}

func envOrDefault(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

func printGeneratedPassword(w io.Writer, password, adminListen string) {
	fmt.Fprintf(w, "llm-gw: generated admin password: %s\n", password)
	fmt.Fprintf(w, "llm-gw: admin console listening on %s\n", adminListen)
	fmt.Fprintln(w, "llm-gw: set LLM_GW_ADMIN_PASSWORD to choose your own password")
}

func ignoredEnvWarnings(cfg *config.Config, getenv func(string) string) []string {
	var warnings []string
	if v := strings.TrimSpace(getenv("LLM_GW_LISTEN")); v != "" && v != cfg.Gateway.Listen {
		warnings = append(warnings, fmt.Sprintf("LLM_GW_LISTEN=%q ignored; config already sets gateway.listen=%q", v, cfg.Gateway.Listen))
	}
	if v := strings.TrimSpace(getenv("LLM_GW_ADMIN_LISTEN")); v != "" && v != cfg.Admin.Listen {
		warnings = append(warnings, fmt.Sprintf("LLM_GW_ADMIN_LISTEN=%q ignored; config already sets admin.listen=%q", v, cfg.Admin.Listen))
	}
	if password := getenv("LLM_GW_ADMIN_PASSWORD"); password != "" {
		if cfg.Admin.PasswordHash == "" || !config.VerifyPassword(cfg.Admin.PasswordHash, password) {
			warnings = append(warnings, "LLM_GW_ADMIN_PASSWORD is ignored; config already has a different admin password")
		}
	}
	return warnings
}
