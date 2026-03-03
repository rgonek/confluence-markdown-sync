package search_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/search"
	"github.com/rgonek/confluence-markdown-sync/internal/search/sqlitestore"
)

// newTestIndexer creates a temporary repo layout with a SQLite-backed Indexer.
func newTestIndexer(t *testing.T) (*search.Indexer, string) {
	t.Helper()

	repoDir := t.TempDir()
	dbPath := filepath.Join(repoDir, ".confluence-search-index", "search.db")
	store, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	ix := search.NewIndexer(store, repoDir)
	t.Cleanup(func() { _ = ix.Close() })
	return ix, repoDir
}

// writeMarkdownFile writes a Markdown file with frontmatter + body into repoDir.
func writeMarkdownFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	absPath := filepath.Join(repoDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(absPath), err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", absPath, err)
	}
}

// writeStateFile writes a minimal .confluence-state.json for a space directory.
func writeStateFile(t *testing.T, repoDir, spaceName, spaceKey string) {
	t.Helper()
	spaceDir := filepath.Join(repoDir, spaceName)
	state := fs.NewSpaceState()
	state.SpaceKey = spaceKey
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

const sampleMD = `---
id: "111"
title: Security Overview
labels:
  - security
  - architecture
---

This page covers our security architecture.

## OAuth2 Flow

OAuth2 flows use PKCE tokens.

### Token Refresh

Refresh tokens are rotated every 15 minutes.

` + "```go" + `
func refresh(token string) error { return nil }
` + "```" + `
`

func TestIndexer_Reindex(t *testing.T) {
	ix, repoDir := newTestIndexer(t)

	writeStateFile(t, repoDir, "DEV", "DEV")
	writeMarkdownFile(t, repoDir, "DEV/overview.md", sampleMD)

	count, err := ix.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if count == 0 {
		t.Error("expected at least 1 document indexed")
	}
}

func TestIndexer_IndexSpace(t *testing.T) {
	ix, repoDir := newTestIndexer(t)

	spaceDir := filepath.Join(repoDir, "DEV")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeMarkdownFile(t, repoDir, "DEV/overview.md", sampleMD)
	writeMarkdownFile(t, repoDir, "DEV/guide.md", `---
title: Guide
---
A guide page.
`)

	count, err := ix.IndexSpace(spaceDir, "DEV")
	if err != nil {
		t.Fatalf("IndexSpace: %v", err)
	}
	// Each file produces at least 1 (page) doc; sampleMD also has sections/code.
	if count < 2 {
		t.Errorf("expected at least 2 docs, got %d", count)
	}
}

func TestIndexer_SkipsAssetsDir(t *testing.T) {
	ix, repoDir := newTestIndexer(t)

	writeStateFile(t, repoDir, "DEV", "DEV")
	// Write a file inside assets/ — should be skipped.
	writeMarkdownFile(t, repoDir, "DEV/assets/image-info.md", `---
title: Asset
---
Should not be indexed.
`)
	// Write a real page.
	writeMarkdownFile(t, repoDir, "DEV/overview.md", sampleMD)

	count, err := ix.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	// Should only index overview.md, not the assets file.
	// overview.md has multiple docs; assets file has 0.
	if count == 0 {
		t.Error("expected docs from overview.md to be indexed")
	}

	// Confirm assets/image-info.md was not indexed.
	store := openStoreFromIndexer(t, repoDir)
	defer func() { _ = store.Close() }()

	results, err := store.Search(search.SearchOptions{Query: "Should not be indexed"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if strings.Contains(r.Document.Path, "assets") {
			t.Errorf("assets file was indexed: %s", r.Document.Path)
		}
	}
}

func TestIndexer_IncrementalUpdate_FallbackOnZeroTime(t *testing.T) {
	ix, repoDir := newTestIndexer(t)

	writeStateFile(t, repoDir, "DEV", "DEV")
	writeMarkdownFile(t, repoDir, "DEV/overview.md", sampleMD)

	// IncrementalUpdate with no prior index should fall back to full reindex.
	count, err := ix.IncrementalUpdate()
	if err != nil {
		t.Fatalf("IncrementalUpdate: %v", err)
	}
	if count == 0 {
		t.Error("expected documents indexed on first IncrementalUpdate")
	}
}

func TestIndexer_IncrementalUpdate_SkipsOldFiles(t *testing.T) {
	ix, repoDir := newTestIndexer(t)

	writeStateFile(t, repoDir, "DEV", "DEV")

	// Write overview.md, sleep, then reindex.
	writeMarkdownFile(t, repoDir, "DEV/overview.md", sampleMD)
	time.Sleep(1100 * time.Millisecond)

	// Full reindex records the timestamp.
	_, err := ix.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	// Sleep again, then write a NEW file after the reindex timestamp.
	time.Sleep(1100 * time.Millisecond)
	writeMarkdownFile(t, repoDir, "DEV/new-page.md", `---
title: New Page
---
Brand new content.
`)

	// Incremental update should only index the new file (1 page doc).
	count, err := ix.IncrementalUpdate()
	if err != nil {
		t.Fatalf("IncrementalUpdate (second): %v", err)
	}
	if count == 0 {
		t.Error("expected new-page.md to be indexed incrementally")
	}
	// new-page.md has 1 page doc + 0 sections + 0 code blocks = 1 doc.
	// overview.md should NOT be re-indexed.
	if count > 2 {
		t.Errorf("expected only new-page.md to be indexed (<=2 docs), got %d", count)
	}
}

func TestIndexer_MultipleSpaces(t *testing.T) {
	ix, repoDir := newTestIndexer(t)

	writeStateFile(t, repoDir, "DEV", "DEV")
	writeStateFile(t, repoDir, "OPS", "OPS")
	writeMarkdownFile(t, repoDir, "DEV/overview.md", sampleMD)
	writeMarkdownFile(t, repoDir, "OPS/deploy.md", `---
title: Deploy
---
Deployment instructions.
`)

	count, err := ix.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if count < 2 {
		t.Errorf("expected docs from both spaces, got %d", count)
	}
}

// openStoreFromIndexer opens a new store handle to the same DB used by the indexer.
// This is needed to run Search assertions independently of the indexer.
func openStoreFromIndexer(t *testing.T, repoDir string) *sqlitestore.Store {
	t.Helper()
	dbPath := filepath.Join(repoDir, ".confluence-search-index", "search.db")
	store, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open store for assertion: %v", err)
	}
	return store
}

// — compile-time interface check —
var _ search.Store = (*sqlitestore.Store)(nil)

// — time stub for incremental test —
func mustNotBeZero(t *testing.T, ts time.Time, label string) {
	t.Helper()
	if ts.IsZero() {
		t.Errorf("%s: expected non-zero time", label)
	}
}
