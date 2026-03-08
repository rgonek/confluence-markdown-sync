package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManagedSnapshotRefForSyncBranch(t *testing.T) {
	t.Run("managed branch", func(t *testing.T) {
		ref, ok := managedSnapshotRefForSyncBranch("sync/ENG/20260305T211238Z")
		if !ok {
			t.Fatal("expected managed sync branch")
		}
		if ref != "refs/confluence-sync/snapshots/ENG/20260305T211238Z" {
			t.Fatalf("unexpected snapshot ref %q", ref)
		}
	})

	t.Run("unmanaged branch", func(t *testing.T) {
		if _, ok := managedSnapshotRefForSyncBranch("sync/test"); ok {
			t.Fatal("expected unmanaged sync branch to be ignored")
		}
	})
}

func TestBuildCleanSyncPlan(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	removableWorktree := filepath.Join(repo, ".confluence-worktrees", "ENG-20260305T211238Z")
	if err := ensureDir(removableWorktree); err != nil {
		t.Fatalf("create removable worktree: %v", err)
	}

	activeWorktree := filepath.Join(repo, ".active-recovery")
	if err := ensureDir(activeWorktree); err != nil {
		t.Fatalf("create active worktree: %v", err)
	}

	snapshotRefs := []string{
		"refs/confluence-sync/snapshots/ENG/20260305T211238Z",
		"refs/confluence-sync/snapshots/OPS/20260305T211239Z",
		"refs/confluence-sync/snapshots/QA/20260305T211240Z",
	}
	syncBranches := []string{
		"sync/ENG/20260305T211238Z",
		"sync/OPS/20260305T211239Z",
		"sync/QA/20260305T211240Z",
		"sync/manual",
	}
	worktreeBranches := map[string][]string{
		"sync/ENG/20260305T211238Z": {removableWorktree},
		"sync/OPS/20260305T211239Z": {activeWorktree},
	}

	plan := buildCleanSyncPlan("sync/QA/20260305T211240Z", []string{removableWorktree}, snapshotRefs, syncBranches, worktreeBranches)

	gotDeleteRefs := append([]string(nil), plan.DeleteSnapshotRefs...)
	gotRetainedRefs := append([]string(nil), plan.RetainedSnapshotRefs...)
	gotDeleteBranches := make([]string, 0, len(plan.DeleteBranches))
	for _, branch := range plan.DeleteBranches {
		gotDeleteBranches = append(gotDeleteBranches, branch.Name)
	}
	gotSkipped := make(map[string]string, len(plan.SkippedBranches))
	for _, branch := range plan.SkippedBranches {
		gotSkipped[branch.Name] = branch.Reason
	}

	if !reflect.DeepEqual(gotDeleteRefs, []string{
		"refs/confluence-sync/snapshots/ENG/20260305T211238Z",
	}) {
		t.Fatalf("DeleteSnapshotRefs = %#v", gotDeleteRefs)
	}
	if !reflect.DeepEqual(gotRetainedRefs, []string{
		"refs/confluence-sync/snapshots/OPS/20260305T211239Z",
		"refs/confluence-sync/snapshots/QA/20260305T211240Z",
	}) {
		t.Fatalf("RetainedSnapshotRefs = %#v", gotRetainedRefs)
	}
	if !reflect.DeepEqual(gotDeleteBranches, []string{
		"sync/ENG/20260305T211238Z",
	}) {
		t.Fatalf("DeleteBranches = %#v", gotDeleteBranches)
	}
	if !strings.HasPrefix(gotSkipped["sync/OPS/20260305T211239Z"], "linked worktree remains at ") {
		t.Fatalf("unexpected OPS skip reason %q", gotSkipped["sync/OPS/20260305T211239Z"])
	}
	if gotSkipped["sync/QA/20260305T211240Z"] != "current HEAD is on this sync branch" {
		t.Fatalf("unexpected QA skip reason %q", gotSkipped["sync/QA/20260305T211240Z"])
	}
	if gotSkipped["sync/manual"] != "branch does not match managed sync/<SpaceKey>/<UTC timestamp> format" {
		t.Fatalf("unexpected manual skip reason %q", gotSkipped["sync/manual"])
	}
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o750)
}
