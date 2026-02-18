package cmd

import (
	"fmt"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/spf13/cobra"
)

// conflict policy values for --on-conflict.
const (
	OnConflictPullMerge = "pull-merge"
	OnConflictForce     = "force"
	OnConflictCancel    = "cancel"
)

func newPushCmd() *cobra.Command {
	var onConflict string

	cmd := &cobra.Command{
		Use:   "push [TARGET]",
		Short: "Push local Markdown changes to Confluence",
		Long: `Push converts local Markdown files to ADF and updates Confluence pages.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.

push always runs validate before any remote write.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			target := config.ParseTarget(raw)
			_ = target

			if err := validateOnConflict(onConflict); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "push: not yet implemented")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve safety confirmations")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when a decision is required")
	cmd.Flags().StringVar(&onConflict, "on-conflict", "", "Non-interactive conflict policy: pull-merge|force|cancel")
	return cmd
}

func validateOnConflict(v string) error {
	if v == "" {
		return nil
	}
	switch v {
	case OnConflictPullMerge, OnConflictForce, OnConflictCancel:
		return nil
	default:
		return fmt.Errorf("invalid --on-conflict value %q: must be pull-merge, force, or cancel", v)
	}
}
