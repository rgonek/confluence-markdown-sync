package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopePath_ResolvesInsideAndRejectsOutside(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	insideDir := filepath.Join(repo, "docs")
	if err := os.MkdirAll(insideDir, 0o750); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	insideFile := filepath.Join(insideDir, "page.md")
	if err := os.WriteFile(insideFile, []byte("doc"), 0o600); err != nil {
		t.Fatalf("write page.md: %v", err)
	}

	client := &Client{RootDir: repo}

	rel, err := client.ScopePath(insideFile)
	if err != nil {
		t.Fatalf("ScopePath(inside) error: %v", err)
	}
	if rel != "docs/page.md" {
		t.Fatalf("ScopePath(inside) = %q, want docs/page.md", rel)
	}

	relRoot, err := client.ScopePath(repo)
	if err != nil {
		t.Fatalf("ScopePath(root) error: %v", err)
	}
	if relRoot != "." {
		t.Fatalf("ScopePath(root) = %q, want .", relRoot)
	}

	outsideDir := t.TempDir()
	if _, err := client.ScopePath(outsideDir); err == nil {
		t.Fatal("ScopePath(outside) expected error")
	}
}

func TestDiffNameStatus_ParsesRenameAsDeleteAndAdd(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	oldPath := filepath.Join(repo, "old.md")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old.md: %v", err)
	}
	runGitForHelpers(t, repo, "add", "old.md")
	runGitForHelpers(t, repo, "commit", "-m", "add old")

	runGitForHelpers(t, repo, "mv", "old.md", "new.md")
	runGitForHelpers(t, repo, "commit", "-m", "rename file")

	client := &Client{RootDir: repo}
	statuses, err := client.DiffNameStatus("HEAD~1", "HEAD", ".")
	if err != nil {
		t.Fatalf("DiffNameStatus() error: %v", err)
	}

	seen := map[string]string{}
	for _, status := range statuses {
		seen[status.Path] = status.Code
	}
	if seen["old.md"] != "D" {
		t.Fatalf("old.md status = %q, want D", seen["old.md"])
	}
	if seen["new.md"] != "A" {
		t.Fatalf("new.md status = %q, want A", seen["new.md"])
	}
}

func TestStashScopeIfDirty_StashesAndRestoresScopedChanges(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	spaceDir := filepath.Join(repo, "Space")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	trackedPath := filepath.Join(spaceDir, "root.md")
	if err := os.WriteFile(trackedPath, []byte("v1\n"), 0o600); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	runGitForHelpers(t, repo, "add", ".")
	runGitForHelpers(t, repo, "commit", "-m", "baseline")

	if err := os.WriteFile(trackedPath, []byte("v2\n"), 0o600); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	untrackedPath := filepath.Join(spaceDir, "notes.txt")
	if err := os.WriteFile(untrackedPath, []byte("local notes\n"), 0o600); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	client := &Client{RootDir: repo}
	stashRef, err := client.StashScopeIfDirty("Space", "ENG", time.Now().UTC())
	if err != nil {
		t.Fatalf("StashScopeIfDirty() error: %v", err)
	}
	if strings.TrimSpace(stashRef) == "" {
		t.Fatal("StashScopeIfDirty() expected stash ref")
	}

	statusAfterStash, err := client.StatusPorcelain("Space")
	if err != nil {
		t.Fatalf("StatusPorcelain() error: %v", err)
	}
	if strings.TrimSpace(statusAfterStash) != "" {
		t.Fatalf("expected clean scoped status after stash, got %q", statusAfterStash)
	}

	if err := client.StashPop(stashRef); err != nil {
		t.Fatalf("StashPop() error: %v", err)
	}

	rawTracked, err := os.ReadFile(trackedPath) //nolint:gosec // temp test repo file
	if err != nil {
		t.Fatalf("read tracked file: %v", err)
	}
	if strings.TrimSpace(string(rawTracked)) != "v2" {
		t.Fatalf("tracked file content = %q, want v2", string(rawTracked))
	}

	rawUntracked, err := os.ReadFile(untrackedPath) //nolint:gosec // temp test repo file
	if err != nil {
		t.Fatalf("read untracked file: %v", err)
	}
	if strings.TrimSpace(string(rawUntracked)) != "local notes" {
		t.Fatalf("untracked file content = %q, want local notes", string(rawUntracked))
	}
}

