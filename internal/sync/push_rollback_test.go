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

func TestPush_RollbackDeletesCreatedPageAndAttachmentsOnUpdateFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	assetPath := filepath.Join(spaceDir, "assets", "new.png")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
		},
		Body: "![asset](assets/new.png)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failUpdate = true

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
	if err == nil {
		t.Fatal("expected update failure")
	}
	if !strings.Contains(err.Error(), "update page") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if remote.uploadAttachmentCalls != 1 {
		t.Fatalf("upload attachment calls = %d, want 1", remote.uploadAttachmentCalls)
	}
	if len(remote.deleteAttachmentCalls) != 1 {
		t.Fatalf("delete attachment calls = %d, want 1", len(remote.deleteAttachmentCalls))
	}
	if len(remote.deletePageCalls) != 1 {
		t.Fatalf("delete page calls = %d, want 1", len(remote.deletePageCalls))
	}
	if len(remote.deletePageOpts) != 1 {
		t.Fatalf("delete page opts = %d, want 1", len(remote.deletePageOpts))
	}
	if remote.deletePageOpts[0].Purge {
		t.Fatalf("rollback should not purge current pages: %+v", remote.deletePageOpts[0])
	}
	if remote.deletePageOpts[0].Draft {
		t.Fatalf("rollback should not use draft delete for current pages: %+v", remote.deletePageOpts[0])
	}

	hasAttachmentRollback := false
	hasPageRollback := false
	for _, diag := range result.Diagnostics {
		switch diag.Code {
		case "ROLLBACK_ATTACHMENT_DELETED":
			hasAttachmentRollback = true
		case "ROLLBACK_PAGE_DELETED":
			hasPageRollback = true
		}
	}
	if !hasAttachmentRollback {
		t.Fatalf("expected ROLLBACK_ATTACHMENT_DELETED diagnostic, got %+v", result.Diagnostics)
	}
	if !hasPageRollback {
		t.Fatalf("expected ROLLBACK_PAGE_DELETED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_RollbackRestoresMetadataOnSyncFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version: 1,
			Status:  "Ready",
			Labels:  []string{"team"},
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
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.contentStatuses["1"] = ""
	remote.labelsByPage["1"] = []string{}
	remote.failAddLabels = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeModify,
			Path: "root.md",
		}},
	})
	if err == nil {
		t.Fatal("expected metadata sync failure")
	}
	if !strings.Contains(err.Error(), "sync metadata") {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.TrimSpace(remote.contentStatuses["1"]); got != "" {
		t.Fatalf("content status after rollback = %q, want empty", got)
	}
	if len(remote.deleteContentStatusCalls) == 0 {
		t.Fatalf("expected rollback to delete content status")
	}
	if len(remote.deleteContentStatusArgs) == 0 || remote.deleteContentStatusArgs[0].PageStatus != "current" {
		t.Fatalf("expected rollback delete content status call with current state, got %+v", remote.deleteContentStatusArgs)
	}

	hasMetadataRollback := false
	hasContentStatusRollback := false
	for _, diag := range result.Diagnostics {
		switch diag.Code {
		case "ROLLBACK_METADATA_RESTORED":
			hasMetadataRollback = true
		case "ROLLBACK_CONTENT_STATUS_RESTORED":
			hasContentStatusRollback = true
		}
	}
	if !hasMetadataRollback {
		t.Fatalf("expected ROLLBACK_METADATA_RESTORED diagnostic, got %+v", result.Diagnostics)
	}
	if !hasContentStatusRollback {
		t.Fatalf("expected ROLLBACK_CONTENT_STATUS_RESTORED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_RollbackDeletesCreatedPageWhenMetadataSyncStatusFails(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:  "New",
			Status: "In progress",
			Labels: []string{"team"},
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failSetContentStatus = true

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
	if err == nil {
		t.Fatal("expected metadata sync failure")
	}
	if !strings.Contains(err.Error(), "sync metadata for new.md: set content status") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if len(remote.setContentStatusCalls) != 1 {
		t.Fatalf("set content status calls = %d, want 1", len(remote.setContentStatusCalls))
	}
	if got := remote.setContentStatusArgs[0]; got.PageID == "" || got.PageStatus != "current" || got.StatusName != "In progress" {
		t.Fatalf("unexpected set content status call: %+v", remote.setContentStatusArgs[0])
	}
	if len(remote.deletePageCalls) != 1 {
		t.Fatalf("delete page calls = %d, want 1", len(remote.deletePageCalls))
	}
	if remote.deletePageOpts[0].Purge || remote.deletePageOpts[0].Draft {
		t.Fatalf("rollback should not purge or draft-delete current page: %+v", remote.deletePageOpts[0])
	}

	hasPageRollback := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ROLLBACK_PAGE_DELETED" {
			hasPageRollback = true
			break
		}
	}
	if !hasPageRollback {
		t.Fatalf("expected ROLLBACK_PAGE_DELETED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_RollbackReportsContentStatusRestoreFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 1,
			Labels:  []string{"team"},
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:           "1",
		SpaceID:      "space-1",
		Title:        "Root",
		Status:       "current",
		Version:      1,
		ParentPageID: "",
		BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.contentStatuses["1"] = "In review"
	remote.labelsByPage["1"] = []string{}
	remote.failAddLabels = true
	remote.failSetContentStatus = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeModify,
			Path: "root.md",
		}},
	})
	if err == nil {
		t.Fatal("expected metadata sync failure")
	}
	if !strings.Contains(err.Error(), "sync metadata for root.md: add labels") {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remote.deleteContentStatusArgs) != 1 {
		t.Fatalf("delete content status calls = %d, want 1", len(remote.deleteContentStatusArgs))
	}
	if len(remote.setContentStatusArgs) != 1 {
		t.Fatalf("set content status calls = %d, want 1", len(remote.setContentStatusArgs))
	}
	if remote.setContentStatusArgs[0].StatusName != "In review" {
		t.Fatalf("unexpected rollback set content status call: %+v", remote.setContentStatusArgs[0])
	}

	if got := strings.TrimSpace(remote.contentStatuses["1"]); got != "" {
		t.Fatalf("content status after failed rollback = %q, want empty", got)
	}

	hasFailureDiag := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ROLLBACK_CONTENT_STATUS_FAILED" {
			hasFailureDiag = true
			break
		}
	}
	if !hasFailureDiag {
		t.Fatalf("expected ROLLBACK_CONTENT_STATUS_FAILED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_RollbackDeletesCreatedDraftPageWithDraftDeleteOption(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "draft.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Draft",
			State: "draft",
		},
		Body: "draft body\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failUpdate = true

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "draft.md",
		}},
	})
	if err == nil {
		t.Fatal("expected update failure")
	}

	if len(remote.deletePageOpts) != 1 {
		t.Fatalf("delete page opts = %d, want 1", len(remote.deletePageOpts))
	}
	if !remote.deletePageOpts[0].Draft {
		t.Fatalf("rollback should delete draft pages with draft=true, got %+v", remote.deletePageOpts[0])
	}
	if remote.deletePageOpts[0].Purge {
		t.Fatalf("rollback should not purge draft pages, got %+v", remote.deletePageOpts[0])
	}
}

