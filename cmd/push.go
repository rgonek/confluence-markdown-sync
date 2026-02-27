package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	return newConfluenceClientFromConfig(cfg)
}

var runPullForPush = runPull

var flagPushPreflight bool
var flagPushKeepOrphanAssets bool
var flagArchiveTaskTimeout = confluence.DefaultArchiveTaskTimeout
var flagArchiveTaskPollInterval = confluence.DefaultArchiveTaskPollInterval

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
	cmd.Flags().BoolVar(&flagPushPreflight, "preflight", false, "Show a concise push plan (changes and validation) without remote writes")
	cmd.Flags().BoolVar(&flagPushKeepOrphanAssets, "keep-orphan-assets", false, "Keep unreferenced attachments instead of deleting them during push")
	cmd.Flags().DurationVar(&flagArchiveTaskTimeout, "archive-task-timeout", confluence.DefaultArchiveTaskTimeout, "Max time to wait for Confluence archive long-task completion")
	cmd.Flags().DurationVar(&flagArchiveTaskPollInterval, "archive-task-poll-interval", confluence.DefaultArchiveTaskPollInterval, "Polling interval while waiting for archive long-task completion")
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
	ctx := getCommandContext(cmd)
	out := ensureSynchronizedCmdOutput(cmd)
	_, restoreLogger := beginCommandRun("push")
	defer restoreLogger()
	if err := ensureWorkspaceSyncReady("push"); err != nil {
		return err
	}

	preflight := flagPushPreflight
	startedAt := time.Now()
	telemetrySpaceKey := "unknown"
	telemetryConflictPolicy := ""
	slog.Info("push_started", "target_mode", target.Mode, "target", target.Value, "dry_run", dryRun, "preflight", preflight)
	defer func() {
		duration := time.Since(startedAt)
		if runErr != nil {
			slog.Warn("push_finished",
				"space_key", telemetrySpaceKey,
				"conflict_policy", telemetryConflictPolicy,
				"dry_run", dryRun,
				"preflight", preflight,
				"duration_ms", duration.Milliseconds(),
				"error", runErr.Error(),
			)
			return
		}
		slog.Info("push_finished",
			"space_key", telemetrySpaceKey,
			"conflict_policy", telemetryConflictPolicy,
			"dry_run", dryRun,
			"preflight", preflight,
			"duration_ms", duration.Milliseconds(),
		)
	}()

	if preflight && dryRun {
		return errors.New("--preflight and --dry-run cannot be used together")
	}
	if !preflight {
		resolvedPolicy, err := resolvePushConflictPolicy(cmd.InOrStdin(), out, onConflict, target.IsSpace())
		if err != nil {
			return err
		}
		onConflict = resolvedPolicy
		telemetryConflictPolicy = resolvedPolicy
	}

	initialCtx, err := resolveInitialPushContext(target)
	if err != nil {
		return err
	}
	spaceDir := initialCtx.spaceDir
	spaceKey := initialCtx.spaceKey
	telemetrySpaceKey = strings.TrimSpace(spaceKey)

	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if !initialCtx.fixedDir {
		remote, err := newPushRemote(cfg)
		if err == nil {
			defer closeRemoteIfPossible(remote)
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

	spaceScopePath, err := gitScopePathFromPath(spaceDir)
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

	if preflight {
		return runPushPreflight(ctx, out, target, spaceKey, spaceDir, gitClient, spaceScopePath, changeScopePath)
	}

	ts := nowUTC()
	tsStr := ts.Format("20060102T150405Z")

	if dryRun {
		return runPushDryRun(ctx, cmd, out, target, spaceKey, spaceDir, onConflict, gitClient, spaceScopePath, changeScopePath)
	}

	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return err
	}

	preSnapshotChanges, err := collectPushChangesForTarget(gitClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return err
	}

	if len(preSnapshotChanges) == 0 {
		_, _ = fmt.Fprintln(out, "push completed with no in-scope markdown changes (no-op)")
		return nil
	}

	headCommit, err := gitClient.ResolveRef("HEAD")
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}

	// 1. Capture Snapshot
	stashRef, err := gitClient.StashScopeIfDirty(spaceScopePath, spaceKey, ts)
	if err != nil {
		return translateWorkspaceGitError(fmt.Errorf("stash failed: %w", err), "push")
	}
	defer func() {
		if stashRef != "" {
			_ = gitClient.StashPop(stashRef)
		}
	}()

	snapshotRef := stashRef
	if snapshotRef == "" {
		snapshotRef = headCommit
	}

	snapshotCommit, err := gitClient.ResolveRef(snapshotRef)
	if err != nil {
		return fmt.Errorf("resolve snapshot ref: %w", err)
	}

	// Sanitize key for git refs (no spaces allowed)
	// We MUST use the actual SpaceKey for refs, not sanitized space name
	refKey := fs.SanitizePathSegment(spaceKey)

	snapshotName := fmt.Sprintf("refs/confluence-sync/snapshots/%s/%s", refKey, tsStr)
	if err := gitClient.UpdateRef(snapshotName, snapshotCommit, "create snapshot"); err != nil {
		return fmt.Errorf("create snapshot ref: %w", err)
	}

	// Keep snapshot ref only on failure, delete on success
	defer func() {
		if runErr == nil {
			_ = gitClient.DeleteRef(snapshotName)
		} else {
			_, _ = fmt.Fprintf(out, "\nSnapshot retained for recovery: %s\n", snapshotName)
		}
	}()

	// 2. Create Sync Branch
	syncBranchName := fmt.Sprintf("sync/%s/%s", refKey, tsStr)
	if err := gitClient.CreateBranch(syncBranchName, headCommit); err != nil {
		return fmt.Errorf("create sync branch: %w", err)
	}

	// Keep sync branch only on failure, delete on success
	defer func() {
		if runErr == nil {
			_ = gitClient.DeleteBranch(syncBranchName)
		} else {
			_, _ = fmt.Fprintf(out, "Sync branch retained for recovery: %s\n", syncBranchName)
		}
	}()

	// 3. Create Worktree
	worktreeDir := filepath.Join(gitClient.RootDir, ".confluence-worktrees", fmt.Sprintf("%s-%s", refKey, tsStr))
	if err := gitClient.AddWorktree(worktreeDir, syncBranchName); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}
	defer func() {
		_ = gitClient.RemoveWorktree(worktreeDir)
	}()

	return runPushInWorktree(ctx, cmd, out, target, spaceKey, spaceDir, onConflict, tsStr,
		gitClient, spaceScopePath, changeScopePath, worktreeDir, syncBranchName, snapshotName, &stashRef)
}

