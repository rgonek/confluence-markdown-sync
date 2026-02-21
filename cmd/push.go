package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

// conflict policy values for --on-conflict.
const (
	OnConflictPullMerge = "pull-merge"
	OnConflictForce     = "force"
	OnConflictCancel    = "cancel"
)

var newPushRemote = func(cfg *config.Config) (syncflow.PushRemote, error) {
	return confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
		Verbose:  flagVerbose,
	})
}

func newPushCmd() *cobra.Command {
	var onConflict string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "push [TARGET]",
		Short: "Push local Markdown changes to Confluence",
		Long: `Push converts local Markdown files to ADF and updates Confluence pages.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.

For space-wide pushes, the conflict policy defaults to "pull-merge" if not specified.
For single-file pushes, a policy must be specified via --on-conflict or chosen via prompt.

push always runs validate before any remote write.
It uses an isolated worktree and a temporary branch to ensure safety.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runPush(cmd, config.ParseTarget(raw), onConflict, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate the push without modifying Confluence or local Git state")
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

func runPush(cmd *cobra.Command, target config.Target, onConflict string, dryRun bool) (runErr error) {
	ctx := context.Background()
	out := cmd.OutOrStdout()

	resolvedPolicy, err := resolvePushConflictPolicy(cmd.InOrStdin(), out, onConflict, target.IsSpace())
	if err != nil {
		return err
	}
	onConflict = resolvedPolicy

	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}
	spaceDir := initialCtx.spaceDir
	spaceKey := initialCtx.spaceKey

	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !initialCtx.fixedDir {
		remote, err := newPushRemote(cfg)
		if err == nil {
			space, err := remote.GetSpace(ctx, spaceKey)
			if err == nil {
				spaceDir = filepath.Join(filepath.Dir(spaceDir), fs.SanitizeSpaceDirName(space.Name, space.Key))
			}
		}
	}

	gitClient, err := git.NewClient()
	if err != nil {
		return err
	}

	spaceScopePath, err := gitClient.ScopePath(spaceDir)
	if err != nil {
		return err
	}

	targetFiles := []string{}
	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		targetFiles = append(targetFiles, abs)
	}
	changeScopePath, err := resolvePushScopePath(gitClient, spaceDir, target, validateTargetContext{spaceDir: spaceDir, files: targetFiles})
	if err != nil {
		return err
	}

	ts := nowUTC()
	tsStr := ts.Format("20060102T150405Z")

	if dryRun {
		fmt.Fprintln(out, "[DRY-RUN] Simulating push (no git or confluence state will be modified)")

		baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
		if err != nil {
			return err
		}

		changes, err := gitClient.DiffNameStatus(baselineRef, "", spaceScopePath)
		if err != nil {
			return fmt.Errorf("diff failed: %w", err)
		}

		untracked, err := gitClient.Run("ls-files", "--others", "--exclude-standard", "--", spaceScopePath)
		if err == nil {
			for _, line := range strings.Split(strings.ReplaceAll(untracked, "\r\n", "\n"), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					changes = append(changes, git.FileStatus{Code: "A", Path: filepath.ToSlash(line)})
				}
			}
		}

		syncChanges, err := toSyncPushChanges(changes, gitClient.RootDir, spaceDir)
		if err != nil {
			return err
		}

		if target.IsFile() {
			changes, err = gitClient.DiffNameStatus(baselineRef, "", changeScopePath)
			if err != nil {
				return err
			}
			untracked, err = gitClient.Run("ls-files", "--others", "--exclude-standard", "--", changeScopePath)
			if err == nil {
				for _, line := range strings.Split(strings.ReplaceAll(untracked, "\r\n", "\n"), "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						changes = append(changes, git.FileStatus{Code: "A", Path: filepath.ToSlash(line)})
					}
				}
			}
			syncChanges, err = toSyncPushChanges(changes, gitClient.RootDir, spaceDir)
			if err != nil {
				return err
			}
		}

		if len(syncChanges) == 0 {
			fmt.Fprintln(out, "push completed with no in-scope markdown changes (no-op)")
			return nil
		}

		// validate
		var currentTarget config.Target
		if target.IsFile() {
			abs, _ := filepath.Abs(target.Value)
			currentTarget = config.Target{Mode: config.TargetModeFile, Value: abs}
		} else {
			currentTarget = config.Target{Mode: config.TargetModeSpace, Value: spaceDir}
		}
		if err := runValidateTarget(out, currentTarget); err != nil {
			return fmt.Errorf("pre-push validate failed: %w", err)
		}

		envPath = findEnvPath(spaceDir)
		cfg, err = config.Load(envPath)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		realRemote, err := newPushRemote(cfg)
		if err != nil {
			return fmt.Errorf("create confluence client: %w", err)
		}

		remote := &dryRunPushRemote{inner: realRemote, out: out, domain: cfg.Domain}

		state, err := fs.LoadState(spaceDir)
		if err != nil {
			return fmt.Errorf("load state: %w", err)
		}

		var progress syncflow.Progress
		if !flagVerbose {
			progress = newConsoleProgress(out, "[DRY-RUN] Syncing to Confluence")
		}

		result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
			SpaceKey:       spaceKey,
			SpaceDir:       spaceDir,
			Domain:         cfg.Domain,
			State:          state,
			Changes:        syncChanges,
			ConflictPolicy: toSyncConflictPolicy(onConflict),
			Progress:       progress,
		})
		if err != nil {
			var conflictErr *syncflow.PushConflictError
			if errors.As(err, &conflictErr) {
				return formatPushConflictError(conflictErr)
			}
			return err
		}

		fmt.Fprintf(out, "\n[DRY-RUN] push completed: %d page change(s) would be synced\n", len(result.Commits))
		return nil
	}

	// 1. Capture Snapshot
	stashRef, err := gitClient.StashScopeIfDirty(spaceScopePath, spaceKey, ts)
	if err != nil {
		return fmt.Errorf("stash failed: %w", err)
	}
	// Note: We intentionally DO NOT defer drop/restore here immediately,
	// because restoration logic depends on success/failure of the flow.

	snapshotRef := stashRef
	if snapshotRef == "" {
		snapshotRef = "HEAD"
	}

	snapshotCommit, err := gitClient.ResolveRef(snapshotRef)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("resolve snapshot ref: %w", err)
	}

	// Sanitize key for git refs (no spaces allowed)
	// We MUST use the actual SpaceKey for refs, not sanitized space name
	refKey := fs.SanitizePathSegment(spaceKey)

	snapshotName := fmt.Sprintf("refs/confluence-sync/snapshots/%s/%s", refKey, tsStr)
	if err := gitClient.UpdateRef(snapshotName, snapshotCommit, "create snapshot"); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("create snapshot ref: %w", err)
	}

	// Keep snapshot ref only on failure, delete on success
	defer func() {
		if runErr == nil {
			_ = gitClient.DeleteRef(snapshotName)
		} else {
			fmt.Fprintf(out, "\nSnapshot retained for recovery: %s\n", snapshotName)
		}
	}()

	// 2. Create Sync Branch
	syncBranchName := fmt.Sprintf("sync/%s/%s", refKey, tsStr)
	if err := gitClient.CreateBranch(syncBranchName, snapshotCommit); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("create sync branch: %w", err)
	}

	// Keep sync branch only on failure, delete on success
	defer func() {
		if runErr == nil {
			_ = gitClient.DeleteBranch(syncBranchName)
		} else {
			fmt.Fprintf(out, "Sync branch retained for recovery: %s\n", syncBranchName)
		}
	}()

	// 3. Create Worktree
	worktreeDir := filepath.Join(gitClient.RootDir, ".confluence-worktrees", fmt.Sprintf("%s-%s", refKey, tsStr))
	if err := gitClient.AddWorktree(worktreeDir, syncBranchName); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("create worktree: %w", err)
	}
	defer func() {
		_ = gitClient.RemoveWorktree(worktreeDir)
	}()

	// Resolve HEAD from main repo to reset SyncBranch
	currentHead, err := gitClient.ResolveRef("HEAD")
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("resolve HEAD: %w", err)
	}

	// 4. Validate (in worktree)
	wtSpaceDir := filepath.Join(worktreeDir, spaceScopePath)
	wtClient := &git.Client{RootDir: worktreeDir}

	// Reset SyncBranch to HEAD (mixed) to ensure commits are granular and based on HEAD
	if _, err := wtClient.Run("reset", "--mixed", currentHead); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("reset worktree: %w", err)
	}

	var wtTarget config.Target

	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		relFile, _ := filepath.Rel(spaceDir, abs) // Assumes single file
		wtFile := filepath.Join(wtSpaceDir, relFile)
		wtTarget = config.Target{Mode: config.TargetModeFile, Value: wtFile}
	} else {
		wtTarget = config.Target{Mode: config.TargetModeSpace, Value: wtSpaceDir}
	}

	if err := runValidateTarget(out, wtTarget); err != nil {

		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("pre-push validate failed: %w", err)
	}

	// 5. Diff (Snapshot vs Baseline)
	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return err
	}

	wtClient = &git.Client{RootDir: worktreeDir}
	changes, err := wtClient.DiffNameStatus(baselineRef, snapshotCommit, spaceScopePath)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("diff failed: %w", err)
	}

	syncChanges, err := toSyncPushChanges(changes, gitClient.RootDir, spaceDir)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return err
	}

	if target.IsFile() {
		// Filter syncChanges for the specific file
		// Or assume re-running diff with scoped path is better
		// changeScopePath is absolute path in repo? No, gitClient.ScopePath returns relative to root.
		// DiffNameStatus expects relative to root.
		changes, err = wtClient.DiffNameStatus(baselineRef, "HEAD", changeScopePath)
		if err != nil {
			if stashRef != "" {
				_ = gitClient.StashPop(stashRef)
			}
			return err
		}
		syncChanges, err = toSyncPushChanges(changes, gitClient.RootDir, spaceDir)
		if err != nil {
			if stashRef != "" {
				_ = gitClient.StashPop(stashRef)
			}
			return err
		}
	}

	if len(syncChanges) == 0 {
		fmt.Fprintln(out, "push completed with no in-scope markdown changes (no-op)")
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return nil
	}

	if err := requireSafetyConfirmation(cmd.InOrStdin(), out, "push", len(syncChanges), pushHasDeleteChange(syncChanges)); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return err
	}

	// 6. Push (in worktree)
	envPath = findEnvPath(wtSpaceDir) // Load config from worktree
	cfg, err = config.Load(envPath)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPushRemote(cfg)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("create confluence client: %w", err)
	}

	state, err := fs.LoadState(wtSpaceDir)
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("load state: %w", err)
	}

	var progress syncflow.Progress
	if !flagVerbose {
		progress = newConsoleProgress(out, "Syncing to Confluence")
	}

	result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
		SpaceKey:       spaceKey,
		SpaceDir:       wtSpaceDir, // Use worktree dir!
		Domain:         cfg.Domain,
		State:          state,
		Changes:        syncChanges,
		ConflictPolicy: toSyncConflictPolicy(onConflict),
		Progress:       progress,
	})
	if err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		var conflictErr *syncflow.PushConflictError
		if errors.As(err, &conflictErr) {
			return formatPushConflictError(conflictErr)
		}
		return err
	}

	if len(result.Commits) == 0 {
		fmt.Fprintln(out, "push completed with no pushable markdown changes (no-op)")
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return nil
	}

	// 7. Commit in Worktree
	if err := fs.SaveState(wtSpaceDir, result.State); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("save state: %w", err)
	}

	for i, commitPlan := range result.Commits {
		filesToAdd := make([]string, 0, len(commitPlan.StagedPaths)+1)
		for _, relPath := range commitPlan.StagedPaths {
			filesToAdd = append(filesToAdd, filepath.Join(wtSpaceDir, relPath))
		}
		if i == len(result.Commits)-1 {
			filesToAdd = append(filesToAdd, filepath.Join(wtSpaceDir, fs.StateFileName))
		}

		repoPaths := make([]string, 0, len(filesToAdd))
		for _, absPath := range filesToAdd {
			rel, _ := filepath.Rel(worktreeDir, absPath)
			repoPaths = append(repoPaths, rel)
		}

		if err := wtClient.AddForce(repoPaths...); err != nil {
			if stashRef != "" {
				_ = gitClient.StashPop(stashRef)
			}
			return fmt.Errorf("git add failed: %w", err)
		}

		subject := fmt.Sprintf("Sync %q to Confluence (v%d)", commitPlan.PageTitle, commitPlan.Version)
		body := fmt.Sprintf(
			"Page ID: %s\nURL: %s\n\nConfluence-Page-ID: %s\nConfluence-Version: %d\nConfluence-Space-Key: %s\nConfluence-URL: %s",
			commitPlan.PageID,
			commitPlan.URL,
			commitPlan.PageID,
			commitPlan.Version,
			commitPlan.SpaceKey,
			commitPlan.URL,
		)
		if err := wtClient.Commit(subject, body); err != nil {
			if stashRef != "" {
				_ = gitClient.StashPop(stashRef)
			}
			return fmt.Errorf("git commit failed: %w", err)
		}

		fmt.Fprintf(out, "pushed %s (page %s, v%d)\n", commitPlan.Path, commitPlan.PageID, commitPlan.Version)
	}

	// 8. Rebase Sync Branch onto HEAD (main repo)
	if err := gitClient.RemoveWorktree(worktreeDir); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("remove worktree: %w", err)
	}

	// 9. Merge
	if err := gitClient.Merge(syncBranchName, ""); err != nil {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
		return fmt.Errorf("merge sync branch: %w", err)
	}

	// 10. Tag
	tagName := fmt.Sprintf("confluence-sync/push/%s/%s", refKey, tsStr)
	tagMsg := fmt.Sprintf("Confluence push sync for %s at %s", spaceKey, tsStr)
	if err := gitClient.Tag(tagName, tagMsg); err != nil {
		fmt.Fprintf(out, "warning: failed to create tag: %v\n", err)
	}

	// 11. Restore Stash
	if stashRef != "" {
		if err := gitClient.StashPop(stashRef); err != nil {
			fmt.Fprintf(out, "warning: stash restore had conflicts: %v\n", err)
		}
	}

	fmt.Fprintf(out, "push completed: %d page change(s) synced\n", len(result.Commits))
	return nil
}

func resolvePushScopePath(client *git.Client, spaceDir string, target config.Target, targetCtx validateTargetContext) (string, error) {
	if target.IsFile() {
		if len(targetCtx.files) != 1 {
			return "", fmt.Errorf("expected one file target, got %d", len(targetCtx.files))
		}
		return client.ScopePath(targetCtx.files[0])
	}
	return client.ScopePath(spaceDir)
}

func gitPushBaselineRef(client *git.Client, spaceKey string) (string, error) {
	spaceKey = strings.TrimSpace(spaceKey)
	if spaceKey == "" {
		return "", fmt.Errorf("space key is required")
	}

	refKey := fs.SanitizePathSegment(spaceKey)
	tagsRaw, err := client.Run(
		"tag",
		"--list",
		fmt.Sprintf("confluence-sync/pull/%s/*", refKey),
		fmt.Sprintf("confluence-sync/push/%s/*", refKey),
	)
	if err != nil {
		return "", err
	}

	bestTag := ""
	bestStamp := ""
	for _, line := range strings.Split(strings.ReplaceAll(tagsRaw, "\r\n", "\n"), "\n") {
		tag := strings.TrimSpace(line)
		if tag == "" {
			continue
		}
		parts := strings.Split(tag, "/")
		if len(parts) < 4 {
			continue
		}
		timestamp := parts[len(parts)-1]
		if timestamp > bestStamp {
			bestStamp = timestamp
			bestTag = tag
		}
	}
	if bestTag != "" {
		return bestTag, nil
	}

	rootCommitRaw, err := client.Run("rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", err
	}
	lines := strings.Fields(rootCommitRaw)
	if len(lines) == 0 {
		return "", fmt.Errorf("unable to determine baseline commit")
	}
	return lines[0], nil
}

func toSyncPushChanges(changes []git.FileStatus, repoRoot, spaceDir string) ([]syncflow.PushFileChange, error) {
	normalizedSpaceDir, err := normalizeCommandPath(spaceDir)
	if err != nil {
		return nil, err
	}

	out := make([]syncflow.PushFileChange, 0, len(changes))
	for _, change := range changes {
		absPath := filepath.Join(repoRoot, filepath.FromSlash(change.Path))
		normalizedAbsPath, err := normalizeCommandPath(absPath)
		if err != nil {
			return nil, err
		}

		relPath, err := filepath.Rel(normalizedSpaceDir, normalizedAbsPath)
		if err != nil {
			return nil, err
		}

		relPath = filepath.Clean(relPath)
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			lowerSpace := strings.ToLower(filepath.ToSlash(normalizedSpaceDir))
			lowerAbs := strings.ToLower(filepath.ToSlash(normalizedAbsPath))
			if lowerAbs != lowerSpace && !strings.HasPrefix(lowerAbs, lowerSpace+"/") {
				continue
			}
			relPath = strings.TrimPrefix(lowerAbs, lowerSpace)
			relPath = strings.TrimPrefix(relPath, "/")
		}

		relPath = filepath.ToSlash(relPath)
		if relPath == "." || strings.HasPrefix(relPath, "../") {
			continue
		}

		var changeType syncflow.PushChangeType
		switch change.Code {
		case "A":
			changeType = syncflow.PushChangeAdd
		case "M", "T":
			changeType = syncflow.PushChangeModify
		case "D":
			changeType = syncflow.PushChangeDelete
		default:
			continue
		}

		out = append(out, syncflow.PushFileChange{Type: changeType, Path: relPath})
	}
	return out, nil
}

func normalizeCommandPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		absPath = resolvedPath
	}
	return filepath.Clean(absPath), nil
}

func toSyncConflictPolicy(policy string) syncflow.PushConflictPolicy {
	switch policy {
	case OnConflictPullMerge:
		return syncflow.PushConflictPolicyPullMerge
	case OnConflictForce:
		return syncflow.PushConflictPolicyForce
	case OnConflictCancel:
		return syncflow.PushConflictPolicyCancel
	default:
		return syncflow.PushConflictPolicyCancel
	}
}

func pushHasDeleteChange(changes []syncflow.PushFileChange) bool {
	for _, change := range changes {
		if change.Type == syncflow.PushChangeDelete {
			return true
		}
	}
	return false
}

func formatPushConflictError(conflictErr *syncflow.PushConflictError) error {
	switch conflictErr.Policy {
	case syncflow.PushConflictPolicyPullMerge:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): pull-merge policy selected; run cms pull and merge local changes before retrying push",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	case syncflow.PushConflictPolicyForce:
		return conflictErr
	default:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): rerun with --on-conflict=force to overwrite or --on-conflict=pull-merge to reconcile",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	}
}
