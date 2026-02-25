package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestResolveValidateTargetContext_ResolvesSanitizedSpaceDirectoryByKey(t *testing.T) {
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "Technical documentation (TD)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "TD",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{"root.md": "1"},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	chdirRepo(t, repo)

	ctx, err := resolveValidateTargetContext(config.Target{Mode: config.TargetModeSpace, Value: "TD"})
	if err != nil {
		t.Fatalf("resolveValidateTargetContext() error: %v", err)
	}

	if ctx.spaceDir != spaceDir {
		t.Fatalf("spaceDir = %q, want %q", ctx.spaceDir, spaceDir)
	}
	if len(ctx.files) != 1 || filepath.Base(ctx.files[0]) != "root.md" {
		t.Fatalf("files = %v, want [root.md]", ctx.files)
	}
}

func TestRunValidateTarget_BlocksTamperedIDAgainstState(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "ENG", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "2", Space: "ENG", Version: 1},
		Body:        "content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTarget(out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for tampered id")
	}
	if !strings.Contains(out.String(), "[immutable] id") {
		t.Fatalf("expected immutable id issue, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_BlocksTamperedSpaceAgainstState(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "ENG", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "OPS", Version: 1},
		Body:        "content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTarget(out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for tampered space")
	}
	if !strings.Contains(out.String(), "[immutable] space") {
		t.Fatalf("expected immutable space issue, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_BlocksCurrentToDraftTransition(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "ENG", Version: 1, State: "current"},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "ENG", Version: 1, State: "draft"},
		Body:        "content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTarget(out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for current->draft transition")
	}
	if !strings.Contains(out.String(), "[immutable] state") {
		t.Fatalf("expected immutable state issue, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_AllowsDraftToDraftForExistingDraftPage(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "ENG", Version: 1, State: "draft"},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Space: "ENG", Version: 1, State: "draft"},
		Body:        "updated content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTarget(out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success for draft->draft, got: %v\nOutput:\n%s", err, out.String())
	}
}
