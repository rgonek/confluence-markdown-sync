package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

type initialPullContext struct {
	spaceKey     string
	spaceDir     string
	targetPageID string
	fixedDir     bool
}

type pullImpact struct {
	changedMarkdown int
	deletedMarkdown int
	prefetchedPages []confluence.Page
}

func resolveInitialPullContext(target config.Target) (initialPullContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return initialPullContext{}, err
	}

	if target.IsFile() {
		absPath, err := filepath.Abs(target.Value)
		if err != nil {
			return initialPullContext{}, err
		}

		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return initialPullContext{}, fmt.Errorf("read target file %s: %w", target.Value, err)
		}

		pageID := strings.TrimSpace(doc.Frontmatter.ID)
		if pageID == "" {
			return initialPullContext{}, fmt.Errorf("target file %s missing id", target.Value)
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
			spaceKey:     spaceKey,
			spaceDir:     spaceDir,
			targetPageID: pageID,
			fixedDir:     true,
		}, nil
	}

	if target.Value == "" {
		// If we are in a tracked directory, use it.
		if _, err := os.Stat(filepath.Join(cwd, fs.StateFileName)); err == nil {
			state, err := fs.LoadState(cwd)
			if err == nil {
				if strings.TrimSpace(state.SpaceKey) != "" {
					return initialPullContext{
						spaceKey: state.SpaceKey,
						spaceDir: cwd,
						fixedDir: true,
					}, nil
				}
			}

			return initialPullContext{
				spaceKey: inferSpaceKeyFromDirName(cwd),
				spaceDir: cwd,
				fixedDir: true,
			}, nil
		}

		spaceDir, err := filepath.Abs(cwd)
		if err != nil {
			return initialPullContext{}, err
		}
		return initialPullContext{
			spaceKey: filepath.Base(spaceDir),
			spaceDir: spaceDir,
			fixedDir: false,
		}, nil
	}

	if info, statErr := os.Stat(target.Value); statErr == nil && info.IsDir() {
		spaceDir, err := filepath.Abs(target.Value)
		if err != nil {
			return initialPullContext{}, err
		}

		// Check if it is a tracked directory
		if _, err := os.Stat(filepath.Join(spaceDir, fs.StateFileName)); err == nil {
			state, err := fs.LoadState(spaceDir)
			if err == nil {
				if strings.TrimSpace(state.SpaceKey) != "" {
					return initialPullContext{
						spaceKey: state.SpaceKey,
						spaceDir: spaceDir,
						fixedDir: true,
					}, nil
				}
			}

			return initialPullContext{
				spaceKey: inferSpaceKeyFromDirName(spaceDir),
				spaceDir: spaceDir,
				fixedDir: true,
			}, nil
		}

		return initialPullContext{
			spaceKey: filepath.Base(spaceDir),
			spaceDir: spaceDir,
			fixedDir: true, // User explicitly provided a directory
		}, nil
	}

	spaceDir := filepath.Join(cwd, target.Value)
	if _, err := os.Stat(spaceDir); err != nil {
		// Try to find a directory that looks like "Name (KEY)"
		if items, err := os.ReadDir(cwd); err == nil {
			suffix := fmt.Sprintf("(%s)", target.Value)
			for _, item := range items {
				if item.IsDir() && strings.HasSuffix(item.Name(), suffix) {
					spaceDir = filepath.Join(cwd, item.Name())
					return initialPullContext{
						spaceKey: target.Value,
						spaceDir: spaceDir,
						fixedDir: true,
					}, nil
				}
			}
		}
	}

	spaceDir, err = filepath.Abs(spaceDir)
	if err != nil {
		return initialPullContext{}, err
	}

	return initialPullContext{
		spaceKey: target.Value,
		spaceDir: spaceDir,
		fixedDir: false,
	}, nil
}

