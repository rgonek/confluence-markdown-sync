package cmd

import (
	"fmt"
	"io"
	"os"
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
	_, err := runRelinkWithResult(cmd, target)
	return err
}

type relinkRunResult struct {
	MutatedFiles []string
}

func runRelinkWithResult(cmd *cobra.Command, target string) (relinkRunResult, error) {
	if err := ensureWorkspaceSyncReady("relink"); err != nil {
		return relinkRunResult{}, err
	}

	repoRoot, err := gitRepoRoot()
	if err != nil {
		return relinkRunResult{}, err
	}

	index, err := sync.BuildGlobalPageIndex(repoRoot)
	if err != nil {
		return relinkRunResult{}, fmt.Errorf("build global index: %w", err)
	}

	states, err := fs.FindAllStateFiles(repoRoot)
	if err != nil {
		return relinkRunResult{}, fmt.Errorf("discover spaces: %w", err)
	}

	out := reportWriter(cmd, ensureSynchronizedCmdOutput(cmd))

	if target != "" {
		return runTargetedRelink(cmd, out, repoRoot, target, index, states)
	}

	return runGlobalRelink(cmd, out, repoRoot, index, states)
}

func runTargetedRelink(cmd *cobra.Command, out io.Writer, _ string, target string, index sync.GlobalPageIndex, states map[string]fs.SpaceState) (relinkRunResult, error) {
	runResult := relinkRunResult{MutatedFiles: []string{}}

	// 1. Resolve target space
	targetSpaceDir := ""
	targetSpaceKey := ""
	normalizedStates := make(map[string]string, len(states))
	for dir := range states {
		normalizedStates[normalizeRelinkPath(dir)] = dir
	}

	// Check if target is a directory in states
	absTarget, _ := filepath.Abs(target)
	if resolvedDir, ok := normalizedStates[normalizeRelinkPath(absTarget)]; ok {
		targetSpaceDir = resolvedDir
		state := states[resolvedDir]
		// Extract space key from one of the files or something?
		// Better to resolve space key like pull does.
		targetSpaceKey = getSpaceKeyFromState(resolvedDir, state)
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
		return runResult, fmt.Errorf("could not find managed space for target %q", target)
	}

	// 2. Identify all PageIDs belonging to target space
	targetPageIDs := make(map[string]struct{})
	state := states[targetSpaceDir]
	for _, id := range state.PagePathIndex {
		if id != "" {
			targetPageIDs[id] = struct{}{}
		}
	}

	_, _ = fmt.Fprintf(out, "Relinking references to space %s (%s)...\n", targetSpaceKey, targetSpaceDir)

	// 3. Scan all OTHER spaces
	for dir, state := range states {
		if dir == targetSpaceDir {
			continue
		}

		currentSpaceKey := getSpaceKeyFromState(dir, state)

		// 1. Dry run to see if there are changes
		spaceResult, err := relinkSpaceFiles(dir, index, targetPageIDs, true)
		if err != nil {
			return runResult, err
		}

		if spaceResult.Summary.LinksConverted == 0 {
			continue
		}

		// 2. Prompt
		msg := fmt.Sprintf("Found %d absolute links in %d files in space %s pointing to %s. Update %s?",
			spaceResult.Summary.LinksConverted, spaceResult.Summary.FilesChanged, currentSpaceKey, targetSpaceKey, currentSpaceKey)
		if err := requireSafetyConfirmation(cmd.InOrStdin(), out, msg, spaceResult.Summary.FilesChanged, false); err != nil {
			if flagNonInteractive {
				return runResult, err
			}
			// User said No or error, skip this space
			continue
		}

		// 3. Apply changes
		appliedResult, err := relinkSpaceFiles(dir, index, targetPageIDs, false)
		runResult.MutatedFiles = append(runResult.MutatedFiles, appliedResult.MutatedFiles...)
		if err != nil {
			return runResult, err
		}

		_, _ = fmt.Fprintf(out, "Updated %d links in %d files in space %s.\n", appliedResult.Summary.LinksConverted, appliedResult.Summary.FilesChanged, currentSpaceKey)
	}

	return runResult, nil
}

func runGlobalRelink(cmd *cobra.Command, out io.Writer, _ string, index sync.GlobalPageIndex, states map[string]fs.SpaceState) (relinkRunResult, error) {
	result := relinkRunResult{MutatedFiles: []string{}}

	for dir, state := range states {
		spaceKey := getSpaceKeyFromState(dir, state)

		// 1. Dry run
		spaceResult, err := relinkSpaceFiles(dir, index, nil, true)
		if err != nil {
			return result, err
		}

		if spaceResult.Summary.LinksConverted == 0 {
			continue
		}

		// 2. Prompt
		msg := fmt.Sprintf("Found %d absolute links in %d files in space %s that can be resolved. Update %s?",
			spaceResult.Summary.LinksConverted, spaceResult.Summary.FilesChanged, spaceKey, spaceKey)
		if err := requireSafetyConfirmation(cmd.InOrStdin(), out, msg, spaceResult.Summary.FilesChanged, false); err != nil {
			if flagNonInteractive {
				return result, err
			}
			continue
		}

		// 3. Apply
		appliedResult, err := relinkSpaceFiles(dir, index, nil, false)
		result.MutatedFiles = append(result.MutatedFiles, appliedResult.MutatedFiles...)
		if err != nil {
			return result, err
		}

		_, _ = fmt.Fprintf(out, "Updated %d links in %d files in space %s.\n", appliedResult.Summary.LinksConverted, appliedResult.Summary.FilesChanged, spaceKey)
	}
	return result, nil
}

func getSpaceKeyFromState(dir string, state fs.SpaceState) string {
	if key := strings.TrimSpace(state.SpaceKey); key != "" {
		return key
	}
	return inferSpaceKeyFromDirName(dir)
}

type relinkSpaceFilesResult struct {
	Summary      sync.RelinkResult
	MutatedFiles []string
}

func relinkSpaceFiles(spaceDir string, index sync.GlobalPageIndex, targetPageIDs map[string]struct{}, dryRun bool) (relinkSpaceFilesResult, error) {
	result := relinkSpaceFilesResult{MutatedFiles: []string{}}
	filteredIndex := filteredRelinkIndex(index, targetPageIDs)
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		result.Summary.FilesSeen++
		if dryRun {
			changed, linksConverted, err := sync.ResolveLinksInFile(path, filteredIndex, true)
			if err != nil {
				return err
			}
			if changed {
				result.Summary.FilesChanged++
				result.Summary.LinksConverted += linksConverted
				result.MutatedFiles = append(result.MutatedFiles, path)
			}
			return nil
		}
		changed, linksConverted, err := sync.ResolveLinksInFile(path, filteredIndex, false)
		if err != nil {
			return err
		}
		if changed {
			result.Summary.FilesChanged++
			result.Summary.LinksConverted += linksConverted
			result.MutatedFiles = append(result.MutatedFiles, path)
		}
		return nil
	})
	return result, err
}

func filteredRelinkIndex(index sync.GlobalPageIndex, targetPageIDs map[string]struct{}) sync.GlobalPageIndex {
	if len(targetPageIDs) == 0 {
		return index
	}
	filtered := make(sync.GlobalPageIndex)
	for id, path := range index {
		if _, ok := targetPageIDs[id]; ok {
			filtered[id] = path
		}
	}
	return filtered
}

func normalizeRelinkPath(path string) string {
	normalized := filepath.Clean(strings.TrimSpace(path))
	if normalized == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil {
		normalized = resolved
	}
	return strings.ToLower(filepath.Clean(normalized))
}
