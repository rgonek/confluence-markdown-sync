package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestResolveGlobalIndexRoot_FindsGitWorktreeRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /tmp/fake\n"), 0o600); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	root, err := ResolveGlobalIndexRoot(spaceDir)
	if err != nil {
		t.Fatalf("ResolveGlobalIndexRoot() error: %v", err)
	}
	if root != repo {
		t.Fatalf("root = %q, want %q", root, repo)
	}
}

func TestBuildGlobalPageIndex_ScansMarkdownWithoutStateFiles(t *testing.T) {
	repo := t.TempDir()
	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	engPath := filepath.Join(engDir, "root.md")
	if err := fs.WriteMarkdownDocument(engPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "100", Version: 1},
		Body:        "root\n",
	}); err != nil {
		t.Fatalf("write eng markdown: %v", err)
	}

	tdPath := filepath.Join(tdDir, "target.md")
	if err := fs.WriteMarkdownDocument(tdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Target", ID: "200", Version: 1},
		Body:        "target\n",
	}); err != nil {
		t.Fatalf("write td markdown: %v", err)
	}

	index, err := BuildGlobalPageIndex(repo)
	if err != nil {
		t.Fatalf("BuildGlobalPageIndex() error: %v", err)
	}

	if got := normalizeAbsolutePathKey(index["100"]); got != normalizeAbsolutePathKey(engPath) {
		t.Fatalf("index[100] = %q, want %q", index["100"], engPath)
	}
	if got := normalizeAbsolutePathKey(index["200"]); got != normalizeAbsolutePathKey(tdPath) {
		t.Fatalf("index[200] = %q, want %q", index["200"], tdPath)
	}
}
