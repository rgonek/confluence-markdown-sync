package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
)

// --- Target parser tests ---

func TestParseTarget_FileMode(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"page.md"},
		{"./spaces/MYSPACE/page.md"},
		{"/absolute/path/to/page.md"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := config.ParseTarget(tc.input)
			if !got.IsFile() {
				t.Errorf("ParseTarget(%q) mode = Space; want File", tc.input)
			}
			if got.Value != tc.input {
				t.Errorf("ParseTarget(%q) value = %q; want %q", tc.input, got.Value, tc.input)
			}
		})
	}
}

func TestParseTarget_SpaceMode(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"MYSPACE"},
		{""},
		{"~myspace"},
		{"some/path/without-extension"},
		{"page.mdx"}, // does not end with .md
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := config.ParseTarget(tc.input)
			if !got.IsSpace() {
				t.Errorf("ParseTarget(%q) mode = File; want Space", tc.input)
			}
			if got.Value != tc.input {
				t.Errorf("ParseTarget(%q) value = %q; want %q", tc.input, got.Value, tc.input)
			}
		})
	}
}

// --- Config loading / precedence tests ---

func TestLoad_AtlassianVars(t *testing.T) {
	t.Setenv("ATLASSIAN_DOMAIN", "https://example.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "tok123")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Domain != "https://example.atlassian.net" {
		t.Errorf("Domain = %q; want %q", cfg.Domain, "https://example.atlassian.net")
	}
	if cfg.Email != "user@example.com" {
		t.Errorf("Email = %q; want %q", cfg.Email, "user@example.com")
	}
	if cfg.APIToken != "tok123" {
		t.Errorf("APIToken = %q; want %q", cfg.APIToken, "tok123")
	}
}

func TestLoad_LegacyVarsPrecedence(t *testing.T) {
	// Legacy CONFLUENCE_* should win over ATLASSIAN_*.
	t.Setenv("CONFLUENCE_URL", "https://legacy.atlassian.net")
	t.Setenv("ATLASSIAN_DOMAIN", "https://should-not-win.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "tok123")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Domain != "https://legacy.atlassian.net" {
		t.Errorf("Domain = %q; want legacy value", cfg.Domain)
	}
}

func TestLoad_DotEnvFile(t *testing.T) {
	// Fully unset vars so godotenv.Load can populate them from the .env file.
	for _, k := range []string{
		"ATLASSIAN_DOMAIN", "ATLASSIAN_EMAIL", "ATLASSIAN_API_TOKEN",
		"CONFLUENCE_URL", "CONFLUENCE_EMAIL", "CONFLUENCE_API_TOKEN",
	} {
		prev, had := os.LookupEnv(k)
		os.Unsetenv(k)
		if had {
			t.Cleanup(func() { os.Setenv(k, prev) })
		}
	}

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "ATLASSIAN_DOMAIN=https://dotenv.atlassian.net\n" +
		"ATLASSIAN_EMAIL=dotenv@example.com\n" +
		"ATLASSIAN_API_TOKEN=dotenvtok\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(envFile)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Domain != "https://dotenv.atlassian.net" {
		t.Errorf("Domain = %q; want dotenv value", cfg.Domain)
	}
	if cfg.Email != "dotenv@example.com" {
		t.Errorf("Email = %q; want dotenv value", cfg.Email)
	}
	if cfg.APIToken != "dotenvtok" {
		t.Errorf("APIToken = %q; want dotenv value", cfg.APIToken)
	}
}

func TestLoad_MissingConfig(t *testing.T) {
	for _, k := range []string{
		"ATLASSIAN_DOMAIN", "ATLASSIAN_EMAIL", "ATLASSIAN_API_TOKEN",
		"CONFLUENCE_URL", "CONFLUENCE_EMAIL", "CONFLUENCE_API_TOKEN",
	} {
		prev, had := os.LookupEnv(k)
		os.Unsetenv(k)
		if had {
			t.Cleanup(func() { os.Setenv(k, prev) })
		}
	}

	_, err := config.Load("")
	if err == nil {
		t.Fatal("Load() expected error for missing config, got nil")
	}
}

func TestLoad_TrailingSlashStripped(t *testing.T) {
	t.Setenv("ATLASSIAN_DOMAIN", "https://example.atlassian.net/")
	t.Setenv("ATLASSIAN_EMAIL", "user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "tok")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Domain != "https://example.atlassian.net" {
		t.Errorf("Domain trailing slash not stripped: %q", cfg.Domain)
	}
}
