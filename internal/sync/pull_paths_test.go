package sync

import (
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestPlanPagePaths_MaintainsConfluenceHierarchy(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Root"},
		{ID: "2", Title: "Child", ParentPageID: "1"},
		{ID: "3", Title: "Grand Child", ParentPageID: "2"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages, nil)

	if got := relByID["1"]; got != "Root/Root.md" {
		t.Fatalf("root path = %q, want Root/Root.md", got)
	}
	if got := relByID["2"]; got != "Root/Child/Child.md" {
		t.Fatalf("child path = %q, want Root/Child/Child.md", got)
	}
	if got := relByID["3"]; got != "Root/Child/Grand-Child.md" {
		t.Fatalf("grandchild path = %q, want Root/Child/Grand-Child.md", got)
	}
}

func TestPlanPagePaths_FallsBackToTopLevelWhenParentMissing(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "2", Title: "Child", ParentPageID: "missing-parent"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages, nil)

	if got := relByID["2"]; got != "Child.md" {
		t.Fatalf("fallback path = %q, want Child.md", got)
	}
}

func TestPlanPagePaths_UsesFolderHierarchy(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Start Here", ParentPageID: "folder-2", ParentType: "folder"},
	}
	folderByID := map[string]confluence.Folder{
		"folder-1": {ID: "folder-1", Title: "Policies", ParentID: ""},
		"folder-2": {ID: "folder-2", Title: "Onboarding", ParentID: "folder-1"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages, folderByID)

	if got := relByID["1"]; got != "Policies/Onboarding/Start-Here.md" {
		t.Fatalf("folder-based path = %q, want Policies/Onboarding/Start-Here.md", got)
	}
}

func TestPlanPagePaths_PreservesExistingPathWhenTitleChangesInSameParent(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Renamed Page"},
	}
	previousPageIndex := map[string]string{
		"custom-title.md": "1",
	}

	_, relByID := PlanPagePaths(spaceDir, previousPageIndex, pages, nil)

	if got := relByID["1"]; got != "custom-title.md" {
		t.Fatalf("preserved path = %q, want custom-title.md", got)
	}
}

func TestPlanPagePaths_SubtreeRootTitleRenameMovesOwnedDirectory(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Renamed Root"},
		{ID: "2", Title: "Child", ParentPageID: "1"},
	}
	previousPageIndex := map[string]string{
		"Original-Root/Original-Root.md": "1",
		"Original-Root/Child.md":         "2",
	}

	_, relByID := PlanPagePaths(spaceDir, previousPageIndex, pages, nil)

	if got := relByID["1"]; got != "Renamed-Root/Renamed-Root.md" {
		t.Fatalf("root path = %q, want Renamed-Root/Renamed-Root.md", got)
	}
	if got := relByID["2"]; got != "Renamed-Root/Child.md" {
		t.Fatalf("child path = %q, want Renamed-Root/Child.md", got)
	}
}
