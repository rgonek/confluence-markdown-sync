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
)

func TestCleanCmd(t *testing.T) {
	runParallelCommandTest(t)
	tempDir := t.TempDir()

	worktreesDir := filepath.Join(tempDir, ".confluence-worktrees")
	if err := os.MkdirAll(filepath.Join(worktreesDir, "wt1"), 0o700); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktreesDir, "wt2"), 0o700); err != nil {
		t.Fatalf("create worktree: %v", err)
	}

	t.Run("listCleanWorktreeDirs", func(t *testing.T) {
		dirs, err := listCleanWorktreeDirs(worktreesDir)
		if err != nil {
			t.Fatalf("listCleanWorktreeDirs() error: %v", err)
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
		stateDir := filepath.Join(tempDir, "SPACE")
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			t.Fatalf("create state dir: %v", err)
		}
		if err := fs.SaveState(stateDir, fs.SpaceState{SpaceKey: "SPACE"}); err != nil {
			t.Fatalf("save state: %v", err)
		}

		out := new(bytes.Buffer)
		if err := normalizeCleanStates(out, tempDir); err != nil {
			t.Fatalf("normalizeCleanStates() error: %v", err)
		}
		if !strings.Contains(out.String(), "Normalized 1 state file(s)") {
			t.Fatalf("unexpected normalize output: %q", out.String())
		}
	})

	t.Run("confirmCleanActions non-interactive yes", func(t *testing.T) {
		flagYes = true
		flagNonInteractive = true
		defer func() {
			flagYes = false
			flagNonInteractive = false
		}()

		if err := confirmCleanActions(bytes.NewBufferString("\n"), new(bytes.Buffer), "main", 1, 1, 1); err != nil {
			t.Fatalf("confirmCleanActions() error: %v", err)
		}
	})

	t.Run("confirmCleanActions non-interactive without yes", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = true
		defer func() { flagNonInteractive = false }()

		if err := confirmCleanActions(bytes.NewBufferString("\n"), new(bytes.Buffer), "main", 1, 1, 1); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("confirmCleanActions interactive yes", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = false

		if err := confirmCleanActions(bytes.NewBufferString("yes\n"), new(bytes.Buffer), "main", 1, 1, 1); err != nil {
			t.Fatalf("confirmCleanActions() error: %v", err)
		}
	})

	t.Run("confirmCleanActions interactive no", func(t *testing.T) {
		flagYes = false
		flagNonInteractive = false

		if err := confirmCleanActions(bytes.NewBufferString("n\n"), new(bytes.Buffer), "main", 1, 1, 1); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRunClean_RemovesSafeSyncBranchAndSnapshot(t *testing.T) {
	runParallelCommandTest(t)

	repo := setupGitRepoForClean(t)
	chdirRepo(t, repo)
	setCleanAutomationFlags(t)

	syncBranch := "sync/ENG/20260305T211238Z"
	snapshotRef := "refs/confluence-sync/snapshots/ENG/20260305T211238Z"
	worktreeDir := filepath.Join(repo, ".confluence-worktrees", "ENG-20260305T211238Z")

	runGitForClean(t, repo, "branch", syncBranch, "main")
	runGitForClean(t, repo, "worktree", "add", worktreeDir, syncBranch)
	head := strings.TrimSpace(runGitForClean(t, repo, "rev-parse", syncBranch))
	runGitForClean(t, repo, "update-ref", snapshotRef, head)

	out := runCleanForTest(t)

	if _, err := os.Stat(worktreeDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir to be removed, stat err=%v", err)
	}
	if branchList := strings.TrimSpace(runGitForClean(t, repo, "branch", "--list", syncBranch)); branchList != "" {
		t.Fatalf("expected sync branch to be deleted, got %q", branchList)
	}
	if refs := strings.TrimSpace(runGitForClean(t, repo, "for-each-ref", "--format=%(refname)", snapshotRef)); refs != "" {
		t.Fatalf("expected snapshot ref to be deleted, got %q", refs)
	}
	if currentBranch := strings.TrimSpace(runGitForClean(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); currentBranch != "main" {
		t.Fatalf("expected to remain on main, got %q", currentBranch)
	}

	if !strings.Contains(out, "Deleted snapshot ref: "+snapshotRef) {
		t.Fatalf("expected snapshot deletion in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Deleted sync branch: "+syncBranch) {
		t.Fatalf("expected sync branch deletion in output, got:\n%s", out)
	}
	if !strings.Contains(out, "clean completed: removed 1 worktree(s), deleted 1 snapshot ref(s), deleted 1 sync branch(es), skipped 0 sync branch(es)") {
		t.Fatalf("unexpected summary output:\n%s", out)
	}
}

func TestRunClean_PreservesCurrentSyncBranchRecoveryArtifacts(t *testing.T) {
	runParallelCommandTest(t)

	repo := setupGitRepoForClean(t)
	chdirRepo(t, repo)
	setCleanAutomationFlags(t)

	syncBranch := "sync/ENG/20260305T211239Z"
	snapshotRef := "refs/confluence-sync/snapshots/ENG/20260305T211239Z"

	runGitForClean(t, repo, "checkout", "-b", syncBranch)
	head := strings.TrimSpace(runGitForClean(t, repo, "rev-parse", "HEAD"))
	runGitForClean(t, repo, "update-ref", snapshotRef, head)

	out := runCleanForTest(t)

	if currentBranch := strings.TrimSpace(runGitForClean(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); currentBranch != syncBranch {
		t.Fatalf("expected to stay on %s, got %q", syncBranch, currentBranch)
	}
	if branchList := strings.TrimSpace(runGitForClean(t, repo, "branch", "--list", syncBranch)); branchList == "" {
		t.Fatalf("expected sync branch to be retained")
	}
	if refs := strings.TrimSpace(runGitForClean(t, repo, "for-each-ref", "--format=%(refname)", snapshotRef)); refs != snapshotRef {
		t.Fatalf("expected snapshot ref to be retained, got %q", refs)
	}
	if !strings.Contains(out, "Retained sync branch "+syncBranch+": current HEAD is on this sync branch") {
		t.Fatalf("expected retained branch reason in output, got:\n%s", out)
	}
	if !strings.Contains(out, "clean completed: removed 0 worktree(s), deleted 0 snapshot ref(s), deleted 0 sync branch(es), skipped 1 sync branch(es)") {
		t.Fatalf("unexpected summary output:\n%s", out)
	}
}

func TestRunClean_PreservesLinkedWorktreeRecoveryArtifacts(t *testing.T) {
	runParallelCommandTest(t)

	repo := setupGitRepoForClean(t)
	chdirRepo(t, repo)
	setCleanAutomationFlags(t)

	syncBranch := "sync/ENG/20260305T211240Z"
	snapshotRef := "refs/confluence-sync/snapshots/ENG/20260305T211240Z"
	activeWorktree := filepath.Join(repo, "active-recovery")

	runGitForClean(t, repo, "branch", syncBranch, "main")
	runGitForClean(t, repo, "worktree", "add", activeWorktree, syncBranch)
	head := strings.TrimSpace(runGitForClean(t, repo, "rev-parse", syncBranch))
	runGitForClean(t, repo, "update-ref", snapshotRef, head)

	out := runCleanForTest(t)

	if _, err := os.Stat(activeWorktree); err != nil {
		t.Fatalf("expected linked worktree to remain: %v", err)
	}
	if branchList := strings.TrimSpace(runGitForClean(t, repo, "branch", "--list", syncBranch)); branchList == "" {
		t.Fatalf("expected sync branch to be retained")
	}
	if refs := strings.TrimSpace(runGitForClean(t, repo, "for-each-ref", "--format=%(refname)", snapshotRef)); refs != snapshotRef {
		t.Fatalf("expected snapshot ref to be retained, got %q", refs)
	}
	if !strings.Contains(out, "Retained sync branch "+syncBranch+": linked worktree remains at ") {
		t.Fatalf("expected linked-worktree retain reason in output, got:\n%s", out)
	}
}

func TestRunClean_AlreadyCleanReportsExplicitNoopSummary(t *testing.T) {
	runParallelCommandTest(t)

	repo := setupGitRepoForClean(t)
	chdirRepo(t, repo)
	setCleanAutomationFlags(t)

	out := runCleanForTest(t)

	if strings.Contains(out, "Deleted snapshot ref:") {
		t.Fatalf("expected no snapshot refs to be deleted, got:\n%s", out)
	}
	if strings.Contains(out, "Deleted sync branch:") {
		t.Fatalf("expected no sync branches to be deleted, got:\n%s", out)
	}
	if !strings.Contains(out, "clean completed: workspace is already clean (removed 0 worktree(s), deleted 0 snapshot ref(s), deleted 0 sync branch(es), skipped 0 sync branch(es))") {
		t.Fatalf("unexpected summary output:\n%s", out)
	}
}

func setupGitRepoForClean(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	runGitForClean(t, repo, "init", "-b", "main")
	runGitForClean(t, repo, "config", "user.email", "clean-test@example.com")
	runGitForClean(t, repo, "config", "user.name", "clean-test")

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitForClean(t, repo, "add", "file.txt")
	runGitForClean(t, repo, "commit", "-m", "initial")

	return repo
}

func runGitForClean(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...) //nolint:gosec // test helper executes git only
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func setCleanAutomationFlags(t *testing.T) {
	t.Helper()

	previousYes := flagYes
	previousNonInteractive := flagNonInteractive
	previousSupportsProgress := outputSupportsProgress

	flagYes = true
	flagNonInteractive = true
	outputSupportsProgress = func(io.Writer) bool { return false }

	t.Cleanup(func() {
		flagYes = previousYes
		flagNonInteractive = previousNonInteractive
		outputSupportsProgress = previousSupportsProgress
	})
}

func runCleanForTest(t *testing.T) string {
	t.Helper()

	cmd := newCleanCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader("y\n"))

	if err := runClean(cmd, nil); err != nil {
		t.Fatalf("runClean() error: %v\nOutput:\n%s", err, out.String())
	}
	return out.String()
}
