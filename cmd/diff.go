package cmd

import (
	"fmt"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff [TARGET]",
		Short: "Show diff between local Markdown and remote Confluence content",
		Long: `Diff fetches remote Confluence content, converts it to Markdown,
and shows a diff against local files using git diff --no-index.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			target := config.ParseTarget(raw)
			_ = target
			fmt.Fprintln(cmd.OutOrStdout(), "diff: not yet implemented")
			return nil
		},
	}
}
