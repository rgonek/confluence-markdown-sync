package sync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestFindOrphanAssets_ReturnsOnlyUnreferencedAssets(t *testing.T) {
	spaceDir := t.TempDir()

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root"},
		Body:        "![Used](assets/used.png)\n[Doc](assets/used.pdf)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	usedImage := filepath.Join(spaceDir, "assets", "used.png")
	usedDoc := filepath.Join(spaceDir, "assets", "used.pdf")
	orphanImage := filepath.Join(spaceDir, "assets", "orphan.png")
	orphanNested := filepath.Join(spaceDir, "assets", "nested", "ghost.txt")

	for _, path := range []string{usedImage, usedDoc, orphanImage, orphanNested} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	orphans, err := FindOrphanAssets(spaceDir)
	if err != nil {
		t.Fatalf("FindOrphanAssets() error: %v", err)
	}

	want := []string{"assets/nested/ghost.txt", "assets/orphan.png"}
	if !reflect.DeepEqual(orphans, want) {
		t.Fatalf("orphans = %v, want %v", orphans, want)
	}
}