func runPushPreflight(
	ctx context.Context,
	out io.Writer,
	target config.Target,
	spaceKey, spaceDir string,
	gitClient *git.Client,
	spaceScopePath, changeScopePath string,
) error {
	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return err
	}
	syncChanges, err := collectPushChangesForTarget(gitClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "preflight for space %s\n", spaceKey)
	if len(syncChanges) == 0 {
		_, _ = fmt.Fprintln(out, "no in-scope markdown changes")
		return nil
	}

	var currentTarget config.Target
	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		currentTarget = config.Target{Mode: config.TargetModeFile, Value: abs}
	} else {
		currentTarget = config.Target{Mode: config.TargetModeSpace, Value: spaceDir}
	}
	if err := runValidateTargetWithContext(ctx, out, currentTarget); err != nil {
		return fmt.Errorf("preflight validate failed: %w", err)
	}

	addCount, modifyCount, deleteCount := summarizePushChanges(syncChanges)
	_, _ = fmt.Fprintf(out, "changes: %d (A:%d M:%d D:%d)\n", len(syncChanges), addCount, modifyCount, deleteCount)
	for _, change := range syncChanges {
		_, _ = fmt.Fprintf(out, "  %s %s\n", change.Type, change.Path)
	}
	if len(syncChanges) > 10 || deleteCount > 0 {
		_, _ = fmt.Fprintln(out, "safety confirmation would be required")
	}
	return nil
}

