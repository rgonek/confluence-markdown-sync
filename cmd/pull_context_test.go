package cmd

import (
	"bytes"
	"context"
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

func TestResolveInitialPullContext_TrackedDirWithoutSpaceKeyUsesDirSuffix(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "Technical documentation (TD)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"missing.md": "1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	chdirRepo(t, spaceDir)

	ctx, err := resolveInitialPullContext(config.Target{Mode: config.TargetModeSpace, Value: ""})
	if err != nil {
		t.Fatalf("resolveInitialPullContext() error: %v", err)
	}

	if ctx.spaceDir != spaceDir {
		t.Fatalf("spaceDir = %q, want %q", ctx.spaceDir, spaceDir)
	}
	if ctx.spaceKey != "TD" {
		t.Fatalf("spaceKey = %q, want TD", ctx.spaceKey)
	}
	if !ctx.fixedDir {
		t.Fatal("expected fixedDir=true for tracked directory")
	}
}

func TestListAllPullChangesForEstimate_UsesContinuationOffsets(t *testing.T) {
	starts := make([]int, 0)

	remote := &cmdFakePullRemote{
		listChanges: func(opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
			starts = append(starts, opts.Start)
			switch opts.Start {
			case 0:
				return confluence.ChangeListResult{
					Changes:   []confluence.Change{{PageID: "1"}},
					HasMore:   true,
					NextStart: 40,
				}, nil
			case 40:
				return confluence.ChangeListResult{
					Changes:   []confluence.Change{{PageID: "2"}},
					HasMore:   true,
					NextStart: 90,
				}, nil
			case 90:
				return confluence.ChangeListResult{
					Changes: []confluence.Change{{PageID: "3"}},
					HasMore: false,
				}, nil
			default:
				return confluence.ChangeListResult{}, fmt.Errorf("unexpected start: %d", opts.Start)
			}
		},
	}

	changes, err := listAllPullChangesForEstimate(context.Background(), remote, confluence.ChangeListOptions{
		SpaceKey: "ENG",
		Limit:    25,
	}, nil)
	if err != nil {
		t.Fatalf("listAllPullChangesForEstimate() error: %v", err)
	}

	if len(changes) != 3 {
		t.Fatalf("changes count = %d, want 3", len(changes))
	}
	if len(starts) != 3 {
		t.Fatalf("start count = %d, want 3", len(starts))
	}
	if starts[0] != 0 || starts[1] != 40 || starts[2] != 90 {
		t.Fatalf("starts = %v, want [0 40 90]", starts)
	}
}

func TestIsPullGeneratedPath(t *testing.T) {
	testCases := []struct {
		path string
		want bool
	}{
		{path: "Engineering (ENG)/root.md", want: true},
		{path: "Engineering (ENG)/.confluence-state.json", want: true},
		{path: "Engineering (ENG)/assets/1/att.png", want: true},
		{path: "Engineering (ENG)/notes.txt", want: false},
		{path: "Engineering (ENG)/scripts/build.ps1", want: false},
	}

	for _, tc := range testCases {
		got := isPullGeneratedPath(tc.path)
		if got != tc.want {
			t.Fatalf("isPullGeneratedPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestRunPull_NonInteractiveRequiresYesForHighImpact(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := buildBulkPullRemote(t, 11)

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)
	setAutomationFlags(t, false, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("runPull() expected confirmation error")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("expected confirmation error, got: %v", err)
	}
}

func TestRunPull_YesBypassesHighImpactConfirmation(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := buildBulkPullRemote(t, 11)

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)
	setAutomationFlags(t, true, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if got := len(state.PagePathIndex); got != 11 {
		t.Fatalf("expected 11 synced pages, got %d", got)
	}
}

func TestRunPull_ForcePullRefreshesEntireSpace(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		LastPullHighWatermark: "2026-02-02T00:00:00Z",
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Root",
			Version:      2,
			LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
		}},
		changes: []confluence.Change{},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	rootDoc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if err != nil {
		t.Fatalf("read root.md: %v", err)
	}
	if !strings.Contains(rootDoc.Body, "new body") {
		t.Fatalf("expected root.md body to be refreshed on force pull, got:\n%s", rootDoc.Body)
	}
}

func TestRunPull_ForceFlagRejectedForFileTarget(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	filePath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "body\n",
	})

	chdirRepo(t, repo)

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	// We need to allow it to resolve space metadata for file mode too now
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
	}
	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	err := runPull(cmd, config.Target{Mode: config.TargetModeFile, Value: filePath})
	if err == nil {
		t.Fatal("expected error for --force on file target")
	}
	if !strings.Contains(err.Error(), "--force is only supported for space targets") {
		t.Fatalf("unexpected error: %v", err)
	}
}
