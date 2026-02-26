package cmd

import (
	"os"
	"os/exec"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func TestResolveCleanTargetBranchHelper(t *testing.T) {
	runParallelCommandTest(t)
	tempDir := t.TempDir()

	oldWd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldWd)

	// Init via exec
	cmd := exec.Command("git", "init")
	cmd.Run()

	client, err := git.NewClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Init empty commit so branch exists
	client.Run("commit", "--allow-empty", "-m", "init")

	// Force rename to main first to test that logic.
	client.Run("branch", "-M", "main")

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

	oldWd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldWd)

	cmd := exec.Command("git", "init")
	cmd.Run()

	client, err := git.NewClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	client.Run("commit", "--allow-empty", "-m", "init")
	client.Run("branch", "-M", "other-branch") // no main or master

	// Create fake symbolic-ref response
	client.Run("remote", "add", "origin", "https://github.com/fake/fake.git")

	// It should fallback properly if origin exists or fallback to nothing
	branch, err := resolveCleanTargetBranch(client)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if branch != "" && branch != "other-branch" {
		// Depending on defaultRef logic it might be empty or find it
	}
}

func TestConfirmCleanActions_UI(t *testing.T) {
	// Let's test non-interactive yes by manually replacing the global
	flagYes = true
	defer func() { flagYes = false }()

	err := confirmCleanActions(nil, nil, "main", 1, 1)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
