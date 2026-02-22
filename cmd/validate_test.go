package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestResolveValidateTargetContext_ResolvesSanitizedSpaceDirectoryByKey(t *testing.T) {
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "Technical documentation (TD)")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
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
