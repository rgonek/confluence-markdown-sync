package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPush_BlocksImmutableIDTampering(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "2",

			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := &fakeFolderPushRemote{
		foldersByID: map[string]confluence.Folder{},
	}

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
		t.Fatal("expected immutable id validation error")
	}
	if !strings.Contains(err.Error(), "changed immutable id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_IgnoresFrontmatterSpace(t *testing.T) {
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
		Status:  "current",
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
	if err != nil {
		t.Fatalf("expected push success with ignored space key, got: %v", err)
	}
}

func TestPush_BlocksCurrentToDraftTransition(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version: 1,
			State:   "draft",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := &fakeFolderPushRemote{
		foldersByID: map[string]confluence.Folder{},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Root",
				Status:  "current",
				Version: 1,
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
		pages: []confluence.Page{{
			ID:      "1",
			SpaceID: "space-1",
			Title:   "Root",
			Status:  "current",
			Version: 1,
		}},
	}

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
		t.Fatal("expected current-to-draft validation error")
	}
	if !strings.Contains(err.Error(), "cannot be transitioned from current to draft") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(mdPath); statErr != nil {
		t.Fatalf("markdown file should remain present: %v", statErr)
	}
}

func TestPush_RetriesUpdateWhenHierarchyParentIsStale(t *testing.T) {
	spaceDir := t.TempDir()
	rootDir := filepath.Join(spaceDir, "Root")
	if err := os.MkdirAll(rootDir, 0o750); err != nil {
		t.Fatalf("mkdir root dir: %v", err)
	}

	rootPath := filepath.Join(rootDir, "Root.md")
	if err := fs.WriteMarkdownDocument(rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "stale-parent",

			Version: 1,
		},
		Body: "root\n",
	}); err != nil {
		t.Fatalf("write Root.md: %v", err)
	}

	childPath := filepath.Join(rootDir, "Child.md")
	if err := fs.WriteMarkdownDocument(childPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Child",
			ID:    "child-1",

			Version: 1,
		},
		Body: "child\n",
	}); err != nil {
		t.Fatalf("write Child.md: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.rejectParentID = "stale-parent"
	remote.pagesByID["child-1"] = confluence.Page{
		ID:           "child-1",
		SpaceID:      "space-1",
		ParentPageID: "parent-live",
		Title:        "Child",
		Status:       "current",
		Version:      1,
		BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pagesByID["parent-live"] = confluence.Page{
		ID:      "parent-live",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["child-1"], remote.pagesByID["parent-live"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"Root/Child.md": "child-1",
				"Root/Root.md":  "stale-parent",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "Root/Child.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if remote.updatePageCalls != 2 {
		t.Fatalf("update page calls = %d, want 2 (initial attempt + retry)", remote.updatePageCalls)
	}
	if len(remote.updateCallInputs) != 2 {
		t.Fatalf("update input calls = %d, want 2", len(remote.updateCallInputs))
	}
	if got := strings.TrimSpace(remote.updateCallInputs[0].ParentPageID); got != "stale-parent" {
		t.Fatalf("initial parent = %q, want stale-parent", got)
	}
	if got := strings.TrimSpace(remote.updateCallInputs[1].ParentPageID); got != "parent-live" {
		t.Fatalf("retry parent = %q, want parent-live", got)
	}

	foundRetryDiag := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "UPDATE_RETRIED_AFTER_NOT_FOUND" {
			foundRetryDiag = true
			break
		}
	}
	if !foundRetryDiag {
		t.Fatalf("expected UPDATE_RETRIED_AFTER_NOT_FOUND diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_NewPageWithContentStatusSyncsLozenge(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:  "New",
			Status: "Ready to review",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
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

	pageID := strings.TrimSpace(result.State.PagePathIndex["new.md"])
	if pageID == "" {
		t.Fatalf("expected new page id in state, got %+v", result.State.PagePathIndex)
	}
	if got := strings.TrimSpace(remote.contentStatuses[pageID]); got != "Ready to review" {
		t.Fatalf("content status = %q, want Ready to review", got)
	}
	if len(remote.setContentStatusArgs) != 1 {
		t.Fatalf("set content status args = %d, want 1", len(remote.setContentStatusArgs))
	}
	if got := remote.setContentStatusArgs[0]; got.PageStatus != "current" || got.StatusName != "Ready to review" {
		t.Fatalf("unexpected content status call: %+v", got)
	}
}

func TestPush_ExistingPageCanSetAndClearContentStatus(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	writeDoc := func(status string) {
		t.Helper()
		if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
			Frontmatter: fs.Frontmatter{
				Title:   "Root",
				ID:      "1",
				Version: 1,
				Status:  status,
			},
			Body: "content\n",
		}); err != nil {
			t.Fatalf("write markdown: %v", err)
		}
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	writeDoc("In progress")
	if _, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	}); err != nil {
		t.Fatalf("Push() set status unexpected error: %v", err)
	}

	if len(remote.setContentStatusArgs) != 1 {
		t.Fatalf("set content status args = %d, want 1", len(remote.setContentStatusArgs))
	}
	if got := remote.setContentStatusArgs[0]; got.PageStatus != "current" || got.StatusName != "In progress" {
		t.Fatalf("unexpected set content status call: %+v", got)
	}

	writeDoc("")
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.contentStatuses["1"] = "In progress"

	if _, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	}); err != nil {
		t.Fatalf("Push() clear status unexpected error: %v", err)
	}

	if len(remote.deleteContentStatusArgs) != 1 {
		t.Fatalf("delete content status args = %d, want 1", len(remote.deleteContentStatusArgs))
	}
	if got := remote.deleteContentStatusArgs[0]; got.PageStatus != "current" {
		t.Fatalf("unexpected delete content status call: %+v", got)
	}
	if got := strings.TrimSpace(remote.contentStatuses["1"]); got != "" {
		t.Fatalf("content status after clear = %q, want empty", got)
	}
}
