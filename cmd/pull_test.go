package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
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

	setupEnv(t)
	chdirRepo(t, repo)

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

	setupEnv(t)
	chdirRepo(t, repo)

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

func TestRunPull_NonInteractiveRequiresYesForHighImpact(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := buildBulkPullRemote(t, 11)

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)
	setAutomationFlags(t, false, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"})
	if err == nil {
		t.Fatal("runPull() expected confirmation error")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("expected confirmation error, got: %v", err)
	}

	entries, err := os.ReadDir(spaceDir)
	if err != nil {
		t.Fatalf("read space dir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".md" {
			t.Fatalf("unexpected markdown file %s written despite missing confirmation", entry.Name())
		}
	}
}

func TestRunPull_YesBypassesHighImpactConfirmation(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := buildBulkPullRemote(t, 11)

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)
	setAutomationFlags(t, true, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got := len(state.PagePathIndex); got != 11 {
		t.Fatalf("expected 11 synced pages, got %d", got)
	}
}

func TestRunPull_WorksWithoutGitRemoteConfigured(t *testing.T) {
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

	if remotes := strings.TrimSpace(runGitForTest(t, repo, "remote")); remotes != "" {
		t.Fatalf("expected no git remotes, got %q", remotes)
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

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}); err != nil {
		t.Fatalf("runPull() error without git remote: %v", err)
	}
}

func buildBulkPullRemote(t *testing.T, pageCount int) *cmdFakePullRemote {
	t.Helper()

	pages := make([]confluence.Page, 0, pageCount)
	pagesByID := make(map[string]confluence.Page, pageCount)
	for i := 1; i <= pageCount; i++ {
		id := fmt.Sprintf("%d", i)
		title := fmt.Sprintf("Page %d", i)
		page := confluence.Page{
			ID:           id,
			SpaceID:      "space-1",
			Title:        title,
			Version:      1,
			LastModified: time.Date(2026, time.February, 2, 10, i, 0, 0, time.UTC),
			BodyADF:      rawJSON(t, simpleADF(fmt.Sprintf("Body %d", i))),
		}
		pages = append(pages, confluence.Page{
			ID:           page.ID,
			SpaceID:      page.SpaceID,
			Title:        page.Title,
			Version:      page.Version,
			LastModified: page.LastModified,
		})
		pagesByID[id] = page
	}

	return &cmdFakePullRemote{
		space:       confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages:       pages,
		pagesByID:   pagesByID,
		attachments: map[string][]byte{},
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
