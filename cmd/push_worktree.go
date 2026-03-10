package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

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
) (pushWorktreeOutcome, error) {
	outcome := pushWorktreeOutcome{}
	warnings := make([]string, 0)
	addWarning := func(message string) {
		warnings = append(warnings, message)
		_, _ = fmt.Fprintf(out, "warning: %s\n", message)
	}

	// 4. Validate (in worktree)
	wtSpaceDir := filepath.Join(worktreeDir, spaceScopePath)
	wtClient := &git.Client{RootDir: worktreeDir}
	if err := os.MkdirAll(wtSpaceDir, 0o750); err != nil {
		return outcome, fmt.Errorf("prepare worktree space directory: %w", err)
	}

	if strings.TrimSpace(*stashRef) != "" {
		if err := wtClient.StashApply(snapshotRefName); err != nil {
			return outcome, fmt.Errorf("materialize snapshot in worktree: %w", err)
		}
		if err := restoreUntrackedFromStashParent(wtClient, snapshotRefName, spaceScopePath); err != nil {
			return outcome, err
		}
	}
	if err := os.MkdirAll(wtSpaceDir, 0o750); err != nil {
		return outcome, fmt.Errorf("prepare worktree scope directory: %w", err)
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

	// 5. Diff (Snapshot vs Baseline)
	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return outcome, err
	}

	wtClient = &git.Client{RootDir: worktreeDir}
	syncChanges, err := collectPushChangesForTarget(wtClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return outcome, err
	}

	// 4. Validate (in worktree) using the same scope as preflight and dry-run.
	if err := runPushValidation(ctx, out, wtTarget, wtSpaceDir, "pre-push validate failed"); err != nil {
		return outcome, err
	}

	if len(syncChanges) == 0 {
		_, _ = fmt.Fprintln(out, "push completed: no in-scope markdown changes found in worktree (no-op)")
		outcome.NoChanges = true
		return outcome, nil
	}

	if pushHasDeleteChange(syncChanges) {
		wtState, stateErr := fs.LoadState(wtSpaceDir)
		if stateErr == nil {
			printDestructivePushPreview(out, syncChanges, wtSpaceDir, wtState)
		}
	}

	if err := requireSafetyConfirmation(cmd.InOrStdin(), out, "push", len(syncChanges), pushHasDeleteChange(syncChanges)); err != nil {
		return outcome, err
	}

	// 6. Push (in worktree)
	envPath := findEnvPath(wtSpaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return outcome, fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPushRemote(cfg)
	if err != nil {
		return outcome, fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(remote)

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		return outcome, fmt.Errorf("load state: %w", err)
	}

	globalPageIndex, err := buildWorkspaceGlobalPageIndex(wtSpaceDir)
	if err != nil {
		return outcome, fmt.Errorf("build global page index: %w", err)
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
	outcome.Result = result
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
					if err := restorePushStash(gitClient, *stashRef, spaceScopePath, nil); err != nil {
						return outcome, fmt.Errorf("restore local workspace before automatic pull-merge: %w", err)
					}
					*stashRef = ""
				}
				backupRepoPath, backupContent := captureAutoPullMergeBackup(gitClient.RootDir, target)
				prevDiscardLocal := flagPullDiscardLocal
				flagPullDiscardLocal = false
				pullReport, pullErr := runPullForPush(cmd, target)
				outcome.ConflictResolution = &commandRunReportConflictResolution{
					Policy:               OnConflictPullMerge,
					MutatedFiles:         append([]string(nil), pullReport.MutatedFiles...),
					Diagnostics:          append([]commandRunReportDiagnostic(nil), pullReport.Diagnostics...),
					AttachmentOperations: append([]commandRunReportAttachmentOp(nil), pullReport.AttachmentOperations...),
					FallbackModes:        append([]string(nil), pullReport.FallbackModes...),
				}
				flagPullDiscardLocal = prevDiscardLocal
				if pullErr != nil {
					outcome.ConflictResolution.Status = "failed"
					printAutoPullMergeNextSteps(out, target)
					return outcome, fmt.Errorf("automatic pull-merge failed: %w", pullErr)
				}
				if strings.TrimSpace(backupRepoPath) != "" && len(backupContent) > 0 {
					if writtenBackup, writeErr := writeAutoPullMergeBackup(gitClient.RootDir, backupRepoPath, backupContent); writeErr == nil && strings.TrimSpace(writtenBackup) != "" {
						_, _ = fmt.Fprintf(out, "saved local edits from before automatic pull-merge as %q\n", writtenBackup)
					}
				}
				outcome.ConflictResolution.Status = "completed"
				retryCmd := "conf push"
				if target.IsFile() {
					retryCmd = fmt.Sprintf("conf push %q", target.Value)
				}
				_, _ = fmt.Fprintf(out, "automatic pull-merge completed. If there were no content conflicts, rerun `%s` to resume the push.\n", retryCmd)
				printAutoPullMergeNextSteps(out, target)
				return outcome, nil
			}
			return outcome, formatPushConflictError(conflictErr)
		}
		printPushDiagnostics(out, result.Diagnostics)
		return outcome, err
	}

	if len(result.Commits) == 0 {
		slog.Info("push_sync_result", "space_key", spaceKey, "commit_count", 0, "diagnostics", len(result.Diagnostics))
		_, _ = fmt.Fprintln(out, "push completed: changed files produced no pushable content after validation (no-op)")
		outcome.NoChanges = true
		return outcome, nil
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
			return outcome, err
		}
	} else {
		if err := finalizePushGit(); err != nil {
			return outcome, err
		}
	}

	if err := fs.SaveState(spaceDir, result.State); err != nil {
		addWarning(fmt.Sprintf("failed to save local state: %v", err))
	}

	printPushWarningSummary(out, warnings)
	printPushSyncSummary(out, result.Commits, result.Diagnostics)

	_, _ = fmt.Fprintf(out, "push completed: %d page change(s) synced\n", len(result.Commits))
	slog.Info("push_sync_result", "space_key", spaceKey, "commit_count", len(result.Commits), "diagnostics", len(result.Diagnostics))
	outcome.Warnings = append(outcome.Warnings, warnings...)
	return outcome, nil
}

