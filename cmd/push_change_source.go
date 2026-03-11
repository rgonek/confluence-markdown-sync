package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

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