func estimatePullImpactWithSpace(
	ctx context.Context,
	remote syncflow.PullRemote,
	space confluence.Space,
	targetPageID string,
	state fs.SpaceState,
	overlapWindow time.Duration,
	forceFull bool,
	progress syncflow.Progress,
) (pullImpact, error) {
	if progress != nil {
		progress.SetDescription("Analyzing pull impact")
	}

	pages, err := listAllPullPagesForEstimate(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: space.Key,
		Status:   "current",
		Limit:    100,
	}, progress)
	if err != nil {
		return pullImpact{}, fmt.Errorf("list pages for safety check: %w", err)
	}

	pageByID := make(map[string]confluence.Page, len(pages))
	for _, page := range pages {
		pageByID[page.ID] = page
	}

	targetPageID = strings.TrimSpace(targetPageID)
	if targetPageID != "" {
		if _, exists := pageByID[targetPageID]; !exists {
			return pullImpact{}, nil
		}
		return pullImpact{changedMarkdown: 1}, nil
	}

	deletedIDs := map[string]struct{}{}
	for _, pageID := range state.PagePathIndex {
		if pageID == "" {
			continue
		}
		if _, exists := pageByID[pageID]; !exists {
			// Check if it's a draft before assuming deletion
			page, err := remote.GetPage(ctx, pageID)
			if err != nil {
				if errors.Is(err, confluence.ErrNotFound) {
					deletedIDs[pageID] = struct{}{}
					continue
				}
				// If we can't check, assume it's still there to be safe (don't mark as deleted in estimate)
				continue
			}
			if page.SpaceID != space.ID || !syncflow.IsSyncableRemotePageStatus(page.Status) {
				deletedIDs[pageID] = struct{}{}
				continue
			}
			// It exists in the same space, probably a draft or just missing from list
		}
	}

	if forceFull {
		return pullImpact{
			changedMarkdown: len(pageByID),
			deletedMarkdown: len(deletedIDs),
		}, nil
	}

	changedIDs := map[string]struct{}{}
	if strings.TrimSpace(state.LastPullHighWatermark) == "" {
		for _, page := range pages {
			changedIDs[page.ID] = struct{}{}
		}
	} else {
		watermark, err := time.Parse(time.RFC3339, strings.TrimSpace(state.LastPullHighWatermark))
		if err != nil {
			return pullImpact{}, fmt.Errorf("parse last_pull_high_watermark: %w", err)
		}

		since := watermark.Add(-overlapWindow)
		changes, err := listAllPullChangesForEstimate(ctx, remote, confluence.ChangeListOptions{
			SpaceKey: space.Key,
			Since:    since,
			Limit:    100,
		}, progress)
		if err != nil {
			return pullImpact{}, fmt.Errorf("list incremental changes for safety check: %w", err)
		}

		for _, change := range changes {
			if _, exists := pageByID[change.PageID]; exists {
				changedIDs[change.PageID] = struct{}{}
			}
		}
	}

	return pullImpact{
		changedMarkdown: len(changedIDs),
		deletedMarkdown: len(deletedIDs),
		prefetchedPages: pages,
	}, nil
}

func cleanupFailedPullScope(repoRoot, scopePath string) {
	abortInProgressPullGitOps(repoRoot)

	if _, err := runGit(repoRoot, "restore", "--source=HEAD", "--staged", "--worktree", "--", scopePath); err != nil {
		_, _ = runGit(repoRoot, "checkout", "HEAD", "--", scopePath)
	}
	removeScopedPullGeneratedFiles(repoRoot, scopePath)
}

func abortInProgressPullGitOps(repoRoot string) {
	if hasGitRef(repoRoot, "MERGE_HEAD") {
		_, _ = runGit(repoRoot, "merge", "--abort")
	}
	if hasGitRef(repoRoot, "CHERRY_PICK_HEAD") {
		_, _ = runGit(repoRoot, "cherry-pick", "--abort")
	}
	if hasGitRef(repoRoot, "REVERT_HEAD") {
		_, _ = runGit(repoRoot, "revert", "--abort")
	}

	gitDir := filepath.Join(repoRoot, ".git")
	if dirExists(filepath.Join(gitDir, "rebase-apply")) || dirExists(filepath.Join(gitDir, "rebase-merge")) {
		_, _ = runGit(repoRoot, "rebase", "--abort")
	}
}

func hasGitRef(repoRoot, refName string) bool {
	_, err := runGit(repoRoot, "rev-parse", "--verify", "--quiet", refName)
	return err == nil
}

func removeScopedPullGeneratedFiles(repoRoot, scopePath string) {
	out, err := runGit(repoRoot, "ls-files", "--others", "--exclude-standard", "--", scopePath)
	if err != nil {
		return
	}

	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		repoPath := strings.TrimSpace(line)
		if repoPath == "" {
			continue
		}
		repoPath = filepath.ToSlash(filepath.Clean(repoPath))
		if !isPullGeneratedPath(repoPath) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(repoRoot, filepath.FromSlash(repoPath)))
	}
}

func isPullGeneratedPath(repoPath string) bool {
	normalized := strings.TrimSpace(filepath.ToSlash(filepath.Clean(repoPath)))
	if normalized == "" || normalized == "." {
		return false
	}

	if strings.EqualFold(filepath.Base(normalized), fs.StateFileName) {
		return true
	}
	if strings.HasSuffix(strings.ToLower(normalized), ".md") {
		return true
	}

	segments := strings.Split(normalized, "/")
	for _, segment := range segments {
		if strings.EqualFold(segment, "assets") {
			return true
		}
	}

	return false
}

