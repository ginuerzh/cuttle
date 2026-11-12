package bootstrap

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveExplicitPathMustExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := Resolve(missing, envMap(nil), nil)
	if err == nil {
		t.Fatal("want error for missing explicit config")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name %q", err, missing)
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Run("explicit wins", func(t *testing.T) {
		base := t.TempDir()
		explicit := filepath.Join(base, "explicit.yaml")
		dataDir := filepath.Join(base, "data")
		dataConfig := filepath.Join(dataDir, ConfigFileName)
		legacy := filepath.Join(base, "legacy", ConfigFileName)
		writeFile(t, explicit)
		writeFile(t, dataConfig)
		writeFile(t, legacy)

		paths, err := Resolve(explicit, envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{legacy})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != explicit || !paths.Existing {
			t.Fatalf("paths = %+v, want explicit %q existing", paths, explicit)
		}
	})

	t.Run("data config wins over legacy", func(t *testing.T) {
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		dataConfig := filepath.Join(dataDir, ConfigFileName)
		legacy := filepath.Join(base, "legacy", ConfigFileName)
		writeFile(t, dataConfig)
		writeFile(t, legacy)

		paths, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{legacy})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != dataConfig || !paths.Existing {
			t.Fatalf("paths = %+v, want data config %q existing", paths, dataConfig)
		}
	})

	t.Run("secondary fallback", func(t *testing.T) {
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		legacy := filepath.Join(base, "legacy", ConfigFileName)
		writeFile(t, legacy)

		paths, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{legacy})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != legacy || !paths.Existing {
			t.Fatalf("paths = %+v, want legacy %q existing", paths, legacy)
		}
	})

	t.Run("generate in data dir", func(t *testing.T) {
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		legacy := filepath.Join(base, "legacy", ConfigFileName)
		want := filepath.Join(dataDir, ConfigFileName)

		paths, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{legacy})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != want || paths.Existing {
			t.Fatalf("paths = %+v, want generated config %q not existing", paths, want)
		}
	})
}

func setUserConfigDir(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := userConfigDir
	userConfigDir = fn
	t.Cleanup(func() { userConfigDir = old })
}

func setGetwd(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := getwd
	getwd = fn
	t.Cleanup(func() { getwd = old })
}

func TestResolveDefaultDataDir(t *testing.T) {
	configHome := t.TempDir()
	setUserConfigDir(t, func() (string, error) { return configHome, nil })

	paths, err := Resolve("", envMap(nil), []string{})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(configHome, DirName)
	if paths.DataDir != wantDir || paths.ConfigPath != filepath.Join(wantDir, ConfigFileName) {
		t.Fatalf("paths = %+v, want data dir %q", paths, wantDir)
	}
	if paths.DataDirFromFallback {
		t.Fatal("DataDirFromFallback = true, want false")
	}
}

func TestResolveDefaultDataDirFallsBackToWorkingDir(t *testing.T) {
	workDir := t.TempDir()
	setUserConfigDir(t, func() (string, error) { return "", errors.New("$HOME is not defined") })
	setGetwd(t, func() (string, error) { return workDir, nil })

	paths, err := Resolve("", envMap(nil), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != workDir || paths.ConfigPath != filepath.Join(workDir, ConfigFileName) {
		t.Fatalf("paths = %+v, want data dir %q", paths, workDir)
	}
	if !paths.DataDirFromFallback {
		t.Fatal("DataDirFromFallback = false, want true")
	}
}

func TestResolveExplicitDataDirIgnoresUserConfigDir(t *testing.T) {
	dataDir := t.TempDir()
	setUserConfigDir(t, func() (string, error) {
		t.Fatal("userConfigDir called although CUTTLE_DATA is set")
		return "", nil
	})
	setGetwd(t, func() (string, error) {
		t.Fatal("getwd called although CUTTLE_DATA is set")
		return "", nil
	})

	paths, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != dataDir || paths.DataDirFromFallback {
		t.Fatalf("paths = %+v, want %q without fallback", paths, dataDir)
	}
}

func TestResolveRelativeDataDirIsAbsolute(t *testing.T) {
	paths, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": "relative/data"}), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(paths.DataDir) || !filepath.IsAbs(paths.ConfigPath) {
		t.Fatalf("paths are not absolute: %+v", paths)
	}
	if !strings.HasSuffix(paths.DataDir, filepath.Join("relative", "data")) {
		t.Fatalf("DataDir = %q, want suffix relative/data", paths.DataDir)
	}
}

func TestResolveDataDirErrorNamesEnv(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(file, "data")

	_, err := Resolve("", envMap(map[string]string{"CUTTLE_DATA": dataDir}), []string{})
	if err == nil {
		t.Fatal("want error for data dir under a regular file")
	}
	if !strings.Contains(err.Error(), "CUTTLE_DATA") {
		t.Fatalf("error %q does not name CUTTLE_DATA", err)
	}
}