func printAutoPullMergeNextSteps(out io.Writer, target config.Target) {
	_, _ = fmt.Fprintln(out, "Next steps:")
	_, _ = fmt.Fprintln(out, "  1. Review any conflict markers or preserved backup files.")
	_, _ = fmt.Fprintln(out, "  2. Resolve the affected markdown files and run `git add <file>` for each resolved file.")
	if target.IsFile() {
		_, _ = fmt.Fprintf(out, "  3. Rerun `conf push %q --on-conflict=cancel` once the file is resolved.\n", target.Value)
		return
	}
	_, _ = fmt.Fprintln(out, "  3. Rerun `conf push <SPACE_KEY> --on-conflict=cancel` once the files are resolved.")
}

func captureAutoPullMergeBackup(repoRoot string, target config.Target) (string, []byte) {
	if !target.IsFile() {
		return "", nil
	}

	absPath, err := filepath.Abs(target.Value)
	if err != nil {
		return "", nil
	}
	raw, err := os.ReadFile(absPath) //nolint:gosec // target.Value is an explicit user-selected markdown path
	if err != nil {
		return "", nil
	}
	repoPath, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return "", nil
	}
	return filepath.ToSlash(repoPath), raw
}

func writeAutoPullMergeBackup(repoRoot, repoPath string, raw []byte) (string, error) {
	if strings.TrimSpace(repoPath) == "" || len(raw) == 0 {
		return "", nil
	}

	backupRepoPath, err := makeConflictBackupPath(repoRoot, repoPath, "My Local Changes")
	if err != nil {
		return "", err
	}
	backupAbsPath := filepath.Join(repoRoot, filepath.FromSlash(backupRepoPath))
	if err := os.MkdirAll(filepath.Dir(backupAbsPath), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(backupAbsPath, raw, 0o600); err != nil {
		return "", err
	}
	return backupRepoPath, nil
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
