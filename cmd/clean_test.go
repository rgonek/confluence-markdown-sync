package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func TestCleanCmd(t *testing.T) {
	runParallelCommandTest(t)
	tempDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tempDir, ".git"), 0700)
	if err != nil {
		t.Fatalf("failed to create fake git dir: %v", err)
	}

	worktreesDir := filepath.Join(tempDir, ".confluence-worktrees")
	err = os.MkdirAll(filepath.Join(worktreesDir, "wt1"), 0700)
	if err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}
	err = os.MkdirAll(filepath.Join(worktreesDir, "wt2"), 0700)
	if err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	t.Run("listCleanWorktreeDirs", func(t *testing.T) {
		dirs, err := listCleanWorktreeDirs(worktreesDir)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(dirs) != 2 {
			t.Fatalf("expected 2 worktree dirs, got %d", len(dirs))
		}
	})

	t.Run("listCleanWorktreeDirs missing dir", func(t *testing.T) {
		dirs, err := listCleanWorktreeDirs(filepath.Join(tempDir, "missing"))
		if err != nil {
			t.Fatalf("expected no error for missing dir, got %v", err)
		}
		if len(dirs) != 0 {
			t.Fatalf("expected 0 worktree dirs, got %d", len(dirs))
		}
	})

	t.Run("normalizeCleanStates", func(t *testing.T) {
		stateFile := filepath.Join(tempDir, "SPACE", ".confluence-state.json")
		err = os.MkdirAll(filepath.Dir(stateFile), 0700)
		if err != nil {
			t.Fatalf("failed to create state dir: %v", err)
		}

		state := fs.SpaceState{SpaceKey: "SPACE"}
		err = fs.SaveState(filepath.Dir(stateFile), state)
		if err != nil {
			t.Fatalf("failed to save state: %v", err)
		}

		out := new(bytes.Buffer)
		err = normalizeCleanStates(out, tempDir)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if !bytes.Contains(out.Bytes(), []byte("Normalized")) {
			t.Fatalf("expected 'Normalized' in output, got %q", out.String())
		}
	})

	t.Run("confirmCleanActions non-interactive yes", func(t *testing.T) {
		flagYes = true
		flagNonInteractive = true
		defer func() {
			flagYes = false
			flagNonInteractive = false
		}()

		out := new(bytes.Buffer)
		in := bytes.NewBufferString("\n")

		err := confirmCleanActions(in, out, "main", 1, 1)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("confirmCleanActions non-interactive without yes", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = true
		defer func() { flagNonInteractive = false }()

		out := new(bytes.Buffer)
		in := bytes.NewBufferString("\n")

		err := confirmCleanActions(in, out, "main", 1, 1)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("confirmCleanActions interactive yes", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = false

		out := new(bytes.Buffer)
		in := bytes.NewBufferString("y\n")

		err := confirmCleanActions(in, out, "main", 1, 1)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("confirmCleanActions interactive yes alternative", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = false

		out := new(bytes.Buffer)
		in := bytes.NewBufferString("yes\n")

		err := confirmCleanActions(in, out, "main", 1, 1)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("confirmCleanActions interactive no", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = false

		out := new(bytes.Buffer)
		in := bytes.NewBufferString("n\n")

		err := confirmCleanActions(in, out, "main", 1, 1)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("branchExists helper", func(t *testing.T) {
		// Mock git client just enough for branchExists
		client := &git.Client{} // This is risky but branchExists is small
		if branchExists(client, "") {
			t.Fatalf("expected false for empty branch name")
		}
	})
}

func TestRunClean_Integration(t *testing.T) {
	// DO NOT runParallelCommandTest here because we modify global flags
	repo := setupGitRepoForClean(t)

	oldWD, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldWD)
	}()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	// Create a sync branch and switch to it
	runGitForClean(t, repo, "checkout", "-b", "sync/test")

	// Create a snapshot ref
	head, _ := git.RunGit(repo, "rev-parse", "HEAD")
	head = strings.TrimSpace(head)
	snapshotRef := "refs/confluence-sync/snapshots/TEST/123"
	runGitForClean(t, repo, "update-ref", snapshotRef, head)

	// Create a dummy worktree dir
	wtDir := filepath.Join(repo, ".confluence-worktrees", "test-wt")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("failed to create worktree dir: %v", err)
	}

	// Run clean
	oldYes := flagYes
	oldNonInt := flagNonInteractive
	flagYes = true
	flagNonInteractive = true

	// Mock outputSupportsProgress to false to avoid interactive forms
	oldSupportsProgress := outputSupportsProgress
	outputSupportsProgress = func(out io.Writer) bool { return false }

	defer func() {
		flagYes = oldYes
		flagNonInteractive = oldNonInt
		outputSupportsProgress = oldSupportsProgress
	}()

	cmd := newCleanCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	// Add a trailing newline to the input to satisfy any potential reads
	cmd.SetIn(strings.NewReader("y\n"))

	if err := runClean(cmd, nil); err != nil {
		t.Fatalf("runClean failed: %v\nOutput: %s", err, out.String())
	}

	// Verify actions
	client, _ := git.NewClient()
	branch, _ := client.CurrentBranch()
	if branch != "main" {
		t.Errorf("expected branch to be main, got %q", branch)
	}

	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Errorf("expected worktree dir to be removed")
	}

	refs, _ := client.Run("for-each-ref", "--format=%(refname)", "refs/confluence-sync/snapshots/")
	if strings.Contains(refs, snapshotRef) {
		t.Errorf("expected snapshot ref to be deleted")
	}
}

func setupGitRepoForClean(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitForClean(t, repo, "init", "-b", "main")
	runGitForClean(t, repo, "config", "user.email", "clean-test@example.com")
	runGitForClean(t, repo, "config", "user.name", "clean-test")

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGitForClean(t, repo, "add", "file.txt")
	runGitForClean(t, repo, "commit", "-m", "initial")

	return repo
}

func runGitForClean(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", args, err, string(out))
	}
}