func findSpaceDirFromFile(filePath, spaceKey string) string {
	dir := filepath.Dir(filePath)
	for {
		if filepath.Base(dir) == spaceKey {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, fs.StateFileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(filePath)
}

func inferSpaceKeyFromDirName(spaceDir string) string {
	base := strings.TrimSpace(filepath.Base(spaceDir))
	if base == "" {
		return base
	}
	if strings.HasSuffix(base, ")") {
		openIdx := strings.LastIndex(base, "(")
		if openIdx >= 0 && openIdx < len(base)-1 {
			candidate := strings.TrimSpace(base[openIdx+1 : len(base)-1])
			if candidate != "" {
				return candidate
			}
		}
	}
	return base
}

func findEnvPath(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(startDir, ".env")
}

func gitRepoRoot() (string, error) {
	root, err := runGit("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("pull requires a git repository: %w", err)
	}
	return strings.TrimSpace(root), nil
}

func gitScopePath(repoRoot, scopeDir string) (string, error) {
	normalizedRepoRoot, err := normalizeRepoPath(repoRoot)
	if err != nil {
		return "", err
	}
	normalizedScopeDir, err := normalizeRepoPath(scopeDir)
	if err != nil {
		return "", err
	}

	// Case-insensitive comparison for Windows
	isOutside := false
	rel, err := filepath.Rel(normalizedRepoRoot, normalizedScopeDir)
	if err != nil {
		isOutside = true
	} else {
		rel = filepath.Clean(rel)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			isOutside = true
		}
	}

	if isOutside {
		// Final check: if they are actually the same path or one is prefix of other (case-insensitive)
		lowerRoot := strings.ToLower(filepath.ToSlash(normalizedRepoRoot))
		lowerScope := strings.ToLower(filepath.ToSlash(normalizedScopeDir))
		if !strings.HasPrefix(lowerScope, lowerRoot) {
			return "", fmt.Errorf("space directory %s is outside repository root %s", scopeDir, repoRoot)
		}
		// If it IS a subpath but filepath.Rel failed or returned .., recalculate rel
		rel = strings.TrimPrefix(lowerScope, lowerRoot)
		rel = strings.TrimPrefix(rel, "/")
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return ".", nil
	}
	return rel, nil
}

func normalizeRepoPath(p string) (string, error) {
	absPath, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		absPath = resolvedPath
	}

	// On Windows, handle case sensitivity and short paths for comparison
	if strings.TrimSpace(absPath) != "" {
		if longPath, err := filepath.Abs(absPath); err == nil {
			absPath = longPath
		}
	}

	absPath = filepath.Clean(absPath)

	return absPath, nil
}

func listAllPullPagesForEstimate(
	ctx context.Context,
	remote syncflow.PullRemote,
	opts confluence.PageListOptions,
	progress syncflow.Progress,
) ([]confluence.Page, error) {
	result := []confluence.Page{}
	cursor := opts.Cursor
	iterations := 0
	for {
		if iterations >= maxPaginationIterations {
			return nil, fmt.Errorf("pagination loop exceeded %d iterations for space %s", maxPaginationIterations, opts.SpaceID)
		}
		iterations++
		opts.Cursor = cursor
		pageResult, err := remote.ListPages(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, pageResult.Pages...)
		if progress != nil {
			progress.Add(len(pageResult.Pages))
		}
		if strings.TrimSpace(pageResult.NextCursor) == "" || pageResult.NextCursor == cursor {
			break
		}
		cursor = pageResult.NextCursor
	}
	return result, nil
}

func listAllPullChangesForEstimate(
	ctx context.Context,
	remote syncflow.PullRemote,
	opts confluence.ChangeListOptions,
	progress syncflow.Progress,
) ([]confluence.Change, error) {
	result := []confluence.Change{}
	start := opts.Start
	iterations := 0
	for {
		if iterations >= maxPaginationIterations {
			return nil, fmt.Errorf("pagination loop exceeded %d iterations for changes since %v", maxPaginationIterations, opts.Since)
		}
		iterations++
		opts.Start = start
		changeResult, err := remote.ListChanges(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, changeResult.Changes...)
		if progress != nil {
			progress.Add(len(changeResult.Changes))
		}
		if !changeResult.HasMore {
			break
		}

		next := changeResult.NextStart
		if next <= start {
			next = start + len(changeResult.Changes)
		}
		if next <= start && opts.Limit > 0 {
			next = start + opts.Limit
		}
		if next <= start {
			break
		}
		start = next
	}
	return result, nil
}

func runGit(workdir string, args ...string) (string, error) {
	gitArgs := args
	if runtime.GOOS == "windows" {
		gitArgs = append([]string{"-c", "core.longpaths=true"}, args...)
	}

	cmd := exec.Command("git", gitArgs...) //nolint:gosec // Intentionally running git
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(gitArgs, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(gitArgs, " "), msg)
	}
	return string(out), nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
