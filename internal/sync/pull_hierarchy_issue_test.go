package sync

import (
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestPlanPagePaths_HierarchyWithSubpages(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Root"},
		{ID: "2", Title: "Child", ParentPageID: "1"},
		{ID: "3", Title: "Grand Child", ParentPageID: "2"},
		{ID: "4", Title: "Leaf"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages, nil)

	// Root has a child (Child), so it should be Root/Root.md
	if got := relByID["1"]; got != "Root/Root.md" {
		t.Errorf("root path = %q, want Root/Root.md", got)
	}
	// Child has a child (Grand Child), so it should be Root/Child/Child.md
	if got := relByID["2"]; got != "Root/Child/Child.md" {
		t.Errorf("child path = %q, want Root/Child/Child.md", got)
	}
	// Grand Child has no children, so it should be Root/Child/Grand-Child.md
	if got := relByID["3"]; got != "Root/Child/Grand-Child.md" {
		t.Errorf("grandchild path = %q, want Root/Child/Grand-Child.md", got)
	}
	// Leaf has no children, so it should be Leaf.md
	if got := relByID["4"]; got != "Leaf.md" {
		t.Errorf("leaf path = %q, want Leaf.md", got)
	}
}