func writeBootstrapConfig(t *testing.T, dataDir string, cfg *config.Config) string {
	t.Helper()
	config.ApplyDefaults(cfg)
	path := filepath.Join(dataDir, ConfigFileName)
	if err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareGeneratesConfig(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "nested", "data")
	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated {
		t.Fatal("Generated = false, want true")
	}
	if res.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", res.DataDir, dataDir)
	}
	if !res.ResetSessions {
		t.Fatal("ResetSessions = false, want true for a generated config")
	}
	if _, err := os.Stat(filepath.Join(dataDir, SessionsResetFileName)); err != nil {
		t.Fatalf("sessions reset marker: %v", err)
	}
	if len(res.GeneratedPassword) != 32 {
		t.Fatalf("generated password length = %d, want 32", len(res.GeneratedPassword))
	}
	if !strings.Contains(buf.String(), res.GeneratedPassword) {
		t.Fatal("stderr does not contain the generated password")
	}
	if !config.VerifyPassword(res.Config.Admin.PasswordHash, res.GeneratedPassword) {
		t.Fatal("stored hash does not verify the generated password")
	}
	if res.Config.Stats.Path != filepath.Join(dataDir, "stats.sqlite") {
		t.Fatalf("Stats.Path = %q", res.Config.Stats.Path)
	}
	if res.Config.Stats.RetainRequests != config.Duration(168*time.Hour) {
		t.Fatalf("RetainRequests = %v, want 168h", time.Duration(res.Config.Stats.RetainRequests))
	}
	if res.Config.Stats.RetainRollups != config.Duration(2160*time.Hour) {
		t.Fatalf("RetainRollups = %v, want 2160h", time.Duration(res.Config.Stats.RetainRollups))
	}
	if res.Config.Gateway.Listen != ":8787" {
		t.Fatalf("Gateway.Listen = %q", res.Config.Gateway.Listen)
	}
	if res.Config.Admin.Listen != "127.0.0.1:8788" {
		t.Fatalf("Admin.Listen = %q", res.Config.Admin.Listen)
	}
	if len(res.Config.Entrypoints) != 0 || len(res.Config.Providers) != 0 || len(res.Config.Nodes) != 0 || len(res.Config.Routes) != 0 {
		t.Fatalf("topology is not empty: %+v", res.Config)
	}
	info, err := os.Stat(res.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
	if _, err := config.Load(res.ConfigPath); err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
}

