package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func TestHandleFolderPageFallbackAcceptsAndMaterializesPageNode(t *testing.T) {
	spaceDir := t.TempDir()

	oldNonInteractive := flagNonInteractive
	flagNonInteractive = false
	t.Cleanup(func() { flagNonInteractive = oldNonInteractive })

	out := &bytes.Buffer{}
	indexRelPath, accepted, err := handleFolderPageFallback(
		strings.NewReader("y\n"),
		out,
		spaceDir,
		&syncflow.FolderPageFallbackRequiredError{
			Path:   "Parent/Child",
			Reason: "unsupported tenant capability",
		},
	)
	if err != nil {
		t.Fatalf("handleFolderPageFallback() error: %v", err)
	}
	if !accepted {
		t.Fatal("expected fallback acceptance")
	}
	if indexRelPath != "Parent/Child/Child.md" {
		t.Fatalf("index path = %q, want Parent/Child/Child.md", indexRelPath)
	}

	doc, readErr := fs.ReadMarkdownDocument(filepath.Join(spaceDir, filepath.FromSlash(indexRelPath)))
	if readErr != nil {
		t.Fatalf("read materialized page node: %v", readErr)
	}
	if doc.Frontmatter.Title != "Child" {
		t.Fatalf("materialized title = %q, want Child", doc.Frontmatter.Title)
	}
	if !strings.Contains(out.String(), "created page-backed hierarchy node Parent/Child/Child.md") {
		t.Fatalf("expected confirmation output, got:\n%s", out.String())
	}
}

func TestHandleFolderPageFallbackNonInteractiveFailsClosed(t *testing.T) {
	spaceDir := t.TempDir()

	oldNonInteractive := flagNonInteractive
	flagNonInteractive = true
	t.Cleanup(func() { flagNonInteractive = oldNonInteractive })

	_, accepted, err := handleFolderPageFallback(
		strings.NewReader(""),
		&bytes.Buffer{},
		spaceDir,
		&syncflow.FolderPageFallbackRequiredError{
			Path:   "Parent",
			Reason: "folder semantic conflict",
		},
	)
	if err == nil {
		t.Fatal("handleFolderPageFallback() expected non-interactive refusal")
	}
	if accepted {
		t.Fatal("expected fallback to remain unaccepted")
	}
	if !strings.Contains(err.Error(), "explicit interactive confirmation") {
		t.Fatalf("expected interactive-confirmation guidance, got: %v", err)
	}
}
