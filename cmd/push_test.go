package cmd

import (
	"bytes"
	"context"
	"errors"
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

func TestRunPush_UnresolvedValidationStopsBeforeRemoteWrites(t *testing.T) {
	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ConfluencePageID:       "1",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "[Broken](missing.md)\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	factoryCalls := 0
	oldFactory := newPushRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) {
		factoryCalls++
		return &cmdFakePushRemote{}, nil
	}
	t.Cleanup(func() { newPushRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, OnConflictCancel)
	if err == nil {
		t.Fatal("runPush() expected error for unresolved link")
	}
	if !strings.Contains(err.Error(), "pre-push validate failed") {
		t.Fatalf("expected pre-push validate failure, got: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("expected push remote factory to not be called, got %d", factoryCalls)
	}

	headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
	if headBefore != headAfter {
		t.Fatalf("HEAD changed on validation failure: before=%s after=%s", headBefore, headAfter)
	}
}

func TestRunPush_ConflictPolicies(t *testing.T) {
	testCases := []struct {
		name            string
		policy          string
		wantErrContains string
		wantUpdates     int
		wantVersion     int
	}{
		{
			name:            "cancel",
			policy:          OnConflictCancel,
			wantErrContains: "rerun with --on-conflict=force",
			wantUpdates:     0,
		},
		{
			name:            "pull-merge",
			policy:          OnConflictPullMerge,
			wantErrContains: "pull-merge policy selected",
			wantUpdates:     0,
		},
		{
			name:        "force",
			policy:      OnConflictForce,
			wantUpdates: 1,
			wantVersion: 4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			spaceDir := preparePushRepoWithBaseline(t, repo)

			writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
				Frontmatter: fs.Frontmatter{
					Title:                  "Root",
					ConfluencePageID:       "1",
					ConfluenceSpaceKey:     "ENG",
					ConfluenceVersion:      1,
					ConfluenceLastModified: "2026-02-01T10:00:00Z",
				},
				Body: "Updated local content\n",
			})
			runGitForTest(t, repo, "add", ".")
			runGitForTest(t, repo, "commit", "-m", "local change")

			fake := newCmdFakePushRemote(3)
			oldFactory := newPushRemote
			newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
			t.Cleanup(func() { newPushRemote = oldFactory })

			setupEnv(t)
			chdirRepo(t, repo)

			headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

			cmd := &cobra.Command{}
			cmd.SetOut(&bytes.Buffer{})
			err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, tc.policy)

			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("runPush() expected error containing %q", tc.wantErrContains)
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErrContains)
				}
			} else if err != nil {
				t.Fatalf("runPush() unexpected error: %v", err)
			}

			if len(fake.updateCalls) != tc.wantUpdates {
				t.Fatalf("update calls = %d, want %d", len(fake.updateCalls), tc.wantUpdates)
			}
			if tc.wantUpdates > 0 {
				gotVersion := fake.updateCalls[0].Input.Version
				if gotVersion != tc.wantVersion {
					t.Fatalf("update version = %d, want %d", gotVersion, tc.wantVersion)
				}
			}

			headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
			if tc.wantUpdates == 0 && headBefore != headAfter {
				t.Fatalf("HEAD changed for conflict case %q", tc.name)
			}
		})
	}
}

func TestRunPush_WritesStructuredCommitTrailers(t *testing.T) {
	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ConfluencePageID:       "1",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	fake := newCmdFakePushRemote(1)
	fake.webURL = "https://example.atlassian.net/wiki/pages/1"
	oldFactory := newPushRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	t.Cleanup(func() { newPushRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, OnConflictCancel); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}

	headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
	if headBefore == headAfter {
		t.Fatal("expected push to create a commit")
	}

	// Check logs to find the sync commit (it might be merged)
	// We look for the subject in recent history
	logOut := runGitForTest(t, repo, "log", "-5", "--pretty=%B")
	if !strings.Contains(logOut, `Sync "Root" to Confluence (v2)`) {
		t.Fatalf("commit with subject 'Sync \"Root\" to Confluence (v2)' not found in log:\n%s", logOut)
	}

	// We also verify trailers are present in that commit's message
	for _, expected := range []string{
		"Confluence-Page-ID: 1",
		"Confluence-Version: 2",
		"Confluence-Space-Key: ENG",
		"Confluence-URL: https://example.atlassian.net/wiki/pages/1",
	} {
		if !strings.Contains(logOut, expected) {
			t.Fatalf("commit message missing %q:\n%s", expected, logOut)
		}
	}
}

