package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPush_WarnsWhenRecoveryMetadataWriteFails(t *testing.T) {
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
	oldNow := nowUTC
	fixedNow := time.Date(2026, time.February, 1, 12, 34, 56, 0, time.UTC)
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return failingFake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return failingFake, nil }
	nowUTC = func() time.Time { return fixedNow }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
		nowUTC = oldNow
	})

	blockingDir := filepath.Join(repo, ".git", "confluence-recovery")
	if err := os.MkdirAll(blockingDir, 0o750); err != nil {
		t.Fatalf("mkdir recovery root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockingDir, "ENG"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocking recovery path: %v", err)
	}

	setupEnv(t)
	chdirRepo(t, spaceDir)

	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false)
	if err == nil {
		t.Fatal("runPush() expected error")
	}
	if !strings.Contains(err.Error(), "simulated update failure") {
		t.Fatalf("expected primary push failure to be preserved, got: %v", err)
	}
	if !strings.Contains(out.String(), "warning: failed to persist recovery metadata") {
		t.Fatalf("expected warning about recovery metadata write failure, got:\n%s", out.String())
	}
}

func TestRunPush_WarnsWhenRecoveryMetadataCleanupFails(t *testing.T) {
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

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	oldNow := nowUTC
	fixedNow := time.Date(2026, time.February, 1, 12, 34, 57, 0, time.UTC)
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	nowUTC = func() time.Time { return fixedNow }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
		nowUTC = oldNow
	})

	recoveryPath := filepath.Join(repo, ".git", "confluence-recovery", "ENG", fixedNow.Format("20060102T150405Z")+".json")
	if err := os.MkdirAll(recoveryPath, 0o750); err != nil {
		t.Fatalf("mkdir blocking recovery metadata path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(recoveryPath, "keep.txt"), []byte("block deletion"), 0o600); err != nil {
		t.Fatalf("write blocking recovery metadata child: %v", err)
	}

	setupEnv(t)
	chdirRepo(t, spaceDir)

	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)

	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "warning: failed to clean up recovery metadata") {
		t.Fatalf("expected warning about recovery metadata cleanup failure, got:\n%s", out.String())
	}
}
