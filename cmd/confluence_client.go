package cmd

import (
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func newConfluenceClientFromConfig(cfg *config.Config) (*confluence.Client, error) {
	return confluence.NewClient(confluence.ClientConfig{
		BaseURL:          cfg.Domain,
		Email:            cfg.Email,
		APIToken:         cfg.APIToken,
		UserAgent:        buildUserAgent(Version),
		RateLimitRPS:     flagRateLimitRPS,
		RetryMaxAttempts: flagRetryMaxAttempts,
		RetryBaseDelay:   flagRetryBaseDelay,
		RetryMaxDelay:    flagRetryMaxDelay,
	})
}

func buildUserAgent(version string) string {
	cleanVersion := strings.TrimSpace(version)
	if cleanVersion == "" {
		cleanVersion = "dev"
	}
	return "conf/" + cleanVersion
}

func closeRemoteIfPossible(remote any) {
	type closer interface {
		Close() error
	}

	if c, ok := remote.(closer); ok {
		_ = c.Close()
	}
}
