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

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPush_UnresolvedValidationStopsBeforeRemoteWrites(t *testing.T) {
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
		Body: "[Broken](missing.md)\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	factoryCalls := 0
	oldFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) {
		factoryCalls++
		return &cmdFakePushRemote{}, nil
	}
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) {
		return &cmdFakePushRemote{}, nil
	}
	t.Cleanup(func() {
		newPushRemote = oldFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false)
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

func TestRunPush_WritesStructuredCommitTrailers(t *testing.T) {
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

	fake := newCmdFakePushRemote(1)
	fake.webURL = "https://example.atlassian.net/wiki/pages/1"
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
	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}

	headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
	if headBefore == headAfter {
		t.Fatal("expected push to create a commit")
	}

	logOut := runGitForTest(t, repo, "log", "-5", "--pretty=%B")
	if !strings.Contains(logOut, `Sync "Root" to Confluence (v2)`) {
		t.Fatalf("commit with subject 'Sync \"Root\" to Confluence (v2)' not found in log:\n%s", logOut)
	}

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

func TestRunPush_KeepsStateFileUntracked(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	trackedBefore := strings.TrimSpace(runGitForTest(t, repo, "ls-files", "**/.confluence-state.json"))
	if trackedBefore != "" {
		t.Fatalf("expected no tracked state file before push, got %q", trackedBefore)
	}

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

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got := strings.TrimSpace(state.AttachmentIndex["assets/1/new.png"]); got == "" {
		t.Fatalf("expected attachment index to be updated for assets/1/new.png")
	}

	trackedAfter := strings.TrimSpace(runGitForTest(t, repo, "ls-files", "**/.confluence-state.json"))
	if trackedAfter != "" {
		t.Fatalf("expected no tracked state file after push, got %q", trackedAfter)
	}
}

func TestRunPush_NoopSkipsSnapshotBranchAndTag(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

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

	headBefore := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}

	headAfter := strings.TrimSpace(runGitForTest(t, repo, "rev-parse", "HEAD"))
	if headBefore != headAfter {
		t.Fatalf("expected no-op push to keep HEAD unchanged: before=%s after=%s", headBefore, headAfter)
	}

	if refs := strings.TrimSpace(runGitForTest(t, repo, "for-each-ref", "refs/confluence-sync/snapshots/ENG/")); refs != "" {
		t.Fatalf("expected no snapshot refs for no-op push, got:\n%s", refs)
	}
	if branches := strings.TrimSpace(runGitForTest(t, repo, "branch", "--list", "sync/ENG/*")); branches != "" {
		t.Fatalf("expected no sync branch for no-op push, got:\n%s", branches)
	}
	if tags := strings.TrimSpace(runGitForTest(t, repo, "tag", "--list", "confluence-sync/push/ENG/*")); tags != "" {
		t.Fatalf("expected no push sync tag for no-op push, got: %s", tags)
	}
	if factoryCalls != 0 {
		t.Fatalf("expected no remote factory calls for early no-op push, got %d", factoryCalls)
	}
}

func TestRunPush_WorksWithoutGitRemoteConfigured(t *testing.T) {
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

	if remotes := strings.TrimSpace(runGitForTest(t, repo, "remote")); remotes != "" {
		t.Fatalf("expected no git remotes, got %q", remotes)
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
		t.Fatalf("runPush() error without git remote: %v", err)
	}
}

func TestRunPush_MermaidWarningAppearsBeforePushAndPushesCodeBlockADF(t *testing.T) {
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
		Body: "```mermaid\ngraph TD\n  A --> B\n```\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local mermaid change")

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

	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v\nOutput:\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), "MERMAID_PRESERVED_AS_CODEBLOCK") {
		t.Fatalf("expected Mermaid warning in push output, got:\n%s", out.String())
	}
	if len(fake.updateCalls) == 0 {
		t.Fatal("expected push to update at least one page")
	}

	adf := string(fake.updateCalls[len(fake.updateCalls)-1].Input.BodyADF)
	if !strings.Contains(adf, "\"type\":\"codeBlock\"") {
		t.Fatalf("expected Mermaid push ADF to contain codeBlock node, got: %s", adf)
	}
	if !strings.Contains(adf, "\"language\":\"mermaid\"") {
		t.Fatalf("expected Mermaid push ADF to preserve mermaid language, got: %s", adf)
	}
}