func runPushDryRun(
	ctx context.Context,
	cmd *cobra.Command,
	out io.Writer,
	target config.Target,
	spaceKey, spaceDir, onConflict string,
	gitClient *git.Client,
	spaceScopePath, changeScopePath string,
) error {
	_, _ = fmt.Fprintln(out, "[DRY-RUN] Simulating push (no git or confluence state will be modified)")

	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return err
	}

	syncChanges, err := collectPushChangesForTarget(gitClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return err
	}

	if len(syncChanges) == 0 {
		_, _ = fmt.Fprintln(out, "push completed with no in-scope markdown changes (no-op)")
		return nil
	}

	var currentTarget config.Target
	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		currentTarget = config.Target{Mode: config.TargetModeFile, Value: abs}
	} else {
		currentTarget = config.Target{Mode: config.TargetModeSpace, Value: spaceDir}
	}
	if err := runValidateTargetWithContext(ctx, out, currentTarget); err != nil {
		return fmt.Errorf("pre-push validate failed: %w", err)
	}

	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	realRemote, err := newPushRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(realRemote)

	remote := &dryRunPushRemote{inner: realRemote, out: out, domain: cfg.Domain}

	dryRunSpaceDir, cleanupDryRun, err := prepareDryRunSpaceDir(spaceDir)
	if err != nil {
		return err
	}
	defer cleanupDryRun()

	state, err := fs.LoadState(dryRunSpaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	var progress syncflow.Progress
	if !flagVerbose && outputSupportsProgress(out) {
		progress = newConsoleProgress(out, "[DRY-RUN] Syncing to Confluence")
	}

	result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
		SpaceKey:            spaceKey,
		SpaceDir:            dryRunSpaceDir,
		Domain:              cfg.Domain,
		State:               state,
		Changes:             syncChanges,
		ConflictPolicy:      toSyncConflictPolicy(onConflict),
		KeepOrphanAssets:    flagPushKeepOrphanAssets,
		DryRun:              true,
		ArchiveTimeout:      normalizedArchiveTaskTimeout(),
		ArchivePollInterval: normalizedArchiveTaskPollInterval(),
		Progress:            progress,
	})
	if err != nil {
		var conflictErr *syncflow.PushConflictError
		if errors.As(err, &conflictErr) {
			return formatPushConflictError(conflictErr)
		}
		printPushDiagnostics(out, result.Diagnostics)
		return err
	}

	_, _ = fmt.Fprintf(out, "\n[DRY-RUN] push completed: %d page change(s) would be synced\n", len(result.Commits))
	printPushDiagnostics(out, result.Diagnostics)
	printPushSyncSummary(out, result.Commits, result.Diagnostics)
	return nil
}

