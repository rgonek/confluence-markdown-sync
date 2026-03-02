package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPush_IncludesUntrackedAssetsFromWorkspaceSnapshot(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "![asset](assets/new.png)\n",
	})

	assetPath := filepath.Join(spaceDir, "assets", "new.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}
	if len(fake.uploadAttachmentCalls) != 1 {
		t.Fatalf("expected one uploaded attachment, got %d", len(fake.uploadAttachmentCalls))
	}
}

func TestRunPush_FailureRetainsSnapshotAndSyncBranch(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content that will fail\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	fake := newCmdFakePushRemote(1)
	failingFake := &failingPushRemote{cmdFakePushRemote: fake}

	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return failingFake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return failingFake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false)
	if err == nil {
		t.Fatal("runPush() expected error")
	}

	refs := runGitForTest(t, repo, "for-each-ref", "refs/confluence-sync/snapshots/ENG/")
	if strings.TrimSpace(refs) == "" {
		t.Error("expected snapshot ref to be retained on failure")
	}

	branches := runGitForTest(t, repo, "branch", "--list", "sync/ENG/*")
	if strings.TrimSpace(branches) == "" {
		t.Error("expected sync branch to be retained on failure")
	}
}

func TestRunPush_PreservesOutOfScopeChanges(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	outOfScope := filepath.Join(repo, "README.md")
	if err := os.WriteFile(outOfScope, []byte("Original README"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGitForTest(t, repo, "add", "README.md")
	runGitForTest(t, repo, "commit", "-m", "add readme")

	if err := os.WriteFile(outOfScope, []byte("Modified README"), 0o600); err != nil {
		t.Fatalf("modify readme: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false)
	if err != nil {
		t.Fatalf("runPush() failed: %v", err)
	}

	content, err := os.ReadFile(outOfScope) //nolint:gosec // test path is created in t.TempDir
	if err != nil {
		t.Fatalf("read out-of-scope file: %v", err)
	}
	if string(content) != "Modified README" {
		t.Errorf("out-of-scope change lost! got %q, want %q", string(content), "Modified README")
	}

	doc, _ := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if doc.Frontmatter.Version != 2 {
		t.Errorf("expected version 2, got %d", doc.Frontmatter.Version)
	}

	stashList := runGitForTest(t, repo, "stash", "list")
	if strings.TrimSpace(stashList) != "" {
		t.Errorf("expected stash to be empty, got:\n%s", stashList)
	}
}

func TestRunPush_DoesNotWarnForSyncedUntrackedFilesInStash(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	newPagePath := filepath.Join(spaceDir, "new-page.md")
	writeMarkdown(t, newPagePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New Page",
		},
		Body: "New page content\n",
	})

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() failed: %v", err)
	}

	if strings.Contains(out.String(), "stash restore had conflicts") {
		t.Fatalf("expected stash restore without conflict warning, got:\n%s", out.String())
	}

	stashList := runGitForTest(t, repo, "stash", "list")
	if strings.TrimSpace(stashList) != "" {
		t.Fatalf("expected stash to be empty, got:\n%s", stashList)
	}

	doc, err := fs.ReadMarkdownDocument(newPagePath)
	if err != nil {
		t.Fatalf("read new page markdown: %v", err)
	}
	if strings.TrimSpace(doc.Frontmatter.ID) == "" {
		t.Fatalf("expected pushed new page to have assigned ID")
	}
	if doc.Frontmatter.Version <= 0 {
		t.Fatalf("expected pushed new page version > 0, got %d", doc.Frontmatter.Version)
	}
}

func TestRunPush_FileTargetRestoresUnsyncedScopedTrackedChangesFromStash(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	secondaryPath := filepath.Join(spaceDir, "secondary.md")
	writeMarkdown(t, secondaryPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Secondary",
			ID:    "2",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Baseline secondary content\n",
	})

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	state.PagePathIndex["secondary.md"] = "2"
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "add secondary page")

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated root content\n",
	})

	writeMarkdown(t, secondaryPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Secondary",
			ID:    "2",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Locally modified secondary content\n",
	})

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPush(cmd, config.Target{Mode: config.TargetModeFile, Value: rootPath}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() failed: %v", err)
	}

	if strings.Contains(out.String(), "stash restore had conflicts") {
		t.Fatalf("expected stash restore without conflict warning, got:\n%s", out.String())
	}

	secondaryDoc, err := fs.ReadMarkdownDocument(secondaryPath)
	if err != nil {
		t.Fatalf("read secondary markdown: %v", err)
	}
	if !strings.Contains(secondaryDoc.Body, "Locally modified secondary content") {
		t.Fatalf("secondary markdown body lost local change: %q", secondaryDoc.Body)
	}

	stashList := runGitForTest(t, repo, "stash", "list")
	if strings.TrimSpace(stashList) != "" {
		t.Fatalf("expected stash to be empty, got:\n%s", stashList)
	}
}
