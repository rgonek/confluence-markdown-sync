package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPush_NewPageFailsWhenTrackedPageWithSameTitleExistsInSameDirectory(t *testing.T) {
	spaceDir := t.TempDir()

	existingPath := filepath.Join(spaceDir, "Conflict-Test-Page.md")
	if err := fs.WriteMarkdownDocument(existingPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Conflict Test Page",
			ID:    "1",

			Version: 1,
		},
		Body: "existing\n",
	}); err != nil {
		t.Fatalf("write existing markdown: %v", err)
	}

	newPath := filepath.Join(spaceDir, "Conflict-Test.md")
	if err := fs.WriteMarkdownDocument(newPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Conflict Test Page",
		},
		Body: "new\n",
	}); err != nil {
		t.Fatalf("write new markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"Conflict-Test-Page.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "Conflict-Test.md",
		}},
	})
	if err == nil {
		t.Fatal("expected duplicate title validation error")
	}
	if !strings.Contains(err.Error(), "duplicates tracked page") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_DeleteAlreadyArchivedPageTreatsArchiveAsNoOp(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archivePagesErr = confluence.ErrArchived

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if len(result.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(result.Commits))
	}
	if _, exists := result.State.PagePathIndex["old.md"]; exists {
		t.Fatalf("page index should not contain old.md after successful archive no-op")
	}
	if len(remote.archiveTaskCalls) != 0 {
		t.Fatalf("archive task calls = %d, want 0 when archive is already applied", len(remote.archiveTaskCalls))
	}

	foundDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_ALREADY_APPLIED" {
			foundDiagnostic = true
			break
		}
	}
	if !foundDiagnostic {
		t.Fatalf("expected ARCHIVE_ALREADY_APPLIED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_DeletePageRemovesLocalTrackedAssets(t *testing.T) {
	spaceDir := t.TempDir()
	assetPath := filepath.Join(spaceDir, "assets", "1", "att-1-file.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir asset dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("asset"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archiveTaskStatus = confluence.ArchiveTaskStatus{TaskID: "task-1", State: confluence.ArchiveTaskStateSucceeded}

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if _, err := os.Stat(assetPath); !os.IsNotExist(err) {
		t.Fatalf("expected local asset to be removed during page delete, stat=%v", err)
	}
	if _, exists := result.State.AttachmentIndex["assets/1/att-1-file.png"]; exists {
		t.Fatalf("expected deleted asset to be removed from state")
	}
}

func TestPush_ArchivedRemotePageReturnsActionableError(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "archived",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected archived page error")
	}
	if !strings.Contains(err.Error(), "is archived remotely") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_DeleteBlocksLocalStateWhenArchiveTaskDoesNotComplete(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archiveTaskStatus = confluence.ArchiveTaskStatus{TaskID: "task-1", State: confluence.ArchiveTaskStateInProgress, RawStatus: "RUNNING"}
	remote.archiveTaskWaitErr = confluence.ErrArchiveTaskTimeout

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err == nil {
		t.Fatal("expected archive wait failure")
	}
	if !strings.Contains(err.Error(), "wait for archive task") {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Commits) != 0 {
		t.Fatalf("commits = %d, want 0", len(result.Commits))
	}
	if got := strings.TrimSpace(result.State.PagePathIndex["old.md"]); got != "1" {
		t.Fatalf("page index old.md = %q, want 1", got)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-1-file.png"]); got != "att-1" {
		t.Fatalf("attachment index was mutated on archive failure: %q", got)
	}
	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0", len(remote.deleteAttachmentCalls))
	}

	hasTimeoutDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_TASK_TIMEOUT" || diag.Code == "ARCHIVE_TASK_STILL_RUNNING" {
			hasTimeoutDiagnostic = true
			break
		}
	}
	if !hasTimeoutDiagnostic {
		t.Fatalf("expected archive timeout diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_DeleteTreatsArchiveAsSuccessfulWhenVerificationShowsArchived(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Status:  "current",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archiveTaskStatus = confluence.ArchiveTaskStatus{TaskID: "task-1", State: confluence.ArchiveTaskStateInProgress, RawStatus: "ENQUEUED"}
	remote.archiveTaskWaitErr = confluence.ErrArchiveTaskTimeout
	remote.waitForArchiveTaskHook = func(f *rollbackPushRemote, _ string) {
		page := f.pagesByID["1"]
		page.Status = "archived"
		f.pagesByID["1"] = page
	}

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if len(result.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(result.Commits))
	}

	found := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_CONFIRMED_AFTER_WAIT_FAILURE" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ARCHIVE_CONFIRMED_AFTER_WAIT_FAILURE diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_NewPageContentStatusPreflightFailsBeforeRemoteMutation(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:  "New",
			Status: "Unknown Status",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.contentStates = []confluence.ContentState{{ID: 80, Name: "Ready to review", Color: "FFAB00"}}

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected content status preflight failure")
	}
	if !strings.Contains(err.Error(), "content status preflight failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if remote.createPageCalls != 0 {
		t.Fatalf("create page calls = %d, want 0 after preflight failure", remote.createPageCalls)
	}
	if remote.updatePageCalls != 0 {
		t.Fatalf("update page calls = %d, want 0 after preflight failure", remote.updatePageCalls)
	}
}

func TestPush_NewPageContentStatusFallsBackWhenCatalogEndpointsUnsupported(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:  "New",
			Status: "Unlisted Status",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	compatErr := &confluence.APIError{
		StatusCode: 501,
		Method:     "GET",
		URL:        "/wiki/rest/api/content-states",
		Message:    "Not Implemented",
	}

	remote := newRollbackPushRemote()
	remote.listContentStatesErr = compatErr
	remote.listSpaceContentStatesErr = compatErr
	remote.getAvailableStatesErr = compatErr

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if len(remote.setContentStatusArgs) != 1 {
		t.Fatalf("set content status args = %d, want 1", len(remote.setContentStatusArgs))
	}
	if got := remote.setContentStatusArgs[0].StatusName; got != "Unlisted Status" {
		t.Fatalf("status name = %q, want %q", got, "Unlisted Status")
	}
	if result.State.PagePathIndex["new.md"] == "" {
		t.Fatalf("expected pushed page to be tracked, got state %+v", result.State.PagePathIndex)
	}
}

func TestPush_NewPageDuplicateTitleErrorIncludesNonCurrentCollision(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Collision"},
		Body:        "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failCreatePageErr = errors.New("A page with this title already exists")
	remote.pages = []confluence.Page{
		{ID: "draft-1", SpaceID: "space-1", Title: "Collision", Status: "draft"},
	}

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected duplicate title error")
	}
	if !strings.Contains(err.Error(), "status=draft") || !strings.Contains(err.Error(), "id=draft-1") {
		t.Fatalf("expected actionable duplicate title error, got: %v", err)
	}
}

func TestPushConflictError_Error(t *testing.T) {
	err := &PushConflictError{
		Path:          "docs/page.md",
		PageID:        "42",
		LocalVersion:  3,
		RemoteVersion: 5,
		Policy:        PushConflictPolicyCancel,
	}
	got := err.Error()
	want := "remote version conflict for docs/page.md (page 42): local=3 remote=5 policy=cancel"
	if got != want {
		t.Errorf("PushConflictError.Error() = %q, want %q", got, want)
	}
}
