package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunPull_HealsCorruptedStateFileWithConflictMarkers(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 1,
		},
		Body: "old body\n",
	})

	corrupted := []byte(`<<<<<<< HEAD
{"space_key":"ENG","page_path_index":{"root.md":"1"}}
=======
{"space_key":"ENG","page_path_index":{"other.md":"2"}}
>>>>>>> sync/ENG/20260226T120000Z
`)
	if err := os.WriteFile(filepath.Join(spaceDir, fs.StateFileName), corrupted, 0o600); err != nil {
		t.Fatalf("write corrupted state: %v", err)
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

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("runPull() error: %v", err)
	}

	if !strings.Contains(out.String(), "Git conflict detected") {
		t.Fatalf("expected conflict-healing warning, got:\n%s", out.String())
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load healed state: %v", err)
	}
	if got := strings.TrimSpace(state.PagePathIndex["root.md"]); got != "1" {
		t.Fatalf("healed page_path_index[root.md] = %q, want 1", got)
	}

	rawState, err := os.ReadFile(filepath.Join(spaceDir, fs.StateFileName)) //nolint:gosec // test data
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if strings.Contains(string(rawState), "<<<<<<<") {
		t.Fatalf("state file still contains conflict markers:\n%s", string(rawState))
	}
}

func TestListDirtyMarkdownPathsForScope(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	if err := os.WriteFile(rootPath, []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write root: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	if err := os.WriteFile(rootPath, []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("modify root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spaceDir, "new.md"), []byte("new\n"), 0o600); err != nil {
		t.Fatalf("write new markdown: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spaceDir, "notes.txt"), []byte("ignore\n"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	dirty, err := listDirtyMarkdownPathsForScope(repo, "Engineering (ENG)")
	if err != nil {
		t.Fatalf("listDirtyMarkdownPathsForScope() error: %v", err)
	}

	if _, ok := dirty["root.md"]; !ok {
		t.Fatalf("expected root.md in dirty set, got %#v", dirty)
	}
	if _, ok := dirty["new.md"]; !ok {
		t.Fatalf("expected new.md in dirty set, got %#v", dirty)
	}
	if _, ok := dirty["notes.txt"]; ok {
		t.Fatalf("expected notes.txt to be excluded from dirty markdown set, got %#v", dirty)
	}
}

func TestWarnSkippedDirtyDeletions_PrintsWarningForIntersectingPaths(t *testing.T) {
	out := &bytes.Buffer{}
	warnSkippedDirtyDeletions(out, []string{"root.md", "docs/guide.md"}, map[string]struct{}{"docs/guide.md": {}})

	text := out.String()
	if !strings.Contains(text, "Skipped local deletion of 'docs/guide.md'") {
		t.Fatalf("expected warning for docs/guide.md, got:\n%s", text)
	}
	if strings.Contains(text, "root.md") {
		t.Fatalf("did not expect warning for root.md, got:\n%s", text)
	}
}
