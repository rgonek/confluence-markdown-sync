package sync

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

const (
	folderLookupUnavailablePath    = "folder hierarchy"
	folderLookupUnavailableMessage = "folder lookup unavailable, falling back to page-only hierarchy for affected pages"
)

type FolderLookupFallbackTracker struct {
	mu   sync.Mutex
	seen map[string]folderLookupFallbackState
}

type folderLookupFallbackState struct {
	count                int
	suppressionAnnounced bool
}

func NewFolderLookupFallbackTracker() *FolderLookupFallbackTracker {
	return &FolderLookupFallbackTracker{
		seen: map[string]folderLookupFallbackState{},
	}
}

func (t *FolderLookupFallbackTracker) Report(scope string, path string, err error) (PullDiagnostic, bool) {
	if t == nil || err == nil {
		return PullDiagnostic{}, false
	}

	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = folderLookupUnavailablePath
	}

	path = strings.TrimSpace(path)
	if path == "" {
		path = folderLookupUnavailablePath
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
			"folder_lookup_unavailable_falling_back_to_pages",
			"scope", scope,
			"path", path,
			"error", err.Error(),
			"note", "continuing with page-based hierarchy fallback; repeated folder lookup failures in this run will be suppressed",
		)
		return PullDiagnostic{
			Path:    folderLookupUnavailablePath,
			Code:    "FOLDER_LOOKUP_UNAVAILABLE",
			Message: folderLookupUnavailableMessage,
		}, true
	}

	if announceSuppression {
		slog.Info(
			"folder_lookup_unavailable_repeats_suppressed",
			"scope", scope,
			"path", path,
			"error", err.Error(),
			"repeat_count", state.count-1,
		)
	}

	return PullDiagnostic{}, false
}

func folderFallbackFingerprint(err error) string {
	var apiErr *confluence.APIError
	if errors.As(err, &apiErr) {
		return strings.Join([]string{
			strings.TrimSpace(apiErr.Method),
			strings.TrimSpace(apiErr.URL),
			strconv.Itoa(apiErr.StatusCode),
			strings.TrimSpace(apiErr.Message),
		}, "|")
	}
	return strings.TrimSpace(err.Error())
}
