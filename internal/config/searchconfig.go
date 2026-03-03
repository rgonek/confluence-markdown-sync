package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SearchConfig holds per-repo search preferences loaded from .conf.yaml.
type SearchConfig struct {
	Engine       string // "sqlite" | "bleve" — default "sqlite"
	Limit        int    // max results — default 20
	ResultDetail string // "full" | "standard" | "minimal" — default "full"
}

type confYAML struct {
	Search struct {
		Engine       string `yaml:"engine"`
		Limit        int    `yaml:"limit"`
		ResultDetail string `yaml:"result_detail"`
	} `yaml:"search"`
}

// LoadSearchConfig reads <repoRoot>/.conf.yaml and returns a SearchConfig with
// defaults applied. A missing file is not an error — defaults are returned.
func LoadSearchConfig(repoRoot string) (SearchConfig, error) {
	defaults := SearchConfig{
		Engine:       "sqlite",
		Limit:        20,
		ResultDetail: "full",
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".conf.yaml")) //nolint:gosec // path is repo root + fixed filename
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaults, nil
		}
		return defaults, err
	}

	var raw confYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return defaults, err
	}

	cfg := defaults
	if raw.Search.Engine != "" {
		cfg.Engine = raw.Search.Engine
	}
	if raw.Search.Limit > 0 {
		cfg.Limit = raw.Search.Limit
	}
	if raw.Search.ResultDetail != "" {
		cfg.ResultDetail = raw.Search.ResultDetail
	}
	return cfg, nil
}
