package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func ensureWorkspaceSyncReady(action string) error {
	paths, err := listWorkspaceUnmergedPaths()
	if err != nil {
		return translateWorkspaceGitError(err, action)
	}
	if len(paths) == 0 {
		return nil
	}

	return fmt.Errorf(
		"the workspace is currently in a syncing state with unresolved files (%s). finish reconciling these files, then rerun `conf %s`",
		summarizePaths(paths, 3),
		strings.TrimSpace(action),
	)
}

func listWorkspaceUnmergedPaths() ([]string, error) {
	gitClient, err := git.NewClient()
	if err != nil {
		// Commands that rely on git will return their own explicit errors.
		return nil, nil
	}

	raw, err := gitClient.Run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(path))
	}

	return paths, nil
}

func summarizePaths(paths []string, max int) string {
	if len(paths) == 0 {
		return ""
	}
	if max <= 0 || len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(paths[:max], ", "), len(paths)-max)
}

func translateWorkspaceGitError(err error, action string) error {
	if err == nil {
		return nil
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "could not write index") ||
		strings.Contains(message, "needs merge") ||
		strings.Contains(message, "unmerged") {
		return fmt.Errorf(
			"the workspace is currently in a syncing state with unresolved files. finish reconciling your pending edits, then rerun `conf %s`",
			strings.TrimSpace(action),
		)
	}

	return err
}
