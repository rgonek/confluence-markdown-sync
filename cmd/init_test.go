package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInit_CreatesInitialCommitWhenRepoMissing(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	chdirRepo(t, repo)

	t.Setenv("GIT_AUTHOR_NAME", "conf-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conf-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "conf-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conf-test@example.com")

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
	if message != "chore: initialize conf workspace" {
		t.Fatalf("commit message = %q, want %q", message, "chore: initialize conf workspace")
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
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)

	if err := os.WriteFile(filepath.Join(repo, "baseline.txt"), []byte("baseline\n"), 0o600); err != nil {
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

func TestRunInit_ScaffoldsDotEnvFromExistingEnvironmentWithoutPrompt(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	chdirRepo(t, repo)

	t.Setenv("ATLASSIAN_DOMAIN", "https://env-example.atlassian.net/")
	t.Setenv("ATLASSIAN_EMAIL", "env-user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "env-token-123")
	t.Setenv("GIT_AUTHOR_NAME", "conf-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conf-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "conf-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conf-test@example.com")

	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader(""))

	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	dotEnvRaw, err := os.ReadFile(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}

	dotEnv := string(dotEnvRaw)
	if !strings.Contains(dotEnv, "ATLASSIAN_DOMAIN=https://env-example.atlassian.net\n") {
		t.Fatalf(".env missing normalized domain:\n%s", dotEnv)
	}
	if !strings.Contains(dotEnv, "ATLASSIAN_EMAIL=env-user@example.com\n") {
		t.Fatalf(".env missing email:\n%s", dotEnv)
	}
	if !strings.Contains(dotEnv, "ATLASSIAN_API_TOKEN=env-token-123\n") {
		t.Fatalf(".env missing API token:\n%s", dotEnv)
	}

	output := out.String()
	if !strings.Contains(output, "Scaffolding it from existing Atlassian environment variables.") {
		t.Fatalf("expected env-backed scaffolding message, got:\n%s", output)
	}
	if strings.Contains(output, "Please enter your Atlassian credentials") {
		t.Fatalf("did not expect interactive credential prompt when env is complete:\n%s", output)
	}
}

func TestRunInit_PartialEnvironmentStillPromptsForCredentials(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	chdirRepo(t, repo)

	t.Setenv("CONFLUENCE_URL", "")
	t.Setenv("CONFLUENCE_EMAIL", "")
	t.Setenv("CONFLUENCE_API_TOKEN", "")
	t.Setenv("ATLASSIAN_DOMAIN", "https://env-example.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "")
	t.Setenv("ATLASSIAN_API_TOKEN", "")
	t.Setenv("GIT_AUTHOR_NAME", "conf-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "conf-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "conf-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "conf-test@example.com")

	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader("https://prompt-example.atlassian.net\nprompt-user@example.com\nprompt-token-123\n"))

	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	dotEnvRaw, err := os.ReadFile(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}

	dotEnv := string(dotEnvRaw)
	if !strings.Contains(dotEnv, "ATLASSIAN_DOMAIN=https://prompt-example.atlassian.net\n") {
		t.Fatalf(".env missing prompted domain:\n%s", dotEnv)
	}
	if !strings.Contains(dotEnv, "ATLASSIAN_EMAIL=prompt-user@example.com\n") {
		t.Fatalf(".env missing prompted email:\n%s", dotEnv)
	}
	if !strings.Contains(dotEnv, "ATLASSIAN_API_TOKEN=prompt-token-123\n") {
		t.Fatalf(".env missing prompted API token:\n%s", dotEnv)
	}

	output := out.String()
	if !strings.Contains(output, "Please enter your Atlassian credentials") {
		t.Fatalf("expected interactive credential prompt when env is partial, got:\n%s", output)
	}
	if strings.Contains(output, "Scaffolding it from existing Atlassian environment variables.") {
		t.Fatalf("did not expect env-backed scaffolding message when env is partial:\n%s", output)
	}
}

func TestRunInit_ExistingDotEnvRemainsUnchanged(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)

	originalDotEnv := "# existing credentials\nATLASSIAN_DOMAIN=https://existing.atlassian.net\nATLASSIAN_EMAIL=existing-user@example.com\nATLASSIAN_API_TOKEN=existing-token\n"
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte(originalDotEnv), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("CONFLUENCE_URL", "")
	t.Setenv("CONFLUENCE_EMAIL", "")
	t.Setenv("CONFLUENCE_API_TOKEN", "")
	t.Setenv("ATLASSIAN_DOMAIN", "https://env-example.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "env-user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "env-token-123")

	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader(""))

	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	dotEnvRaw, err := os.ReadFile(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if string(dotEnvRaw) != originalDotEnv {
		t.Fatalf(".env changed unexpectedly:\n%s", string(dotEnvRaw))
	}

	output := out.String()
	if !strings.Contains(output, ".env already exists") {
		t.Fatalf("expected existing .env message, got:\n%s", output)
	}
	if strings.Contains(output, "Scaffolding it from existing Atlassian environment variables.") {
		t.Fatalf("did not expect scaffolding message when .env already exists:\n%s", output)
	}
	if strings.Contains(output, "Please enter your Atlassian credentials") {
		t.Fatalf("did not expect credential prompt when .env already exists:\n%s", output)
	}
}
