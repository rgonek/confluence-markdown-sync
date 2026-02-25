package cmd

import (
	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func newConfluenceClientFromConfig(cfg *config.Config) (*confluence.Client, error) {
	return confluence.NewClient(confluence.ClientConfig{
		BaseURL:          cfg.Domain,
		Email:            cfg.Email,
		APIToken:         cfg.APIToken,
		RateLimitRPS:     flagRateLimitRPS,
		RetryMaxAttempts: flagRetryMaxAttempts,
		RetryBaseDelay:   flagRetryBaseDelay,
		RetryMaxDelay:    flagRetryMaxDelay,
	})
}

func closeRemoteIfPossible(remote any) {
	type closer interface {
		Close() error
	}

	if c, ok := remote.(closer); ok {
		_ = c.Close()
	}
}
