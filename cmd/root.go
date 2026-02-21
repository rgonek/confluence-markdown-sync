// Package cmd contains all cobra command definitions for the cms CLI.
package cmd

import (
	"github.com/spf13/cobra"
)

// automation flags shared by pull and push.
var (
	flagYes               bool
	flagNonInteractive    bool
	flagSkipMissingAssets bool
	flagVerbose           bool
)

var rootCmd = &cobra.Command{
	Use:   "cms",
	Short: "cms — Confluence Markdown Sync CLI",
	Long: `cms syncs Confluence pages with a local Markdown workspace.

It converts Confluence ADF content to Markdown for local editing,
and converts Markdown back to ADF for publishing updates.`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output (log HTTP requests)")
	rootCmd.AddCommand(
		newInitCmd(),
		newPullCmd(),
		newPushCmd(),
		newValidateCmd(),
		newDiffCmd(),
	)
}
