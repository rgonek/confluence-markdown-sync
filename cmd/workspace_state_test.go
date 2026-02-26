package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWorkspaceSyncReady_BlocksUnmergedWorkspace(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	notesPath := filepath.Join(repo, "notes.md")
	if err := os.WriteFile(notesPath, []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGitForTest(t, repo, "add", "notes.md")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	runGitForTest(t, repo, "checkout", "-b", "feature/conflict")
	if err := os.WriteFile(notesPath, []byte("feature branch change\n"), 0o600); err != nil {
		t.Fatalf("write feature change: %v", err)
	}
	runGitForTest(t, repo, "add", "notes.md")
	runGitForTest(t, repo, "commit", "-m", "feature change")

	runGitForTest(t, repo, "checkout", "main")
	if err := os.WriteFile(notesPath, []byte("main branch change\n"), 0o600); err != nil {
		t.Fatalf("write main change: %v", err)
	}
	runGitForTest(t, repo, "add", "notes.md")
	runGitForTest(t, repo, "commit", "-m", "main change")

	mergeCmd := exec.Command("git", "merge", "feature/conflict")
	mergeCmd.Dir = repo
	if mergeOut, err := mergeCmd.CombinedOutput(); err == nil {
		t.Fatalf("expected merge conflict, got success output: %s", string(mergeOut))
	}

	chdirRepo(t, repo)
	err := ensureWorkspaceSyncReady("push")
	if err == nil {
		t.Fatal("expected ensureWorkspaceSyncReady to block unresolved workspace")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "syncing state") {
		t.Fatalf("expected syncing-state message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "notes.md") {
		t.Fatalf("expected unresolved path in error, got: %v", err)
	}
}
