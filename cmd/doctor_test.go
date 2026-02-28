package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestRunDoctor(t *testing.T) {
	runParallelCommandTest(t)

	cmd := newDoctorCmd()
	if cmd == nil {
		t.Fatal("expected command not to be nil")
	}

	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"page.md":    "1",
		"missing.md": "2",
		"empty.md":   "",
	}
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	pageContent := "---\nid: 1\nversion: 1\n---\npage"
	if err := os.WriteFile(filepath.Join(spaceDir, "page.md"), []byte(pageContent), 0o600); err != nil {
		t.Fatalf("write page: %v", err)
	}

	orphanContent := "---\nid: 3\nversion: 1\n---\norphan"
	if err := os.WriteFile(filepath.Join(spaceDir, "orphan.md"), []byte(orphanContent), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	conflictContent := "---\nid: 4\nversion: 1\n---\n<<<<<<<\nlocal\n=======\nremote\n>>>>>>>\n"
	if err := os.WriteFile(filepath.Join(spaceDir, "conflict.md"), []byte(conflictContent), 0o600); err != nil {
		t.Fatalf("write conflict: %v", err)
	}
	state.PagePathIndex["conflict.md"] = "4"
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	mismatchContent := "---\nid: 5\nversion: 1\n---\nmismatch"
	if err := os.WriteFile(filepath.Join(spaceDir, "mismatch.md"), []byte(mismatchContent), 0o600); err != nil {
		t.Fatalf("write mismatch: %v", err)
	}
	state.PagePathIndex["mismatch.md"] = "6"
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	unreadableFile := filepath.Join(spaceDir, "unreadable.md")
	if err := os.WriteFile(unreadableFile, []byte(""), 0o200); err != nil { // write-only
		t.Fatalf("write unreadable: %v", err)
	}
	state.PagePathIndex["unreadable.md"] = "7"
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	target := config.Target{Value: spaceDir, Mode: config.TargetModeSpace}

	// Test without repair
	if err := runDoctor(cmd, target, false); err != nil {
		t.Fatalf("runDoctor failed: %v", err)
	}

	// Test with repair
	if err := runDoctor(cmd, target, true); err != nil {
		t.Fatalf("runDoctor repair failed: %v", err)
	}

	newState, _ := fs.LoadState(spaceDir)
	if newState.PagePathIndex["page.md"] != "1" {
		t.Errorf("expected page.md to stay")
	}
	if _, ok := newState.PagePathIndex["missing.md"]; ok {
		t.Errorf("expected missing.md to be removed")
	}
	if newState.PagePathIndex["orphan.md"] != "3" {
		t.Errorf("expected orphan.md to be added")
	}
	if _, ok := newState.PagePathIndex["empty.md"]; ok {
		t.Errorf("expected empty.md to be removed")
	}
}
