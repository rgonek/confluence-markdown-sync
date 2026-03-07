package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
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

var runPullForPush = func(cmd *cobra.Command, target config.Target) (commandRunReport, error) {
	return runPullWithReport(cmd, target, false)
}

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
	addReportJSONFlag(cmd)
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
	actualOut := ensureSynchronizedCmdOutput(cmd)
	out := reportWriter(cmd, actualOut)
	runID, restoreLogger := beginCommandRun("push")
	defer restoreLogger()
	preflight := flagPushPreflight
	startedAt := time.Now()
	report := newCommandRunReport(runID, "push", target, startedAt)
	defer func() {
		if !commandRequestsJSONReport(cmd) {
			return
		}
		report.finalize(runErr, time.Now())
		_ = writeCommandRunReport(actualOut, report)
	}()
	if err := ensureWorkspaceSyncReady("push"); err != nil {
		return err
	}

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
	report.Target.SpaceKey = strings.TrimSpace(spaceKey)

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
	report.Target.SpaceDir = spaceDir
	if target.IsFile() {
		report.Target.File = target.Value
	}

	gitClient, err := git.NewClient()
	if err != nil {
		return err
	}
	currentBranch, err := gitClient.CurrentBranch()
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
		return runPushPreflight(ctx, out, target, spaceKey, spaceDir, gitClient, spaceScopePath, changeScopePath, onConflict)
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
	report.setRecoveryArtifactStatus("snapshot_ref", snapshotName, "created")

	// Keep snapshot ref only on failure, delete on success
	defer func() {
		if runErr == nil {
			if err := gitClient.DeleteRef(snapshotName); err == nil {
				report.setRecoveryArtifactStatus("snapshot_ref", snapshotName, "cleaned_up")
			} else {
				report.setRecoveryArtifactStatus("snapshot_ref", snapshotName, "retained")
			}
		} else {
			report.setRecoveryArtifactStatus("snapshot_ref", snapshotName, "retained")
			_, _ = fmt.Fprintf(out, "\nSnapshot retained for recovery: %s\n", snapshotName)
		}
	}()

	// 2. Create Sync Branch
	syncBranchName := fmt.Sprintf("sync/%s/%s", refKey, tsStr)
	if err := gitClient.CreateBranch(syncBranchName, headCommit); err != nil {
		return fmt.Errorf("create sync branch: %w", err)
	}
	report.setRecoveryArtifactStatus("sync_branch", syncBranchName, "created")

	// Keep sync branch only on failure, delete on success
	defer func() {
		if runErr == nil {
			if err := gitClient.DeleteBranch(syncBranchName); err == nil {
				report.setRecoveryArtifactStatus("sync_branch", syncBranchName, "cleaned_up")
			} else {
				report.setRecoveryArtifactStatus("sync_branch", syncBranchName, "retained")
			}
		} else {
			report.setRecoveryArtifactStatus("sync_branch", syncBranchName, "retained")
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

	defer func() {
		if runErr == nil {
			if err := deleteRecoveryMetadata(gitClient.RootDir, refKey, tsStr); err != nil {
				_, _ = fmt.Fprintf(out, "warning: failed to clean up recovery metadata: %v\n", err)
			}
			return
		}
		if err := writeRecoveryMetadata(gitClient.RootDir, recoveryMetadata{
			SpaceKey:       refKey,
			Timestamp:      tsStr,
			SyncBranch:     syncBranchName,
			SnapshotRef:    snapshotName,
			OriginalBranch: strings.TrimSpace(currentBranch),
			FailureReason:  runErr.Error(),
		}); err != nil {
			_, _ = fmt.Fprintf(out, "warning: failed to persist recovery metadata: %v\n", err)
		}
	}()

	outcome, err := runPushInWorktree(ctx, cmd, out, target, spaceKey, spaceDir, onConflict, tsStr,
		gitClient, spaceScopePath, changeScopePath, worktreeDir, syncBranchName, snapshotName, &stashRef)
	report.Diagnostics = append(report.Diagnostics, reportDiagnosticsFromPush(outcome.Result.Diagnostics, spaceDir)...)
	for _, commit := range outcome.Result.Commits {
		report.MutatedFiles = append(report.MutatedFiles, reportRelativePath(spaceDir, commit.Path))
		report.MutatedPages = append(report.MutatedPages, commandRunReportPage{
			Path:    reportRelativePath(spaceDir, commit.Path),
			PageID:  strings.TrimSpace(commit.PageID),
			Title:   strings.TrimSpace(commit.PageTitle),
			Version: commit.Version,
			Deleted: commit.Deleted,
		})
	}
	report.AttachmentOperations = append(report.AttachmentOperations, reportAttachmentOpsFromPush(outcome.Result, spaceDir)...)
	report.FallbackModes = append(report.FallbackModes, fallbackModesFromPushDiagnostics(outcome.Result.Diagnostics)...)
	if outcome.ConflictResolution != nil {
		report.ConflictResolution = outcome.ConflictResolution
		report.MutatedFiles = append(report.MutatedFiles, outcome.ConflictResolution.MutatedFiles...)
		report.Diagnostics = append(report.Diagnostics, outcome.ConflictResolution.Diagnostics...)
		report.AttachmentOperations = append(report.AttachmentOperations, outcome.ConflictResolution.AttachmentOperations...)
		report.FallbackModes = append(report.FallbackModes, outcome.ConflictResolution.FallbackModes...)
	}
	return err
}

// runPushInWorktree executes validate → diff → push → commit → merge → tag
// inside the already-created sync worktree. stashRef is a pointer so the
// pull-merge conflict path can clear it and prevent a double-pop in the defer.
