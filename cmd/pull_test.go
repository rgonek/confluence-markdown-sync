package cmd

import (
	"bytes"
	"errors"
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
			Title: "Root",
			ID:    "1",

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
			Title: "Root",
			ID:    "1",

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
			Title: "Root",
			ID:    "1",

			Version:                2,
			CreatedBy:              "User author-1",
			CreatedAt:              "2026-02-01T10:00:00Z",
			UpdatedBy:              "User author-1",
			UpdatedAt:              "2026-02-01T11:00:00Z",
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
			Title: "Root",
			ID:    "1",

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
			Title: "Draft Page",
			ID:    "10",

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

func TestPullNoOp_ExplainsReason_NoRemoteChanges(t *testing.T) {
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
			Version:                2,
			ConfluenceLastModified: "2026-02-01T11:00:00Z",
		},
		Body: "body\n",
	})
	// Save state with a watermark set after the remote page's last-modified time
	// so the incremental change detection finds nothing new.
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:              "ENG",
		LastPullHighWatermark: "2026-02-01T12:00:00Z",
		PagePathIndex:         map[string]string{"Root.md": "1"},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	// ListChanges returns no changes since the watermark — remote is up-to-date.
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
				BodyADF:      rawJSON(t, simpleADF("body")),
			},
		},
		// Empty changes list means ListChanges returns nothing → no changed page IDs.
		changes:     []confluence.Change{},
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	oldNow := nowUTC
	nowUTC = func() time.Time { return time.Date(2026, time.February, 1, 13, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowUTC = oldNow })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "no remote changes since last sync") {
		t.Fatalf("expected no-op message to explain reason (no remote changes), got:\n%s", got)
	}
}

func TestRunPull_IncrementalCreateMaterializesRemotePageWithoutForce(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	writeMarkdown(t, filepath.Join(spaceDir, "Parent.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Parent",
			ID:      "10",
			Version: 1,
		},
		Body: "old parent\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:              "ENG",
		LastPullHighWatermark: "2026-03-09T09:00:00Z",
		PagePathIndex: map[string]string{
			"Parent.md": "10",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	modifiedAt := time.Date(2026, time.March, 9, 9, 30, 0, 0, time.UTC)
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "10", SpaceID: "space-1", Title: "Parent", Version: 1, LastModified: modifiedAt},
			{ID: "20", SpaceID: "space-1", Title: "Remote Child", ParentPageID: "10", Version: 1, LastModified: modifiedAt},
		},
		changes: []confluence.Change{
			{PageID: "20", SpaceKey: "ENG", Version: 1, LastModified: modifiedAt},
		},
		pagesByID: map[string]confluence.Page{
			"10": {
				ID:           "10",
				SpaceID:      "space-1",
				Title:        "Parent",
				Version:      1,
				LastModified: modifiedAt,
				BodyADF:      rawJSON(t, simpleADF("parent body")),
			},
			"20": {
				ID:           "20",
				SpaceID:      "space-1",
				Title:        "Remote Child",
				ParentPageID: "10",
				Version:      1,
				LastModified: modifiedAt,
				BodyADF:      rawJSON(t, simpleADF("remote child body")),
			},
		},
		attachments: map[string][]byte{},
	}
	childFetches := 0
	fake.getPageFunc = func(pageID string) (confluence.Page, error) {
		if pageID == "20" {
			childFetches++
			if childFetches == 1 {
				return confluence.Page{}, confluence.ErrNotFound
			}
		}
		page, ok := fake.pagesByID[pageID]
		if !ok {
			return confluence.Page{}, confluence.ErrNotFound
		}
		return page, nil
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

	if childFetches < 2 {
		t.Fatalf("expected child page fetch retry, got %d attempt(s)", childFetches)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Parent", "Remote-Child.md")); err != nil {
		t.Fatalf("expected incremental pull to materialize remote child markdown: %v", err)
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got := state.PagePathIndex["Parent/Remote-Child.md"]; got != "20" {
		t.Fatalf("state page_path_index[Parent/Remote-Child.md] = %q, want 20", got)
	}
}

func TestRunPull_IncrementalUpdateReconcilesRemoteVersionWithoutForce(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	writeMarkdown(t, filepath.Join(spaceDir, "Remote-Page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Remote Page",
			ID:      "20",
			Version: 1,
		},
		Body: "old body\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:              "ENG",
		LastPullHighWatermark: "2026-03-09T11:00:00Z",
		PagePathIndex: map[string]string{
			"Remote-Page.md": "20",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	changeTime := time.Date(2026, time.March, 9, 11, 30, 0, 0, time.UTC)
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "20", SpaceID: "space-1", Title: "Remote Page", Version: 1, LastModified: time.Date(2026, time.March, 9, 11, 0, 0, 0, time.UTC)},
		},
		changes: []confluence.Change{
			{PageID: "20", SpaceKey: "ENG", Version: 2, LastModified: changeTime},
		},
		pagesByID: map[string]confluence.Page{
			"20": {
				ID:           "20",
				SpaceID:      "space-1",
				Title:        "Remote Page",
				Version:      2,
				LastModified: changeTime,
				BodyADF:      rawJSON(t, simpleADF("fresh body")),
			},
		},
		attachments: map[string][]byte{},
	}
	updateFetches := 0
	fake.getPageFunc = func(pageID string) (confluence.Page, error) {
		if pageID != "20" {
			return confluence.Page{}, confluence.ErrNotFound
		}
		updateFetches++
		if updateFetches == 1 {
			return confluence.Page{
				ID:           "20",
				SpaceID:      "space-1",
				Title:        "Remote Page",
				Version:      1,
				LastModified: time.Date(2026, time.March, 9, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("old body")),
			}, nil
		}
		return fake.pagesByID["20"], nil
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

	if updateFetches < 2 {
		t.Fatalf("expected updated page fetch retry, got %d attempt(s)", updateFetches)
	}

	doc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "Remote-Page.md"))
	if err != nil {
		t.Fatalf("read Remote-Page.md: %v", err)
	}
	if doc.Frontmatter.Version != 2 {
		t.Fatalf("version = %d, want 2", doc.Frontmatter.Version)
	}
	if !strings.Contains(doc.Body, "fresh body") {
		t.Fatalf("expected updated body after incremental pull, got:\n%s", doc.Body)
	}
	if strings.Contains(out.String(), "all remote updates were outside the target scope") {
		t.Fatalf("unexpected false no-op message:\n%s", out.String())
	}
}
