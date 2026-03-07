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

func TestPlanPagePaths_TreatsParentTypesCaseInsensitively(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Root"},
		{ID: "2", Title: "Direct Child", ParentPageID: "1", ParentType: "PAGE"},
		{ID: "3", Title: "Folder Child", ParentPageID: "folder-1", ParentType: "folder"},
	}
	folderByID := map[string]confluence.Folder{
		"folder-1": {ID: "folder-1", Title: "Section", ParentID: "1", ParentType: "PAGE"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages, folderByID)

	if got := relByID["1"]; got != "Root/Root.md" {
		t.Fatalf("root path = %q, want Root/Root.md", got)
	}
	if got := relByID["2"]; got != "Root/Direct-Child.md" {
		t.Fatalf("direct child path = %q, want Root/Direct-Child.md", got)
	}
	if got := relByID["3"]; got != "Root/Section/Folder-Child.md" {
		t.Fatalf("folder child path = %q, want Root/Section/Folder-Child.md", got)
	}
}

func TestPull_RoundTripsMixedHierarchyToIndexPaths(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	emptyADF := map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Version: 1, LastModified: modifiedAt},
			{ID: "2", SpaceID: "space-1", Title: "Direct Child", ParentPageID: "1", ParentType: "PAGE", Version: 1, LastModified: modifiedAt},
			{ID: "3", SpaceID: "space-1", Title: "Folder Child", ParentPageID: "folder-1", ParentType: "folder", Version: 1, LastModified: modifiedAt},
			{ID: "4", SpaceID: "space-1", Title: "Nested Folder Child", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-1": {ID: "folder-1", Title: "Section", ParentID: "1", ParentType: "PAGE"},
			"folder-2": {ID: "folder-2", Title: "Subsection", ParentID: "folder-1", ParentType: "folder"},
		},
		pagesByID: map[string]confluence.Page{
			"1": {ID: "1", SpaceID: "space-1", Title: "Root", Version: 1, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
			"2": {ID: "2", SpaceID: "space-1", Title: "Direct Child", ParentPageID: "1", ParentType: "PAGE", Version: 1, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
			"3": {ID: "3", SpaceID: "space-1", Title: "Folder Child", ParentPageID: "folder-1", ParentType: "folder", Version: 1, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
			"4": {ID: "4", SpaceID: "space-1", Title: "Nested Folder Child", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			PagePathIndex: map[string]string{
				`Legacy\Root.md`: "1",
			},
			FolderPathIndex: map[string]string{
				`Legacy\Section`: "folder-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	expectedPaths := map[string]string{
		"Root/Root.md":                                   "1",
		"Root/Direct-Child.md":                           "2",
		"Root/Section/Folder-Child.md":                   "3",
		"Root/Section/Subsection/Nested-Folder-Child.md": "4",
	}
	for relPath, pageID := range expectedPaths {
		if _, err := os.Stat(filepath.Join(spaceDir, filepath.FromSlash(relPath))); err != nil {
			t.Fatalf("expected markdown at %s: %v", relPath, err)
		}
		if got := result.State.PagePathIndex[relPath]; got != pageID {
			t.Fatalf("state page_path_index[%s] = %q, want %s", relPath, got, pageID)
		}
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Root.md")); !os.IsNotExist(err) {
		t.Fatalf("Root.md should not exist at top level, stat error=%v", err)
	}

	if got := result.State.FolderPathIndex["Root/Section"]; got != "folder-1" {
		t.Fatalf("state folder_path_index[Root/Section] = %q, want folder-1", got)
	}
	if got := result.State.FolderPathIndex["Root/Section/Subsection"]; got != "folder-2" {
		t.Fatalf("state folder_path_index[Root/Section/Subsection] = %q, want folder-2", got)
	}
	for path := range result.State.FolderPathIndex {
		if strings.Contains(path, `\`) {
			t.Fatalf("folder_path_index should use slash-normalized paths, got %q", path)
		}
	}
}

func TestNormalizePullAndPushState_NormalizeAllPathIndexes(t *testing.T) {
	state := fs.SpaceState{
		PagePathIndex: map[string]string{
			`Root\Root.md`: "1",
		},
		AttachmentIndex: map[string]string{
			`assets\1\diagram.png`: "att-1",
		},
		FolderPathIndex: map[string]string{
			`Root\Section`: "folder-1",
		},
	}

	pullState := normalizePullState(state)
	if got := pullState.PagePathIndex["Root/Root.md"]; got != "1" {
		t.Fatalf("normalizePullState page path = %q, want 1", got)
	}
	if got := pullState.AttachmentIndex["assets/1/diagram.png"]; got != "att-1" {
		t.Fatalf("normalizePullState attachment path = %q, want att-1", got)
	}
	if got := pullState.FolderPathIndex["Root/Section"]; got != "folder-1" {
		t.Fatalf("normalizePullState folder path = %q, want folder-1", got)
	}

	pushState := normalizePushState(state)
	if got := pushState.PagePathIndex["Root/Root.md"]; got != "1" {
		t.Fatalf("normalizePushState page path = %q, want 1", got)
	}
	if got := pushState.AttachmentIndex["assets/1/diagram.png"]; got != "att-1" {
		t.Fatalf("normalizePushState attachment path = %q, want att-1", got)
	}
	if got := pushState.FolderPathIndex["Root/Section"]; got != "folder-1" {
		t.Fatalf("normalizePushState folder path = %q, want folder-1", got)
	}
}
