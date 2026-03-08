package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

func runPushPreflight(
	ctx context.Context,
	out io.Writer,
	target config.Target,
	spaceKey, spaceDir string,
	gitClient *git.Client,
	spaceScopePath, changeScopePath string,
	onConflict string,
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
		_, _ = fmt.Fprintf(out, "preflight for space %s: no local markdown changes detected since last sync (no-op)\n", spaceKey)
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

	// Load config and create remote to probe capabilities and list remote pages.
	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPushRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(remote)

	// Probe GetContentStatus to detect degraded modes.
	_, probeErr := remote.GetContentStatus(ctx, "", "")
	if isPreflightCapabilityProbeError(probeErr) {
		_, _ = fmt.Fprintln(out, "Remote capability concerns:")
		_, _ = fmt.Fprintln(out, "  content-status metadata sync disabled for this push")
	}

	// List remote pages for mutation planning.
	remotePageByID := map[string]confluence.Page{}
	space, spaceErr := remote.GetSpace(ctx, spaceKey)
	if spaceErr == nil {
		listResult, listErr := remote.ListPages(ctx, confluence.PageListOptions{
			SpaceID:  space.ID,
			SpaceKey: spaceKey,
			Status:   "current",
			Limit:    100,
		})
		if listErr == nil {
			for _, page := range listResult.Pages {
				remotePageByID[strings.TrimSpace(page.ID)] = page
			}
		}
	}

	// Load local state for attachment comparison.
	state, _ := fs.LoadState(spaceDir)

	// Build planned page mutations.
	_, _ = fmt.Fprintln(out, "Planned page mutations:")
	for _, change := range syncChanges {
		absPath := filepath.Join(spaceDir, filepath.FromSlash(change.Path))
		switch change.Type {
		case syncflow.PushChangeAdd:
			_, _ = fmt.Fprintf(out, "  add %s\n", change.Path)
		case syncflow.PushChangeDelete:
			// Look up page ID: try frontmatter first (file may still exist in worktree),
			// then fall back to the state index.
			pageID := ""
			fm, fmErr := fs.ReadFrontmatter(absPath)
			if fmErr == nil {
				pageID = strings.TrimSpace(fm.ID)
			}
			if pageID == "" {
				pageID = strings.TrimSpace(state.PagePathIndex[change.Path])
			}
			if pageID != "" {
				remotePage, hasRemote := remotePageByID[pageID]
				if hasRemote && strings.TrimSpace(remotePage.Title) != "" {
					_, _ = fmt.Fprintf(out, "  ⚠ Destructive: delete %s (page %s, %q)\n", change.Path, pageID, remotePage.Title)
				} else {
					_, _ = fmt.Fprintf(out, "  ⚠ Destructive: delete %s (page %s)\n", change.Path, pageID)
				}
			} else {
				_, _ = fmt.Fprintf(out, "  ⚠ Destructive: delete %s\n", change.Path)
			}
		case syncflow.PushChangeModify:
			fm, fmErr := fs.ReadFrontmatter(absPath)
			if fmErr != nil {
				_, _ = fmt.Fprintf(out, "  update %s\n", change.Path)
				continue
			}
			pageID := strings.TrimSpace(fm.ID)
			if pageID == "" {
				_, _ = fmt.Fprintf(out, "  update %s\n", change.Path)
				continue
			}
			remotePage, hasRemote := remotePageByID[pageID]
			if !hasRemote {
				_, _ = fmt.Fprintf(out, "  update %s (page %s)\n", change.Path, pageID)
				continue
			}
			// Compute planned version based on conflict policy.
			var plannedVersion int
			if onConflict == OnConflictForce {
				plannedVersion = remotePage.Version + 1
			} else {
				plannedVersion = fm.Version + 1
			}
			_, _ = fmt.Fprintf(out, "  update %s (page %s, %q, version %d)\n",
				change.Path, pageID, remotePage.Title, plannedVersion)
		}
	}

	// Build planned attachment mutations.
	uploads, deletes := preflightAttachmentMutations(ctx, spaceDir, syncChanges, state)
	if len(uploads) > 0 || len(deletes) > 0 {
		_, _ = fmt.Fprintln(out, "Planned attachment mutations:")
		for _, u := range uploads {
			_, _ = fmt.Fprintf(out, "  upload %s\n", u)
		}
		for _, d := range deletes {
			_, _ = fmt.Fprintf(out, "  delete %s\n", d)
		}
	}

	addCount, modifyCount, deleteCount := summarizePushChanges(syncChanges)
	_, _ = fmt.Fprintf(out, "changes: %d (A:%d M:%d D:%d)\n", len(syncChanges), addCount, modifyCount, deleteCount)
	if deleteCount > 0 {
		_, _ = fmt.Fprintln(out, "Destructive operations in this push:")
		for _, change := range syncChanges {
			if change.Type != syncflow.PushChangeDelete {
				continue
			}
			absPath := filepath.Join(spaceDir, filepath.FromSlash(change.Path))
			pageID := ""
			fm, fmErr := fs.ReadFrontmatter(absPath)
			if fmErr == nil {
				pageID = strings.TrimSpace(fm.ID)
			}
			if pageID == "" {
				pageID = strings.TrimSpace(state.PagePathIndex[change.Path])
			}
			if pageID != "" {
				remotePage, hasRemote := remotePageByID[pageID]
				if hasRemote && strings.TrimSpace(remotePage.Title) != "" {
					_, _ = fmt.Fprintf(out, "  archive %s %q (%s)\n", pageID, remotePage.Title, change.Path)
				} else {
					_, _ = fmt.Fprintf(out, "  archive %s (%s)\n", pageID, change.Path)
				}
			} else {
				_, _ = fmt.Fprintf(out, "  delete %s\n", change.Path)
			}
		}
	}
	if len(syncChanges) > 10 || deleteCount > 0 {
		_, _ = fmt.Fprintln(out, "safety confirmation would be required")
	}
	return nil
}

