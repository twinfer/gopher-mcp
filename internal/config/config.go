package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const FileName = ".repo-mcp.yaml"

// Load reads .repo-mcp.yaml from root. When the file is absent, returns the
// zero RepoConfig with found=false and no error.
func Load(root string) (cfg RepoConfig, found bool, err error) {
	path := filepath.Join(root, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RepoConfig{}, false, nil
		}
		return RepoConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RepoConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	return cfg, true, nil
}
