package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestAcquireWorkspaceLock_BlocksConcurrentMutations(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)

	lockPath := filepath.Join(repo, ".git", workspaceLockFilename)
	raw := []byte("{\n  \"command\": \"push\",\n  \"pid\": 424242,\n  \"created_at\": \"" + time.Now().UTC().Format(time.RFC3339) + "\"\n}")
	if err := os.WriteFile(lockPath, raw, 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	_, err := acquireWorkspaceLock("pull")
	if err == nil {
		t.Fatal("expected lock acquisition to fail while another pid holds the lock")
	}
	if !strings.Contains(err.Error(), "already mutating this repository") {
		t.Fatalf("unexpected lock error: %v", err)
	}
}

func TestDoctor_ReportsStaleWorkspaceSyncLock(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	runGitForTest(t, repo, "add", "README.md")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}
	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	lockPath := filepath.Join(repo, ".git", workspaceLockFilename)
	raw := []byte("{\n  \"command\": \"push\",\n  \"pid\": 99999,\n  \"created_at\": \"" + time.Now().Add(-workspaceLockStaleAfter-time.Minute).UTC().Format(time.RFC3339) + "\"\n}")
	if err := os.WriteFile(lockPath, raw, 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	out := new(bytes.Buffer)
	cmd := newDoctorCmd()
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))

	target := config.Target{Value: spaceDir, Mode: config.TargetModeSpace}
	if err := runDoctor(cmd, target, false); err != nil {
		t.Fatalf("runDoctor() error: %v", err)
	}
	if !strings.Contains(out.String(), "workspace-sync-lock") || !strings.Contains(out.String(), "appears stale") {
		t.Fatalf("expected stale lock issue in doctor output, got:\n%s", out.String())
	}
}