func TestRunPush_FailureRetainsSnapshotAndSyncBranch(t *testing.T) {
	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	// Make a change
	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ConfluencePageID:       "1",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content that will fail\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	// Fake remote that fails on update
	fake := newCmdFakePushRemote(1)
	failingFake := &failingPushRemote{cmdFakePushRemote: fake}

	oldFactory := newPushRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return failingFake, nil }
	t.Cleanup(func() { newPushRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, OnConflictCancel)
	if err == nil {
		t.Fatal("runPush() expected error")
	}

	// Verify Snapshot Ref exists
	// refs format: refs/confluence-sync/snapshots/ENG/TIMESTAMP
	// We list all refs matching the pattern
	refs := runGitForTest(t, repo, "for-each-ref", "refs/confluence-sync/snapshots/ENG/")
	if strings.TrimSpace(refs) == "" {
		t.Error("expected snapshot ref to be retained on failure")
	}

	// Verify Sync Branch exists
	branches := runGitForTest(t, repo, "branch", "--list", "sync/ENG/*")
	if strings.TrimSpace(branches) == "" {
		t.Error("expected sync branch to be retained on failure")
	}

	// Verify Worktree is cleaned up (always cleanup)
	// We can check .confluence-worktrees dir
	wtDir := filepath.Join(repo, ".confluence-worktrees")
	if _, err := os.Stat(wtDir); err == nil {
		entries, _ := os.ReadDir(wtDir)
		if len(entries) > 0 {
			// Actually, defer cleanup should remove the specific worktree.
			// git worktree prune might be needed if directory is removed but git metadata remains.
			// But our implementation calls RemoveWorktree.
			// Let's check 'git worktree list'.
			// "worktree list" output format: path <SHA> [branch]
			// We check if any path contains .confluence-worktrees
			wts := runGitForTest(t, repo, "worktree", "list")
			if strings.Contains(wts, ".confluence-worktrees") {
				// Wait, if RemoveWorktree failed or wasn't called (it is deferred), it might persist.
				// But we expect it to be removed.
				t.Errorf("expected worktree to be removed, got list:\n%s", wts)
			}
		}
	}
}

func TestRunPush_PreservesOutOfScopeChanges(t *testing.T) {
	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	// Create out-of-scope file
	outOfScope := filepath.Join(repo, "README.md")
	if err := os.WriteFile(outOfScope, []byte("Original README"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForTest(t, repo, "add", "README.md")
	runGitForTest(t, repo, "commit", "-m", "add readme")

	// Modify out-of-scope file (unstaged)
	if err := os.WriteFile(outOfScope, []byte("Modified README"), 0o644); err != nil {
		t.Fatalf("modify readme: %v", err)
	}

	// Modify in-scope file
	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ConfluencePageID:       "1",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})
	// We leave it unstaged (runPush should snapshot it)

	fake := newCmdFakePushRemote(1)
	oldFactory := newPushRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	t.Cleanup(func() { newPushRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, OnConflictCancel)
	t.Logf("runPush stdout:\n%s", out.String())
	if err != nil {
		t.Fatalf("runPush() failed: %v", err)
	}

	// Verify out-of-scope file preserved
	content, err := os.ReadFile(outOfScope)
	if err != nil {
		t.Fatalf("read out-of-scope file: %v", err)
	}
	if string(content) != "Modified README" {
		t.Errorf("out-of-scope change lost! got %q, want %q", string(content), "Modified README")
	}

	// Verify in-scope file updated (version bump)
	doc, _ := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if doc.Frontmatter.ConfluenceVersion != 2 {
		t.Logf("git status:\n%s", runGitForTest(t, repo, "status"))
		t.Logf("git log -p:\n%s", runGitForTest(t, repo, "log", "-p"))
		content, _ := os.ReadFile(filepath.Join(spaceDir, "root.md"))
		t.Logf("root.md content:\n%s", string(content))
		t.Errorf("expected version 2, got %d", doc.Frontmatter.ConfluenceVersion)
	}

	// Verify stash is popped (empty)
	stashList := runGitForTest(t, repo, "stash", "list")
	if strings.TrimSpace(stashList) != "" {
		t.Errorf("expected stash to be empty, got:\n%s", stashList)
	}
}

type failingPushRemote struct {
	*cmdFakePushRemote
}

func (f *failingPushRemote) UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	return confluence.Page{}, errors.New("simulated update failure")
}

