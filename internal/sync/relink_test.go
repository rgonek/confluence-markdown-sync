package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPageID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.atlassian.net/wiki/pages/viewpage.action?pageId=123", "123"},
		{"https://example.atlassian.net/wiki/spaces/SPACE/pages/123/Page+Title", "123"},
		{"https://example.atlassian.net/wiki/pages/123", "123"},
		{"/wiki/pages/123/Title", "123"},
		{"not a url", ""},
		{"", ""},
	}

	for _, tt := range tests {
		if got := ExtractPageID(tt.url); got != tt.expected {
			t.Errorf("ExtractPageID(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestResolveLinksInFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "relink-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("cleanup temp dir: %v", err)
		}
	})

	sourcePath := filepath.Join(tmpDir, "source.md")
	targetPath := filepath.Join(tmpDir, "target.md")

	content := `Check this [link](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456) and [another](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456#Section).
Not a [confluence link](https://google.com).`
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	index := GlobalPageIndex{
		"456": targetPath,
	}

	changed, count, err := ResolveLinksInFile(sourcePath, index, false)
	if err != nil {
		t.Fatal(err)
	}

	if !changed {
		t.Error("expected changes, got none")
	}
	if count != 2 {
		t.Errorf("expected 2 links converted, got %d", count)
	}

	newContent, err := os.ReadFile(sourcePath) //nolint:gosec // test file path is controlled in temp dir
	if err != nil {
		t.Fatal(err)
	}

	expected := `Check this [link](target.md) and [another](target.md#Section).
Not a [confluence link](https://google.com).`
	if string(newContent) != expected {
		t.Errorf("unexpected content:\nGOT:\n%s\nWANT:\n%s", string(newContent), expected)
	}
}

func TestResolveLinksInFile_SkipsCodeSpansAndFencedCode(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.md")
	targetPath := filepath.Join(tmpDir, "target.md")

	content := "Inline code `see [code](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)`\n\n```md\n[fenced](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)\n```\n\nReal [link](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)\n"
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, count, err := ResolveLinksInFile(sourcePath, GlobalPageIndex{"456": targetPath}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected file to change")
	}
	if count != 1 {
		t.Fatalf("converted links = %d, want 1", count)
	}

	raw, err := os.ReadFile(sourcePath) //nolint:gosec // test file path is controlled in temp dir
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	want := "Inline code `see [code](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)`\n\n```md\n[fenced](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)\n```\n\nReal [link](target.md)\n"
	if got != want {
		t.Fatalf("unexpected content:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestResolveLinksInFile_PreservesAnchorAndTitle(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.md")
	targetPath := filepath.Join(tmpDir, "target.md")

	content := `[spec](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456#Section "Read this")`
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, count, err := ResolveLinksInFile(sourcePath, GlobalPageIndex{"456": targetPath}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || count != 1 {
		t.Fatalf("changed=%v count=%d, want changed=true count=1", changed, count)
	}

	raw, err := os.ReadFile(sourcePath) //nolint:gosec // test file path is controlled in temp dir
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `[spec](target.md#Section "Read this")`; got != want {
		t.Fatalf("unexpected content: got %q want %q", got, want)
	}
}

func TestResolveLinksInFile_EncodesSpacesInRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.md")
	targetPath := filepath.Join(tmpDir, "Target Page.md")

	content := `[spec](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)`
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, count, err := ResolveLinksInFile(sourcePath, GlobalPageIndex{"456": targetPath}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || count != 1 {
		t.Fatalf("changed=%v count=%d, want changed=true count=1", changed, count)
	}

	raw, err := os.ReadFile(sourcePath) //nolint:gosec // test file path is controlled in temp dir
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `[spec](Target%20Page.md)`; got != want {
		t.Fatalf("unexpected content: got %q want %q", got, want)
	}
}

func TestResolveLinksInFile_HandlesEscapedAndNestedBracketLabels(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.md")
	targetPath := filepath.Join(tmpDir, "target.md")

	content := "[outer \\[inner\\]](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456)\n[nested [label]](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=456#A)\n"
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, count, err := ResolveLinksInFile(sourcePath, GlobalPageIndex{"456": targetPath}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || count != 2 {
		t.Fatalf("changed=%v count=%d, want changed=true count=2", changed, count)
	}

	raw, err := os.ReadFile(sourcePath) //nolint:gosec // test file path is controlled in temp dir
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), "[outer \\[inner\\]](target.md)\n[nested [label]](target.md#A)\n"; got != want {
		t.Fatalf("unexpected content:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestResolveLinksInFile_NoRewritableLinks(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.md")

	content := "[external](https://example.com/docs)\n[unknown](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=999)\n"
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, count, err := ResolveLinksInFile(sourcePath, GlobalPageIndex{"456": filepath.Join(tmpDir, "target.md")}, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected file to remain unchanged")
	}
	if count != 0 {
		t.Fatalf("converted links = %d, want 0", count)
	}

	raw, err := os.ReadFile(sourcePath) //nolint:gosec // test file path is controlled in temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Fatalf("file content changed unexpectedly: %q", string(raw))
	}
}

func TestBuildGlobalPageIndex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "global-index-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("cleanup temp dir: %v", err)
		}
	})

	// Create two spaces
	space1 := filepath.Join(tmpDir, "space1")
	space2 := filepath.Join(tmpDir, "space2")
	if err := os.MkdirAll(space1, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(space2, 0o750); err != nil {
		t.Fatal(err)
	}

	// Create state files
	state1 := `{"page_path_index": {"page1.md": "101"}}`
	state2 := `{"page_path_index": {"page2.md": "201"}}`
	if err := os.WriteFile(filepath.Join(space1, ".confluence-state.json"), []byte(state1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(space2, ".confluence-state.json"), []byte(state2), 0o600); err != nil {
		t.Fatal(err)
	}

	index, err := BuildGlobalPageIndex(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(index) != 2 {
		t.Errorf("expected 2 entries, got %d", len(index))
	}

	if p, ok := index["101"]; !ok || filepath.Base(p) != "page1.md" {
		t.Errorf("missing or wrong path for 101: %s", p)
	}
	if p, ok := index["201"]; !ok || filepath.Base(p) != "page2.md" {
		t.Errorf("missing or wrong path for 201: %s", p)
	}
}
