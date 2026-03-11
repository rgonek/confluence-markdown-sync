package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPush_FileModeStillRequiresOnConflict(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	rootFile := filepath.Join(spaceDir, "root.md")

	writeMarkdown(t, rootFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	factoryCalls := 0
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) {
		factoryCalls++
		return newCmdFakePushRemote(1), nil
	}
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) {
		return newCmdFakePushRemote(1), nil
	}
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)
	setAutomationFlags(t, false, true) // non-interactive

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	// Pass empty onConflict with file target; should fail
	err := runPush(cmd, config.Target{Mode: config.TargetModeFile, Value: rootFile}, "", false)

	if err == nil {
		t.Fatal("runPush() expected non-interactive on-conflict error for file mode")
	}
	if !strings.Contains(err.Error(), "--non-interactive requires --on-conflict") {
		t.Fatalf("unexpected error: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("expected remote factory to not be called, got %d", factoryCalls)
	}
}

func TestRunPush_FileTargetDetectsWorkspaceChanges(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	rootFile := filepath.Join(spaceDir, "root.md")

	writeMarkdown(t, rootFile, fs.MarkdownDocument{
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
	cmd.SetOut(&bytes.Buffer{})

	if err := runPush(cmd, config.Target{Mode: config.TargetModeFile, Value: rootFile}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("expected one update call for file target push, got %d", len(fake.updateCalls))
	}
}

func TestRunPush_FileTargetAllowsMissingIDForNewPage(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	newFile := filepath.Join(spaceDir, "new-page.md")

	writeMarkdown(t, newFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New page",
		},
		Body: "new content\n",
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
	cmd.SetOut(&bytes.Buffer{})

	if err := runPush(cmd, config.Target{Mode: config.TargetModeFile, Value: newFile}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("expected one update call for new file push, got %d", len(fake.updateCalls))
	}

	doc, err := fs.ReadMarkdownDocument(newFile)
	if err != nil {
		t.Fatalf("read new page markdown: %v", err)
	}
	if strings.TrimSpace(doc.Frontmatter.ID) == "" {
		t.Fatal("expected push to persist generated id for new page")
	}
	if doc.Frontmatter.Version <= 0 {
		t.Fatalf("expected positive version after push, got %d", doc.Frontmatter.Version)
	}
}

func TestRunPush_SpaceModeAssumesPullMerge(t *testing.T) {
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
		Body: "Updated local content\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	// Set remote version to 2 to trigger a conflict
	fake := newCmdFakePushRemote(2)
	factoryCalls := 0
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) {
		factoryCalls++
		return fake, nil
	}
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) {
		return fake, nil
	}
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)
	setAutomationFlags(t, false, true) // non-interactive
	oldMergeResolution := flagMergeResolution
	flagMergeResolution = "keep-both"
	t.Cleanup(func() { flagMergeResolution = oldMergeResolution })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	// Pass empty onConflict; should default to pull-merge for space mode
	// and return nil (success) after auto-pulling
	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, "", false)

	if err != nil {
		t.Fatalf("runPush() unexpected error with default pull-merge policy: %v", err)
	}
	if factoryCalls == 0 {
		t.Fatal("expected remote factory to be called")
	}
}

func TestRunPush_SpaceModePullMergeRequiresMergeResolutionInNonInteractiveMode(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	setupEnv(t)
	chdirRepo(t, spaceDir)
	setAutomationFlags(t, false, true)
	oldMergeResolution := flagMergeResolution
	flagMergeResolution = ""
	t.Cleanup(func() { flagMergeResolution = oldMergeResolution })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, "", false)
	if err == nil {
		t.Fatal("runPush() expected merge-resolution requirement error")
	}
	if !strings.Contains(err.Error(), "--merge-resolution") {
		t.Fatalf("expected merge-resolution guidance, got: %v", err)
	}
}