func TestRefsBranchesWorktreesAndCommitHelpers(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	seedPath := filepath.Join(repo, "seed.txt")
	if err := os.WriteFile(seedPath, []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitForHelpers(t, repo, "add", ".")
	runGitForHelpers(t, repo, "commit", "-m", "seed")

	client := &Client{RootDir: repo}
	head, err := client.ResolveRef("HEAD")
	if err != nil {
		t.Fatalf("ResolveRef(HEAD) error: %v", err)
	}

	refName := "refs/confluence-sync/snapshots/ENG/test"
	if err := client.UpdateRef(refName, head, "test snapshot"); err != nil {
		t.Fatalf("UpdateRef() error: %v", err)
	}
	if resolved, err := client.ResolveRef(refName); err != nil || strings.TrimSpace(resolved) != strings.TrimSpace(head) {
		t.Fatalf("ResolveRef(snapshot) = %q, %v; want %q", resolved, err, head)
	}
	if err := client.DeleteRef(refName); err != nil {
		t.Fatalf("DeleteRef() error: %v", err)
	}

	if err := client.CreateBranch("sync/test", "HEAD"); err != nil {
		t.Fatalf("CreateBranch() error: %v", err)
	}

	worktreePath := filepath.Join(repo, ".worktrees", "sync-test")
	if err := client.AddWorktree(worktreePath, "sync/test"); err != nil {
		t.Fatalf("AddWorktree() error: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree should exist: %v", err)
	}

	if err := os.WriteFile(filepath.Join(worktreePath, "from-worktree.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	wtClient := &Client{RootDir: worktreePath}
	if err := wtClient.Add("from-worktree.txt"); err != nil {
		t.Fatalf("Add() error in worktree: %v", err)
	}
	if err := wtClient.Commit("worktree commit", "body"); err != nil {
		t.Fatalf("Commit() error in worktree: %v", err)
	}

	if err := client.RemoveWorktree(worktreePath); err != nil {
		t.Fatalf("RemoveWorktree() error: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path should be removed, stat error: %v", err)
	}

	if err := client.DeleteBranch("sync/test"); err != nil {
		t.Fatalf("DeleteBranch() error: %v", err)
	}
}

func TestBranchOperations(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	client := &Client{RootDir: repo}

	// Test CurrentBranch
	branch, err := client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	if branch != "main" {
		t.Fatalf("CurrentBranch() = %q, want main", branch)
	}

	// Test Create and Switch Branch (using git commands since Switch isn't in our client yet, but we want to test CurrentBranch)
	runGitForHelpers(t, repo, "checkout", "-b", "feature")
	branch, err = client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch() after checkout error: %v", err)
	}
	if branch != "feature" {
		t.Fatalf("CurrentBranch() = %q, want feature", branch)
	}

	// Test Merge
	runGitForHelpers(t, repo, "checkout", "main")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature content"), 0o600); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGitForHelpers(t, repo, "add", "feature.txt")
	runGitForHelpers(t, repo, "commit", "-m", "feature change")

	// Create another branch to merge from
	if err := client.CreateBranch("to-merge", "HEAD"); err != nil {
		t.Fatalf("CreateBranch error: %v", err)
	}

	// Switch to to-merge and make a change
	runGitForHelpers(t, repo, "checkout", "to-merge")
	if err := os.WriteFile(filepath.Join(repo, "merged.txt"), []byte("merged content"), 0o600); err != nil {
		t.Fatalf("write merged file: %v", err)
	}
	runGitForHelpers(t, repo, "add", "merged.txt")
	runGitForHelpers(t, repo, "commit", "-m", "to-merge change")

	// Back to main and merge
	runGitForHelpers(t, repo, "checkout", "main")
	if err := client.Merge("to-merge", "merge to-merge"); err != nil {
		t.Fatalf("Merge() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, "merged.txt")); err != nil {
		t.Fatalf("merged file should exist: %v", err)
	}
}

func TestCommitAndTagOperations(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	client := &Client{RootDir: repo}

	// Test AddForce
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("ignored.txt\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForHelpers(t, repo, "add", ".gitignore")
	runGitForHelpers(t, repo, "commit", "-m", "add gitignore")

	ignoredFile := filepath.Join(repo, "ignored.txt")
	if err := os.WriteFile(ignoredFile, []byte("ignored content"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	// Regular add should fail (or rather, not add the file)
	_ = client.Add("ignored.txt")
	status, _ := client.StatusPorcelain("ignored.txt")
	if strings.Contains(status, "A") || strings.Contains(status, "M") {
		t.Fatalf("ignored file should not be staged by regular Add")
	}

	// AddForce should stage it
	if err := client.AddForce("ignored.txt"); err != nil {
		t.Fatalf("AddForce() error: %v", err)
	}
	status, _ = client.StatusPorcelain("ignored.txt")
	if !strings.Contains(status, "A") {
		t.Fatalf("ignored file should be staged by AddForce, got status %q", status)
	}

	// Test Tag
	if err := client.Commit("commit for tag", ""); err != nil {
		t.Fatalf("Commit error: %v", err)
	}
	tagName := "v1.0.0"
	if err := client.Tag(tagName, "release v1.0.0"); err != nil {
		t.Fatalf("Tag() error: %v", err)
	}

	tags := runGitForHelpers(t, repo, "tag")
	if !strings.Contains(tags, tagName) {
		t.Fatalf("tag %s not found in tags: %s", tagName, tags)
	}
}

func TestStashApplyAndDrop(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	client := &Client{RootDir: repo}

	// Make a change and stash it
	filePath := filepath.Join(repo, "change.txt")
	if err := os.WriteFile(filePath, []byte("change"), 0o600); err != nil {
		t.Fatalf("write change file: %v", err)
	}
	runGitForHelpers(t, repo, "add", "change.txt")
	runGitForHelpers(t, repo, "stash", "push", "-m", "test stash")

	// Verify stashed
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatal("file should be stashed and gone")
	}

	// Apply stash
	if err := client.StashApply("stash@{0}"); err != nil {
		t.Fatalf("StashApply() error: %v", err)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatal("file should be back after StashApply")
	}

	// Drop stash
	if err := client.StashDrop("stash@{0}"); err != nil {
		t.Fatalf("StashDrop() error: %v", err)
	}
}

func TestWorktreePrune(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	client := &Client{RootDir: repo}

	// Just call it to see it doesn't fail
	if err := client.PruneWorktrees(); err != nil {
		t.Fatalf("PruneWorktrees() error: %v", err)
	}
}

func TestNewClient(t *testing.T) {
	repo := setupGitRepoForHelpers(t)
	oldWD, _ := os.Getwd()
	defer os.Chdir(oldWD)

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if !strings.EqualFold(evalFinalPath(client.RootDir), evalFinalPath(repo)) {
		t.Fatalf("NewClient RootDir = %q, want %q", client.RootDir, repo)
	}
}

func setupGitRepoForHelpers(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitForHelpers(t, repo, "init", "-b", "main")
	runGitForHelpers(t, repo, "config", "user.email", "git-helpers-test@example.com")
	runGitForHelpers(t, repo, "config", "user.name", "git-helpers-test")
	// Add initial commit so HEAD exists
	if err := os.WriteFile(filepath.Join(repo, ".initial"), []byte("init"), 0o600); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runGitForHelpers(t, repo, "add", ".initial")
	runGitForHelpers(t, repo, "commit", "-m", "initial commit")
	return repo
}

func runGitForHelpers(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
