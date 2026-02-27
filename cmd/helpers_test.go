package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

var (
	commandTestMu sync.Mutex
	setupEnvOnce  sync.Once
	setupEnvErr   error
)

func runParallelCommandTest(t *testing.T) {
	t.Helper()

	commandTestMu.Lock()
	t.Cleanup(commandTestMu.Unlock)
}

func setupGitRepo(t *testing.T, repo string) {
	t.Helper()
	runGitForTest(t, repo, "init", "-b", "main")
	runGitForTest(t, repo, "config", "user.email", "conf-test@example.com")
	runGitForTest(t, repo, "config", "user.name", "conf-test")
}

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // Intentionally running git in test
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func writeMarkdown(t *testing.T, path string, doc fs.MarkdownDocument) {
	t.Helper()
	if err := fs.WriteMarkdownDocument(path, doc); err != nil {
		t.Fatalf("write markdown %s: %v", path, err)
	}
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}

func simpleADF(text string) map[string]any {
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": text,
					},
				},
			},
		},
	}
}

func setupEnv(t *testing.T) {
	t.Helper()

	setupEnvOnce.Do(func() {
		if err := os.Setenv("ATLASSIAN_DOMAIN", "https://example.atlassian.net"); err != nil {
			setupEnvErr = err
			return
		}
		if err := os.Setenv("ATLASSIAN_EMAIL", "user@example.com"); err != nil {
			setupEnvErr = err
			return
		}
		if err := os.Setenv("ATLASSIAN_API_TOKEN", "token-123"); err != nil {
			setupEnvErr = err
		}
	})

	if setupEnvErr != nil {
		t.Fatalf("setup env: %v", setupEnvErr)
	}
}

func chdirRepo(t *testing.T, repo string) {
	t.Helper()
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevDir) })
}

func setAutomationFlags(t *testing.T, yes, nonInteractive bool) {
	t.Helper()
	previousYes := flagYes
	previousNonInteractive := flagNonInteractive
	previousSkipMissingAssets := flagSkipMissingAssets
	previousPullForce := flagPullForce

	flagYes = yes
	flagNonInteractive = nonInteractive
	flagSkipMissingAssets = false
	flagPullForce = false

	t.Cleanup(func() {
		flagYes = previousYes
		flagNonInteractive = previousNonInteractive
		flagSkipMissingAssets = previousSkipMissingAssets
		flagPullForce = previousPullForce
	})
}
