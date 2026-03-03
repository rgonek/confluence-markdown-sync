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
