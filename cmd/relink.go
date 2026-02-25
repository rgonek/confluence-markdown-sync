package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func newRelinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relink [TARGET]",
		Short: "Resolve absolute Confluence links to local relative paths",
		Long: `Relink scans local Markdown files for absolute Confluence URLs and replaces them
with relative paths to the corresponding local files, if they are managed in this repository.

TARGET can be a SPACE_KEY or a path to a space directory. If provided, relink will
focus on resolving links that point to pages within that space.
If omitted, relink will attempt to resolve all possible links across all managed spaces.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runRelink(cmd, raw)
		},
	}
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve safety confirmations")
	return cmd
}

func runRelink(cmd *cobra.Command, target string) error {
	repoRoot, err := gitRepoRoot()
	if err != nil {
		return err
	}

	index, err := sync.BuildGlobalPageIndex(repoRoot)
	if err != nil {
		return fmt.Errorf("build global index: %w", err)
	}

	states, err := fs.FindAllStateFiles(repoRoot)
	if err != nil {
		return fmt.Errorf("discover spaces: %w", err)
	}

	if target != "" {
		return runTargetedRelink(cmd, repoRoot, target, index, states)
	}

	return runGlobalRelink(cmd, repoRoot, index, states)
}

func runTargetedRelink(cmd *cobra.Command, repoRoot, target string, index sync.GlobalPageIndex, states map[string]fs.SpaceState) error {
	// 1. Resolve target space
	targetSpaceDir := ""
	targetSpaceKey := ""

	// Check if target is a directory in states
	absTarget, _ := filepath.Abs(target)
	if state, ok := states[absTarget]; ok {
		targetSpaceDir = absTarget
		// Extract space key from one of the files or something?
		// Better to resolve space key like pull does.
		targetSpaceKey = getSpaceKeyFromState(absTarget, state)
	} else {
		// Try to find by space key
		for dir, state := range states {
			key := getSpaceKeyFromState(dir, state)
			if strings.EqualFold(key, target) {
				targetSpaceDir = dir
				targetSpaceKey = key
				break
			}
		}
	}

	if targetSpaceDir == "" {
		return fmt.Errorf("could not find managed space for target %q", target)
	}

	// 2. Identify all PageIDs belonging to target space
	targetPageIDs := make(map[string]struct{})
	state := states[targetSpaceDir]
	for _, id := range state.PagePathIndex {
		if id != "" {
			targetPageIDs[id] = struct{}{}
		}
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Relinking references to space %s (%s)...\n", targetSpaceKey, targetSpaceDir)

	// 3. Scan all OTHER spaces
	for dir, state := range states {
		if dir == targetSpaceDir {
			continue
		}

		currentSpaceKey := getSpaceKeyFromState(dir, state)

		// 1. Dry run to see if there are changes
		result, err := sync.ResolveLinksInSpace(dir, index, targetPageIDs, true)
		if err != nil {
			return err
		}

		if result.LinksConverted == 0 {
			continue
		}

		// 2. Prompt
		msg := fmt.Sprintf("Found %d absolute links in %d files in space %s pointing to %s. Update %s?",
			result.LinksConverted, result.FilesChanged, currentSpaceKey, targetSpaceKey, currentSpaceKey)
		if err := requireSafetyConfirmation(cmd.InOrStdin(), cmd.OutOrStdout(), msg, result.FilesChanged, false); err != nil {
			if flagNonInteractive {
				return err
			}
			// User said No or error, skip this space
			continue
		}

		// 3. Apply changes
		result, err = sync.ResolveLinksInSpace(dir, index, targetPageIDs, false)
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated %d links in %d files in space %s.\n", result.LinksConverted, result.FilesChanged, currentSpaceKey)
	}

	return nil
}

func runGlobalRelink(cmd *cobra.Command, repoRoot string, index sync.GlobalPageIndex, states map[string]fs.SpaceState) error {
	for dir, state := range states {
		spaceKey := getSpaceKeyFromState(dir, state)

		// 1. Dry run
		result, err := sync.ResolveLinksInSpace(dir, index, nil, true)
		if err != nil {
			return err
		}

		if result.LinksConverted == 0 {
			continue
		}

		// 2. Prompt
		msg := fmt.Sprintf("Found %d absolute links in %d files in space %s that can be resolved. Update %s?",
			result.LinksConverted, result.FilesChanged, spaceKey, spaceKey)
		if err := requireSafetyConfirmation(cmd.InOrStdin(), cmd.OutOrStdout(), msg, result.FilesChanged, false); err != nil {
			if flagNonInteractive {
				return err
			}
			continue
		}

		// 3. Apply
		result, err = sync.ResolveLinksInSpace(dir, index, nil, false)
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated %d links in %d files in space %s.\n", result.LinksConverted, result.FilesChanged, spaceKey)
	}
	return nil
}

func getSpaceKeyFromState(dir string, state fs.SpaceState) string {
	for relPath := range state.PagePathIndex {
		doc, err := fs.ReadMarkdownDocument(filepath.Join(dir, filepath.FromSlash(relPath)))
		if err == nil && doc.Frontmatter.Space != "" {
			return doc.Frontmatter.Space
		}
	}
	return filepath.Base(dir)
}
