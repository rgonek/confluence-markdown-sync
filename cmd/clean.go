package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	"github.com/spf13/cobra"
)

func newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "clean",
		Aliases: []string{"repair"},
		Short:   "clean leftover sync workspace state",
		Long: `clean inspects and cleans leftover sync artifacts when pull/push was interrupted.

It can:
- switch away from sync/* branches,
- remove stale .confluence-worktrees directories,
- prune refs/confluence-sync/snapshots/* refs,
- and normalize readable state files.`,
		Args: cobra.NoArgs,
		RunE: runClean,
	}

	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve clean actions")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when confirmation is required")
	return cmd
}

func runClean(cmd *cobra.Command, _ []string) error {
	out := ensureSynchronizedCmdOutput(cmd)

	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("init git client: %w", err)
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		return err
	}
	currentBranch = strings.TrimSpace(currentBranch)

	targetBranch := ""
	if strings.HasPrefix(currentBranch, "sync/") {
		targetBranch, _ = resolveCleanTargetBranch(client)
	}

	worktreesRoot := filepath.Join(client.RootDir, ".confluence-worktrees")
	worktreeDirs, err := listCleanWorktreeDirs(worktreesRoot)
	if err != nil {
		return err
	}

	snapshotRefs, err := listCleanSnapshotRefs(client)
	if err != nil {
		return err
	}

	hasActions := (targetBranch != "") || len(worktreeDirs) > 0 || len(snapshotRefs) > 0

	_, _ = fmt.Fprintf(out, "Repository: %s\n", client.RootDir)
	if strings.HasPrefix(currentBranch, "sync/") {
		if targetBranch != "" {
			_, _ = fmt.Fprintf(out, "Branch: %s (will switch to %s)\n", currentBranch, targetBranch)
		} else {
			_, _ = fmt.Fprintf(out, "Branch: %s (no fallback branch detected)\n", currentBranch)
		}
	} else {
		_, _ = fmt.Fprintf(out, "Branch: %s\n", currentBranch)
	}
	_, _ = fmt.Fprintf(out, "Worktrees in .confluence-worktrees: %d\n", len(worktreeDirs))
	_, _ = fmt.Fprintf(out, "Snapshot refs: %d\n", len(snapshotRefs))

	if !hasActions {
		if err := normalizeCleanStates(out, client.RootDir); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "clean completed: workspace is already clean")
		return nil
	}

	if err := confirmCleanActions(cmd.InOrStdin(), out, currentBranch, len(worktreeDirs), len(snapshotRefs)); err != nil {
		return err
	}

	if targetBranch != "" {
		if _, err := client.Run("checkout", targetBranch); err != nil {
			return fmt.Errorf("checkout %s: %w", targetBranch, err)
		}
		_, _ = fmt.Fprintf(out, "Switched branch to %s\n", targetBranch)
	}

	removedWorktrees := 0
	for _, wtDir := range worktreeDirs {
		if err := client.RemoveWorktree(wtDir); err != nil {
			if rmErr := os.RemoveAll(wtDir); rmErr != nil {
				return fmt.Errorf("remove worktree %s: %w", wtDir, err)
			}
		}
		removedWorktrees++
	}
	if err := client.PruneWorktrees(); err != nil {
		_, _ = fmt.Fprintf(out, "warning: failed to prune worktrees: %v\n", err)
	}

	deletedRefs := 0
	for _, ref := range snapshotRefs {
		if delErr := client.DeleteRef(ref); delErr != nil {
			_, _ = fmt.Fprintf(out, "warning: failed to delete snapshot ref %s: %v\n", ref, delErr)
			continue
		}
		deletedRefs++
	}

	if err := normalizeCleanStates(out, client.RootDir); err != nil {
		return err
	}

	// Remove search index directory if present.
	searchIndexPath := filepath.Join(client.RootDir, ".confluence-search-index")
	if _, statErr := os.Stat(searchIndexPath); statErr == nil {
		if rmErr := os.RemoveAll(searchIndexPath); rmErr != nil {
			_, _ = fmt.Fprintf(out, "warning: failed to remove search index: %v\n", rmErr)
		} else {
			_, _ = fmt.Fprintln(out, "Removed .confluence-search-index/")
		}
	}

	_, _ = fmt.Fprintf(out, "clean completed: removed %d worktree(s), deleted %d snapshot ref(s)\n", removedWorktrees, deletedRefs)
	return nil
}

func confirmCleanActions(in io.Reader, out io.Writer, branch string, worktreeCount, refCount int) error {
	if flagYes {
		return nil
	}
	if flagNonInteractive {
		return fmt.Errorf("clean requires confirmation; rerun with --yes")
	}

	title := fmt.Sprintf("Apply clean actions for branch=%s, worktrees=%d, snapshot refs=%d?", branch, worktreeCount, refCount)
	if outputSupportsProgress(out) {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description("This may switch branches and delete stale sync metadata.").
					Value(&confirm),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("clean cancelled")
		}
		return nil
	}

	if _, err := fmt.Fprintf(out, "%s [y/N]: ", title); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}
	choice, err := readPromptLine(in)
	if err != nil {
		return err
	}
	choice = strings.ToLower(strings.TrimSpace(choice))
	if choice != "y" && choice != "yes" {
		return fmt.Errorf("clean cancelled")
	}
	return nil
}

func resolveCleanTargetBranch(client *git.Client) (string, error) {
	if branchExists(client, "main") {
		return "main", nil
	}
	if branchExists(client, "master") {
		return "master", nil
	}

	defaultRef, err := client.Run("symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", nil
	}
	defaultRef = strings.TrimSpace(defaultRef)
	parts := strings.Split(defaultRef, "/")
	if len(parts) == 0 {
		return "", nil
	}
	name := strings.TrimSpace(parts[len(parts)-1])
	if name == "" {
		return "", nil
	}
	if branchExists(client, name) {
		return name, nil
	}
	return "", nil
}

func branchExists(client *git.Client, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	_, err := client.Run("show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

func listCleanWorktreeDirs(worktreesRoot string) ([]string, error) {
	entries, err := os.ReadDir(worktreesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", worktreesRoot, err)
	}

	dirs := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(worktreesRoot, entry.Name()))
	}
	sort.Strings(dirs)
	return dirs, nil
}

func listCleanSnapshotRefs(client *git.Client) ([]string, error) {
	raw, err := client.Run("for-each-ref", "--format=%(refname)", "refs/confluence-sync/snapshots/")
	if err != nil {
		return nil, fmt.Errorf("list snapshot refs: %w", err)
	}
	refs := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		refs = append(refs, line)
	}
	sort.Strings(refs)
	return refs, nil
}

func normalizeCleanStates(out io.Writer, repoRoot string) error {
	states, err := fs.FindAllStateFiles(repoRoot)
	if err != nil {
		if fs.IsStateConflictError(err) {
			_, _ = fmt.Fprintf(out, "warning: state normalization skipped due to conflict markers: %v\n", err)
			return nil
		}
		return fmt.Errorf("scan state files: %w", err)
	}

	normalized := 0
	for spaceDir, state := range states {
		if saveErr := fs.SaveState(spaceDir, state); saveErr != nil {
			_, _ = fmt.Fprintf(out, "warning: failed to normalize %s: %v\n", fs.StatePath(spaceDir), saveErr)
			continue
		}
		normalized++
	}
	if normalized > 0 {
		_, _ = fmt.Fprintf(out, "Normalized %d state file(s)\n", normalized)
	}
	return nil
}
