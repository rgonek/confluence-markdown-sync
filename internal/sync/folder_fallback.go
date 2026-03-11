package sync

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

const (
	folderLookupUnavailablePath = "folder hierarchy"
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
		cause := folderFallbackCauseLabel(err)
		slog.Warn(
			"folder_lookup_unavailable_falling_back_to_pages",
			"scope", scope,
			"path", path,
			"cause", cause,
			"error", err.Error(),
			"note", fmt.Sprintf("continuing with page-based hierarchy fallback because of %s; repeated folder lookup failures in this run will be suppressed", cause),
		)
		return PullDiagnostic{
			Path:    folderLookupUnavailablePath,
			Code:    "FOLDER_LOOKUP_UNAVAILABLE",
			Message: folderLookupUnavailableMessage(err),
		}, true
	}

	if announceSuppression {
		slog.Info(
			"folder_lookup_unavailable_repeats_suppressed",
			"scope", scope,
			"path", path,
			"cause", folderFallbackCauseLabel(err),
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
			folderFallbackFailureClass(apiErr.URL),
			strconv.Itoa(apiErr.StatusCode),
			strings.TrimSpace(apiErr.Message),
		}, "|")
	}
	return strings.TrimSpace(err.Error())
}

func folderFallbackFailureClass(rawURL string) string {
	path := normalizeFolderFallbackURLPath(rawURL)
	switch {
	case path == "/wiki/api/v2/folders":
		return "folder-list"
	case strings.HasPrefix(path, "/wiki/api/v2/folders/"):
		return "folder-item"
	case path != "":
		return path
	default:
		return "unknown-folder-api"
	}
}

func normalizeFolderFallbackURLPath(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}

	if parsed, err := url.Parse(rawURL); err == nil && strings.TrimSpace(parsed.Path) != "" {
		rawURL = parsed.Path
	} else if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}

	rawURL = strings.TrimSpace(rawURL)
	rawURL = strings.TrimSuffix(rawURL, "/")
	return rawURL
}

func folderLookupUnavailableMessage(err error) string {
	switch folderFallbackCause(err) {
	case folderFallbackCauseUnsupportedCapability:
		return "compatibility mode active: tenant does not support the folder API; falling back to page-only hierarchy for affected pages"
	default:
		return "compatibility mode active: folder API endpoint failed upstream; falling back to page-only hierarchy for affected pages"
	}
}

func folderCompatibilityModeMessage(err error) string {
	switch folderFallbackCause(err) {
	case folderFallbackCauseUnsupportedCapability:
		return "compatibility mode active: tenant does not support the folder API; using page-based hierarchy mode for this push"
	default:
		return "compatibility mode active: folder API endpoint failed upstream; using page-based hierarchy mode for this push"
	}
}

type folderFallbackCauseKind string

const (
	folderFallbackCauseUnsupportedCapability folderFallbackCauseKind = "unsupported tenant capability"
	folderFallbackCauseUpstreamFailure       folderFallbackCauseKind = "upstream endpoint failure"
	folderFallbackCauseSemanticConflict      folderFallbackCauseKind = "folder semantic conflict"
	folderFallbackCauseMissingFolder         folderFallbackCauseKind = "missing folder"
)

func folderFallbackCause(err error) folderFallbackCauseKind {
	var apiErr *confluence.APIError
	switch {
	case err == nil:
		return folderFallbackCauseUpstreamFailure
	case errors.Is(err, confluence.ErrNotFound):
		return folderFallbackCauseMissingFolder
	case isCompatibilityProbeError(err):
		return folderFallbackCauseUnsupportedCapability
	case errors.As(err, &apiErr) && (apiErr.StatusCode == 400 || apiErr.StatusCode == 409):
		return folderFallbackCauseSemanticConflict
	default:
		return folderFallbackCauseUpstreamFailure
	}
}

func folderFallbackCauseLabel(err error) string {
	return string(folderFallbackCause(err))
}

func shouldDegradeFolderLookupError(err error) bool {
	switch folderFallbackCause(err) {
	case folderFallbackCauseMissingFolder, folderFallbackCauseUnsupportedCapability, folderFallbackCauseUpstreamFailure:
		return true
	default:
		return false
	}
}

func isFolderDowngradeCandidate(err error) bool {
	switch folderFallbackCause(err) {
	case folderFallbackCauseUnsupportedCapability:
		return true
	case folderFallbackCauseSemanticConflict:
		lower := strings.ToLower(strings.TrimSpace(err.Error()))
		return strings.Contains(lower, "title already exists") ||
			strings.Contains(lower, "already exists") ||
			strings.Contains(lower, "duplicate") ||
			strings.Contains(lower, "conflict")
	default:
		return false
	}
}
