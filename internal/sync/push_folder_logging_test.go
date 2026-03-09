package sync

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestFolderListFallbackTracker_SuppressesRepeatedWarnings(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	tracker := newFolderListFallbackTracker()
	err := &confluence.APIError{
		StatusCode: 500,
		Method:     "GET",
		URL:        "/wiki/api/v2/folders",
		Message:    "Internal Server Error",
	}

	tracker.Report("space-scan", err)
	tracker.Report("Parent/Child", err)
	tracker.Report("Parent/Grandchild", err)

	got := logs.String()
	if strings.Count(got, "folder_list_unavailable_falling_back_to_pages") != 1 {
		t.Fatalf("expected one warning log, got:\n%s", got)
	}
	if strings.Count(got, "folder_list_unavailable_repeats_suppressed") != 1 {
		t.Fatalf("expected one suppression log, got:\n%s", got)
	}
	if strings.Contains(got, "repeat_count=2") {
		t.Fatalf("suppression log should only be emitted once, got:\n%s", got)
	}
	if !strings.Contains(got, "upstream endpoint failure") {
		t.Fatalf("expected upstream failure cause in logs, got:\n%s", got)
	}
	if !strings.Contains(got, "page-based hierarchy fallback") {
		t.Fatalf("expected clearer fallback note, got:\n%s", got)
	}
}

func TestFolderListFallbackTracker_LogsUnsupportedCapabilityCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	tracker := newFolderListFallbackTracker()
	err := &confluence.APIError{
		StatusCode: 501,
		Method:     "GET",
		URL:        "/wiki/api/v2/folders",
		Message:    "Not Implemented",
	}

	tracker.Report("space-scan", err)

	got := logs.String()
	if !strings.Contains(got, "unsupported tenant capability") {
		t.Fatalf("expected unsupported capability cause in logs, got:\n%s", got)
	}
}
