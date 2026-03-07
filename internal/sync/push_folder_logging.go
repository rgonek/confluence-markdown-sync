package sync

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

type folderListFallbackTracker struct {
	mu   sync.Mutex
	seen map[string]folderListFallbackState
}

type folderListFallbackState struct {
	count                int
	suppressionAnnounced bool
}

func newFolderListFallbackTracker() *folderListFallbackTracker {
	return &folderListFallbackTracker{
		seen: map[string]folderListFallbackState{},
	}
}

func (t *folderListFallbackTracker) Report(scope string, err error) {
	if t == nil || err == nil {
		return
	}

	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "space-scan"
	}

	key := folderFallbackFingerprint(err)

	t.mu.Lock()
	state := t.seen[key]
	state.count++

	firstOccurrence := state.count == 1
	announceSuppression := false
	if !firstOccurrence && !state.suppressionAnnounced {
		state.suppressionAnnounced = true
		announceSuppression = true
	}
	t.seen[key] = state
	t.mu.Unlock()

	if firstOccurrence {
		slog.Warn(
			"folder_list_unavailable_falling_back_to_pages",
			"scope", scope,
			"error", err.Error(),
			"note", "continuing with page-based hierarchy fallback; repeated folder-list failures in this push will be suppressed",
		)
		return
	}

	if announceSuppression {
		slog.Info(
			"folder_list_unavailable_repeats_suppressed",
			"scope", scope,
			"error", err.Error(),
			"repeat_count", state.count-1,
		)
	}
}

func listAllPushFoldersWithTracking(
	ctx context.Context,
	remote PushRemote,
	opts confluence.FolderListOptions,
	tracker *folderListFallbackTracker,
	scope string,
) ([]confluence.Folder, error) {
	folders, err := listAllPushFolders(ctx, remote, opts)
	if err != nil {
		tracker.Report(scope, err)
	}
	return folders, err
}