func TestPush_RollbackRestoresPageContentOnPostUpdateFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Updated Title",
			ID:    "1",

			Version: 1,
			Labels:  []string{"team"},
		},
		Body: "new local content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	originalBody := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"remote baseline"}]}]}`)
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:           "1",
		SpaceID:      "space-1",
		Title:        "Original Title",
		ParentPageID: "parent-1",
		Status:       "draft",
		Version:      1,
		BodyADF:      append([]byte(nil), originalBody...),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.contentStatuses["1"] = ""
	remote.labelsByPage["1"] = []string{}
	remote.failAddLabels = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeModify,
			Path: "root.md",
		}},
	})
	if err == nil {
		t.Fatal("expected metadata sync failure")
	}
	if !strings.Contains(err.Error(), "sync metadata") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.updatePageCalls < 2 {
		t.Fatalf("update page calls = %d, want at least 2 (apply + rollback)", remote.updatePageCalls)
	}

	restored := remote.pagesByID["1"]
	if restored.Title != "Original Title" {
		t.Fatalf("restored title = %q, want Original Title", restored.Title)
	}
	if restored.Status != "draft" {
		t.Fatalf("restored status = %q, want draft", restored.Status)
	}
	if string(restored.BodyADF) != string(originalBody) {
		t.Fatalf("restored body = %s, want %s", string(restored.BodyADF), string(originalBody))
	}

	hasContentRollback := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ROLLBACK_PAGE_CONTENT_RESTORED" {
			hasContentRollback = true
			break
		}
	}
	if !hasContentRollback {
		t.Fatalf("expected ROLLBACK_PAGE_CONTENT_RESTORED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_DryRunSkipsRollbackAttempts(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	assetPath := filepath.Join(spaceDir, "assets", "new.png")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
		},
		Body: "![asset](assets/new.png)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failUpdate = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		DryRun:         true,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected update failure")
	}
	if !strings.Contains(err.Error(), "update page") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if remote.uploadAttachmentCalls != 1 {
		t.Fatalf("upload attachment calls = %d, want 1", remote.uploadAttachmentCalls)
	}
	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0 in dry-run", len(remote.deleteAttachmentCalls))
	}
	if len(remote.deletePageCalls) != 0 {
		t.Fatalf("delete page calls = %d, want 0 in dry-run", len(remote.deletePageCalls))
	}

	for _, diag := range result.Diagnostics {
		if strings.HasPrefix(diag.Code, "ROLLBACK_") {
			t.Fatalf("unexpected rollback diagnostic in dry-run: %+v", diag)
		}
	}
}
