package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	localUntracked := filepath.Join(spaceDir, "local-notes.md")
	if err := os.WriteFile(localUntracked, []byte("local notes\n"), 0o600); err != nil {
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

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	oldNow := nowUTC
	nowUTC = func() time.Time { return time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowUTC = oldNow })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	actualSpaceDir := filepath.Join(repo, "Engineering (ENG)")
	localUntracked = filepath.Join(actualSpaceDir, "local-notes.md")

	localRaw, err := os.ReadFile(localUntracked) //nolint:gosec // test path is created under t.TempDir
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

func TestResolveInitialPullContext_TrackedDirWithoutSpaceKeyUsesDirSuffix(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "Technical documentation (TD)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"missing.md": "1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	chdirRepo(t, spaceDir)

	ctx, err := resolveInitialPullContext(config.Target{Mode: config.TargetModeSpace, Value: ""})
	if err != nil {
		t.Fatalf("resolveInitialPullContext() error: %v", err)
	}

	if ctx.spaceDir != spaceDir {
		t.Fatalf("spaceDir = %q, want %q", ctx.spaceDir, spaceDir)
	}
	if ctx.spaceKey != "TD" {
		t.Fatalf("spaceKey = %q, want TD", ctx.spaceKey)
	}
	if !ctx.fixedDir {
		t.Fatal("expected fixedDir=true for tracked directory")
	}
}

func TestListAllPullChangesForEstimate_UsesContinuationOffsets(t *testing.T) {
	starts := make([]int, 0)

	remote := &cmdFakePullRemote{
		listChanges: func(opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
			starts = append(starts, opts.Start)
			switch opts.Start {
			case 0:
				return confluence.ChangeListResult{
					Changes:   []confluence.Change{{PageID: "1"}},
					HasMore:   true,
					NextStart: 40,
				}, nil
			case 40:
				return confluence.ChangeListResult{
					Changes:   []confluence.Change{{PageID: "2"}},
					HasMore:   true,
					NextStart: 90,
				}, nil
			case 90:
				return confluence.ChangeListResult{
					Changes: []confluence.Change{{PageID: "3"}},
					HasMore: false,
				}, nil
			default:
				return confluence.ChangeListResult{}, fmt.Errorf("unexpected start: %d", opts.Start)
			}
		},
	}

	changes, err := listAllPullChangesForEstimate(context.Background(), remote, confluence.ChangeListOptions{
		SpaceKey: "ENG",
		Limit:    25,
	}, nil)
	if err != nil {
		t.Fatalf("listAllPullChangesForEstimate() error: %v", err)
	}

	if len(changes) != 3 {
		t.Fatalf("changes count = %d, want 3", len(changes))
	}
	if len(starts) != 3 {
		t.Fatalf("start count = %d, want 3", len(starts))
	}
	if starts[0] != 0 || starts[1] != 40 || starts[2] != 90 {
		t.Fatalf("starts = %v, want [0 40 90]", starts)
	}
}

func TestRunPull_FailureCleanupPreservesStateFile(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:              "ENG",
		LastPullHighWatermark: "2026-02-01T00:00:00Z",
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(spaceDir, "local-notes.md"), []byte("local notes\n"), 0o600); err != nil {
		t.Fatalf("write local notes: %v", err)
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
			},
		},
		getPageErr:  errors.New("simulated page fetch failure"),
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("runPull() expected error")
	}

	statePath := filepath.Join(spaceDir, fs.StateFileName)
	if _, statErr := os.Stat(statePath); statErr != nil {
		t.Fatalf("expected state file to be preserved on pull failure, got: %v", statErr)
	}

	stateAfter, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state after failure: %v", err)
	}
	if stateAfter.LastPullHighWatermark != "2026-02-01T00:00:00Z" {
		t.Fatalf("state watermark changed unexpectedly: %q", stateAfter.LastPullHighWatermark)
	}
}

func TestRunPull_NoopDoesNotCreateTag(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	baselineDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                2,
			Author:                 "User author-1",
			CreatedAt:              "2026-02-01T10:00:00Z",
			LastModifiedBy:         "User author-1",
			LastModifiedAt:         "2026-02-01T11:00:00Z",
			ConfluenceLastModified: "2026-02-01T11:00:00Z",
		},
		Body: "same body\n",
	}
	writeMarkdown(t, filepath.Join(spaceDir, "Root.md"), baselineDoc)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:                   "1",
				SpaceID:              "space-1",
				Title:                "Root",
				Version:              2,
				AuthorID:             "author-1",
				CreatedAt:            time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC),
				LastModifiedAuthorID: "author-1",
				LastModified:         time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:                   "1",
				SpaceID:              "space-1",
				Title:                "Root",
				Version:              2,
				AuthorID:             "author-1",
				CreatedAt:            time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC),
				LastModifiedAuthorID: "author-1",
				LastModified:         time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:              rawJSON(t, simpleADF("same body")),
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

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
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
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
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

	err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("runPull() expected confirmation error")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("expected confirmation error, got: %v", err)
	}
}