func TestPrepareUsesEnvDefaults(t *testing.T) {
	dataDir := t.TempDir()
	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":           dataDir,
			"CUTTLE_LISTEN":         "127.0.0.1:18080",
			"CUTTLE_ADMIN_LISTEN":   "127.0.0.1:18081",
			"CUTTLE_ADMIN_PASSWORD": "correct-horse",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated {
		t.Fatal("Generated = false, want true")
	}
	if res.GeneratedPassword != "" {
		t.Fatal("a password was generated despite CUTTLE_ADMIN_PASSWORD")
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
	if res.Config.Gateway.Listen != "127.0.0.1:18080" {
		t.Fatalf("Gateway.Listen = %q", res.Config.Gateway.Listen)
	}
	if res.Config.Admin.Listen != "127.0.0.1:18081" {
		t.Fatalf("Admin.Listen = %q", res.Config.Admin.Listen)
	}
	if !config.VerifyPassword(res.Config.Admin.PasswordHash, "correct-horse") {
		t.Fatal("stored hash does not verify CUTTLE_ADMIN_PASSWORD")
	}
}

func TestPrepareRejectsShortPassword(t *testing.T) {
	dataDir := t.TempDir()
	var buf bytes.Buffer
	_, err := Prepare(Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":           dataDir,
			"CUTTLE_ADMIN_PASSWORD": "short",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err == nil {
		t.Fatal("want error for short password")
	}
	if !strings.Contains(err.Error(), "8") {
		t.Fatalf("error %q does not mention the minimum length", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, ConfigFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file exists after a rejected password: %v", statErr)
	}
}

func TestPrepareRestart(t *testing.T) {
	dataDir := t.TempDir()
	var buf bytes.Buffer
	opts := Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	}
	first, err := Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ClearSessionsReset(first.DataDir); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	second, err := Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated {
		t.Fatal("second Prepare regenerated the config")
	}
	if second.ResetSessions {
		t.Fatal("second Prepare wants a session reset after the marker was cleared")
	}
	if second.GeneratedPassword != "" {
		t.Fatal("second Prepare generated a password")
	}
	if buf.Len() != 0 {
		t.Fatalf("second Prepare wrote to stderr: %q", buf.String())
	}
	if second.Config.Admin.PasswordHash != first.Config.Admin.PasswordHash {
		t.Fatal("password hash changed across restart")
	}
}

func TestPrepareExistingConfigWarnings(t *testing.T) {
	dataDir := t.TempDir()
	hash, err := config.HashPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Gateway: config.Gateway{Listen: "127.0.0.1:1111"},
		Admin:   config.Admin{Listen: "127.0.0.1:2222", PasswordHash: hash},
		Stats:   config.Stats{Path: filepath.Join(dataDir, "stats.sqlite")},
	}
	writeBootstrapConfig(t, dataDir, cfg)

	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":           dataDir,
			"CUTTLE_LISTEN":         "127.0.0.1:3333",
			"CUTTLE_ADMIN_LISTEN":   "127.0.0.1:4444",
			"CUTTLE_ADMIN_PASSWORD": "new-password",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Generated {
		t.Fatal("Generated = true for an existing config")
	}
	if res.ResetSessions {
		t.Fatal("ResetSessions = true without a pending marker")
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
	if len(res.Warnings) != 3 {
		t.Fatalf("warnings = %#v, want 3", res.Warnings)
	}
	for _, name := range []string{"CUTTLE_LISTEN", "CUTTLE_ADMIN_LISTEN", "CUTTLE_ADMIN_PASSWORD"} {
		found := false
		for _, warning := range res.Warnings {
			if strings.Contains(warning, name) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("warnings %#v do not mention %s", res.Warnings, name)
		}
	}
	if res.Config.Gateway.Listen != "127.0.0.1:1111" || res.Config.Admin.Listen != "127.0.0.1:2222" {
		t.Fatalf("env values overrode the config: %+v", res.Config)
	}
	if !config.VerifyPassword(res.Config.Admin.PasswordHash, "old-password") {
		t.Fatal("env password overrode the config hash")
	}

	buf.Reset()
	res, err = Prepare(Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":           dataDir,
			"CUTTLE_LISTEN":         "127.0.0.1:1111",
			"CUTTLE_ADMIN_LISTEN":   "127.0.0.1:2222",
			"CUTTLE_ADMIN_PASSWORD": "old-password",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("matching env values produced warnings: %#v", res.Warnings)
	}
}

func TestPrepareExistingEmptyHash(t *testing.T) {
	dataDir := t.TempDir()
	cfg := &config.Config{}
	writeBootstrapConfig(t, dataDir, cfg)

	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":           dataDir,
			"CUTTLE_ADMIN_PASSWORD": "some-password",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Admin.PasswordHash != "" {
		t.Fatal("env password enabled admin on an existing config")
	}
	if res.ResetSessions {
		t.Fatal("ResetSessions = true without a pending marker")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("want a warning for an ignored env password")
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "disabled") {
		t.Fatalf("warnings do not explain the disabled console: %#v", res.Warnings)
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
}

func TestPrepareUnwritableDataDir(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": filepath.Join(file, "data")}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err == nil {
		t.Fatal("want error for unwritable data dir")
	}
	if !strings.Contains(err.Error(), "CUTTLE_DATA") {
		t.Fatalf("error %q does not name CUTTLE_DATA", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
}

func TestPrepareExistingStatsError(t *testing.T) {
	dataDir := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	statsPath := filepath.Join(file, "stats.sqlite")
	cfg := &config.Config{Stats: config.Stats{Path: statsPath}}
	writeBootstrapConfig(t, dataDir, cfg)

	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatalf("Prepare opened stats: %v", err)
	}
	if res.ResetSessions {
		t.Fatal("ResetSessions = true without a pending marker")
	}
	if res.Config.Stats.Path != statsPath {
		t.Fatalf("Stats.Path = %q, want %q", res.Config.Stats.Path, statsPath)
	}
	store, err := stats.Open(res.Config.Stats.Path)
	if err == nil {
		store.Close()
		t.Fatal("want stats.Open to fail for an unwritable path")
	}
}

func TestGeneratePassword(t *testing.T) {
	password, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(password) != 32 {
		t.Fatalf("password length = %d, want 32", len(password))
	}
	if _, err := base64.RawURLEncoding.DecodeString(password); err != nil {
		t.Fatalf("password is not base64url: %v", err)
	}
}

func TestPreparePrintsPasswordBeforeLoadFailure(t *testing.T) {
	old := loadConfig
	loadConfig = func(string, ...config.LoadOption) (*config.Config, error) {
		return nil, errors.New("load failed")
	}
	defer func() { loadConfig = old }()

	dataDir := t.TempDir()
	var buf bytes.Buffer
	_, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err == nil {
		t.Fatal("want the load error")
	}
	if !strings.Contains(buf.String(), "generated admin password") {
		t.Fatal("password was not printed before the load failure")
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, ConfigFileName)); statErr != nil {
		t.Fatalf("config file was not written: %v", statErr)
	}
}

func TestPrepareRetriesPendingSessionReset(t *testing.T) {
	dataDir := t.TempDir()
	var buf bytes.Buffer
	opts := Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	}
	first, err := Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ResetSessions {
		t.Fatal("first Prepare did not request a session reset")
	}

	// Simulate a startup that failed before clearing sessions: the marker
	// remains and the next start must retry.
	buf.Reset()
	second, err := Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated {
		t.Fatal("second Prepare regenerated the config")
	}
	if !second.ResetSessions {
		t.Fatal("pending session reset was not retried")
	}
	if second.DataDir != dataDir {
		t.Fatalf("DataDir = %q, want %q", second.DataDir, dataDir)
	}
	if buf.Len() != 0 {
		t.Fatalf("second Prepare wrote to stderr: %q", buf.String())
	}

	if err := ClearSessionsReset(dataDir); err != nil {
		t.Fatal(err)
	}
	third, err := Prepare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if third.ResetSessions {
		t.Fatal("ResetSessions = true after clearing the marker")
	}
}

func TestClearSessionsReset(t *testing.T) {
	dir := t.TempDir()
	if err := ClearSessionsReset(dir); err != nil {
		t.Fatalf("missing marker: %v", err)
	}
	marker := filepath.Join(dir, SessionsResetFileName)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearSessionsReset(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker still exists: %v", err)
	}
}

func TestPrepareExclusiveWriteLoadsExisting(t *testing.T) {
	old := writeConfig
	writeConfig = func(path string, _ *config.Config) error {
		existing := &config.Config{Gateway: config.Gateway{Listen: "127.0.0.1:19999"}}
		config.ApplyDefaults(existing)
		if err := config.Write(path, existing); err != nil {
			return err
		}
		return config.ErrConfigExists
	}
	defer func() { writeConfig = old }()

	dataDir := t.TempDir()
	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Generated {
		t.Fatal("Generated = true after losing the first-run race")
	}
	if res.ResetSessions {
		t.Fatal("ResetSessions = true after losing the first-run race")
	}
	if res.Config.Gateway.Listen != "127.0.0.1:19999" {
		t.Fatalf("Gateway.Listen = %q, want the existing config", res.Config.Gateway.Listen)
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, SessionsResetFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker was left behind: %v", err)
	}
}

func TestPrepareWriteErrorNamesEnv(t *testing.T) {
	old := writeConfig
	writeConfig = func(string, *config.Config) error {
		return errors.New("permission denied")
	}
	defer func() { writeConfig = old }()

	dataDir := t.TempDir()
	var buf bytes.Buffer
	_, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err == nil {
		t.Fatal("want a write error")
	}
	if !strings.Contains(err.Error(), "CUTTLE_DATA") {
		t.Fatalf("error %q does not name CUTTLE_DATA", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
}

func TestPrepareTrimsEnvPasswordNewline(t *testing.T) {
	dataDir := t.TempDir()
	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv: envMap(map[string]string{
			"CUTTLE_DATA":           dataDir,
			"CUTTLE_ADMIN_PASSWORD": "correct-horse\n",
		}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GeneratedPassword != "" {
		t.Fatal("a password was generated despite CUTTLE_ADMIN_PASSWORD")
	}
	if !config.VerifyPassword(res.Config.Admin.PasswordHash, "correct-horse") {
		t.Fatal("hash does not verify the trimmed password")
	}
	if config.VerifyPassword(res.Config.Admin.PasswordHash, "correct-horse\n") {
		t.Fatal("hash still requires the trailing newline")
	}
	if buf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", buf.String())
	}
}

func TestPrepareDataDirPermissionWarning(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated {
		t.Fatal("Generated = false, want true")
	}
	found := false
	for _, warning := range res.Warnings {
		if strings.Contains(warning, "chmod 700") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings = %#v, want a chmod 700 warning", res.Warnings)
	}
}

// TestPrepareLogsConfigMigration pins the startup write-back signal: the
// logger main creates needs the config being written, so Prepare must report a
// migrated config on stderr instead of rewriting it silently.
func TestPrepareLogsConfigMigration(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, ConfigFileName)
	legacy := `
admin:
  listen: "127.0.0.1:0"
providers:
  - name: alpha
    type: generic
    chat: https://a.example/v1
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	res, err := Prepare(Options{
		Getenv:      envMap(map[string]string{"CUTTLE_DATA": dataDir}),
		SearchPaths: []string{},
		Stderr:      &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Generated {
		t.Fatal("Generated = true, want the existing config")
	}
	out := buf.String()
	if !strings.Contains(out, "config migrated") {
		t.Fatalf("stderr = %q, want the migration info line", out)
	}
	for _, want := range []string{"entity ids written", path + ".bak-"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr = %q, want %q", out, want)
		}
	}
}
