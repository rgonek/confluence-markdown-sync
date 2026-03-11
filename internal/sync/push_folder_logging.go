package sync

import (
	"log/slog"
	"strings"
	"sync"
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
		cause := folderFallbackCauseLabel(err)
		slog.Warn(
			"folder_api_unavailable_falling_back_to_pages",
			"scope", scope,
			"cause", cause,
			"error", err.Error(),
			"note", "continuing with page-based hierarchy fallback because of "+cause+"; repeated folder API failures in this push will be suppressed",
		)
		return
	}

	if announceSuppression {
		slog.Info(
			"folder_api_unavailable_repeats_suppressed",
			"scope", scope,
			"cause", folderFallbackCauseLabel(err),
			"error", err.Error(),
			"repeat_count", state.count-1,
		)
	}
}
