// Package bootstrap resolves the cuttle config location and creates a
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
	"time"

	"github.com/go-gost/core/logger"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/logging"
)

const (
	// DirName is the directory name used under the user config directory.
	DirName = "cuttle"
	// ConfigFileName is the config file name inside the data directory.
	ConfigFileName = "cuttle.yaml"
	// StatsFileName is the stats database name inside the data directory.
	StatsFileName = "stats.sqlite"
	// SessionsResetFileName marks a first-run config whose persisted sessions
	// still need to be cleared. It is removed after a successful clear.
	SessionsResetFileName = ".cuttle-sessions-reset"
)

// Seams for the user config directory and the working directory.
var (
	userConfigDir = os.UserConfigDir
	getwd         = os.Getwd
)

// defaultDataDir returns <os.UserConfigDir>/cuttle. When the user config
// directory is unavailable it falls back to the working directory and reports
// the fallback so Prepare can warn.
func defaultDataDir() (dir string, fallback bool) {
	if dir, err := userConfigDir(); err == nil {
		if dir = strings.TrimSpace(dir); dir != "" {
			return filepath.Join(dir, DirName), false
		}
	}
	if wd, err := getwd(); err == nil {
		if wd = strings.TrimSpace(wd); wd != "" {
			return wd, true
		}
	}
	return ".", true
}

// applyWorkingDirFallback switches paths to the working directory when the data
// directory came from the per-user default. It reports whether it switched.
func applyWorkingDirFallback(paths *Paths) bool {
	if !paths.DataDirDefault || paths.DataDirFromFallback {
		return false
	}
	wd, err := getwd()
	if err != nil {
		return false
	}
	wd = strings.TrimSpace(wd)
	if wd == "" {
		return false
	}
	paths.DataDir = filepath.Clean(wd)
	paths.ConfigPath = filepath.Join(paths.DataDir, ConfigFileName)
	paths.DataDirFromFallback = true
	return true
}

// dataDirFallbackWarning reports the working-directory fallback, or "" when the
// data directory came from CUTTLE_DATA or the user config directory.
func dataDirFallbackWarning(paths Paths) string {
	if !paths.DataDirFromFallback {
		return ""
	}
	return fmt.Sprintf("CUTTLE_DATA is unset and the user config directory is unavailable; using %s", paths.DataDir)
}

// DefaultSearchPaths are checked after the data-directory config and before a
// new config is generated.
var DefaultSearchPaths = []string{
	"/etc/cuttle/cuttle.yaml",
	"cuttle.yaml",
}

// Paths is the resolved config location.
type Paths struct {
	DataDir    string
	ConfigPath string
	Existing   bool
	// DataDirFromFallback is true when CUTTLE_DATA was unset and the user
	// config directory was unavailable, so the working directory was used.
	DataDirFromFallback bool
	// DataDirDefault is true when CUTTLE_DATA was unset, so DataDir came from
	// the user config directory rather than from the environment.
	DataDirDefault bool
}