func TestRunPull_YesBypassesHighImpactConfirmation(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
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

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
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

func TestRunPull_RecreatesMissingSpaceDirWithoutRestoringDeletionStash(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	writeMarkdown(t, filepath.Join(spaceDir, "Root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	if err := os.RemoveAll(spaceDir); err != nil {
		t.Fatalf("remove space dir: %v", err)
	}

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Root",
			Version:      2,
			LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
		}},
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

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Root.md")); err != nil {
		t.Fatalf("expected Root.md to be recreated after pull: %v", err)
	}
}

func TestRunPull_ForcePullRefreshesEntireSpace(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		LastPullHighWatermark: "2026-02-02T00:00:00Z",
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Root",
			Version:      2,
			LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
		}},
		changes: []confluence.Change{},
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

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	rootDoc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if err != nil {
		t.Fatalf("read root.md: %v", err)
	}
	if !strings.Contains(rootDoc.Body, "new body") {
		t.Fatalf("expected root.md body to be refreshed on force pull, got:\n%s", rootDoc.Body)
	}
}

func TestRunPull_ForceFlagRejectedForFileTarget(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	filePath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "body\n",
	})

	chdirRepo(t, repo)

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	// We need to allow it to resolve space metadata for file mode too now
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
	}
	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	err := runPull(cmd, config.Target{Mode: config.TargetModeFile, Value: filePath})
	if err == nil {
		t.Fatal("expected error for --force on file target")
	}
	if !strings.Contains(err.Error(), "--force is only supported for space targets") {
		t.Fatalf("unexpected error: %v", err)
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
	folderByID  map[string]confluence.Folder
	folderErr   error
	getPageErr  error
	changes     []confluence.Change
	listChanges func(opts confluence.ChangeListOptions) (confluence.ChangeListResult, error)
	pagesByID   map[string]confluence.Page
	attachments map[string][]byte
}

func (f *cmdFakePullRemote) GetUser(_ context.Context, accountID string) (confluence.User, error) {
	return confluence.User{AccountID: accountID, DisplayName: "User " + accountID}, nil
}

func (f *cmdFakePullRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *cmdFakePullRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *cmdFakePullRemote) GetFolder(_ context.Context, folderID string) (confluence.Folder, error) {
	if f.folderErr != nil {
		return confluence.Folder{}, f.folderErr
	}
	folder, ok := f.folderByID[folderID]
	if !ok {
		return confluence.Folder{}, confluence.ErrNotFound
	}
	return folder, nil
}

func (f *cmdFakePullRemote) ListChanges(_ context.Context, opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
	if f.listChanges != nil {
		return f.listChanges(opts)
	}
	return confluence.ChangeListResult{Changes: f.changes}, nil
}

func (f *cmdFakePullRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	if f.getPageErr != nil {
		return confluence.Page{}, f.getPageErr
	}
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *cmdFakePullRemote) GetContentStatus(_ context.Context, pageID string) (string, error) {
	return "", nil
}

func (f *cmdFakePullRemote) GetLabels(_ context.Context, pageID string) ([]string, error) {
	return nil, nil
}

func (f *cmdFakePullRemote) DownloadAttachment(_ context.Context, attachmentID string, pageID string, out io.Writer) error {
	raw, ok := f.attachments[attachmentID]
	if !ok {
		return confluence.ErrNotFound
	}
	_, err := out.Write(raw)
	return err
}

func TestRunPull_DraftSpaceListing(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	// Page 10 is known locally as a draft
	writeMarkdown(t, filepath.Join(spaceDir, "draft.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Draft Page",
			ID:      "10",
			Space:   "ENG",
			Version: 1,
			Status:  "draft",
		},
		Body: "draft body\n",
	})
	state := fs.SpaceState{
		PagePathIndex: map[string]string{
			"draft.md": "10",
		},
	}
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial draft")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		// Remote space listing only returns current pages (none in this case)
		pages: []confluence.Page{},
		pagesByID: map[string]confluence.Page{
			"10": {
				ID:      "10",
				SpaceID: "space-1",
				Title:   "Draft Page",
				Status:  "draft",
				BodyADF: rawJSON(t, simpleADF("remote draft body")),
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
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	// draft.md should NOT be deleted, and should be updated from remote
	doc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "draft.md"))
	if err != nil {
		t.Fatalf("read draft.md: %v", err)
	}
	if !strings.Contains(doc.Body, "remote draft body") {
		t.Errorf("draft.md not updated from remote, body = %q", doc.Body)
	}
	if doc.Frontmatter.State != "draft" {
		t.Errorf("draft.md status = %q, want draft", doc.Frontmatter.State)
	}
}
