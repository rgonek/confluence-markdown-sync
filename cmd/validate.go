package cmd

import (
	"fmt"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [TARGET]",
		Short: "Validate local Markdown files against sync invariants",
		Long: `Validate checks frontmatter schema, immutable key integrity,
link/asset resolution, and Markdown-to-ADF conversion.

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
			fmt.Fprintln(cmd.OutOrStdout(), "validate: not yet implemented")
			return nil
		},
	}
}
