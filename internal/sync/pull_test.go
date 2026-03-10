package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPull_ReportsHierarchyPathMoveDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Policies"), 0o750); err != nil {
		t.Fatalf("mkdir policies dir: %v", err)
	}

	writeDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "local child\n",
	}
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Policies", "Child.md"), writeDoc); err != nil {
		t.Fatalf("write old child doc: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	emptyADF := map[string]any{"version": 1, "type": "doc", "content": []any{}}
	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-2": {ID: "folder-2", Title: "Archive"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			PagePathIndex: map[string]string{
				"Policies/Child.md": "2",
			},
		},
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Archive", "Child.md")); err != nil {
		t.Fatalf("expected moved markdown at Archive/Child.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Policies", "Child.md")); !os.IsNotExist(err) {
		t.Fatalf("old markdown path should be deleted, stat error=%v", err)
	}

	movedDiag := findPullDiagnostic(result.Diagnostics, "PAGE_PATH_MOVED")
	if movedDiag == nil {
		t.Fatalf("expected PAGE_PATH_MOVED diagnostic, got %+v", result.Diagnostics)
	}
	if movedDiag.Path != "Policies/Child.md" {
		t.Fatalf("moved diagnostic path = %q, want Policies/Child.md", movedDiag.Path)
	}
	if !strings.Contains(movedDiag.Message, "Policies/Child.md") || !strings.Contains(movedDiag.Message, "Archive/Child.md") {
		t.Fatalf("move diagnostic message = %q, want old and new paths", movedDiag.Message)
	}
}

func TestPull_ReportsSanitizedPathMoveDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Ops"), 0o750); err != nil {
		t.Fatalf("mkdir ops dir: %v", err)
	}

	writeDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "local child\n",
	}
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Ops", "Child.md"), writeDoc); err != nil {
		t.Fatalf("write old child doc: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 13, 0, 0, 0, time.UTC)
	emptyADF := map[string]any{"version": 1, "type": "doc", "content": []any{}}
	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-1", ParentType: "folder", Version: 2, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-1": {ID: "folder-1", Title: "Ops!"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-1", ParentType: "folder", Version: 2, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			PagePathIndex: map[string]string{
				"Ops/Child.md": "2",
			},
		},
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Ops!", "Child.md")); err != nil {
		t.Fatalf("expected moved markdown at Ops!/Child.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Ops", "Child.md")); !os.IsNotExist(err) {
		t.Fatalf("old markdown path should be deleted, stat error=%v", err)
	}

	movedDiag := findPullDiagnostic(result.Diagnostics, "PAGE_PATH_MOVED")
	if movedDiag == nil {
		t.Fatalf("expected PAGE_PATH_MOVED diagnostic, got %+v", result.Diagnostics)
	}
	if movedDiag.Path != "Ops/Child.md" {
		t.Fatalf("moved diagnostic path = %q, want Ops/Child.md", movedDiag.Path)
	}
	if !strings.Contains(movedDiag.Message, "Ops/Child.md") || !strings.Contains(movedDiag.Message, "Ops!/Child.md") {
		t.Fatalf("move diagnostic message = %q, want old and new paths", movedDiag.Message)
	}
}

func findPullDiagnostic(diags []PullDiagnostic, code string) *PullDiagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}