func TestRunPush_CrossSpaceRelativeLinkParityWithValidate(t *testing.T) {
	runParallelCommandTest(t)

	testCases := []struct {
		name           string
		sourceDirName  string
		sourceKey      string
		targetDirName  string
		targetKey      string
		targetPageID   string
		targetFileName string
	}{
		{
			name:           "ENG_to_TD",
			sourceDirName:  "Engineering (ENG)",
			sourceKey:      "ENG",
			targetDirName:  "Technical Docs (TD)",
			targetKey:      "TD",
			targetPageID:   "200",
			targetFileName: "target.md",
		},
		{
			name:           "TD_to_ENG",
			sourceDirName:  "Technical Docs (TD)",
			sourceKey:      "TD",
			targetDirName:  "Engineering (ENG)",
			targetKey:      "ENG",
			targetPageID:   "300",
			targetFileName: "target.md",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			setupGitRepo(t, repo)
			setupEnv(t)

			sourceSpaceDir := filepath.Join(repo, tc.sourceDirName)
			targetSpaceDir := filepath.Join(repo, tc.targetDirName)
			if err := os.MkdirAll(sourceSpaceDir, 0o750); err != nil {
				t.Fatalf("mkdir source dir: %v", err)
			}
			if err := os.MkdirAll(targetSpaceDir, 0o750); err != nil {
				t.Fatalf("mkdir target dir: %v", err)
			}

			targetPath := filepath.Join(targetSpaceDir, tc.targetFileName)
			writeMarkdown(t, targetPath, fs.MarkdownDocument{
				Frontmatter: fs.Frontmatter{
					Title:   "Target",
					ID:      tc.targetPageID,
					Version: 1,
				},
				Body: "target\n",
			})

			if err := fs.SaveState(sourceSpaceDir, fs.SpaceState{SpaceKey: tc.sourceKey}); err != nil {
				t.Fatalf("save source state: %v", err)
			}
			if err := fs.SaveState(targetSpaceDir, fs.SpaceState{
				SpaceKey:      tc.targetKey,
				PagePathIndex: map[string]string{tc.targetFileName: tc.targetPageID},
			}); err != nil {
				t.Fatalf("save target state: %v", err)
			}

			if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
				t.Fatalf("write .gitignore: %v", err)
			}

			runGitForTest(t, repo, "add", ".")
			runGitForTest(t, repo, "commit", "-m", "baseline")
			runGitForTest(t, repo, "tag", "-a", fmt.Sprintf("confluence-sync/pull/%s/20260305T120000Z", tc.sourceKey), "-m", "baseline pull")

			linkTargetDir := strings.ReplaceAll(tc.targetDirName, " ", "%20")
			writeMarkdown(t, filepath.Join(sourceSpaceDir, "new.md"), fs.MarkdownDocument{
				Frontmatter: fs.Frontmatter{
					Title: "New Page",
				},
				Body: fmt.Sprintf("[Cross Space](../%s/%s#section-a)\n", linkTargetDir, tc.targetFileName),
			})

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

			chdirRepo(t, sourceSpaceDir)

			validateOut := &bytes.Buffer{}
			if err := runValidateTargetWithContext(context.Background(), validateOut, config.Target{Mode: config.TargetModeSpace, Value: sourceSpaceDir}); err != nil {
				t.Fatalf("validate failed before push: %v\nOutput:\n%s", err, validateOut.String())
			}

			cmd := &cobra.Command{}
			cmd.SetOut(&bytes.Buffer{})
			if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
				t.Fatalf("runPush() unexpected error: %v", err)
			}

			if len(fake.updateCalls) == 0 {
				t.Fatal("expected at least one update call")
			}
			body := string(fake.updateCalls[len(fake.updateCalls)-1].Input.BodyADF)
			expectedFragment := "pageId=" + tc.targetPageID + "#section-a"
			if !strings.Contains(body, expectedFragment) {
				t.Fatalf("expected pushed ADF to contain %q, body=%s", expectedFragment, body)
			}
		})
	}
}

type failingPushRemote struct {
	*cmdFakePushRemote
}

func (f *failingPushRemote) UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	return confluence.Page{}, errors.New("simulated update failure")
}

func TestPushNoOp_ExplainsReason(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	// No local changes after baseline — push should be a no-op.
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
	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false); err != nil {
		t.Fatalf("runPush() unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "no local markdown changes detected since last sync") {
		t.Fatalf("expected no-op message to explain reason, got:\n%s", got)
	}
	if factoryCalls != 0 {
		t.Fatalf("expected no remote calls for early no-op push, got %d", factoryCalls)
	}
}

func TestPushNoOp_DryRunExplainsReason(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)

	// No local changes after baseline — dry-run push should be a no-op.
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) {
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
	if err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, true); err != nil {
		t.Fatalf("runPush() dry-run unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "no local markdown changes detected since last sync") {
		t.Fatalf("expected dry-run no-op message to explain reason, got:\n%s", got)
	}
}
