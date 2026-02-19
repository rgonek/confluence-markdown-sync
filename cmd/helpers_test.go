package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func setupGitRepo(t *testing.T, repo string) {
	t.Helper()
	runGitForTest(t, repo, "init", "-b", "main")
	runGitForTest(t, repo, "config", "user.email", "cms-test@example.com")
	runGitForTest(t, repo, "config", "user.name", "cms-test")
}

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
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
	t.Setenv("ATLASSIAN_DOMAIN", "https://example.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "token-123")
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