func preparePushRepoWithBaseline(t *testing.T, repo string) string {
	t.Helper()
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
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Baseline\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")
	runGitForTest(t, repo, "tag", "-a", "confluence-sync/pull/ENG/20260201T120000Z", "-m", "baseline pull")

	return spaceDir
}

type cmdFakePushRemote struct {
	space                 confluence.Space
	pages                 []confluence.Page
	pagesByID             map[string]confluence.Page
	updateCalls           []cmdPushUpdateCall
	archiveCalls          [][]string
	deletePageCalls       []string
	uploadAttachmentCalls []confluence.AttachmentUploadInput
	deleteAttachmentCalls []string
	webURL                string
}

type cmdPushUpdateCall struct {
	PageID string
	Input  confluence.PageUpsertInput
}

func newCmdFakePushRemote(remoteVersion int) *cmdFakePushRemote {
	page := confluence.Page{
		ID:           "1",
		SpaceID:      "space-1",
		Title:        "Root",
		Version:      remoteVersion,
		LastModified: time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC),
		WebURL:       "https://example.atlassian.net/wiki/pages/1",
	}
	return &cmdFakePushRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{page},
		pagesByID: map[string]confluence.Page{
			"1": page,
		},
		webURL: page.WebURL,
	}
}

func (f *cmdFakePushRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *cmdFakePushRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *cmdFakePushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *cmdFakePushRemote) UpdatePage(_ context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	f.updateCalls = append(f.updateCalls, cmdPushUpdateCall{PageID: pageID, Input: input})
	updated := confluence.Page{
		ID:           pageID,
		SpaceID:      input.SpaceID,
		Title:        input.Title,
		ParentPageID: input.ParentPageID,
		Version:      input.Version,
		LastModified: time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC),
		WebURL:       firstOrDefault(strings.TrimSpace(f.webURL), fmt.Sprintf("https://example.atlassian.net/wiki/pages/%s", pageID)),
	}
	f.pagesByID[pageID] = updated
	f.pages = []confluence.Page{updated}
	return updated, nil
}

func (f *cmdFakePushRemote) ArchivePages(_ context.Context, pageIDs []string) (confluence.ArchiveResult, error) {
	clone := append([]string(nil), pageIDs...)
	f.archiveCalls = append(f.archiveCalls, clone)
	return confluence.ArchiveResult{TaskID: "task-1"}, nil
}

func (f *cmdFakePushRemote) DeletePage(_ context.Context, pageID string, _ bool) error {
	f.deletePageCalls = append(f.deletePageCalls, pageID)
	return nil
}

func (f *cmdFakePushRemote) UploadAttachment(_ context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	f.uploadAttachmentCalls = append(f.uploadAttachmentCalls, input)
	id := fmt.Sprintf("att-%d", len(f.uploadAttachmentCalls))
	return confluence.Attachment{ID: id, PageID: input.PageID, Filename: input.Filename}, nil
}

func (f *cmdFakePushRemote) DeleteAttachment(_ context.Context, attachmentID string) error {
	f.deleteAttachmentCalls = append(f.deleteAttachmentCalls, attachmentID)
	return nil
}

func firstOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
