package sync

import (
	"context"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

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
	folderIndex := map[string]string{}

	if got := resolveParentIDFromHierarchy("Root/Child.md", "page-child", "", pageIndex, folderIndex); got != "page-root" {
		t.Fatalf("parent for Root/Child.md = %q, want page-root", got)
	}

	if got := resolveParentIDFromHierarchy("Root/Sub/Leaf.md", "page-leaf", "", pageIndex, folderIndex); got != "page-sub" {
		t.Fatalf("parent for Root/Sub/Leaf.md = %q, want page-sub", got)
	}

	if got := resolveParentIDFromHierarchy("Root/Root.md", "page-root", "", pageIndex, folderIndex); got != "" {
		t.Fatalf("parent for Root/Root.md = %q, want empty", got)
	}
}

func TestResolveParentIDFromHierarchy_FallsBackToFrontmatterParent(t *testing.T) {
	pageIndex := PageIndex{
		"Policies/Onboarding/Start-Here.md": "page-start",
	}
	folderIndex := map[string]string{}

	if got := resolveParentIDFromHierarchy("Policies/Onboarding/Start-Here.md", "page-start", "folder-4623368196", pageIndex, folderIndex); got != "folder-4623368196" {
		t.Fatalf("fallback parent = %q, want folder-4623368196", got)
	}

	if got := resolveParentIDFromHierarchy("Standalone.md", "page-standalone", "", pageIndex, folderIndex); got != "" {
		t.Fatalf("standalone parent = %q, want empty", got)
	}
}

func TestEnsureFolderHierarchy_UsesIndexPageAsParent(t *testing.T) {
	remote := &fakeFolderPushRemote{foldersByID: map[string]confluence.Folder{}}
	folderIndex := map[string]string{}
	pageIndex := PageIndex{"Parent/Parent.md": "page-parent"}

	result, err := ensureFolderHierarchy(
		context.Background(),
		remote,
		"space-1",
		"Parent/Sub",
		"Parent/Sub/Child.md",
		pageIndex,
		folderIndex,
		nil,
	)
	if err != nil {
		t.Fatalf("ensureFolderHierarchy() error: %v", err)
	}

	if _, ok := result["Parent"]; ok {
		t.Fatalf("expected Parent folder to be skipped when index page exists")
	}
	if result["Parent/Sub"] == "" {
		t.Fatalf("expected Parent/Sub folder to be created")
	}
	if len(remote.folders) != 1 {
		t.Fatalf("expected exactly one created folder, got %d", len(remote.folders))
	}
	if remote.folders[0].ParentType != "page" || remote.folders[0].ParentID != "page-parent" {
		t.Fatalf("expected Parent/Sub folder parent page-parent/page, got parentID=%q parentType=%q", remote.folders[0].ParentID, remote.folders[0].ParentType)
	}
}

func TestCollapseFolderParentIfIndexPage_ReparentsChildren(t *testing.T) {
	remote := &fakeFolderPushRemote{}
	folderIndex := map[string]string{"Parent": "folder-parent"}
	remotePageByID := map[string]confluence.Page{
		"page-parent": {ID: "page-parent", ParentType: "folder", ParentPageID: "folder-parent"},
		"page-a":      {ID: "page-a", ParentType: "folder", ParentPageID: "folder-parent"},
		"page-b":      {ID: "page-b", ParentType: "folder", ParentPageID: "folder-parent"},
	}
	diagnostics := []PushDiagnostic{}

	collapseFolderParentIfIndexPage(context.Background(), remote, "Parent/Parent.md", "page-parent", folderIndex, remotePageByID, &diagnostics)

	if _, ok := folderIndex["Parent"]; ok {
		t.Fatalf("expected Parent folder index entry to be removed")
	}
	if len(remote.moves) != 2 {
		t.Fatalf("expected 2 child move operations, got %d", len(remote.moves))
	}
	if remotePageByID["page-a"].ParentType != "page" || remotePageByID["page-a"].ParentPageID != "page-parent" {
		t.Fatalf("expected page-a to be reparented under page-parent")
	}
	if remotePageByID["page-b"].ParentType != "page" || remotePageByID["page-b"].ParentPageID != "page-parent" {
		t.Fatalf("expected page-b to be reparented under page-parent")
	}

	foundCollapse := false
	for _, d := range diagnostics {
		if d.Code == "FOLDER_COLLAPSED" {
			foundCollapse = true
			break
		}
	}
	if !foundCollapse {
		t.Fatalf("expected FOLDER_COLLAPSED diagnostic")
	}
}
