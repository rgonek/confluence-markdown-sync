// Package cmd contains all cobra command definitions for the conf CLI.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

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
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output (log HTTP requests)")
	rootCmd.Flags().BoolVar(&flagVersion, "version", false, "Print conf version and exit")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		level := slog.LevelWarn
		if flagVerbose {
			level = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		return nil
	}
	rootCmd.AddCommand(
		newInitCmd(),
		newPullCmd(),
		newPushCmd(),
		newValidateCmd(),
		newDiffCmd(),
		newRelinkCmd(),
		newVersionCmd(),
	)
}
