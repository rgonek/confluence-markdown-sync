package sync

import (
	"path/filepath"
	"strings"
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

func TestPlanPagePaths_ReconcilesExistingPathToCanonicalTitle(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Renamed Page"},
	}
	previousPageIndex := map[string]string{
		"custom-title.md": "1",
	}

	_, relByID := PlanPagePaths(spaceDir, previousPageIndex, pages, nil)

	if got := relByID["1"]; got != "Renamed-Page.md" {
		t.Fatalf("canonical path = %q, want Renamed-Page.md", got)
	}
}

func TestPlanPagePaths_ReconcilesShortSlugToCanonicalPath(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Cross Space Target 2026-03-11-0712", ParentPageID: "10"},
		{ID: "10", Title: "Software Development"},
	}
	previousPageIndex := map[string]string{
		"Software-Development/XT-20260311-0712.md":     "1",
		"Software-Development/Software-Development.md": "10",
	}

	_, relByID := PlanPagePaths(spaceDir, previousPageIndex, pages, nil)

	if got := relByID["1"]; got != "Software-Development/Cross-Space-Target-2026-03-11-0712.md" {
		t.Fatalf("canonical child path = %q, want Software-Development/Cross-Space-Target-2026-03-11-0712.md", got)
	}
}

func TestDetectFolderTitleConflicts_FindsDuplicatePureFolders(t *testing.T) {
	spaceDir := t.TempDir()

	conflicts := DetectFolderTitleConflicts(spaceDir, []string{
		filepath.Join(spaceDir, "API", "One.md"),
		filepath.Join(spaceDir, "Guides", "API", "Two.md"),
		filepath.Join(spaceDir, "Guides", "Guides.md"),
	})

	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want 1 duplicate title", conflicts)
	}
	if conflicts[0].Title != "API" {
		t.Fatalf("conflict title = %q, want API", conflicts[0].Title)
	}
	if got := strings.Join(conflicts[0].Paths, ","); got != "API,Guides/API" {
		t.Fatalf("conflict paths = %q, want API,Guides/API", got)
	}
}

func TestDetectFolderTitleConflicts_IgnoresPageBackedDirectories(t *testing.T) {
	spaceDir := t.TempDir()

	conflicts := DetectFolderTitleConflicts(spaceDir, []string{
		filepath.Join(spaceDir, "API", "API.md"),
		filepath.Join(spaceDir, "API", "One.md"),
		filepath.Join(spaceDir, "Guides", "API", "API.md"),
		filepath.Join(spaceDir, "Guides", "API", "Two.md"),
	})

	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for page-backed directories", conflicts)
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
