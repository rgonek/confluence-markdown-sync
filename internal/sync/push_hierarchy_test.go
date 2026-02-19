package sync

import "testing"

func TestResolveParentIDFromHierarchy_UsesNearestAncestorIndexPage(t *testing.T) {
	pageIndex := PageIndex{
		"Root/Root.md":        "page-root",
		"Root/Child.md":       "page-child",
		"Root/Sub/Sub.md":     "page-sub",
		"Root/Sub/Leaf.md":    "page-leaf",
		"Standalone.md":       "page-standalone",
		"Folder/Entry.md":     "page-folder-entry",
		"Folder/Folder.md":    "page-folder-index",
		"Folder/Other/Doc.md": "page-other",
	}

	if got := resolveParentIDFromHierarchy("Root/Child.md", "page-child", "", pageIndex); got != "page-root" {
		t.Fatalf("parent for Root/Child.md = %q, want page-root", got)
	}

	if got := resolveParentIDFromHierarchy("Root/Sub/Leaf.md", "page-leaf", "", pageIndex); got != "page-sub" {
		t.Fatalf("parent for Root/Sub/Leaf.md = %q, want page-sub", got)
	}

	if got := resolveParentIDFromHierarchy("Root/Root.md", "page-root", "", pageIndex); got != "" {
		t.Fatalf("parent for Root/Root.md = %q, want empty", got)
	}
}

func TestResolveParentIDFromHierarchy_FallsBackToFrontmatterParent(t *testing.T) {
	pageIndex := PageIndex{
		"Policies/Onboarding/Start-Here.md": "page-start",
	}

	if got := resolveParentIDFromHierarchy("Policies/Onboarding/Start-Here.md", "page-start", "folder-4623368196", pageIndex); got != "folder-4623368196" {
		t.Fatalf("fallback parent = %q, want folder-4623368196", got)
	}

	if got := resolveParentIDFromHierarchy("Standalone.md", "page-standalone", "", pageIndex); got != "" {
		t.Fatalf("standalone parent = %q, want empty", got)
	}
}
