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
- remove stale .confluence-worktrees directories,
- prune stale refs/confluence-sync/snapshots/* refs,
- delete stale sync/* recovery branches when safe,
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

	worktreesRoot := filepath.Join(client.RootDir, ".confluence-worktrees")
	worktreeDirs, err := listCleanWorktreeDirs(worktreesRoot)
	if err != nil {
		return err
	}

	snapshotRefs, err := listCleanSnapshotRefs(client)
	if err != nil {
		return err
	}

	worktreeBranches, err := listCleanWorktreeBranches(client)
	if err != nil {
		return err
	}

	syncBranches, err := listCleanSyncBranches(client)
	if err != nil {
		return err
	}

	syncPlan := buildCleanSyncPlan(currentBranch, worktreeDirs, snapshotRefs, syncBranches, worktreeBranches)
	hasActions := len(worktreeDirs) > 0 || len(syncPlan.DeleteSnapshotRefs) > 0 || len(syncPlan.DeleteBranches) > 0

	_, _ = fmt.Fprintf(out, "Repository: %s\n", client.RootDir)
	_, _ = fmt.Fprintf(out, "Branch: %s\n", currentBranch)
	_, _ = fmt.Fprintf(out, "Worktrees in .confluence-worktrees: %d\n", len(worktreeDirs))
	_, _ = fmt.Fprintf(out, "Snapshot refs eligible for cleanup: %d\n", len(syncPlan.DeleteSnapshotRefs))
	_, _ = fmt.Fprintf(out, "Sync branches eligible for cleanup: %d\n", len(syncPlan.DeleteBranches))
	if len(syncPlan.RetainedSnapshotRefs) > 0 {
		_, _ = fmt.Fprintf(out, "Snapshot refs retained for active recovery: %d\n", len(syncPlan.RetainedSnapshotRefs))
	}
	if len(syncPlan.SkippedBranches) > 0 {
		_, _ = fmt.Fprintf(out, "Sync branches retained: %d\n", len(syncPlan.SkippedBranches))
	}

	if !hasActions {
		if err := normalizeCleanStates(out, client.RootDir); err != nil {
			return err
		}
		reportSkippedCleanSyncBranches(out, syncPlan.SkippedBranches)
		if len(syncPlan.SkippedBranches) == 0 {
			_, _ = fmt.Fprintln(out, "clean completed: workspace is already clean (removed 0 worktree(s), deleted 0 snapshot ref(s), deleted 0 sync branch(es), skipped 0 sync branch(es))")
		} else {
			_, _ = fmt.Fprintf(out, "clean completed: removed 0 worktree(s), deleted 0 snapshot ref(s), deleted 0 sync branch(es), skipped %d sync branch(es)\n", len(syncPlan.SkippedBranches))
		}
		return nil
	}

	if err := confirmCleanActions(cmd.InOrStdin(), out, currentBranch, len(worktreeDirs), len(syncPlan.DeleteSnapshotRefs), len(syncPlan.DeleteBranches)); err != nil {
		return err
	}

	removedWorktrees := 0
	for _, wtDir := range worktreeDirs {
		if err := client.RemoveWorktree(wtDir); err != nil {
			if rmErr := os.RemoveAll(wtDir); rmErr != nil {
				return fmt.Errorf("remove worktree %s: %w", wtDir, err)
			}
		}
		removedWorktrees++
		_, _ = fmt.Fprintf(out, "Removed worktree: %s\n", wtDir)
	}
	if err := client.PruneWorktrees(); err != nil {
		_, _ = fmt.Fprintf(out, "warning: failed to prune worktrees: %v\n", err)
	}

	deletedRefs := 0
	deletedSnapshotRefs := make(map[string]struct{}, len(syncPlan.DeleteSnapshotRefs))
	for _, ref := range syncPlan.DeleteSnapshotRefs {
		if delErr := client.DeleteRef(ref); delErr != nil {
			_, _ = fmt.Fprintf(out, "warning: failed to delete snapshot ref %s: %v\n", ref, delErr)
			continue
		}
		deletedRefs++
		deletedSnapshotRefs[ref] = struct{}{}
		_, _ = fmt.Fprintf(out, "Deleted snapshot ref: %s\n", ref)
	}

	deletedBranches := 0
	skippedBranches := append([]cleanSkippedSyncBranch(nil), syncPlan.SkippedBranches...)
	for _, plan := range syncPlan.DeleteBranches {
		if plan.RequiresSnapshotDeletion {
			if _, ok := deletedSnapshotRefs[plan.SnapshotRef]; !ok {
				skippedBranches = append(skippedBranches, cleanSkippedSyncBranch{
					Name:   plan.Name,
					Reason: fmt.Sprintf("retained because snapshot ref %s could not be removed", plan.SnapshotRef),
				})
				continue
			}
		}
		if err := client.DeleteBranch(plan.Name); err != nil {
			_, _ = fmt.Fprintf(out, "warning: failed to delete sync branch %s: %v\n", plan.Name, err)
			skippedBranches = append(skippedBranches, cleanSkippedSyncBranch{
				Name:   plan.Name,
				Reason: err.Error(),
			})
			continue
		}
		deletedBranches++
		_, _ = fmt.Fprintf(out, "Deleted sync branch: %s\n", plan.Name)
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

	reportSkippedCleanSyncBranches(out, skippedBranches)
	_, _ = fmt.Fprintf(out, "clean completed: removed %d worktree(s), deleted %d snapshot ref(s), deleted %d sync branch(es), skipped %d sync branch(es)\n", removedWorktrees, deletedRefs, deletedBranches, len(skippedBranches))
	return nil
}

func confirmCleanActions(in io.Reader, out io.Writer, branch string, worktreeCount, refCount, syncBranchCount int) error {
	if flagYes {
		return nil
	}
	if flagNonInteractive {
		return fmt.Errorf("clean requires confirmation; rerun with --yes")
	}

	title := fmt.Sprintf("Apply clean actions for branch=%s, worktrees=%d, snapshot refs=%d, sync branches=%d?", branch, worktreeCount, refCount, syncBranchCount)
	if outputSupportsProgress(out) {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description("This removes stale sync metadata while retaining active recovery branches.").
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

func listCleanWorktreeBranches(client *git.Client) (map[string][]string, error) {
	raw, err := client.Run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}

	result := make(map[string][]string)
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var worktreePath string
	var branchRef string
	flush := func() {
		if worktreePath == "" || !strings.HasPrefix(branchRef, "refs/heads/") {
			worktreePath = ""
			branchRef = ""
			return
		}
		branch := strings.TrimPrefix(branchRef, "refs/heads/")
		result[branch] = append(result[branch], cleanPathForComparison(worktreePath))
		worktreePath = ""
		branchRef = ""
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			worktreePath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			branchRef = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		}
	}
	flush()

	for branch := range result {
		sort.Strings(result[branch])
	}
	return result, nil
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

func listCleanSyncBranches(client *git.Client) ([]string, error) {
	raw, err := client.Run("for-each-ref", "--format=%(refname:short)", "refs/heads/sync/")
	if err != nil {
		return nil, fmt.Errorf("list sync branches: %w", err)
	}

	branches := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		branches = append(branches, line)
	}
	sort.Strings(branches)
	return branches, nil
}

type cleanSyncBranchPlan struct {
	Name                     string
	SnapshotRef              string
	RequiresSnapshotDeletion bool
}

type cleanSkippedSyncBranch struct {
	Name   string
	Reason string
}

type cleanSyncPlan struct {
	DeleteSnapshotRefs   []string
	RetainedSnapshotRefs []string
	DeleteBranches       []cleanSyncBranchPlan
	SkippedBranches      []cleanSkippedSyncBranch
}

func buildCleanSyncPlan(currentBranch string, removableWorktrees, snapshotRefs, syncBranches []string, worktreeBranches map[string][]string) cleanSyncPlan {
	removableSet := make(map[string]struct{}, len(removableWorktrees))
	for _, wtDir := range removableWorktrees {
		removableSet[cleanPathForComparison(wtDir)] = struct{}{}
	}

	snapshotSet := make(map[string]struct{}, len(snapshotRefs))
	for _, ref := range snapshotRefs {
		snapshotSet[ref] = struct{}{}
	}

	protectedSnapshotRefs := make(map[string]struct{})
	deleteBranches := make([]cleanSyncBranchPlan, 0, len(syncBranches))
	skippedBranches := make([]cleanSkippedSyncBranch, 0)

	for _, branch := range syncBranches {
		snapshotRef, ok := managedSnapshotRefForSyncBranch(branch)
		if branch == currentBranch {
			skippedBranches = append(skippedBranches, cleanSkippedSyncBranch{
				Name:   branch,
				Reason: "current HEAD is on this sync branch",
			})
			if ok {
				if _, exists := snapshotSet[snapshotRef]; exists {
					protectedSnapshotRefs[snapshotRef] = struct{}{}
				}
			}
			continue
		}
		if !ok {
			skippedBranches = append(skippedBranches, cleanSkippedSyncBranch{
				Name:   branch,
				Reason: "branch does not match managed sync/<SpaceKey>/<UTC timestamp> format",
			})
			continue
		}
		if reason := cleanSyncBranchWorktreeBlockReason(worktreeBranches[branch], removableSet); reason != "" {
			skippedBranches = append(skippedBranches, cleanSkippedSyncBranch{
				Name:   branch,
				Reason: reason,
			})
			if _, exists := snapshotSet[snapshotRef]; exists {
				protectedSnapshotRefs[snapshotRef] = struct{}{}
			}
			continue
		}
		_, requiresSnapshotDeletion := snapshotSet[snapshotRef]
		deleteBranches = append(deleteBranches, cleanSyncBranchPlan{
			Name:                     branch,
			SnapshotRef:              snapshotRef,
			RequiresSnapshotDeletion: requiresSnapshotDeletion,
		})
	}

	deleteSnapshotRefs := make([]string, 0, len(snapshotRefs))
	retainedSnapshotRefs := make([]string, 0)
	for _, ref := range snapshotRefs {
		if _, keep := protectedSnapshotRefs[ref]; keep {
			retainedSnapshotRefs = append(retainedSnapshotRefs, ref)
			continue
		}
		deleteSnapshotRefs = append(deleteSnapshotRefs, ref)
	}

	return cleanSyncPlan{
		DeleteSnapshotRefs:   deleteSnapshotRefs,
		RetainedSnapshotRefs: retainedSnapshotRefs,
		DeleteBranches:       deleteBranches,
		SkippedBranches:      skippedBranches,
	}
}

func managedSnapshotRefForSyncBranch(branch string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(branch), "/")
	if len(parts) != 3 || parts[0] != "sync" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return fmt.Sprintf("refs/confluence-sync/snapshots/%s/%s", parts[1], parts[2]), true
}

func cleanSyncBranchWorktreeBlockReason(paths []string, removableSet map[string]struct{}) string {
	for _, path := range paths {
		normalized := cleanPathForComparison(path)
		if normalized == "" {
			continue
		}
		if _, removable := removableSet[normalized]; removable {
			continue
		}
		if _, err := os.Stat(normalized); err == nil {
			return fmt.Sprintf("linked worktree remains at %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Sprintf("linked worktree status unknown at %s: %v", path, err)
		}
	}
	return ""
}

func cleanPathForComparison(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	return filepath.Clean(abs)
}

func reportSkippedCleanSyncBranches(out io.Writer, skipped []cleanSkippedSyncBranch) {
	if len(skipped) == 0 {
		return
	}
	for _, branch := range skipped {
		_, _ = fmt.Fprintf(out, "Retained sync branch %s: %s\n", branch.Name, branch.Reason)
	}
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