// runPushInWorktree executes validate → diff → push → commit → merge → tag
// inside the already-created sync worktree. stashRef is a pointer so the
// pull-merge conflict path can clear it and prevent a double-pop in the defer.
func runPushInWorktree(
	ctx context.Context,
	cmd *cobra.Command,
	out io.Writer,
	target config.Target,
	spaceKey, spaceDir, onConflict, tsStr string,
	gitClient *git.Client,
	spaceScopePath, changeScopePath string,
	worktreeDir, syncBranchName, snapshotRefName string,
	stashRef *string,
) error {
	warnings := make([]string, 0)
	addWarning := func(message string) {
		warnings = append(warnings, message)
		_, _ = fmt.Fprintf(out, "warning: %s\n", message)
	}

	// 4. Validate (in worktree)
	wtSpaceDir := filepath.Join(worktreeDir, spaceScopePath)
	wtClient := &git.Client{RootDir: worktreeDir}
	if err := os.MkdirAll(wtSpaceDir, 0o750); err != nil {
		return fmt.Errorf("prepare worktree space directory: %w", err)
	}

	if strings.TrimSpace(*stashRef) != "" {
		if err := wtClient.StashApply(snapshotRefName); err != nil {
			return fmt.Errorf("materialize snapshot in worktree: %w", err)
		}
		if err := restoreUntrackedFromStashParent(wtClient, snapshotRefName, spaceScopePath); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(wtSpaceDir, 0o750); err != nil {
		return fmt.Errorf("prepare worktree scope directory: %w", err)
	}

	var wtTarget config.Target
	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		relFile, _ := filepath.Rel(spaceDir, abs)
		wtFile := filepath.Join(wtSpaceDir, relFile)
		wtTarget = config.Target{Mode: config.TargetModeFile, Value: wtFile}
	} else {
		wtTarget = config.Target{Mode: config.TargetModeSpace, Value: wtSpaceDir}
	}

	if err := runValidateTargetWithContext(ctx, out, wtTarget); err != nil {
		return fmt.Errorf("pre-push validate failed: %w", err)
	}

	// 5. Diff (Snapshot vs Baseline)
	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return err
	}

	wtClient = &git.Client{RootDir: worktreeDir}
	syncChanges, err := collectPushChangesForTarget(wtClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return err
	}

	if len(syncChanges) == 0 {
		_, _ = fmt.Fprintln(out, "push completed with no in-scope markdown changes (no-op)")
		return nil
	}

	if err := requireSafetyConfirmation(cmd.InOrStdin(), out, "push", len(syncChanges), pushHasDeleteChange(syncChanges)); err != nil {
		return err
	}

	// 6. Push (in worktree)
	envPath := findEnvPath(wtSpaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPushRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(remote)

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	globalPageIndex, err := syncflow.BuildGlobalPageIndex(worktreeDir)
	if err != nil {
		return fmt.Errorf("build global page index: %w", err)
	}

	var progress syncflow.Progress
	if !flagVerbose && outputSupportsProgress(out) {
		progress = newConsoleProgress(out, "Syncing to Confluence")
	}

	result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
		SpaceKey:            spaceKey,
		SpaceDir:            wtSpaceDir,
		Domain:              cfg.Domain,
		State:               state,
		GlobalPageIndex:     globalPageIndex,
		Changes:             syncChanges,
		ConflictPolicy:      toSyncConflictPolicy(onConflict),
		KeepOrphanAssets:    flagPushKeepOrphanAssets,
		ArchiveTimeout:      normalizedArchiveTaskTimeout(),
		ArchivePollInterval: normalizedArchiveTaskPollInterval(),
		Progress:            progress,
	})
	if err != nil {
		var conflictErr *syncflow.PushConflictError
		if errors.As(err, &conflictErr) {
			slog.Warn("push_conflict_detected",
				"path", conflictErr.Path,
				"page_id", conflictErr.PageID,
				"local_version", conflictErr.LocalVersion,
				"remote_version", conflictErr.RemoteVersion,
				"policy", conflictErr.Policy,
			)
			if onConflict == OnConflictPullMerge {
				slog.Info("push_conflict_resolution", "strategy", OnConflictPullMerge, "action", "run_pull")
				_, _ = fmt.Fprintf(out, "conflict detected for %s; policy is %s, attempting automatic pull-merge...\n", conflictErr.Path, onConflict)
				if strings.TrimSpace(*stashRef) != "" {
					if err := gitClient.StashPop(*stashRef); err != nil {
						return fmt.Errorf("restore local workspace before automatic pull-merge: %w", err)
					}
					*stashRef = ""
				}
				// During pull-merge, automatically discard local changes for files
				// that were deleted remotely, so pull can apply those deletions cleanly
				// instead of warning and skipping them.
				prevDiscardLocal := flagPullDiscardLocal
				flagPullDiscardLocal = true
				pullErr := runPullForPush(cmd, target)
				flagPullDiscardLocal = prevDiscardLocal
				if pullErr != nil {
					return fmt.Errorf("automatic pull-merge failed: %w", pullErr)
				}
				retryCmd := "conf push"
				if target.IsFile() {
					retryCmd = fmt.Sprintf("conf push %q", target.Value)
				}
				_, _ = fmt.Fprintf(out, "automatic pull-merge completed. If there were no content conflicts, rerun `%s` to resume the push.\n", retryCmd)
				return nil
			}
			return formatPushConflictError(conflictErr)
		}
		printPushDiagnostics(out, result.Diagnostics)
		return err
	}

	if len(result.Commits) == 0 {
		slog.Info("push_sync_result", "space_key", spaceKey, "commit_count", 0, "diagnostics", len(result.Diagnostics))
		_, _ = fmt.Fprintln(out, "push completed with no pushable markdown changes (no-op)")
		return nil
	}

	printPushDiagnostics(out, result.Diagnostics)
	finalizePushGit := func() error {
		for _, commitPlan := range result.Commits {
			filesToAdd := make([]string, 0, len(commitPlan.StagedPaths))
			for _, relPath := range commitPlan.StagedPaths {
				filesToAdd = append(filesToAdd, filepath.Join(wtSpaceDir, relPath))
			}

			repoPaths := make([]string, 0, len(filesToAdd))
			for _, absPath := range filesToAdd {
				rel, _ := filepath.Rel(worktreeDir, absPath)
				repoPaths = append(repoPaths, filepath.ToSlash(rel))
			}

			addCandidates := make([]string, 0, len(repoPaths))
			for _, repoPath := range repoPaths {
				absRepoPath := filepath.Join(worktreeDir, filepath.FromSlash(repoPath))
				if _, statErr := os.Stat(absRepoPath); os.IsNotExist(statErr) {
					if _, err := wtClient.Run("rm", "--cached", "--ignore-unmatch", "--", repoPath); err != nil {
						return fmt.Errorf("git rm failed: %w", err)
					}
					continue
				}
				addCandidates = append(addCandidates, repoPath)
			}

			if len(addCandidates) > 0 {
				addArgs := append([]string{"add", "-A", "--"}, addCandidates...)
				if _, err := wtClient.Run(addArgs...); err != nil {
					return fmt.Errorf("git add failed: %w", err)
				}
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
				return fmt.Errorf("git commit failed: %w", err)
			}

			if progress == nil {
				_, _ = fmt.Fprintf(out, "pushed %s (page %s, v%d)\n", commitPlan.Path, commitPlan.PageID, commitPlan.Version)
			}
		}

		if err := gitClient.RemoveWorktree(worktreeDir); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}

		if err := gitClient.Merge(syncBranchName, ""); err != nil {
			return fmt.Errorf("merge sync branch: %w", err)
		}

		refKey := fs.SanitizePathSegment(spaceKey)
		tagName := fmt.Sprintf("confluence-sync/push/%s/%s", refKey, tsStr)
		tagMsg := fmt.Sprintf("Confluence push sync for %s at %s", spaceKey, tsStr)
		if err := gitClient.Tag(tagName, tagMsg); err != nil {
			addWarning(fmt.Sprintf("failed to create tag: %v", err))
		}

		if err := restorePushStash(gitClient, *stashRef, spaceScopePath, result.Commits); err != nil {
			addWarning(fmt.Sprintf("stash restore had conflicts: %v", err))
		}
		*stashRef = ""

		return nil
	}

	if progress != nil {
		if err := runWithIndeterminateStatus(out, "Finalizing push", finalizePushGit); err != nil {
			return err
		}
	} else {
		if err := finalizePushGit(); err != nil {
			return err
		}
	}

	if err := fs.SaveState(spaceDir, result.State); err != nil {
		addWarning(fmt.Sprintf("failed to save local state: %v", err))
	}

	printPushWarningSummary(out, warnings)
	printPushSyncSummary(out, result.Commits, result.Diagnostics)

	_, _ = fmt.Fprintf(out, "push completed: %d page change(s) synced\n", len(result.Commits))
	slog.Info("push_sync_result", "space_key", spaceKey, "commit_count", len(result.Commits), "diagnostics", len(result.Diagnostics))
	return nil
}

