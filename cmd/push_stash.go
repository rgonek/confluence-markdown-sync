package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

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
