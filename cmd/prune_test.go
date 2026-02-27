package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestRunPrune_Integration(t *testing.T) {
	// DO NOT runParallelCommandTest here because we modify global flags
	repo := setupGitRepoForPrune(t)

	oldWD, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldWD)
	}()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	// Create a managed space dir
	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(filepath.Join(spaceDir, "assets"), 0755); err != nil {
		t.Fatalf("failed to create assets dir: %v", err)
	}

	// Create state
	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	// Create markdown referencing one asset
	mdContent := "---\nid: 1\nversion: 1\n---\n![referenced](assets/referenced.png)"
	if err := os.WriteFile(filepath.Join(spaceDir, "page.md"), []byte(mdContent), 0600); err != nil {
		t.Fatalf("failed to write markdown: %v", err)
	}

	// Create assets
	referencedPath := filepath.Join(spaceDir, "assets", "referenced.png")
	orphanPath := filepath.Join(spaceDir, "assets", "orphan.png")
	if err := os.WriteFile(referencedPath, []byte("png"), 0600); err != nil {
		t.Fatalf("failed to write referenced asset: %v", err)
	}
	if err := os.WriteFile(orphanPath, []byte("png"), 0600); err != nil {
		t.Fatalf("failed to write orphan asset: %v", err)
	}

	// Run prune
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

	cmd := newPruneCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader("y\n"))

	target := config.Target{Value: "TEST", Mode: config.TargetModeSpace}
	if err := runPrune(cmd, target); err != nil {
		t.Fatalf("runPrune failed: %v\nOutput: %s", err, out.String())
	}

	// Verify actions
	if _, err := os.Stat(referencedPath); err != nil {
		t.Errorf("expected referenced asset to still exist: %v", err)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Errorf("expected orphan asset to be deleted")
	}
}

func setupGitRepoForPrune(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitForPrune(t, repo, "init", "-b", "main")
	runGitForPrune(t, repo, "config", "user.email", "prune-test@example.com")
	runGitForClean(t, repo, "config", "user.name", "prune-test")

	if err := os.WriteFile(filepath.Join(repo, "init.txt"), []byte("init"), 0600); err != nil {
		t.Fatalf("failed to write init file: %v", err)
	}
	runGitForPrune(t, repo, "add", "init.txt")
	runGitForPrune(t, repo, "commit", "-m", "initial")

	return repo
}

func runGitForPrune(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", args, err, string(out))
	}
}