func resolvePushScopePath(client *git.Client, spaceDir string, target config.Target, targetCtx validateTargetContext) (string, error) {
	_ = client
	if target.IsFile() {
		if len(targetCtx.files) != 1 {
			return "", fmt.Errorf("expected one file target, got %d", len(targetCtx.files))
		}
		return gitScopePathFromPath(targetCtx.files[0])
	}
	return gitScopePathFromPath(spaceDir)
}

func gitScopePathFromPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ".", nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}

	if info.IsDir() {
		prefix, err := git.RunGit(absPath, "rev-parse", "--show-prefix")
		if err != nil {
			return "", err
		}
		prefix = strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
		prefix = strings.TrimSuffix(prefix, "/")
		if prefix == "" {
			return ".", nil
		}
		return filepath.ToSlash(filepath.Clean(prefix)), nil
	}

	dir := filepath.Dir(absPath)
	prefix, err := git.RunGit(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	prefix = strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
	relPath := filepath.ToSlash(filepath.Clean(filepath.Join(prefix, filepath.Base(absPath))))
	return relPath, nil
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

func collectSyncPushChanges(client *git.Client, baselineRef, diffScopePath, spaceScopePath string) ([]syncflow.PushFileChange, error) {
	changes, err := collectGitChangesWithUntracked(client, baselineRef, diffScopePath)
	if err != nil {
		return nil, err
	}
	return toSyncPushChanges(changes, spaceScopePath)
}

func collectPushChangesForTarget(
	client *git.Client,
	baselineRef string,
	target config.Target,
	spaceScopePath string,
	changeScopePath string,
) ([]syncflow.PushFileChange, error) {
	diffScopePath := spaceScopePath
	if target.IsFile() {
		diffScopePath = changeScopePath
	}
	return collectSyncPushChanges(client, baselineRef, diffScopePath, spaceScopePath)
}

func collectGitChangesWithUntracked(client *git.Client, baselineRef, scopePath string) ([]git.FileStatus, error) {
	changes, err := client.DiffNameStatus(baselineRef, "", scopePath)
	if err != nil {
		return nil, fmt.Errorf("diff failed: %w", err)
	}

	untrackedRaw, err := client.Run("ls-files", "--others", "--exclude-standard", "--", scopePath)
	if err == nil {
		for _, line := range strings.Split(strings.ReplaceAll(untrackedRaw, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			changes = append(changes, git.FileStatus{Code: "A", Path: filepath.ToSlash(line)})
		}
	}

	return changes, nil
}

func prepareDryRunSpaceDir(spaceDir string) (string, func(), error) {
	tmpRoot, err := os.MkdirTemp("", "conf-dry-run-*")
	if err != nil {
		return "", nil, fmt.Errorf("create dry-run temp dir: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpRoot)
	}

	dryRunSpaceDir := filepath.Join(tmpRoot, filepath.Base(spaceDir))
	if err := copyDirTree(spaceDir, dryRunSpaceDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("prepare dry-run space copy: %w", err)
	}

	return dryRunSpaceDir, cleanup, nil
}

func copyDirTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o750)
		}

		raw, err := os.ReadFile(path) //nolint:gosec // path comes from filepath.WalkDir under trusted source dir
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
			return err
		}
		return os.WriteFile(targetPath, raw, 0o600)
	})
}

