package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/spf13/cobra"
)

func TestRunRelink_NonInteractiveRequiresYesForHighImpactChanges(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)

	targetDir := filepath.Join(repo, "Target (TGT)")
	sourceDir := filepath.Join(repo, "Source (SRC)")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o750); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(targetDir, "target.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Target", ID: "42", Space: "TGT", Version: 1},
		Body:        "target body\n",
	})
	if err := fs.SaveState(targetDir, fs.SpaceState{
		SpaceKey: "TGT",
		PagePathIndex: map[string]string{
			"target.md": "42",
		},
	}); err != nil {
		t.Fatalf("save target state: %v", err)
	}

	sourceState := fs.SpaceState{
		SpaceKey:      "SRC",
		PagePathIndex: map[string]string{},
	}
	for i := 1; i <= 11; i++ {
		name := fmt.Sprintf("doc-%02d.md", i)
		writeMarkdown(t, filepath.Join(sourceDir, name), fs.MarkdownDocument{
			Frontmatter: fs.Frontmatter{Title: fmt.Sprintf("Doc %d", i), ID: fmt.Sprintf("%d", 100+i), Space: "SRC", Version: 1},
			Body:        "[Target](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=42)\n",
		})
		sourceState.PagePathIndex[name] = fmt.Sprintf("%d", 100+i)
	}
	if err := fs.SaveState(sourceDir, sourceState); err != nil {
		t.Fatalf("save source state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "seed relink fixtures")

	chdirRepo(t, repo)
	setAutomationFlags(t, false, true)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runRelink(cmd, "TGT")
	if err == nil {
		t.Fatal("runRelink() expected non-interactive confirmation error")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(sourceDir, "doc-01.md")) //nolint:gosec // test path is controlled in temp repo
	if err != nil {
		t.Fatalf("read source doc: %v", err)
	}
	if !strings.Contains(string(raw), "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=42") {
		t.Fatalf("expected source doc to remain unchanged on confirmation failure, got:\n%s", string(raw))
	}
}

func TestGetSpaceKeyFromState_PrefersStateMetadata(t *testing.T) {
	dir := t.TempDir()
	state := fs.SpaceState{
		SpaceKey:      "OPS",
		PagePathIndex: map[string]string{"missing.md": "1"},
	}

	if got := getSpaceKeyFromState(dir, state); got != "OPS" {
		t.Fatalf("space key = %q, want OPS", got)
	}
}

func TestGetSpaceKeyFromState_FallsBackToDirectorySuffix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Operations (OPS)")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}

	if got := getSpaceKeyFromState(dir, fs.SpaceState{}); got != "OPS" {
		t.Fatalf("space key = %q, want OPS", got)
	}
}

func TestRunGlobalRelink(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)

	targetDir := filepath.Join(repo, "Target (TGT)")
	sourceDir := filepath.Join(repo, "Source (SRC)")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o750); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(targetDir, "target.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Target", ID: "42", Space: "TGT", Version: 1},
		Body:        "target body\n",
	})
	if err := fs.SaveState(targetDir, fs.SpaceState{
		SpaceKey: "TGT",
		PagePathIndex: map[string]string{
			"target.md": "42",
		},
	}); err != nil {
		t.Fatalf("save target state: %v", err)
	}

	writeMarkdown(t, filepath.Join(sourceDir, "doc.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Doc", ID: "101", Space: "SRC", Version: 1},
		Body:        "[Target](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=42)\n",
	})
	if err := fs.SaveState(sourceDir, fs.SpaceState{
		SpaceKey: "SRC",
		PagePathIndex: map[string]string{
			"doc.md": "101",
		},
	}); err != nil {
		t.Fatalf("save source state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "seed relink fixtures")

	chdirRepo(t, repo)

	oldYes := flagYes
	flagYes = true
	defer func() { flagYes = oldYes }()

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	// Target "" means global relink
	err := runRelink(cmd, "")
	if err != nil {
		t.Fatalf("runRelink(global) failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(sourceDir, "doc.md"))
	if err != nil {
		t.Fatalf("read source doc: %v", err)
	}
	if !strings.Contains(string(raw), "../Target%20%28TGT%29/target.md") {
		t.Fatalf("expected source doc to be relinked, got:\n%s", string(raw))
	}
}
