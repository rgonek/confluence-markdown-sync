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

func TestRunPush_DryRunDoesNotMutateFrontmatter(t *testing.T) {
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
	setAutomationFlags(t, true, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictPullMerge, true); err != nil {
		t.Fatalf("runPush dry-run error: %v", err)
	}

	doc, err := fs.ReadMarkdownDocument(newFile)
	if err != nil {
		t.Fatalf("read new page: %v", err)
	}
	if doc.Frontmatter.ID != "" {
		t.Fatalf("dry-run mutated id: %q", doc.Frontmatter.ID)
	}
	if doc.Frontmatter.Version != 0 {
		t.Fatalf("dry-run mutated version: %d", doc.Frontmatter.Version)
	}
}

func TestRunPush_DryRunDoesNotMutateExistingFrontmatter(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	existingFile := filepath.Join(spaceDir, "root.md")
	docBefore, _ := fs.ReadMarkdownDocument(existingFile)
	originalVersion := docBefore.Frontmatter.Version
	if originalVersion == 0 {
		t.Fatal("expected original version to be non-zero")
	}

	fake := newCmdFakePushRemote(originalVersion)
	oldPushFactory := newPushRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	t.Cleanup(func() { newPushRemote = oldPushFactory })

	setupEnv(t)
	chdirRepo(t, spaceDir)
	setAutomationFlags(t, true, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictForce, true); err != nil {
		t.Fatalf("runPush dry-run error: %v", err)
	}

	docAfter, _ := fs.ReadMarkdownDocument(existingFile)
	if docAfter.Frontmatter.Version != originalVersion {
		t.Fatalf("dry-run mutated version: got %d, want %d", docAfter.Frontmatter.Version, originalVersion)
	}
}

func TestRunPush_DryRunShowsMarkdownPreviewNotRawADF(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	newFile := filepath.Join(spaceDir, "preview-page.md")
	writeMarkdown(t, newFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Preview page",
		},
		Body: "hello dry-run\n",
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
	setAutomationFlags(t, true, true)

	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictPullMerge, true); err != nil {
		t.Fatalf("runPush dry-run error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Body (Markdown preview)") {
		t.Fatalf("expected Markdown preview in dry-run output, got:\n%s", got)
	}
	if strings.Contains(got, "\"type\": \"doc\"") {
		t.Fatalf("dry-run output should not contain raw ADF JSON, got:\n%s", got)
	}
	if !strings.Contains(got, "hello dry-run") {
		t.Fatalf("expected body content in dry-run Markdown preview, got:\n%s", got)
	}
}

func TestRunPush_PreflightShowsPlanWithoutRemoteWrites(t *testing.T) {
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

	previousPreflight := flagPushPreflight
	flagPushPreflight = true
	t.Cleanup(func() { flagPushPreflight = previousPreflight })

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

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, "", false); err != nil {
		t.Fatalf("runPush() preflight unexpected error: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("preflight should not require remote factory here, got %d calls", factoryCalls)
	}

	text := out.String()
	if !strings.Contains(text, "preflight for space ENG") {
		t.Fatalf("preflight output missing header:\n%s", text)
	}
	if !strings.Contains(text, "changes: 1 (A:0 M:1 D:0)") {
		t.Fatalf("preflight output missing change summary:\n%s", text)
	}
}

func TestRunPush_PreflightRejectsDryRunCombination(t *testing.T) {
	runParallelCommandTest(t)

	previousPreflight := flagPushPreflight
	flagPushPreflight = true
	t.Cleanup(func() { flagPushPreflight = previousPreflight })

	cmd := &cobra.Command{}
	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, "", true)
	if err == nil {
		t.Fatal("expected error when combining --preflight and --dry-run")
	}
	if !strings.Contains(err.Error(), "--preflight and --dry-run cannot be used together") {
		t.Fatalf("unexpected error: %v", err)
	}
}