func restoreUntrackedFromStashParent(client *git.Client, stashRef, scopePath string) error {
	stashRef = strings.TrimSpace(stashRef)
	if stashRef == "" {
		return nil
	}

	untrackedRef := stashRef + "^3"
	if _, err := client.Run("rev-parse", "--verify", "--quiet", untrackedRef); err != nil {
		return nil
	}
	untrackedPaths, err := client.Run("ls-tree", "-r", "--name-only", untrackedRef, "--", scopePath)
	if err != nil || strings.TrimSpace(untrackedPaths) == "" {
		return nil
	}

	if _, err := client.Run("checkout", untrackedRef, "--", scopePath); err != nil {
		return fmt.Errorf("restore untracked files from stash: %w", err)
	}
	if _, err := client.Run("reset", "--", scopePath); err != nil {
		return fmt.Errorf("unstage restored untracked files: %w", err)
	}

	return nil
}

func restorePushStash(
	client *git.Client,
	stashRef string,
	spaceScopePath string,
	commits []syncflow.PushCommitPlan,
) error {
	stashRef = strings.TrimSpace(stashRef)
	if stashRef == "" {
		return nil
	}

	stashPaths, err := listStashPaths(client, stashRef, spaceScopePath)
	if err != nil {
		if popErr := client.StashPop(stashRef); popErr != nil {
			return popErr
		}
		return nil
	}

	if len(stashPaths) == 0 {
		return client.StashDrop(stashRef)
	}

	syncedPaths := syncedRepoPathsForPushCommits(spaceScopePath, commits)
	pathsToRestore := make([]string, 0, len(stashPaths))
	for _, path := range stashPaths {
		if _, synced := syncedPaths[path]; synced {
			continue
		}
		pathsToRestore = append(pathsToRestore, path)
	}

	if len(pathsToRestore) == 0 {
		return client.StashDrop(stashRef)
	}

	untrackedSet, err := listStashUntrackedPathSet(client, stashRef, spaceScopePath)
	if err != nil {
		return fmt.Errorf("identify stashed untracked paths: %w", err)
	}

	trackedPaths := make([]string, 0, len(pathsToRestore))
	untrackedPaths := make([]string, 0, len(pathsToRestore))
	for _, path := range pathsToRestore {
		if _, isUntracked := untrackedSet[path]; isUntracked {
			untrackedPaths = append(untrackedPaths, path)
			continue
		}
		trackedPaths = append(trackedPaths, path)
	}

	sort.Strings(trackedPaths)
	sort.Strings(untrackedPaths)

	if len(trackedPaths) > 0 {
		if err := restoreTrackedPathsFromStash(client, stashRef, trackedPaths); err != nil {
			return err
		}
	}

	if err := restoreUntrackedPathsFromStashParent(client, stashRef, untrackedPaths); err != nil {
		return err
	}

	return client.StashDrop(stashRef)
}

func restoreTrackedPathsFromStash(client *git.Client, stashRef string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	stashRef = strings.TrimSpace(stashRef)
	if stashRef == "" {
		return nil
	}

	restoreWorktreeArgs := append([]string{"restore", "--source=" + stashRef, "--worktree", "--"}, paths...)
	if _, err := client.Run(restoreWorktreeArgs...); err != nil {
		return fmt.Errorf("restore tracked workspace changes from stash: %w", err)
	}

	stagedPathSet, err := listStashIndexPathSet(client, stashRef, paths)
	if err != nil {
		return fmt.Errorf("identify stashed staged paths: %w", err)
	}

	stagedPaths := make([]string, 0, len(stagedPathSet))
	for _, path := range paths {
		if _, staged := stagedPathSet[path]; staged {
			stagedPaths = append(stagedPaths, path)
		}
	}
	if len(stagedPaths) == 0 {
		return nil
	}

	restoreStagedArgs := append([]string{"restore", "--source=" + stashRef + "^2", "--staged", "--"}, stagedPaths...)
	if _, err := client.Run(restoreStagedArgs...); err != nil {
		return fmt.Errorf("restore staged workspace changes from stash: %w", err)
	}

	return nil
}

