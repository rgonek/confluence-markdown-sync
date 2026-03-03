// Package cmd contains all cobra command definitions for the conf CLI.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/spf13/cobra"
)

// automation flags shared by pull and push.
var (
	Version               = "dev"
	flagYes               bool
	flagNonInteractive    bool
	flagSkipMissingAssets bool
	flagVerbose           bool
	flagVersion           bool
	flagRateLimitRPS      int
	flagRetryMaxAttempts  int
	flagRetryBaseDelay    time.Duration
	flagRetryMaxDelay     time.Duration
)

var rootCmd = &cobra.Command{
	Use:   "conf",
	Short: "conf — Confluence Markdown Sync CLI",
	Long: `conf syncs Confluence pages with a local Markdown workspace.

It converts Confluence ADF content to Markdown for local editing,
and converts Markdown back to ADF for publishing updates.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagVersion {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), Version)
			return err
		}
		return cmd.Help()
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.ExecuteContext(context.Background())
}

// ExecuteContext runs the root command with the given context.
// This enables graceful signal handling (SIGINT/SIGTERM) when called
// with a signal-aware context.
func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func getCommandContext(cmd *cobra.Command) context.Context {
	ctx := cmd.Context()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func init() {
	// Disable background color detection which can hang on some terminals (e.g., Windows conhost or mintty).
	// This forces lipgloss to assume a dark background, skipping the blocking terminal query.
	lipgloss.SetHasDarkBackground(true)

	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output (log HTTP requests)")
	rootCmd.PersistentFlags().IntVar(&flagRateLimitRPS, "rate-limit-rps", confluence.DefaultRateLimitRPS, "Confluence API request rate limit (requests/second)")
	rootCmd.PersistentFlags().IntVar(&flagRetryMaxAttempts, "retry-max-attempts", confluence.DefaultRetryMaxAttempts, "Maximum retries for retryable Confluence API requests")
	rootCmd.PersistentFlags().DurationVar(&flagRetryBaseDelay, "retry-base-delay", confluence.DefaultRetryBaseDelay, "Base retry delay for exponential backoff")
	rootCmd.PersistentFlags().DurationVar(&flagRetryMaxDelay, "retry-max-delay", confluence.DefaultRetryMaxDelay, "Maximum retry delay")
	rootCmd.Flags().BoolVar(&flagVersion, "version", false, "Print conf version and exit")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := applyHTTPPolicyEnvOverrides(cmd); err != nil {
			return err
		}

		level := slog.LevelWarn
		if flagVerbose {
			level = slog.LevelDebug
		}

		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		slog.Debug("http policy",
			"rate_limit_rps", flagRateLimitRPS,
			"retry_max_attempts", flagRetryMaxAttempts,
			"retry_base_delay", flagRetryBaseDelay.String(),
			"retry_max_delay", flagRetryMaxDelay.String(),
		)
		return nil
	}
	rootCmd.AddCommand(
		newInitCmd(),
		newPullCmd(),
		newPushCmd(),
		newStatusCmd(),
		newCleanCmd(),
		newPruneCmd(),
		newValidateCmd(),
		newDiffCmd(),
		newRelinkCmd(),
		newVersionCmd(),
		newDoctorCmd(),
		newSearchCmd(),
	)
}

func applyHTTPPolicyEnvOverrides(cmd *cobra.Command) error {
	if !cmd.Flags().Changed("rate-limit-rps") {
		if raw := strings.TrimSpace(os.Getenv("CONF_RATE_LIMIT_RPS")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("invalid CONF_RATE_LIMIT_RPS value %q: %w", raw, err)
			}
			flagRateLimitRPS = value
		}
	}

	if !cmd.Flags().Changed("retry-max-attempts") {
		if raw := strings.TrimSpace(os.Getenv("CONF_RETRY_MAX_ATTEMPTS")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("invalid CONF_RETRY_MAX_ATTEMPTS value %q: %w", raw, err)
			}
			flagRetryMaxAttempts = value
		}
	}

	if !cmd.Flags().Changed("retry-base-delay") {
		if raw := strings.TrimSpace(os.Getenv("CONF_RETRY_BASE_DELAY")); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("invalid CONF_RETRY_BASE_DELAY value %q: %w", raw, err)
			}
			flagRetryBaseDelay = value
		}
	}

	if !cmd.Flags().Changed("retry-max-delay") {
		if raw := strings.TrimSpace(os.Getenv("CONF_RETRY_MAX_DELAY")); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("invalid CONF_RETRY_MAX_DELAY value %q: %w", raw, err)
			}
			flagRetryMaxDelay = value
		}
	}

	if flagRateLimitRPS <= 0 {
		flagRateLimitRPS = confluence.DefaultRateLimitRPS
	}
	if flagRetryMaxAttempts < 0 {
		flagRetryMaxAttempts = 0
	}
	if flagRetryBaseDelay <= 0 {
		flagRetryBaseDelay = confluence.DefaultRetryBaseDelay
	}
	if flagRetryMaxDelay <= 0 {
		flagRetryMaxDelay = confluence.DefaultRetryMaxDelay
	}
	if flagRetryMaxDelay < flagRetryBaseDelay {
		flagRetryMaxDelay = flagRetryBaseDelay
	}

	return nil
}
