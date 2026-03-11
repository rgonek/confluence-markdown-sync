package cmd

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPush_ConflictPolicies(t *testing.T) {
	runParallelCommandTest(t)

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
			name:   "pull-merge",
			policy: OnConflictPullMerge,
			// No error expected because it auto-pulls and returns nil
			wantErrContains: "",
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
					Title: "Root",
					ID:    "1",

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

			setupEnv(t)
			chdirRepo(t, spaceDir)

			headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

			cmd := &cobra.Command{}
			cmd.SetOut(&bytes.Buffer{})
			err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, tc.policy, false)

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
			if tc.wantUpdates == 0 && tc.policy != OnConflictPullMerge && headBefore != headAfter {
				t.Fatalf("HEAD changed for conflict case %q", tc.name)
			}
		})
	}
}

func TestRunPush_PullMergeRestoresStashedWorkspaceBeforePull(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	rootPath := filepath.Join(spaceDir, "root.md")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "local uncommitted content\n",
	})

	fake := newCmdFakePushRemote(3)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	restoredBeforePull := false
	oldRunPullForPush := runPullForPush
	runPullForPush = func(_ *cobra.Command, _ config.Target) (commandRunReport, error) {
		doc, err := fs.ReadMarkdownDocument(rootPath)
		if err != nil {
			return commandRunReport{}, err
		}
		restoredBeforePull = strings.Contains(doc.Body, "local uncommitted content")
		return commandRunReport{}, errors.New("stop pull")
	}
	t.Cleanup(func() {
		runPullForPush = oldRunPullForPush
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictPullMerge, false)
	if err == nil {
		t.Fatal("runPush() expected error from stubbed pull")
	}
	if !strings.Contains(err.Error(), "automatic pull-merge failed: stop pull") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !restoredBeforePull {
		t.Fatal("expected local workspace changes to be restored before automatic pull-merge")
	}

	if stashList := strings.TrimSpace(runGitForTest(t, repo, "stash", "list")); stashList != "" {
		t.Fatalf("expected stash to be empty after workspace restore, got:\n%s", stashList)
	}
}

func TestRunPush_PullMergeDoesNotForceDiscardLocalDuringPull(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	rootPath := filepath.Join(spaceDir, "root.md")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "local uncommitted content\n",
	})

	fake := newCmdFakePushRemote(3)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	oldRunPullForPush := runPullForPush
	oldDiscardLocal := flagPullDiscardLocal
	discardLocalDuringPull := true
	restoredBeforePull := false
	runPullForPush = func(_ *cobra.Command, _ config.Target) (commandRunReport, error) {
		discardLocalDuringPull = flagPullDiscardLocal
		doc, err := fs.ReadMarkdownDocument(rootPath)
		if err != nil {
			return commandRunReport{}, err
		}
		restoredBeforePull = strings.Contains(doc.Body, "local uncommitted content")
		doc.Frontmatter.Version = 3
		doc.Body += "\nremote change after pull-merge\n"
		if err := fs.WriteMarkdownDocument(rootPath, doc); err != nil {
			return commandRunReport{}, err
		}
		return commandRunReport{MutatedFiles: []string{"root.md"}}, nil
	}
	flagPullDiscardLocal = true
	t.Cleanup(func() {
		runPullForPush = oldRunPullForPush
		flagPullDiscardLocal = oldDiscardLocal
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictPullMerge, false)
	if err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}
	if discardLocalDuringPull {
		t.Fatal("expected automatic pull-merge to disable discard-local and preserve local edits")
	}
	if !restoredBeforePull {
		t.Fatal("expected local workspace changes to be restored before automatic pull-merge")
	}
	doc, err := fs.ReadMarkdownDocument(rootPath)
	if err != nil {
		t.Fatalf("read root markdown: %v", err)
	}
	if !strings.Contains(doc.Body, "local uncommitted content") {
		t.Fatalf("expected local edit to survive automatic pull-merge, got body %q", doc.Body)
	}
	if doc.Frontmatter.Version != 3 {
		t.Fatalf("expected stubbed pull-merge to update version to 3, got %d", doc.Frontmatter.Version)
	}
	if stashList := strings.TrimSpace(runGitForTest(t, repo, "stash", "list")); stashList != "" {
		t.Fatalf("expected stash to be empty after automatic pull-merge, got:\n%s", stashList)
	}
	if !strings.Contains(out.String(), "automatic pull-merge completed") {
		t.Fatalf("expected pull-merge completion guidance, got:\n%s", out.String())
	}
}
