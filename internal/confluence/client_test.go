//nolint:errcheck // test handlers intentionally ignore best-effort response write errors
package confluence

import (
	"testing"
	"time"
)

func TestNewClient_RequiresCoreConfig(t *testing.T) {
	_, err := NewClient(ClientConfig{})
	if err == nil {
		t.Fatal("NewClient() expected error, got nil")
	}
}

func TestNewClient_AppliesRateAndRetryPolicyConfig(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:          "https://example.test",
		Email:            "user@example.com",
		APIToken:         "token-123",
		RateLimitRPS:     9,
		RetryMaxAttempts: 7,
		RetryBaseDelay:   200 * time.Millisecond,
		RetryMaxDelay:    3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	if got := cap(client.limiter.tokens); got != 9 {
		t.Fatalf("rate limiter capacity = %d, want 9", got)
	}
	if client.retry.maxAttempts != 7 {
		t.Fatalf("retry max attempts = %d, want 7", client.retry.maxAttempts)
	}
	if client.retry.baseDelay != 200*time.Millisecond {
		t.Fatalf("retry base delay = %v, want 200ms", client.retry.baseDelay)
	}
	if client.retry.maxDelay != 3*time.Second {
		t.Fatalf("retry max delay = %v, want 3s", client.retry.maxDelay)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() should be idempotent, got error: %v", err)
	}
}