// isPreflightCapabilityProbeError reports whether err indicates the remote
// does not support the probed API endpoint (404, 405, or 501).
func isPreflightCapabilityProbeError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *confluence.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

// preflightAttachmentMutations returns planned upload and delete paths for
// attachments based on changed markdown files and the current state.
func preflightAttachmentMutations(
	_ context.Context,
	spaceDir string,
	syncChanges []syncflow.PushFileChange,
	state fs.SpaceState,
) (uploads, deletes []string) {
	plannedUploadKeys := map[string]struct{}{}

	for _, change := range syncChanges {
		if change.Type == syncflow.PushChangeDelete {
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(change.Path))
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			continue
		}

		pageID := strings.TrimSpace(doc.Frontmatter.ID)
		if pageID == "" {
			continue
		}

		referencedPaths, err := syncflow.CollectReferencedAssetPaths(spaceDir, absPath, doc.Body)
		if err != nil {
			continue
		}

		for _, assetPath := range referencedPaths {
			// Compute the planned state key: assets/<pageID>/<basename>
			plannedKey := filepath.ToSlash(filepath.Join("assets", pageID, filepath.Base(assetPath)))
			plannedUploadKeys[plannedKey] = struct{}{}

			// If this key is not in the state, it's a new upload.
			if strings.TrimSpace(state.AttachmentIndex[plannedKey]) == "" {
				uploads = append(uploads, plannedKey)
			}
		}

		// Check existing state entries for this page — anything not covered
		// by a planned upload is stale and will be deleted.
		prefix := "assets/" + pageID + "/"
		for stateKey := range state.AttachmentIndex {
			if !strings.HasPrefix(stateKey, prefix) {
				continue
			}
			if _, covered := plannedUploadKeys[stateKey]; !covered {
				deletes = append(deletes, stateKey)
			}
		}
	}

	sort.Strings(uploads)
	sort.Strings(deletes)
	return uploads, deletes
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
		_, _ = fmt.Fprintln(out, "push completed: no local markdown changes detected since last sync (no-op)")
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

	remote := &dryRunPushRemote{inner: realRemote, out: out, domain: cfg.Domain, emitOperations: true}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Build global page index from the original space dir so cross-space
	// links resolve correctly.
	globalPageIndex, err := buildWorkspaceGlobalPageIndex(spaceDir)
	if err != nil {
		return fmt.Errorf("build global page index: %w", err)
	}

	var progress syncflow.Progress
	if !flagVerbose && outputSupportsProgress(out) {
		progress = newConsoleProgress(out, "[DRY-RUN] Syncing to Confluence")
	}

	// Use the original space dir directly — the DryRun flag prevents local
	// file writes, and the dryRunPushRemote prevents remote writes. This
	// keeps sibling space directories accessible for cross-space link
	// resolution.
	result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
		SpaceKey:            spaceKey,
		SpaceDir:            spaceDir,
		Domain:              cfg.Domain,
		State:               state,
		GlobalPageIndex:     globalPageIndex,
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

// printDestructivePushPreview prints a human-readable list of pages that will
// be archived/deleted by the push. It is called before the safety confirmation
// prompt so the operator can see exactly what will be removed.
func printDestructivePushPreview(out io.Writer, changes []syncflow.PushFileChange, spaceDir string, state fs.SpaceState) {
	hasDelete := false
	for _, change := range changes {
		if change.Type == syncflow.PushChangeDelete {
			hasDelete = true
			break
		}
	}
	if !hasDelete {
		return
	}

	_, _ = fmt.Fprintln(out, "Destructive operations in this push:")
	for _, change := range changes {
		if change.Type != syncflow.PushChangeDelete {
			continue
		}
		pageID := ""
		absPath := filepath.Join(spaceDir, filepath.FromSlash(change.Path))
		fm, fmErr := fs.ReadFrontmatter(absPath)
		if fmErr == nil {
			pageID = strings.TrimSpace(fm.ID)
		}
		if pageID == "" {
			pageID = strings.TrimSpace(state.PagePathIndex[change.Path])
		}
		if pageID != "" {
			_, _ = fmt.Fprintf(out, "  archive page %s (%s)\n", pageID, change.Path)
		} else {
			_, _ = fmt.Fprintf(out, "  delete %s\n", change.Path)
		}
	}
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
