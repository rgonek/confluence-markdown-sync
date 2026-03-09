package cmd

import (
	"bytes"
	"context"
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

type preflightCapabilityFakePushRemote struct {
	*cmdFakePushRemote
	contentStatusErr error
}

func (f *preflightCapabilityFakePushRemote) GetContentStatus(_ context.Context, _ string, _ string) (string, error) {
	if f.contentStatusErr != nil {
		return "", f.contentStatusErr
	}
	return "", nil
}

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

func TestRunPush_DryRunResolvesCrossSpaceRelativeLinks(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	siblingSpaceDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(siblingSpaceDir, 0o750); err != nil {
		t.Fatalf("mkdir sibling space: %v", err)
	}
	writeMarkdown(t, filepath.Join(siblingSpaceDir, "Target Page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Target Page",
			ID:      "77",
			Version: 2,
		},
		Body: "Target content\n",
	})

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "See [Target](../Technical%20Docs%20(TD)/Target%20Page.md)\n",
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
		t.Fatalf("runPush dry-run should resolve cross-space relative links: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "push completed: 1 page change(s) would be synced") {
		t.Fatalf("dry-run output missing success summary:\n%s", out.String())
	}
	if len(fake.updateCalls) != 0 {
		t.Fatalf("dry-run should not perform remote writes, got %d update calls", len(fake.updateCalls))
	}
}

func TestRunPush_PreflightShowsPlanWithoutRemoteWrites(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Status:                 "Ready to review",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content with ![diagram](assets/new.png)\n",
	})
	assetDir := filepath.Join(spaceDir, "assets")
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{
			"assets/1/old.png": "att-stale",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(assetDir, "1"), 0o750); err != nil {
		t.Fatalf("mkdir old asset dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "1", "old.png"), []byte("old"), 0o600); err != nil {
		t.Fatalf("write stale asset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "new.png"), []byte("new"), 0o600); err != nil {
		t.Fatalf("write new asset: %v", err)
	}
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
		return &preflightCapabilityFakePushRemote{
			cmdFakePushRemote: newCmdFakePushRemote(1),
			contentStatusErr:  &confluence.APIError{StatusCode: 404, Message: "missing"},
		}, nil
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
	if factoryCalls != 1 {
		t.Fatalf("preflight should use remote factory once, got %d calls", factoryCalls)
	}

	text := out.String()
	if !strings.Contains(text, "preflight for space ENG") {
		t.Fatalf("preflight output missing header:\n%s", text)
	}
	if !strings.Contains(text, "Remote capability concerns:") {
		t.Fatalf("preflight output missing remote capability section:\n%s", text)
	}
	if !strings.Contains(text, "content-status metadata sync disabled for this push") {
		t.Fatalf("preflight output missing degraded-mode detail:\n%s", text)
	}
	if !strings.Contains(text, "Planned page mutations:") || !strings.Contains(text, "update root.md") {
		t.Fatalf("preflight output missing planned page mutations:\n%s", text)
	}
	if !strings.Contains(text, "Planned attachment mutations:") || !strings.Contains(text, "upload assets/1/new.png") || !strings.Contains(text, "delete assets/1/old.png") {
		t.Fatalf("preflight output missing planned attachment mutations:\n%s", text)
	}
}

func TestRunPush_PreflightHonorsExplicitForceConflictPolicy(t *testing.T) {
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

	previousPreflight := flagPushPreflight
	flagPushPreflight = true
	t.Cleanup(func() { flagPushPreflight = previousPreflight })

	fake := newCmdFakePushRemote(3)
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

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictForce, false); err != nil {
		t.Fatalf("runPush() preflight with force unexpected error: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "update root.md (page 1, \"Root\", version 4)") {
		t.Fatalf("preflight output missing forced remote-ahead version plan:\n%s", text)
	}
	if len(fake.updateCalls) != 0 {
		t.Fatalf("preflight should not perform remote writes, got %d update calls", len(fake.updateCalls))
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

func TestRunPush_PreflightShowsDestructiveDeleteProminent(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	// Delete the tracked file to produce a PushChangeDelete entry.
	if err := os.Remove(filepath.Join(spaceDir, "root.md")); err != nil {
		t.Fatalf("remove root.md: %v", err)
	}
	runGitForTest(t, repo, "rm", filepath.Join("Engineering (ENG)", "root.md"))
	runGitForTest(t, repo, "commit", "-m", "delete root page")

	previousPreflight := flagPushPreflight
	flagPushPreflight = true
	t.Cleanup(func() { flagPushPreflight = previousPreflight })

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
		t.Fatalf("runPush() preflight unexpected error: %v", err)
	}

	text := out.String()

	// Delete entries in the planned mutations section must be prominent.
	if !strings.Contains(text, "Destructive: delete") {
		t.Fatalf("preflight output missing prominent destructive delete marker:\n%s", text)
	}
	if !strings.Contains(text, "root.md") {
		t.Fatalf("preflight output missing deleted file name:\n%s", text)
	}

	// A dedicated destructive operations summary section must be present.
	if !strings.Contains(text, "Destructive operations in this push:") {
		t.Fatalf("preflight output missing Destructive operations section:\n%s", text)
	}

	// safety confirmation notice must appear.
	if !strings.Contains(text, "safety confirmation would be required") {
		t.Fatalf("preflight output missing safety confirmation notice:\n%s", text)
	}
}

func TestRunPush_AllModesCatchBrokenLinksIntroducedByDeletion(t *testing.T) {
	runParallelCommandTest(t)

	testCases := []struct {
		name      string
		preflight bool
		dryRun    bool
	}{
		{name: "preflight", preflight: true},
		{name: "dry-run", dryRun: true},
		{name: "push"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			spaceDir := preparePushRepoWithLinkedChildBaseline(t, repo)

			if err := os.Remove(filepath.Join(spaceDir, "child.md")); err != nil {
				t.Fatalf("remove child.md: %v", err)
			}

			previousPreflight := flagPushPreflight
			flagPushPreflight = tc.preflight
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

			out := &bytes.Buffer{}
			cmd := &cobra.Command{}
			cmd.SetOut(out)

			err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, tc.dryRun)
			if err == nil {
				t.Fatal("expected push validation failure")
			}
			if !strings.Contains(err.Error(), "validate failed") {
				t.Fatalf("expected validation failure, got: %v", err)
			}
			if !strings.Contains(out.String(), "Validation failed for root.md") {
				t.Fatalf("expected broken link validation to surface in root.md, got:\n%s", out.String())
			}
			if factoryCalls != 0 {
				t.Fatalf("expected validation failure before remote factory calls, got %d", factoryCalls)
			}
		})
	}
}
