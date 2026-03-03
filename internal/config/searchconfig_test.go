package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
)

func TestLoadSearchConfig_Defaults(t *testing.T) {
	// Missing file → all defaults.
	cfg, err := config.LoadSearchConfig(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Engine != "sqlite" {
		t.Errorf("Engine = %q; want sqlite", cfg.Engine)
	}
	if cfg.Limit != 20 {
		t.Errorf("Limit = %d; want 20", cfg.Limit)
	}
	if cfg.ResultDetail != "full" {
		t.Errorf("ResultDetail = %q; want full", cfg.ResultDetail)
	}
}

func TestLoadSearchConfig_FullFile(t *testing.T) {
	dir := t.TempDir()
	content := "search:\n  engine: bleve\n  limit: 5\n  result_detail: minimal\n"
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadSearchConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Engine != "bleve" {
		t.Errorf("Engine = %q; want bleve", cfg.Engine)
	}
	if cfg.Limit != 5 {
		t.Errorf("Limit = %d; want 5", cfg.Limit)
	}
	if cfg.ResultDetail != "minimal" {
		t.Errorf("ResultDetail = %q; want minimal", cfg.ResultDetail)
	}
}

func TestLoadSearchConfig_PartialFile(t *testing.T) {
	// Only limit set — engine and result_detail should be defaults.
	dir := t.TempDir()
	content := "search:\n  limit: 50\n"
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadSearchConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Engine != "sqlite" {
		t.Errorf("Engine = %q; want sqlite (default)", cfg.Engine)
	}
	if cfg.Limit != 50 {
		t.Errorf("Limit = %d; want 50", cfg.Limit)
	}
	if cfg.ResultDetail != "full" {
		t.Errorf("ResultDetail = %q; want full (default)", cfg.ResultDetail)
	}
}

func TestLoadSearchConfig_ZeroLimitUsesDefault(t *testing.T) {
	// limit: 0 in file → treat as unset, use default 20.
	dir := t.TempDir()
	content := "search:\n  limit: 0\n"
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadSearchConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Limit != 20 {
		t.Errorf("Limit = %d; want 20 (default when 0)", cfg.Limit)
	}
}

func TestLoadSearchConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	// yaml.v3 accepts ":::bad yaml:::" as a valid mapping key, so use a string
	// that actually triggers a parse error (unclosed flow sequence).
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte("key: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadSearchConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
