package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func TestResolvePushDeletePreview_FallsBackToStateAndKeepsRemoteTitle(t *testing.T) {
	t.Parallel()

	spaceDir := t.TempDir()
	state := fs.SpaceState{
		PagePathIndex: map[string]string{
			"root.md": "123",
		},
	}

	preview := resolvePushDeletePreview(spaceDir, state, map[string]confluence.Page{
		"123": {ID: "123", Title: "Root Page"},
	}, "root.md")

	if preview.pageID != "123" {
		t.Fatalf("preview.pageID = %q, want %q", preview.pageID, "123")
	}
	if preview.pageTitle != "Root Page" {
		t.Fatalf("preview.pageTitle = %q, want %q", preview.pageTitle, "Root Page")
	}
	if got := preview.preflightMutationLine(); got != "⚠ Destructive: archive remote page for root.md (page 123, \"Root Page\")" {
		t.Fatalf("preview.preflightMutationLine() = %q", got)
	}
	if got := preview.destructiveSummaryLine(); got != "archive remote page 123 \"Root Page\" (root.md)" {
		t.Fatalf("preview.destructiveSummaryLine() = %q", got)
	}
}

func TestPreflightAttachmentMutations_ScopesDeletesPerPage(t *testing.T) {
	t.Parallel()

	spaceDir := t.TempDir()
	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 1,
		},
		Body: "![new](assets/new.png)\n",
	})
	writeMarkdown(t, filepath.Join(spaceDir, "other.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Other",
			ID:      "2",
			Version: 1,
		},
		Body: "![keep](assets/keep.png)\n",
	})
	for _, relPath := range []string{"assets/new.png", "assets/keep.png"} {
		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", relPath, err)
		}
		if err := os.WriteFile(absPath, []byte(relPath), 0o600); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}

	state := fs.SpaceState{
		AttachmentIndex: map[string]string{
			"assets/1/old.png":  "att-old",
			"assets/2/keep.png": "att-keep",
		},
	}

	uploads, deletes := preflightAttachmentMutations(spaceDir, []syncflow.PushFileChange{
		{Type: syncflow.PushChangeModify, Path: "root.md"},
		{Type: syncflow.PushChangeModify, Path: "other.md"},
	}, state)

	if !reflect.DeepEqual(uploads, []string{"assets/1/new.png"}) {
		t.Fatalf("uploads = %#v", uploads)
	}
	if !reflect.DeepEqual(deletes, []string{"assets/1/old.png"}) {
		t.Fatalf("deletes = %#v", deletes)
	}
}

func TestToSyncPushChanges_FiltersScopeAndNonMarkdownPaths(t *testing.T) {
	t.Parallel()

	changes, err := toSyncPushChanges([]git.FileStatus{
		{Code: "M", Path: "Engineering (ENG)/root.md"},
		{Code: "A", Path: "Engineering (ENG)/nested/child.md"},
		{Code: "D", Path: "Engineering (ENG)/assets/image.png"},
		{Code: "M", Path: "Engineering (ENG)/notes.txt"},
		{Code: "M", Path: "Other/file.md"},
	}, "Engineering (ENG)")
	if err != nil {
		t.Fatalf("toSyncPushChanges() error = %v", err)
	}

	want := []syncflow.PushFileChange{
		{Type: syncflow.PushChangeModify, Path: "root.md"},
		{Type: syncflow.PushChangeAdd, Path: "nested/child.md"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
}
