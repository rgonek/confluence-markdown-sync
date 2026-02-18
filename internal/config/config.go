// Package config handles environment variable loading and configuration resolution.
// Precedence: CONFLUENCE_* (legacy) -> ATLASSIAN_* -> .env file -> error.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds resolved Confluence credentials.
type Config struct {
	Domain   string
	Email    string
	APIToken string
}

// ErrMissingConfig is returned when required config values cannot be resolved.
var ErrMissingConfig = errors.New("missing configuration")

// Load resolves credentials from environment and optional .env file.
// Precedence: CONFLUENCE_* (legacy) -> ATLASSIAN_* -> .env file.
// The .env path is loaded only if explicit env vars are absent.
func Load(dotEnvPath string) (*Config, error) {
	// Attempt to load .env if provided and env vars are not already set.
	if dotEnvPath != "" {
		if _, err := os.Stat(dotEnvPath); err == nil {
			// Only load .env values that aren't already set in the environment.
			_ = godotenv.Load(dotEnvPath)
		}
	}

	domain := resolve("CONFLUENCE_URL", "ATLASSIAN_DOMAIN")
	email := resolve("CONFLUENCE_EMAIL", "ATLASSIAN_EMAIL")
	token := resolve("CONFLUENCE_API_TOKEN", "ATLASSIAN_API_TOKEN")

	var missing []string
	if domain == "" {
		missing = append(missing, "ATLASSIAN_DOMAIN")
	}
	if email == "" {
		missing = append(missing, "ATLASSIAN_EMAIL")
	}
	if token == "" {
		missing = append(missing, "ATLASSIAN_API_TOKEN")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrMissingConfig, strings.Join(missing, ", "))
	}

	return &Config{
		Domain:   strings.TrimRight(domain, "/"),
		Email:    email,
		APIToken: token,
	}, nil
}

// resolve returns the first non-empty value from the legacy key then the canonical key.
func resolve(legacyKey, canonicalKey string) string {
	if v := os.Getenv(legacyKey); v != "" {
		return v
	}
	return os.Getenv(canonicalKey)
}
