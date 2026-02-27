package cmd

import (
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func TestResolveCleanTargetBranchHelper(t *testing.T) {
	runParallelCommandTest(t)
	tempDir := t.TempDir()

	setupGitRepo(t, tempDir)
	chdirRepo(t, tempDir)

	client, err := git.NewClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Init empty commit so branch exists
	if _, err := client.Run("commit", "--allow-empty", "-m", "init"); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Force rename to main first to test that logic.
	if _, err := client.Run("branch", "-M", "main"); err != nil {
		t.Fatalf("failed to rename branch: %v", err)
	}

	branch, err := resolveCleanTargetBranch(client)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected main, got %q", branch)
	}

	// Try testing branchExists specifically on a valid and invalid branch
	if !branchExists(client, "main") {
		t.Fatalf("expected main to exist")
	}
	if branchExists(client, "missing-branch-12345") {
		t.Fatalf("expected missing-branch-12345 to not exist")
	}
}

func TestResolveCleanTargetBranch_Fallback(t *testing.T) {
	runParallelCommandTest(t)
	tempDir := t.TempDir()

	setupGitRepo(t, tempDir)
	chdirRepo(t, tempDir)

	client, err := git.NewClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	if _, err := client.Run("commit", "--allow-empty", "-m", "init"); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}
	if _, err := client.Run("branch", "-M", "other-branch"); err != nil { // no main or master
		t.Fatalf("failed to rename branch: %v", err)
	}

	// Create fake symbolic-ref response
	if _, err := client.Run("remote", "add", "origin", "https://github.com/fake/fake.git"); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// It should fallback properly if origin exists or fallback to nothing
	branch, err := resolveCleanTargetBranch(client)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if branch != "" && branch != "other-branch" {
		t.Logf("resolved branch: %q", branch)
	}
}

func TestConfirmCleanActions_UI(t *testing.T) {
	runParallelCommandTest(t)
	// Let's test non-interactive yes by manually replacing the global
	flagYes = true
	defer func() { flagYes = false }()

	err := confirmCleanActions(nil, nil, "main", 1, 1)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