func listStashPaths(client *git.Client, stashRef, scopePath string) ([]string, error) {
	args := []string{"diff", "--name-only", stashRef + "^1", stashRef}
	scopePath = normalizeRepoRelPath(scopePath)
	if scopePath != "" {
		args = append(args, "--", scopePath)
	}

	raw, err := client.Run(args...)
	if err != nil {
		return nil, err
	}

	pathSet := map[string]struct{}{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		path := normalizeRepoRelPath(line)
		if path == "" {
			continue
		}
		pathSet[path] = struct{}{}
	}

	untrackedSet, err := listStashUntrackedPathSet(client, stashRef, scopePath)
	if err != nil {
		return nil, err
	}
	for path := range untrackedSet {
		pathSet[path] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func listStashUntrackedPathSet(client *git.Client, stashRef, scopePath string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	stashRef = strings.TrimSpace(stashRef)
	if stashRef == "" {
		return out, nil
	}

	untrackedRef := stashRef + "^3"
	if _, err := client.Run("rev-parse", "--verify", "--quiet", untrackedRef); err != nil {
		return out, nil
	}

	args := []string{"ls-tree", "-r", "--name-only", untrackedRef}
	scopePath = normalizeRepoRelPath(scopePath)
	if scopePath != "" {
		args = append(args, "--", scopePath)
	}

	raw, err := client.Run(args...)
	if err != nil {
		return nil, err
	}

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		path := normalizeRepoRelPath(line)
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}

	return out, nil
}

func listStashIndexPathSet(client *git.Client, stashRef string, scopePaths []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	stashRef = strings.TrimSpace(stashRef)
	if stashRef == "" {
		return out, nil
	}

	args := []string{"diff", "--name-only", stashRef + "^1", stashRef + "^2"}
	if len(scopePaths) > 0 {
		args = append(args, "--")
		args = append(args, scopePaths...)
	}

	raw, err := client.Run(args...)
	if err != nil {
		return nil, err
	}

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		path := normalizeRepoRelPath(line)
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}

	return out, nil
}

func restoreUntrackedPathsFromStashParent(client *git.Client, stashRef string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	stashRef = strings.TrimSpace(stashRef)
	if stashRef == "" {
		return nil
	}

	untrackedRef := stashRef + "^3"
	if _, err := client.Run("rev-parse", "--verify", "--quiet", untrackedRef); err != nil {
		return nil
	}

	checkoutArgs := append([]string{"checkout", untrackedRef, "--"}, paths...)
	if _, err := client.Run(checkoutArgs...); err != nil {
		return fmt.Errorf("restore untracked files from stash: %w", err)
	}

	resetArgs := append([]string{"reset", "--"}, paths...)
	if _, err := client.Run(resetArgs...); err != nil {
		return fmt.Errorf("unstage restored untracked files: %w", err)
	}

	return nil
}

func syncedRepoPathsForPushCommits(spaceScopePath string, commits []syncflow.PushCommitPlan) map[string]struct{} {
	out := map[string]struct{}{}
	scopePath := normalizeRepoRelPath(spaceScopePath)

	for _, commit := range commits {
		for _, relPath := range commit.StagedPaths {
			relPath = normalizeRepoRelPath(relPath)
			if relPath == "" {
				continue
			}

			repoPath := relPath
			if scopePath != "" {
				repoPath = normalizeRepoRelPath(filepath.Join(scopePath, filepath.FromSlash(relPath)))
			}
			if repoPath == "" {
				continue
			}
			out[repoPath] = struct{}{}
		}
	}

	return out
}

func normalizeRepoRelPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

