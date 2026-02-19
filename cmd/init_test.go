package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInit_CreatesInitialCommitWhenRepoMissing(t *testing.T) {
	repo := t.TempDir()
	chdirRepo(t, repo)

	t.Setenv("GIT_AUTHOR_NAME", "cms-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "cms-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "cms-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "cms-test@example.com")

	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader("https://example.atlassian.net\nuser@example.com\ntoken-123\n"))

	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	commitCount := strings.TrimSpace(runGitForTest(t, repo, "rev-list", "--count", "HEAD"))
	if commitCount != "1" {
		t.Fatalf("commit count = %q, want 1", commitCount)
	}

	message := strings.TrimSpace(runGitForTest(t, repo, "log", "-1", "--format=%s"))
	if message != "chore: initialize cms workspace" {
		t.Fatalf("commit message = %q, want %q", message, "chore: initialize cms workspace")
	}

	tracked := runGitForTest(t, repo, "ls-tree", "--name-only", "-r", "HEAD")
	if !strings.Contains(tracked, ".gitignore\n") {
		t.Fatalf("expected .gitignore to be tracked in initial commit; tracked files:\n%s", tracked)
	}
	if strings.Contains(tracked, ".env\n") {
		t.Fatalf(".env should not be tracked in initial commit")
	}
}

func TestRunInit_DoesNotCreateCommitInsideExistingRepo(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)

	if err := os.WriteFile(filepath.Join(repo, "baseline.txt"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatalf("write baseline file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("ATLASSIAN_DOMAIN=https://example.atlassian.net\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	runGitForTest(t, repo, "add", "baseline.txt")
	runGitForTest(t, repo, "commit", "-m", "initial")

	headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

	chdirRepo(t, repo)
	cmd := newInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(""))

	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
	if headBefore != headAfter {
		t.Fatalf("HEAD changed unexpectedly for existing repo: before=%s after=%s", headBefore, headAfter)
	}
}
