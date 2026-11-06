// Package bootstrap resolves the llm-gw config location and creates a
// zero-config first-run configuration when none exists.
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultDataDir is the default LLM_GW_DATA directory.
	DefaultDataDir = "/data"
	// ConfigFileName is the config file name inside the data directory.
	ConfigFileName = "llm-gw.yaml"
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
