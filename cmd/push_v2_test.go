package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

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
