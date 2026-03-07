package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		currentTarget := config.Target{Mode: config.TargetModeFile, Value: abs}
		if err := runValidateTargetWithContext(ctx, out, currentTarget); err != nil {
			return fmt.Errorf("preflight validate failed: %w", err)
		}
	} else {
		changedAbsPaths := pushChangedAbsPaths(spaceDir, syncChanges)
		if err := runValidateChangedPushFiles(ctx, out, spaceDir, changedAbsPaths); err != nil {
			return fmt.Errorf("preflight validate failed: %w", err)
		}
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

	if target.IsFile() {
		abs, _ := filepath.Abs(target.Value)
		currentTarget := config.Target{Mode: config.TargetModeFile, Value: abs}
		if err := runValidateTargetWithContext(ctx, out, currentTarget); err != nil {
			return fmt.Errorf("pre-push validate failed: %w", err)
		}
	} else {
		changedAbsPaths := pushChangedAbsPaths(spaceDir, syncChanges)
		if err := runValidateChangedPushFiles(ctx, out, spaceDir, changedAbsPaths); err != nil {
			return fmt.Errorf("pre-push validate failed: %w", err)
		}
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
	attachmentUploaded := 0
	attachmentPreserved := 0
	attachmentSkipped := 0
	for _, diag := range diagnostics {
		switch diag.Code {
		case "ATTACHMENT_CREATED":
			attachmentUploaded++
		case "ATTACHMENT_DELETED":
			attachmentDeleted++
		case "ATTACHMENT_PRESERVED":
			attachmentPreserved++
			attachmentSkipped++
		default:
			if strings.HasPrefix(diag.Code, "ATTACHMENT_") && strings.Contains(diag.Code, "SKIPPED") {
				attachmentSkipped++
			}
		}
	}

	_, _ = fmt.Fprintln(out, "\nSync Summary:")
	_, _ = fmt.Fprintf(out, "  pages changed: %d (deleted: %d)\n", len(commits), deletedPages)
	if attachmentUploaded > 0 || attachmentDeleted > 0 || attachmentPreserved > 0 || attachmentSkipped > 0 {
		_, _ = fmt.Fprintf(out, "  attachments: uploaded %d, deleted %d, preserved %d, skipped %d\n", attachmentUploaded, attachmentDeleted, attachmentPreserved, attachmentSkipped)
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