// Resolve determines where the config lives. An explicit path must exist. With
// no explicit path it checks $CUTTLE_DATA/cuttle.yaml, then searchPaths, and
// finally returns the path where a new config should be generated.
//
// A nil searchPaths uses DefaultSearchPaths; an empty non-nil slice disables
// secondary candidates.
func Resolve(explicitPath string, getenv func(string) string, searchPaths []string) (Paths, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	dataDir := strings.TrimSpace(getenv("CUTTLE_DATA"))
	defaulted := dataDir == ""
	fromFallback := false
	if dataDir == "" {
		dataDir, fromFallback = defaultDataDir()
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return Paths{}, fmt.Errorf("CUTTLE_DATA %q: %w", dataDir, err)
	}

	paths := Paths{DataDir: absDataDir, DataDirFromFallback: fromFallback, DataDirDefault: defaulted}

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
	if err != nil && applyWorkingDirFallback(&paths) {
		dataConfig = paths.ConfigPath
		ok, err = fileExists(dataConfig)
	}
	if err != nil {
		return Paths{}, fmt.Errorf("CUTTLE_DATA %s: %w", paths.DataDir, err)
	}
	if ok {
		paths.ConfigPath = dataConfig
		paths.Existing = true
		return paths, nil
	}

	if searchPaths == nil {
		searchPaths = DefaultSearchPaths
	}
	for _, path := range searchPaths {
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
var loadConfig = func(path string, opts ...config.LoadOption) (*config.Config, error) {
	return config.Load(path, opts...)
}

// writeConfig is a test seam for the exclusive first-run write.
var writeConfig = config.WriteNew

// Options configures Prepare.
type Options struct {
	// ExplicitPath is the -c/--config value. When non-empty it must exist.
	ExplicitPath string
	// SearchPaths overrides DefaultSearchPaths. A nil slice uses the defaults;
	// an empty non-nil slice disables secondary candidates.
	SearchPaths []string
	// Getenv overrides os.Getenv in tests. A nil function uses os.Getenv.
	Getenv func(string) string
	// LookupEnv overrides os.LookupEnv in tests. A nil function uses
	// os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Stderr receives the one-time generated password. A nil writer uses
	// os.Stderr.
	Stderr io.Writer
}

// Result is the resolved config and first-run metadata.
type Result struct {
	Config     *config.Config
	ConfigPath string
	DataDir    string
	Generated  bool
	// ResetSessions is true for a generated config or when a previous first
	// run left the session-reset marker behind.
	ResetSessions bool
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
	lookupEnv := opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	// An empty value is indistinguishable from an unset one through Getenv, so
	// a set-but-empty CUTTLE_DATA must fail loudly instead of silently moving
	// the data directory to the per-user default.
	if v, ok := lookupEnv("CUTTLE_DATA"); ok && strings.TrimSpace(v) == "" {
		return nil, errors.New("CUTTLE_DATA is set but empty; unset it or set the data directory")
	}
	// The configured log level and format live in the config this load may
	// rewrite, so the load-time migration logs plain text to stderr. Without it
	// the upgrade that rewrites cuttle.yaml would be silent.
	migrateLog, err := logging.NewWithWriter("info", "text", stderr)
	if err != nil {
		return nil, err
	}

	paths, err := Resolve(opts.ExplicitPath, getenv, opts.SearchPaths)
	if err != nil {
		return nil, err
	}
	if paths.Existing {
		cfg, err := loadConfig(paths.ConfigPath, config.WithLogger(migrateLog))
		if err != nil {
			return nil, err
		}
		reset, err := sessionsResetPending(paths.DataDir)
		if err != nil {
			return nil, fmt.Errorf("CUTTLE_DATA %s: %w", paths.DataDir, err)
		}
		warnings := ignoredEnvWarnings(cfg, getenv)
		if w := dataDirFallbackWarning(paths); w != "" {
			warnings = append([]string{w}, warnings...)
		}
		return &Result{
			Config:        cfg,
			ConfigPath:    paths.ConfigPath,
			DataDir:       paths.DataDir,
			ResetSessions: reset,
			Warnings:      warnings,
		}, nil
	}

	return generate(paths, getenv, stderr, migrateLog)
}

func generate(paths Paths, getenv func(string) string, stderr io.Writer, migrateLog logger.Logger) (*Result, error) {
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		if !applyWorkingDirFallback(&paths) {
			return nil, fmt.Errorf("CUTTLE_DATA %s: %w", paths.DataDir, err)
		}
		if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
			return nil, fmt.Errorf("CUTTLE_DATA %s: %w", paths.DataDir, err)
		}
	}
	warnings := dataDirWarnings(paths.DataDir)
	if w := dataDirFallbackWarning(paths); w != "" {
		warnings = append([]string{w}, warnings...)
	}

	pending, err := sessionsResetPending(paths.DataDir)
	if err != nil {
		return nil, fmt.Errorf("CUTTLE_DATA %s: %w", paths.DataDir, err)
	}
	if err := createSessionsResetMarker(paths.DataDir); err != nil {
		return nil, fmt.Errorf("CUTTLE_DATA %s: %w", paths.DataDir, err)
	}

	listen := envOrDefault(getenv, "CUTTLE_LISTEN", ":8787")
	adminListen := envOrDefault(getenv, "CUTTLE_ADMIN_LISTEN", "127.0.0.1:8788")

	var password string
	var hash string
	if envPassword := adminPasswordFromEnv(getenv); envPassword != "" {
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
		Stats: config.Stats{
			Path:           filepath.Join(paths.DataDir, StatsFileName),
			RetainRequests: config.Duration(168 * time.Hour),
			RetainRollups:  config.Duration(2160 * time.Hour),
		},
	}
	config.ApplyDefaults(cfg)
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	if err := writeConfig(paths.ConfigPath, cfg); err != nil {
		if errors.Is(err, config.ErrConfigExists) {
			reset := pending
			if !pending {
				if err := ClearSessionsReset(paths.DataDir); err != nil {
					reset = true
				}
			}
			loaded, err := loadConfig(paths.ConfigPath, config.WithLogger(migrateLog))
			if err != nil {
				return nil, err
			}
			return &Result{
				Config:        loaded,
				ConfigPath:    paths.ConfigPath,
				DataDir:       paths.DataDir,
				ResetSessions: reset,
				Warnings:      append(warnings, ignoredEnvWarnings(loaded, getenv)...),
			}, nil
		}
		return nil, fmt.Errorf("CUTTLE_DATA %s: write config %s: %w", paths.DataDir, paths.ConfigPath, err)
	}

	if password != "" {
		printGeneratedPassword(stderr, password, adminListen)
	}

	loaded, err := loadConfig(paths.ConfigPath, config.WithLogger(migrateLog))
	if err != nil {
		return nil, err
	}
	return &Result{
		Config:            loaded,
		ConfigPath:        paths.ConfigPath,
		DataDir:           paths.DataDir,
		Generated:         true,
		ResetSessions:     true,
		GeneratedPassword: password,
		Warnings:          warnings,
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
		return errors.New("CUTTLE_ADMIN_PASSWORD must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("CUTTLE_ADMIN_PASSWORD must be at most 72 bytes")
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
	fmt.Fprintf(w, "cuttle: generated admin password: %s\n", password)
	fmt.Fprintf(w, "cuttle: admin console listening on %s\n", adminListen)
	fmt.Fprintln(w, "cuttle: set CUTTLE_ADMIN_PASSWORD to choose your own password")
}

func ignoredEnvWarnings(cfg *config.Config, getenv func(string) string) []string {
	var warnings []string
	if v := strings.TrimSpace(getenv("CUTTLE_LISTEN")); v != "" && v != cfg.Gateway.Listen {
		warnings = append(warnings, fmt.Sprintf("CUTTLE_LISTEN=%q ignored; config already sets gateway.listen=%q", v, cfg.Gateway.Listen))
	}
	if v := strings.TrimSpace(getenv("CUTTLE_ADMIN_LISTEN")); v != "" && v != cfg.Admin.Listen {
		warnings = append(warnings, fmt.Sprintf("CUTTLE_ADMIN_LISTEN=%q ignored; config already sets admin.listen=%q", v, cfg.Admin.Listen))
	}
	if password := adminPasswordFromEnv(getenv); password != "" {
		switch {
		case cfg.Admin.PasswordHash == "":
			warnings = append(warnings, "CUTTLE_ADMIN_PASSWORD is ignored; config has no admin password and the admin console is disabled")
		case !config.VerifyPassword(cfg.Admin.PasswordHash, password):
			warnings = append(warnings, "CUTTLE_ADMIN_PASSWORD is ignored; config already has a different admin password")
		}
	}
	return warnings
}

func adminPasswordFromEnv(getenv func(string) string) string {
	return strings.TrimRight(getenv("CUTTLE_ADMIN_PASSWORD"), "\r\n")
}

func dataDirWarnings(dataDir string) []string {
	info, err := os.Stat(dataDir)
	if err != nil {
		return nil
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return []string{fmt.Sprintf("CUTTLE_DATA %s is accessible by group or other; use chmod 700", dataDir)}
}

func sessionsResetPending(dataDir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dataDir, SessionsResetFileName))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func createSessionsResetMarker(dataDir string) error {
	f, err := os.OpenFile(filepath.Join(dataDir, SessionsResetFileName), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// ClearSessionsReset removes the pending marker after sessions were cleared.
func ClearSessionsReset(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, SessionsResetFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
