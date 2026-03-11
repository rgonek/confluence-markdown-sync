package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func TestRunPush_ReportJSONSuccessIsStable(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	setupEnv(t)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
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
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	chdirRepo(t, spaceDir)

	cmd := newPushCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=cancel"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v", err)
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "push", true)
	if !containsString(report.MutatedFiles, "root.md") {
		t.Fatalf("mutated files = %v, want root.md", report.MutatedFiles)
	}
	if len(report.MutatedPages) != 1 || report.MutatedPages[0].PageID != "1" || report.MutatedPages[0].Version != 2 {
		t.Fatalf("mutated pages = %+v, want page 1 version 2", report.MutatedPages)
	}
	if len(report.AttachmentOperations) != 1 {
		t.Fatalf("attachment operations = %+v, want one upload", report.AttachmentOperations)
	}
	if got := report.AttachmentOperations[0]; got.Type != "upload" || got.PageID != "1" || got.Path != "assets/1/new.png" {
		t.Fatalf("unexpected attachment operation: %+v", got)
	}
	if !containsRecoveryArtifact(report, "snapshot_ref", "cleaned_up") {
		t.Fatalf("recovery artifacts = %+v, want cleaned-up snapshot ref", report.RecoveryArtifacts)
	}
	if !containsRecoveryArtifact(report, "sync_branch", "cleaned_up") {
		t.Fatalf("recovery artifacts = %+v, want cleaned-up sync branch", report.RecoveryArtifacts)
	}
}

func TestRunPush_ReportJSONFailureOnWorkspaceSyncStateIsStructured(t *testing.T) {
	runParallelCommandTest(t)

	repo := createUnmergedWorkspaceRepo(t)
	chdirRepo(t, repo)

	cmd := newPushCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=cancel"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected push command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "push", false)
	if !strings.Contains(report.Error, "syncing state with unresolved files") {
		t.Fatalf("error = %q, want syncing-state failure", report.Error)
	}
}

func TestRunPush_ReportJSONPullMergeEmitsSingleObjectAndCapturesPullMergeReport(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	setupEnv(t)

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

	fake := newCmdFakePushRemote(3)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	chdirRepo(t, spaceDir)

	cmd := newPushCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=pull-merge"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	report := decodeCommandReportJSON(t, stdout.Bytes())
	assertReportMetadata(t, report, "push", true)
	if report.ConflictResolution == nil {
		t.Fatalf("conflict resolution = nil, want pull-merge details; report=%+v", report)
	}
	if report.ConflictResolution.Policy != OnConflictPullMerge {
		t.Fatalf("conflict resolution policy = %q, want %q", report.ConflictResolution.Policy, OnConflictPullMerge)
	}
	if report.ConflictResolution.Status != "completed" {
		t.Fatalf("conflict resolution status = %q, want completed", report.ConflictResolution.Status)
	}
	if !containsString(report.ConflictResolution.MutatedFiles, "Root.md") {
		t.Fatalf("conflict resolution mutated files = %v, want Root.md", report.ConflictResolution.MutatedFiles)
	}
	if !containsString(report.MutatedFiles, "Root.md") {
		t.Fatalf("outer mutated files = %v, want Root.md from pull-merge", report.MutatedFiles)
	}
}

func TestRunPush_ReportJSONFailureAroundWorktreeSetupIncludesRecoveryArtifacts(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	setupEnv(t)

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

	oldNow := nowUTC
	fixedNow := time.Date(2026, time.February, 1, 12, 34, 58, 0, time.UTC)
	nowUTC = func() time.Time { return fixedNow }
	t.Cleanup(func() { nowUTC = oldNow })

	worktreeDir := filepath.Join(repo, ".confluence-worktrees", "ENG-"+fixedNow.Format("20060102T150405Z"))
	if err := os.MkdirAll(worktreeDir, 0o750); err != nil {
		t.Fatalf("mkdir blocking worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeDir, "keep.txt"), []byte("block worktree"), 0o600); err != nil {
		t.Fatalf("write blocking worktree file: %v", err)
	}

	chdirRepo(t, spaceDir)

	cmd := newPushCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=cancel"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected push command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "push", false)
	if !containsRecoveryArtifact(report, "snapshot_ref", "retained") {
		t.Fatalf("recovery artifacts = %+v, want retained snapshot ref", report.RecoveryArtifacts)
	}
	if !containsRecoveryArtifact(report, "sync_branch", "retained") {
		t.Fatalf("recovery artifacts = %+v, want retained sync branch", report.RecoveryArtifacts)
	}
}
