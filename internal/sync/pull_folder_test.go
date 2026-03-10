package sync

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestPull_FolderListFailureFallsBackToPageHierarchy(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Start Here",
			ParentPageID: "folder-1",
			ParentType:   "folder",
			Version:      1,
			LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
		}},
		folderErr: &confluence.APIError{
			StatusCode: 500,
			Method:     "GET",
			URL:        "/wiki/api/v2/folders",
			Message:    "Internal Server Error",
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Start Here",
				ParentPageID: "folder-1",
				ParentType:   "folder",
				Version:      1,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, sampleChildADF()),
			},
		},
		attachments: map[string][]byte{
			"att-2": []byte("inline-bytes"),
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Start-Here.md")); err != nil {
		t.Fatalf("expected markdown to be written at top-level fallback path: %v", err)
	}

	foundFolderWarning := false
	for _, d := range result.Diagnostics {
		if d.Code == "FOLDER_LOOKUP_UNAVAILABLE" {
			foundFolderWarning = true
			break
		}
	}
	if !foundFolderWarning {
		t.Fatalf("expected FOLDER_LOOKUP_UNAVAILABLE diagnostic, got %+v", result.Diagnostics)
	}
}

func TestResolveFolderHierarchyFromPages_DeduplicatesFallbackDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	pages := []confluence.Page{
		{ID: "1", Title: "Root", ParentPageID: "folder-1", ParentType: "folder"},
		{ID: "2", Title: "Child", ParentPageID: "folder-2", ParentType: "folder"},
	}
	errBoom := &confluence.APIError{
		StatusCode: 500,
		Method:     "GET",
		URL:        "/wiki/api/v2/folders",
		Message:    "Internal Server Error",
	}
	remote := &fakePullRemote{
		folderErr: errBoom,
	}

	_, diagnostics, err := resolveFolderHierarchyFromPages(context.Background(), remote, pages)
	if err != nil {
		t.Fatalf("resolveFolderHierarchyFromPages() error: %v", err)
	}

	fallbackDiagnostics := 0
	for _, diag := range diagnostics {
		if diag.Code != "FOLDER_LOOKUP_UNAVAILABLE" {
			continue
		}
		fallbackDiagnostics++
		if strings.Contains(diag.Message, "Internal Server Error") {
			t.Fatalf("expected concise diagnostic without raw API error, got %q", diag.Message)
		}
		if strings.Contains(diag.Message, "/wiki/api/v2/folders") {
			t.Fatalf("expected concise diagnostic without raw API URL, got %q", diag.Message)
		}
		if !strings.Contains(diag.Message, "falling back to page-only hierarchy for affected pages") {
			t.Fatalf("expected concise fallback explanation, got %q", diag.Message)
		}
	}
	if fallbackDiagnostics != 1 {
		t.Fatalf("expected one deduplicated fallback diagnostic, got %+v", diagnostics)
	}

	gotLogs := logs.String()
	if strings.Count(gotLogs, "folder_lookup_unavailable_falling_back_to_pages") != 1 {
		t.Fatalf("expected one warning log with raw error details, got:\n%s", gotLogs)
	}
	if !strings.Contains(gotLogs, "Internal Server Error") {
		t.Fatalf("expected raw error details in logs, got:\n%s", gotLogs)
	}
	if !strings.Contains(gotLogs, "/wiki/api/v2/folders") {
		t.Fatalf("expected raw API URL in logs, got:\n%s", gotLogs)
	}
}

type folderLookupErrorByIDRemote struct {
	*fakePullRemote
	errorsByFolderID map[string]error
}

func (r *folderLookupErrorByIDRemote) GetFolder(ctx context.Context, folderID string) (confluence.Folder, error) {
	r.getFolderCalls = append(r.getFolderCalls, folderID)
	if err, ok := r.errorsByFolderID[folderID]; ok {
		return confluence.Folder{}, err
	}
	return r.fakePullRemote.GetFolder(ctx, folderID)
}

func TestResolveFolderHierarchyFromPages_DeduplicatesFallbackDiagnosticsAcrossFolderURLs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	pages := []confluence.Page{
		{ID: "1", Title: "Root", ParentPageID: "folder-1", ParentType: "folder"},
		{ID: "2", Title: "Child", ParentPageID: "folder-2", ParentType: "folder"},
	}
	remote := &folderLookupErrorByIDRemote{
		fakePullRemote: &fakePullRemote{},
		errorsByFolderID: map[string]error{
			"folder-1": &confluence.APIError{
				StatusCode: 500,
				Method:     "GET",
				URL:        "/wiki/api/v2/folders/folder-1",
				Message:    "Internal Server Error",
			},
			"folder-2": &confluence.APIError{
				StatusCode: 500,
				Method:     "GET",
				URL:        "/wiki/api/v2/folders/folder-2",
				Message:    "Internal Server Error",
			},
		},
	}

	_, diagnostics, err := resolveFolderHierarchyFromPages(context.Background(), remote, pages)
	if err != nil {
		t.Fatalf("resolveFolderHierarchyFromPages() error: %v", err)
	}

	fallbackDiagnostics := 0
	for _, diag := range diagnostics {
		if diag.Code == "FOLDER_LOOKUP_UNAVAILABLE" {
			fallbackDiagnostics++
		}
	}
	if fallbackDiagnostics != 1 {
		t.Fatalf("expected one deduplicated fallback diagnostic across folder URLs, got %+v", diagnostics)
	}

	gotLogs := logs.String()
	if count := strings.Count(gotLogs, "folder_lookup_unavailable_falling_back_to_pages"); count != 1 {
		t.Fatalf("expected one warning log across folder URLs, got %d:\n%s", count, gotLogs)
	}
	if count := strings.Count(gotLogs, "folder_lookup_unavailable_repeats_suppressed"); count != 1 {
		t.Fatalf("expected one suppression log across folder URLs, got %d:\n%s", count, gotLogs)
	}
}
