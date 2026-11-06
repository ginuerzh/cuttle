package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

		paths, err := Resolve(explicit, envMap(map[string]string{"LLM_GW_DATA": dataDir}), []string{legacy})
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

		paths, err := Resolve("", envMap(map[string]string{"LLM_GW_DATA": dataDir}), []string{legacy})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != dataConfig || !paths.Existing {
			t.Fatalf("paths = %+v, want data config %q existing", paths, dataConfig)
		}
	})

	t.Run("legacy fallback", func(t *testing.T) {
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		legacy := filepath.Join(base, "legacy", ConfigFileName)
		writeFile(t, legacy)

		paths, err := Resolve("", envMap(map[string]string{"LLM_GW_DATA": dataDir}), []string{legacy})
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

		paths, err := Resolve("", envMap(map[string]string{"LLM_GW_DATA": dataDir}), []string{legacy})
		if err != nil {
			t.Fatal(err)
		}
		if paths.ConfigPath != want || paths.Existing {
			t.Fatalf("paths = %+v, want generated config %q not existing", paths, want)
		}
	})
}

func TestResolveDefaultDataDir(t *testing.T) {
	paths, err := Resolve("", envMap(nil), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if paths.DataDir != "/data" || paths.ConfigPath != "/data/llm-gw.yaml" {
		t.Fatalf("paths = %+v", paths)
	}
}

func TestResolveRelativeDataDirIsAbsolute(t *testing.T) {
	paths, err := Resolve("", envMap(map[string]string{"LLM_GW_DATA": "relative/data"}), []string{})
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

	_, err := Resolve("", envMap(map[string]string{"LLM_GW_DATA": dataDir}), []string{})
	if err == nil {
		t.Fatal("want error for data dir under a regular file")
	}
	if !strings.Contains(err.Error(), "LLM_GW_DATA") {
		t.Fatalf("error %q does not name LLM_GW_DATA", err)
	}
}