func toSyncPushChanges(changes []git.FileStatus, spaceScopePath string) ([]syncflow.PushFileChange, error) {
	normalizedScope := filepath.ToSlash(filepath.Clean(spaceScopePath))
	if normalizedScope == "." {
		normalizedScope = ""
	}

	out := make([]syncflow.PushFileChange, 0, len(changes))
	for _, change := range changes {
		normalizedPath := filepath.ToSlash(filepath.Clean(change.Path))
		relPath := normalizedPath
		if normalizedScope != "" {
			if strings.HasPrefix(normalizedPath, normalizedScope+"/") {
				relPath = strings.TrimPrefix(normalizedPath, normalizedScope+"/")
			} else if normalizedPath == normalizedScope {
				relPath = filepath.Base(filepath.FromSlash(normalizedPath))
			} else {
				continue
			}
		}

		relPath = filepath.ToSlash(filepath.Clean(relPath))
		relPath = strings.TrimPrefix(relPath, "./")
		if relPath == "." || strings.HasPrefix(relPath, "../") {
			continue
		}

		if !strings.HasSuffix(relPath, ".md") || strings.HasPrefix(relPath, "assets/") {
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

func summarizePushChanges(changes []syncflow.PushFileChange) (adds, modifies, deletes int) {
	for _, change := range changes {
		switch change.Type {
		case syncflow.PushChangeAdd:
			adds++
		case syncflow.PushChangeModify:
			modifies++
		case syncflow.PushChangeDelete:
			deletes++
		}
	}
	return adds, modifies, deletes
}

func pushHasDeleteChange(changes []syncflow.PushFileChange) bool {
	for _, change := range changes {
		if change.Type == syncflow.PushChangeDelete {
			return true
		}
	}
	return false
}

func printPushDiagnostics(out io.Writer, diagnostics []syncflow.PushDiagnostic) {
	if len(diagnostics) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "\nDiagnostics:")
	for _, diag := range diagnostics {
		_, _ = fmt.Fprintf(out, "  [%s] %s: %s\n", diag.Code, diag.Path, diag.Message)
	}
}

func printPushWarningSummary(out io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "\nSummary of warnings:")
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(out, "  - %s\n", warning)
	}
}

func printPushSyncSummary(out io.Writer, commits []syncflow.PushCommitPlan, diagnostics []syncflow.PushDiagnostic) {
	if len(commits) == 0 && len(diagnostics) == 0 {
		return
	}

	deletedPages := 0
	for _, commit := range commits {
		if commit.Deleted {
			deletedPages++
		}
	}

	attachmentDeleted := 0
	attachmentPreserved := 0
	for _, diag := range diagnostics {
		switch diag.Code {
		case "ATTACHMENT_DELETED":
			attachmentDeleted++
		case "ATTACHMENT_PRESERVED":
			attachmentPreserved++
		}
	}

	_, _ = fmt.Fprintln(out, "\nSync Summary:")
	_, _ = fmt.Fprintf(out, "  pages changed: %d (deleted: %d)\n", len(commits), deletedPages)
	if attachmentDeleted > 0 || attachmentPreserved > 0 {
		_, _ = fmt.Fprintf(out, "  attachments: deleted %d, preserved %d\n", attachmentDeleted, attachmentPreserved)
	}
	if len(diagnostics) > 0 {
		_, _ = fmt.Fprintf(out, "  diagnostics: %d\n", len(diagnostics))
	}
}

func formatPushConflictError(conflictErr *syncflow.PushConflictError) error {
	switch conflictErr.Policy {
	case syncflow.PushConflictPolicyPullMerge:
		// This should generally be handled by the caller in runPush, but fallback here
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): run 'conf pull' to merge remote changes into your local workspace before retrying push",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	case syncflow.PushConflictPolicyForce:
		return conflictErr
	default:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): rerun with --on-conflict=force to overwrite remote, or run 'conf pull' to merge",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	}
}

func normalizedArchiveTaskTimeout() time.Duration {
	timeout := flagArchiveTaskTimeout
	if timeout <= 0 {
		return confluence.DefaultArchiveTaskTimeout
	}
	return timeout
}

func normalizedArchiveTaskPollInterval() time.Duration {
	interval := flagArchiveTaskPollInterval
	if interval <= 0 {
		interval = confluence.DefaultArchiveTaskPollInterval
	}
	timeout := normalizedArchiveTaskTimeout()
	if interval > timeout {
		return timeout
	}
	return interval
}

func resolveInitialPushContext(target config.Target) (initialPullContext, error) {
	if !target.IsFile() {
		return resolveInitialPullContext(target)
	}

	absPath, err := filepath.Abs(target.Value)
	if err != nil {
		return initialPullContext{}, err
	}

	if _, err := os.Stat(absPath); err != nil {
		return initialPullContext{}, fmt.Errorf("target file %s: %w", target.Value, err)
	}

	spaceDir := findSpaceDirFromFile(absPath, "")
	spaceKey := ""
	if state, stateErr := fs.LoadState(spaceDir); stateErr == nil {
		spaceKey = strings.TrimSpace(state.SpaceKey)
	}
	if spaceKey == "" {
		spaceKey = inferSpaceKeyFromDirName(spaceDir)
	}
	if spaceKey == "" {
		return initialPullContext{}, fmt.Errorf("target file %s missing tracked space context; run pull with a space target first", target.Value)
	}

	return initialPullContext{
		spaceKey: spaceKey,
		spaceDir: spaceDir,
		fixedDir: true,
	}, nil
}
