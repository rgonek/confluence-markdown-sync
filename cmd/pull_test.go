package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPull_RestoresScopedStashAndCreatesTag(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ConfluencePageID:       "1",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	localUntracked := filepath.Join(spaceDir, "local-notes.md")
	if err := os.WriteFile(localUntracked, []byte("local notes\n"), 0o644); err != nil {
		t.Fatalf("write local untracked: %v", err)
	}

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	oldNow := nowUTC
	nowUTC = func() time.Time { return time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowUTC = oldNow })

	t.Setenv("ATLASSIAN_DOMAIN", "https://example.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "token-123")

	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	localRaw, err := os.ReadFile(localUntracked)
	if err != nil {
		t.Fatalf("local untracked file should be restored: %v", err)
	}
	if strings.TrimSpace(string(localRaw)) != "local notes" {
		t.Fatalf("restored local notes content mismatch: %q", string(localRaw))
	}

	tags := strings.TrimSpace(runGitForTest(t, repo, "tag", "--list", "confluence-sync/pull/ENG/*"))
	if tags == "" {
		t.Fatalf("expected pull sync tag to be created")
	}

	stashList := strings.TrimSpace(runGitForTest(t, repo, "stash", "list"))
	if stashList != "" {
		t.Fatalf("stash should be empty after successful restore, got %q", stashList)
	}
}

func TestRunPull_NoopDoesNotCreateTag(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	baselineDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ConfluencePageID:       "1",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      2,
			ConfluenceLastModified: "2026-02-01T11:00:00Z",
		},
		Body: "same body\n",
	}
	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), baselineDoc)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("same body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	oldNow := nowUTC
	nowUTC = func() time.Time { return time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowUTC = oldNow })

	t.Setenv("ATLASSIAN_DOMAIN", "https://example.atlassian.net")
	t.Setenv("ATLASSIAN_EMAIL", "user@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "token-123")

	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
	if headBefore != headAfter {
		t.Fatalf("HEAD changed on noop pull: before=%s after=%s", headBefore, headAfter)
	}

	tags := strings.TrimSpace(runGitForTest(t, repo, "tag", "--list", "confluence-sync/pull/ENG/*"))
	if tags != "" {
		t.Fatalf("expected no pull sync tag on noop, got %q", tags)
	}
}

type cmdFakePullRemote struct {
	space       confluence.Space
	pages       []confluence.Page
	changes     []confluence.Change
	pagesByID   map[string]confluence.Page
	attachments map[string][]byte
}

func (f *cmdFakePullRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *cmdFakePullRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *cmdFakePullRemote) ListChanges(_ context.Context, _ confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
	return confluence.ChangeListResult{Changes: f.changes}, nil
}

func (f *cmdFakePullRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *cmdFakePullRemote) DownloadAttachment(_ context.Context, attachmentID string) ([]byte, error) {
	raw, ok := f.attachments[attachmentID]
	if !ok {
		return nil, confluence.ErrNotFound
	}
	return raw, nil
}

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
