package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func TestCleanCmd(t *testing.T) {
	runParallelCommandTest(t)
	tempDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tempDir, ".git"), 0755)
	if err != nil {
		t.Fatalf("failed to create fake git dir: %v", err)
	}

	worktreesDir := filepath.Join(tempDir, ".confluence-worktrees")
	err = os.MkdirAll(filepath.Join(worktreesDir, "wt1"), 0755)
	if err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}
	err = os.MkdirAll(filepath.Join(worktreesDir, "wt2"), 0755)
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
		err = os.MkdirAll(filepath.Dir(stateFile), 0755)
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
