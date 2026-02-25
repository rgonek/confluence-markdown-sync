package cmd

import (
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/spf13/cobra"
)

func TestApplyHTTPPolicyEnvOverrides_UsesEnvWhenFlagsUnset(t *testing.T) {
	restore := preserveHTTPPolicyFlags(t)
	defer restore()

	t.Setenv("CONF_RATE_LIMIT_RPS", "12")
	t.Setenv("CONF_RETRY_MAX_ATTEMPTS", "5")
	t.Setenv("CONF_RETRY_BASE_DELAY", "250ms")
	t.Setenv("CONF_RETRY_MAX_DELAY", "3s")

	cmd := &cobra.Command{Use: "test"}

	flagRateLimitRPS = confluence.DefaultRateLimitRPS
	flagRetryMaxAttempts = confluence.DefaultRetryMaxAttempts
	flagRetryBaseDelay = confluence.DefaultRetryBaseDelay
	flagRetryMaxDelay = confluence.DefaultRetryMaxDelay

	if err := applyHTTPPolicyEnvOverrides(cmd); err != nil {
		t.Fatalf("applyHTTPPolicyEnvOverrides() error: %v", err)
	}

	if flagRateLimitRPS != 12 {
		t.Fatalf("rate limit = %d, want 12", flagRateLimitRPS)
	}
	if flagRetryMaxAttempts != 5 {
		t.Fatalf("retry attempts = %d, want 5", flagRetryMaxAttempts)
	}
	if flagRetryBaseDelay != 250*time.Millisecond {
		t.Fatalf("retry base delay = %v, want 250ms", flagRetryBaseDelay)
	}
	if flagRetryMaxDelay != 3*time.Second {
		t.Fatalf("retry max delay = %v, want 3s", flagRetryMaxDelay)
	}
}

func TestApplyHTTPPolicyEnvOverrides_DoesNotOverrideExplicitFlags(t *testing.T) {
	restore := preserveHTTPPolicyFlags(t)
	defer restore()

	t.Setenv("CONF_RATE_LIMIT_RPS", "99")
	t.Setenv("CONF_RETRY_MAX_ATTEMPTS", "99")
	t.Setenv("CONF_RETRY_BASE_DELAY", "9s")
	t.Setenv("CONF_RETRY_MAX_DELAY", "10s")

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Int("rate-limit-rps", confluence.DefaultRateLimitRPS, "")
	cmd.Flags().Int("retry-max-attempts", confluence.DefaultRetryMaxAttempts, "")
	cmd.Flags().Duration("retry-base-delay", confluence.DefaultRetryBaseDelay, "")
	cmd.Flags().Duration("retry-max-delay", confluence.DefaultRetryMaxDelay, "")

	_ = cmd.Flags().Set("rate-limit-rps", "17")
	_ = cmd.Flags().Set("retry-max-attempts", "4")
	_ = cmd.Flags().Set("retry-base-delay", "150ms")
	_ = cmd.Flags().Set("retry-max-delay", "2s")

	flagRateLimitRPS = 17
	flagRetryMaxAttempts = 4
	flagRetryBaseDelay = 150 * time.Millisecond
	flagRetryMaxDelay = 2 * time.Second

	if err := applyHTTPPolicyEnvOverrides(cmd); err != nil {
		t.Fatalf("applyHTTPPolicyEnvOverrides() error: %v", err)
	}

	if flagRateLimitRPS != 17 {
		t.Fatalf("rate limit = %d, want 17", flagRateLimitRPS)
	}
	if flagRetryMaxAttempts != 4 {
		t.Fatalf("retry attempts = %d, want 4", flagRetryMaxAttempts)
	}
	if flagRetryBaseDelay != 150*time.Millisecond {
		t.Fatalf("retry base delay = %v, want 150ms", flagRetryBaseDelay)
	}
	if flagRetryMaxDelay != 2*time.Second {
		t.Fatalf("retry max delay = %v, want 2s", flagRetryMaxDelay)
	}
}

func TestApplyHTTPPolicyEnvOverrides_RejectsInvalidDuration(t *testing.T) {
	restore := preserveHTTPPolicyFlags(t)
	defer restore()

	t.Setenv("CONF_RETRY_BASE_DELAY", "not-a-duration")

	err := applyHTTPPolicyEnvOverrides(&cobra.Command{Use: "test"})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}

func preserveHTTPPolicyFlags(t *testing.T) func() {
	t.Helper()
	oldRate := flagRateLimitRPS
	oldAttempts := flagRetryMaxAttempts
	oldBase := flagRetryBaseDelay
	oldMax := flagRetryMaxDelay

	return func() {
		flagRateLimitRPS = oldRate
		flagRetryMaxAttempts = oldAttempts
		flagRetryBaseDelay = oldBase
		flagRetryMaxDelay = oldMax
	}
}
