# Frontmatter Author/Date Search Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Index `created_by`, `created_at`, `updated_by`, `updated_at` from YAML frontmatter and expose them via six new `conf search` filter flags.

**Architecture:** Denormalize the four fields into every `Document` row (page/section/code), identical to how `space_key` and `labels` are handled today. Both SQLite and Bleve backends gain four new columns/fields and new filter clauses in `Search()`. The CLI gains `--created-by`, `--updated-by`, `--created-after`, `--created-before`, `--updated-after`, `--updated-before`.

**Tech Stack:** Go, SQLite FTS5 (`modernc.org/sqlite`), Bleve v2, Cobra CLI

---

## Task 1: Data model — `Document` and `SearchOptions`

**Files:**
- Modify: `internal/search/document.go`

There are no test files for `document.go` (it's a pure data model); the correctness of the new fields is tested downstream in Tasks 2–5.

### Step 1: Add four fields to `Document`

In `internal/search/document.go`, after the `ModTime` field (line 65), add:

```go
// CreatedBy is the Confluence username who created the page (from frontmatter).
CreatedBy string `json:"created_by,omitempty"`

// CreatedAt is the page creation timestamp in RFC3339 format (from frontmatter).
// Empty if the frontmatter field is absent or unparseable.
CreatedAt string `json:"created_at,omitempty"`

// UpdatedBy is the Confluence username who last updated the page (from frontmatter).
UpdatedBy string `json:"updated_by,omitempty"`

// UpdatedAt is the last-updated timestamp in RFC3339 format (from frontmatter).
// Empty if the frontmatter field is absent or unparseable.
UpdatedAt string `json:"updated_at,omitempty"`
```

### Step 2: Add six filter fields to `SearchOptions`

In `internal/search/document.go`, after the `Limit` field in `SearchOptions` (line 88), add:

```go
// CreatedBy restricts results to documents where created_by exactly matches.
CreatedBy string

// UpdatedBy restricts results to documents where updated_by exactly matches.
UpdatedBy string

// CreatedAfter restricts results to documents where created_at >= this RFC3339 value.
CreatedAfter string

// CreatedBefore restricts results to documents where created_at <= this RFC3339 value.
CreatedBefore string

// UpdatedAfter restricts results to documents where updated_at >= this RFC3339 value.
UpdatedAfter string

// UpdatedBefore restricts results to documents where updated_at <= this RFC3339 value.
UpdatedBefore string
```

### Step 3: Verify the project still compiles

```bash
go build ./...
```

Expected: no errors (new fields are backwards compatible — existing code assigns zero values).

### Step 4: Commit

```bash
git add internal/search/document.go
git commit -m "feat(search): add author/date fields to Document and SearchOptions"
```

---

## Task 2: Indexer — populate the four new fields

**Files:**
- Modify: `internal/search/indexer.go`
- Modify: `internal/search/indexer_test.go` (or `internal/search/search_testhelpers_test.go`)

### Step 1: Write the failing test

Open `internal/search/indexer_test.go`. Find the existing test that calls `newTestIndexer` and checks indexed documents. Add a new test after the existing ones:

```go
func TestIndexer_AuthorFieldsPopulated(t *testing.T) {
	ix, repoDir := newTestIndexer(t)
	writeStateFile(t, repoDir, "DEV", "DEV")

	const md = `---
id: "999"
title: Author Test
created_by: alice
created_at: "2024-06-01T09:00:00Z"
updated_by: bob
updated_at: "2024-11-15T14:30:00Z"
---

Body text here.
`
	writeMarkdownFile(t, repoDir, "DEV/author-test.md", md)

	count, err := ix.IndexSpace(filepath.Join(repoDir, "DEV"), "DEV")
	if err != nil {
		t.Fatalf("IndexSpace: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one document indexed")
	}

	// Search with no query to get all docs for this path
	store := storeFromIndexer(t, ix)
	results, err := store.Search(search.SearchOptions{SpaceKey: "DEV"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	for _, r := range results {
		if r.Document.Path != "DEV/author-test.md" {
			continue
		}
		if r.Document.CreatedBy != "alice" {
			t.Errorf("CreatedBy: got %q, want %q", r.Document.CreatedBy, "alice")
		}
		if r.Document.UpdatedBy != "bob" {
			t.Errorf("UpdatedBy: got %q, want %q", r.Document.UpdatedBy, "bob")
		}
		if r.Document.CreatedAt != "2024-06-01T09:00:00Z" {
			t.Errorf("CreatedAt: got %q, want %q", r.Document.CreatedAt, "2024-06-01T09:00:00Z")
		}
		if r.Document.UpdatedAt != "2024-11-15T14:30:00Z" {
			t.Errorf("UpdatedAt: got %q, want %q", r.Document.UpdatedAt, "2024-11-15T14:30:00Z")
		}
		return
	}
	t.Error("document DEV/author-test.md not found in results")
}
```

The test requires a `storeFromIndexer` helper. Check if `search_testhelpers_test.go` exposes it; if not, add it there:

```go
// storeFromIndexer extracts the Store from an Indexer for direct inspection in tests.
// This relies on the Indexer being backed by the test SQLite store created in newTestIndexer.
func storeFromIndexer(t *testing.T, ix *search.Indexer) search.Store {
	t.Helper()
	// newTestIndexer uses SQLite; open the same db.
	// Retrieve the store via the indexer's Close (store is exported through Close).
	// Since Indexer doesn't expose store directly, we re-open the same db.
	// In newTestIndexer the db is at repoDir/.confluence-search-index/search.db.
	// We can't get repoDir here easily — instead, the test should use store directly.
	// See TestIndexer_AuthorFieldsPopulated for the correct pattern.
	t.Skip("storeFromIndexer is not used directly — see test for pattern")
	return nil
}
```

Actually, the simpler approach: refactor `TestIndexer_AuthorFieldsPopulated` to open the store directly (same pattern as `newTestIndexer`):

```go
func TestIndexer_AuthorFieldsPopulated(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(repoDir, ".confluence-search-index", "search.db")
	store, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ix := search.NewIndexer(store, repoDir)
	writeStateFile(t, repoDir, "DEV", "DEV")

	const md = `---
id: "999"
title: Author Test
created_by: alice
created_at: "2024-06-01T09:00:00Z"
updated_by: bob
updated_at: "2024-11-15T14:30:00Z"
---

Body text here.
`
	writeMarkdownFile(t, repoDir, "DEV/author-test.md", md)

	if _, err := ix.IndexSpace(filepath.Join(repoDir, "DEV"), "DEV"); err != nil {
		t.Fatalf("IndexSpace: %v", err)
	}

	results, err := store.Search(search.SearchOptions{SpaceKey: "DEV"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Document.Path != "DEV/author-test.md" {
			continue
		}
		if r.Document.CreatedBy != "alice" {
			t.Errorf("CreatedBy: got %q, want %q", r.Document.CreatedBy, "alice")
		}
		if r.Document.UpdatedBy != "bob" {
			t.Errorf("UpdatedBy: got %q, want %q", r.Document.UpdatedBy, "bob")
		}
		if r.Document.CreatedAt != "2024-06-01T09:00:00Z" {
			t.Errorf("CreatedAt: got %q, want %q", r.Document.CreatedAt, "2024-06-01T09:00:00Z")
		}
		if r.Document.UpdatedAt != "2024-11-15T14:30:00Z" {
			t.Errorf("UpdatedAt: got %q, want %q", r.Document.UpdatedAt, "2024-11-15T14:30:00Z")
		}
		return
	}
	t.Error("document DEV/author-test.md not found in results")
}
```

Make sure `sqlitestore` is imported at the top of `indexer_test.go`.

### Step 2: Run the failing test

```bash
go test ./internal/search/... -run TestIndexer_AuthorFieldsPopulated -v
```

Expected: FAIL — `CreatedBy: got "", want "alice"` (field not yet populated).

### Step 3: Add `normalizeDate` helper and populate fields in `indexer.go`

In `internal/search/indexer.go`, add this helper at the bottom of the file (after the `walkAndIndex` helper):

```go
// normalizeDate attempts to parse s using common date/datetime layouts and returns
// an RFC3339-formatted string in UTC. Returns s unchanged if it is already RFC3339,
// or the original string if it cannot be parsed at all.
func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}
```

In `indexFile()`, update the page document `append` (around line 171):

```go
docs = append(docs, Document{
    ID:        "page:" + docPath,
    Type:      DocTypePage,
    Path:      docPath,
    PageID:    fm.ID,
    Title:     fm.Title,
    SpaceKey:  spaceKey,
    Labels:    labels,
    Content:   mdDoc.Body,
    ModTime:   &modTime,
    CreatedBy: fm.CreatedBy,
    CreatedAt: normalizeDate(fm.CreatedAt),
    UpdatedBy: fm.UpdatedBy,
    UpdatedAt: normalizeDate(fm.UpdatedAt),
})
```

Update the section document `append` (around line 187):

```go
docs = append(docs, Document{
    ID:           fmt.Sprintf("section:%s:%d", docPath, sec.Line),
    Type:         DocTypeSection,
    Path:         docPath,
    PageID:       fm.ID,
    Title:        fm.Title,
    SpaceKey:     spaceKey,
    Labels:       labels,
    Content:      sec.Content,
    HeadingPath:  sec.HeadingPath,
    HeadingText:  sec.HeadingText,
    HeadingLevel: sec.HeadingLevel,
    Line:         sec.Line,
    ModTime:      &modTime,
    CreatedBy:    fm.CreatedBy,
    CreatedAt:    normalizeDate(fm.CreatedAt),
    UpdatedBy:    fm.UpdatedBy,
    UpdatedAt:    normalizeDate(fm.UpdatedAt),
})
```

Update the code block document `append` (around line 204):

```go
docs = append(docs, Document{
    ID:           fmt.Sprintf("code:%s:%d", docPath, cb.Line),
    Type:         DocTypeCode,
    Path:         docPath,
    PageID:       fm.ID,
    Title:        fm.Title,
    SpaceKey:     spaceKey,
    Labels:       labels,
    Content:      cb.Content,
    HeadingPath:  cb.HeadingPath,
    HeadingText:  cb.HeadingText,
    HeadingLevel: cb.HeadingLevel,
    Language:     cb.Language,
    Line:         cb.Line,
    ModTime:      &modTime,
    CreatedBy:    fm.CreatedBy,
    CreatedAt:    normalizeDate(fm.CreatedAt),
    UpdatedBy:    fm.UpdatedBy,
    UpdatedAt:    normalizeDate(fm.UpdatedAt),
})
```

The `"strings"` and `"time"` packages are already imported.

### Step 4: Run the test again

```bash
go test ./internal/search/... -run TestIndexer_AuthorFieldsPopulated -v
```

Expected: FAIL — `CreatedBy: got "", want "alice"`. The indexer sets the fields, but the SQLite store hasn't been updated yet to persist them. That's fine — Task 3 fixes the store.

### Step 5: Compile check

```bash
go build ./...
```

Expected: no errors.

### Step 6: Commit the indexer changes

```bash
git add internal/search/indexer.go internal/search/indexer_test.go
git commit -m "feat(search): populate author/date fields in indexer"
```

---

## Task 3: SQLite backend

**Files:**
- Modify: `internal/search/sqlitestore/schema.go`
- Modify: `internal/search/sqlitestore/store.go`
- Modify: `internal/search/sqlitestore/store_test.go`

### Step 1: Write failing tests

Add to `internal/search/sqlitestore/store_test.go`:

```go
// sampleDocsWithAuthors returns documents that include created_by/updated_by/date fields.
func sampleDocsWithAuthors() []search.Document {
	now := time.Date(2024, 11, 15, 12, 0, 0, 0, time.UTC)
	return []search.Document{
		{
			ID:        "page:DEV/alice-doc.md",
			Type:      search.DocTypePage,
			Path:      "DEV/alice-doc.md",
			SpaceKey:  "DEV",
			Title:     "Alice's Document",
			Content:   "content about deployment",
			CreatedBy: "alice",
			CreatedAt: "2024-01-15T09:00:00Z",
			UpdatedBy: "alice",
			UpdatedAt: "2024-06-01T12:00:00Z",
			ModTime:   &now,
		},
		{
			ID:        "page:DEV/bob-doc.md",
			Type:      search.DocTypePage,
			Path:      "DEV/bob-doc.md",
			SpaceKey:  "DEV",
			Title:     "Bob's Document",
			Content:   "content about authentication",
			CreatedBy: "bob",
			CreatedAt: "2024-03-20T10:00:00Z",
			UpdatedBy: "alice",
			UpdatedAt: "2024-11-01T08:00:00Z",
			ModTime:   &now,
		},
		{
			ID:        "page:OPS/charlie-doc.md",
			Type:      search.DocTypePage,
			Path:      "OPS/charlie-doc.md",
			SpaceKey:  "OPS",
			Title:     "Charlie's Document",
			Content:   "content about operations",
			CreatedBy: "charlie",
			CreatedAt: "2024-09-10T14:00:00Z",
			UpdatedBy: "charlie",
			UpdatedAt: "2024-12-01T16:00:00Z",
			ModTime:   &now,
		},
	}
}

func TestStore_FilterByCreatedBy(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{CreatedBy: "alice"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for CreatedBy=alice, got %d", len(results))
	}
	if results[0].Document.CreatedBy != "alice" {
		t.Errorf("expected CreatedBy=alice, got %q", results[0].Document.CreatedBy)
	}
}

func TestStore_FilterByUpdatedBy(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// alice updated two documents (alice-doc and bob-doc)
	results, err := s.Search(search.SearchOptions{UpdatedBy: "alice"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for UpdatedBy=alice, got %d", len(results))
	}
	for _, r := range results {
		if r.Document.UpdatedBy != "alice" {
			t.Errorf("expected UpdatedBy=alice, got %q", r.Document.UpdatedBy)
		}
	}
}

func TestStore_FilterByCreatedAfterAndBefore(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// created_at between 2024-02-01 and 2024-10-01 => only bob-doc (2024-03-20)
	results, err := s.Search(search.SearchOptions{
		CreatedAfter:  "2024-02-01T00:00:00Z",
		CreatedBefore: "2024-10-01T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		// bob-doc (2024-03-20) and charlie-doc (2024-09-10) are in range
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}
}

func TestStore_FilterByUpdatedAfter(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// updated_at after 2024-10-01 => bob-doc (2024-11-01) and charlie-doc (2024-12-01)
	results, err := s.Search(search.SearchOptions{UpdatedAfter: "2024-10-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for UpdatedAfter, got %d", len(results))
	}
}

func TestStore_AuthorFieldsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	doc := search.Document{
		ID:        "page:TEST/roundtrip.md",
		Type:      search.DocTypePage,
		Path:      "TEST/roundtrip.md",
		SpaceKey:  "TEST",
		Title:     "Round Trip",
		Content:   "test",
		CreatedBy: "alice",
		CreatedAt: "2024-06-15T10:00:00Z",
		UpdatedBy: "bob",
		UpdatedAt: "2024-12-01T08:30:00Z",
	}

	if err := s.Index([]search.Document{doc}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{SpaceKey: "TEST"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0].Document
	if got.CreatedBy != "alice" {
		t.Errorf("CreatedBy: got %q, want %q", got.CreatedBy, "alice")
	}
	if got.CreatedAt != "2024-06-15T10:00:00Z" {
		t.Errorf("CreatedAt: got %q, want %q", got.CreatedAt, "2024-06-15T10:00:00Z")
	}
	if got.UpdatedBy != "bob" {
		t.Errorf("UpdatedBy: got %q, want %q", got.UpdatedBy, "bob")
	}
	if got.UpdatedAt != "2024-12-01T08:30:00Z" {
		t.Errorf("UpdatedAt: got %q, want %q", got.UpdatedAt, "2024-12-01T08:30:00Z")
	}
}
```

### Step 2: Run to verify failures

```bash
go test ./internal/search/sqlitestore/... -run "TestStore_Filter|TestStore_AuthorFields" -v
```

Expected: compile errors or FAIL because columns/filters don't exist yet.

### Step 3: Update the schema (`schema.go`)

In `internal/search/sqlitestore/schema.go`, add four columns to the `CREATE TABLE` statement in `DDL`, after `mod_time`:

```sql
mod_time      TEXT NOT NULL DEFAULT '',
created_by    TEXT NOT NULL DEFAULT '',
created_at    TEXT NOT NULL DEFAULT '',
updated_by    TEXT NOT NULL DEFAULT '',
updated_at    TEXT NOT NULL DEFAULT ''
```

Add two indexes after the existing three:

```sql
CREATE INDEX IF NOT EXISTS idx_documents_created_by ON documents(created_by);
CREATE INDEX IF NOT EXISTS idx_documents_updated_by ON documents(updated_by);
```

Add a `MigrationDDL` constant below `DDL` for upgrading existing databases:

```go
// MigrationDDL contains ALTER TABLE statements to add columns to existing databases.
// Each statement is safe to run on a fresh database (IF NOT EXISTS prevents errors).
// Run this before DDL on every Open.
const MigrationDDL = `
ALTER TABLE documents ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS updated_by TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS updated_at TEXT NOT NULL DEFAULT '';
`
```

### Step 4: Update `applyDDL` in `store.go`

In `internal/search/sqlitestore/store.go`, update `applyDDL` to run the migration first:

```go
func applyDDL(db *sql.DB) error {
	// Run migrations first (safe on fresh DBs — IF NOT EXISTS guards).
	for _, stmt := range strings.Split(strings.TrimSpace(MigrationDDL), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
	}
	if _, err := db.Exec(DDL); err != nil {
		return err
	}
	return nil
}
```

### Step 5: Update `Index()` in `store.go`

Replace the `const query` in `Index()` with:

```go
const query = `
INSERT INTO documents
    (id, type, path, page_id, title, space_key, labels,
     content, heading_path, heading_text, heading_level, language, line, mod_time,
     created_by, created_at, updated_by, updated_at)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    type          = excluded.type,
    path          = excluded.path,
    page_id       = excluded.page_id,
    title         = excluded.title,
    space_key     = excluded.space_key,
    labels        = excluded.labels,
    content       = excluded.content,
    heading_path  = excluded.heading_path,
    heading_text  = excluded.heading_text,
    heading_level = excluded.heading_level,
    language      = excluded.language,
    line          = excluded.line,
    mod_time      = excluded.mod_time,
    created_by    = excluded.created_by,
    created_at    = excluded.created_at,
    updated_by    = excluded.updated_by,
    updated_at    = excluded.updated_at`
```

Update the `stmt.Exec` call to pass the four new values (after `modTimeStr`):

```go
_, err = stmt.Exec(
    d.ID, d.Type, d.Path, d.PageID, d.Title, d.SpaceKey,
    labelsJSON, d.Content, headingPathJSON, d.HeadingText,
    d.HeadingLevel, d.Language, d.Line, modTimeStr,
    d.CreatedBy, d.CreatedAt, d.UpdatedBy, d.UpdatedAt,
)
```

### Step 6: Update `Search()` — SELECT clause

In `Search()`, update the `SELECT` in both `baseQuery` branches to include the four new columns. In the FTS branch:

```go
baseQuery = fmt.Sprintf(`
SELECT d.id, d.type, d.path, d.page_id, d.title, d.space_key,
       d.labels, d.content, d.heading_path, d.heading_text,
       d.heading_level, d.language, d.line, d.mod_time,
       d.created_by, d.created_at, d.updated_by, d.updated_at,
       fts.rank AS score,
       snippet(documents_fts, 1, '[', ']', '...', 10) AS snippet
FROM documents_fts fts
JOIN documents d ON d.rowid = fts.rowid
WHERE %s
ORDER BY fts.rank
LIMIT ?`, whereExpr)
```

In the non-FTS branch:

```go
baseQuery = fmt.Sprintf(`
SELECT d.id, d.type, d.path, d.page_id, d.title, d.space_key,
       d.labels, d.content, d.heading_path, d.heading_text,
       d.heading_level, d.language, d.line, d.mod_time,
       d.created_by, d.created_at, d.updated_by, d.updated_at,
       0.0 AS score,
       '' AS snippet
FROM documents d
%s
ORDER BY d.path, d.line
LIMIT ?`, whereExpr)
```

### Step 7: Update `Search()` — Scan and add filter clauses

Update the `rows.Scan` call to read the four new columns (after `modTimeStr`):

```go
if err := rows.Scan(
    &doc.ID, &doc.Type, &doc.Path, &doc.PageID, &doc.Title,
    &doc.SpaceKey, &labelsJSON, &doc.Content, &hpathJSON,
    &doc.HeadingText, &doc.HeadingLevel, &doc.Language, &doc.Line,
    &modTimeStr, &doc.CreatedBy, &doc.CreatedAt, &doc.UpdatedBy, &doc.UpdatedAt,
    &score, &snippet,
); err != nil {
```

Add the six new filter clauses after the existing `HeadingFilter` block (before `Types`):

```go
if opts.CreatedBy != "" {
    whereClauses = append(whereClauses, "d.created_by = ?")
    args = append(args, opts.CreatedBy)
}
if opts.UpdatedBy != "" {
    whereClauses = append(whereClauses, "d.updated_by = ?")
    args = append(args, opts.UpdatedBy)
}
if opts.CreatedAfter != "" {
    whereClauses = append(whereClauses, "d.created_at >= ?")
    args = append(args, opts.CreatedAfter)
}
if opts.CreatedBefore != "" {
    whereClauses = append(whereClauses, "d.created_at <= ?")
    args = append(args, opts.CreatedBefore)
}
if opts.UpdatedAfter != "" {
    whereClauses = append(whereClauses, "d.updated_at >= ?")
    args = append(args, opts.UpdatedAfter)
}
if opts.UpdatedBefore != "" {
    whereClauses = append(whereClauses, "d.updated_at <= ?")
    args = append(args, opts.UpdatedBefore)
}
```

### Step 8: Run the SQLite tests

```bash
go test ./internal/search/sqlitestore/... -v
```

Expected: all tests PASS, including the new author/date filter tests.

Also run the indexer test from Task 2 to verify end-to-end:

```bash
go test ./internal/search/... -run TestIndexer_AuthorFieldsPopulated -v
```

Expected: PASS.

### Step 9: Run all tests

```bash
go test ./...
```

Expected: all tests pass.

### Step 10: Commit

```bash
git add internal/search/sqlitestore/schema.go internal/search/sqlitestore/store.go internal/search/sqlitestore/store_test.go
git commit -m "feat(search/sqlite): index and filter by created_by, updated_by, created_at, updated_at"
```

---

## Task 4: Bleve backend

**Files:**
- Modify: `internal/search/blevestore/mapping.go`
- Modify: `internal/search/blevestore/store.go`
- Modify: `internal/search/blevestore/store_test.go`

### Step 1: Write failing tests

Add to `internal/search/blevestore/store_test.go`:

```go
func sampleDocsWithAuthors() []search.Document {
	now := time.Date(2024, 11, 15, 12, 0, 0, 0, time.UTC)
	return []search.Document{
		{
			ID:        "page:DEV/alice-doc.md",
			Type:      search.DocTypePage,
			Path:      "DEV/alice-doc.md",
			SpaceKey:  "DEV",
			Title:     "Alice's Document",
			Content:   "content about deployment",
			CreatedBy: "alice",
			CreatedAt: "2024-01-15T09:00:00Z",
			UpdatedBy: "alice",
			UpdatedAt: "2024-06-01T12:00:00Z",
			ModTime:   &now,
		},
		{
			ID:        "page:DEV/bob-doc.md",
			Type:      search.DocTypePage,
			Path:      "DEV/bob-doc.md",
			SpaceKey:  "DEV",
			Title:     "Bob's Document",
			Content:   "content about authentication",
			CreatedBy: "bob",
			CreatedAt: "2024-03-20T10:00:00Z",
			UpdatedBy: "alice",
			UpdatedAt: "2024-11-01T08:00:00Z",
			ModTime:   &now,
		},
	}
}

func TestBleveStore_FilterByCreatedBy(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{CreatedBy: "alice"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for CreatedBy=alice, got %d", len(results))
	}
	if results[0].Document.CreatedBy != "alice" {
		t.Errorf("CreatedBy: got %q, want %q", results[0].Document.CreatedBy, "alice")
	}
}

func TestBleveStore_FilterByUpdatedBy(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{UpdatedBy: "alice"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for UpdatedBy=alice, got %d", len(results))
	}
}

func TestBleveStore_FilterByCreatedAfterAndBefore(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocsWithAuthors()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Only bob-doc (2024-03-20) is in [2024-02-01, 2024-12-31]
	// alice-doc (2024-01-15) is before range
	results, err := s.Search(search.SearchOptions{
		CreatedAfter:  "2024-02-01T00:00:00Z",
		CreatedBefore: "2024-12-31T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestBleveStore_AuthorFieldsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	doc := search.Document{
		ID:        "page:TEST/roundtrip.md",
		Type:      search.DocTypePage,
		Path:      "TEST/roundtrip.md",
		SpaceKey:  "TEST",
		Title:     "Round Trip",
		Content:   "test",
		CreatedBy: "alice",
		CreatedAt: "2024-06-15T10:00:00Z",
		UpdatedBy: "bob",
		UpdatedAt: "2024-12-01T08:30:00Z",
	}

	if err := s.Index([]search.Document{doc}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{SpaceKey: "TEST"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0].Document
	if got.CreatedBy != "alice" {
		t.Errorf("CreatedBy: got %q, want %q", got.CreatedBy, "alice")
	}
	if got.UpdatedBy != "bob" {
		t.Errorf("UpdatedBy: got %q, want %q", got.UpdatedBy, "bob")
	}
}
```

### Step 2: Run to verify failures

```bash
go test ./internal/search/blevestore/... -run "TestBleveStore_Filter|TestBleveStore_Author" -v
```

Expected: FAIL — filters not implemented, author fields not round-tripped.

### Step 3: Update `mapping.go`

In `NewMapping()`, add four new field mappings after the existing ones:

```go
// keyword fields — author names for exact-match filtering
dm.AddFieldMappingsAt("created_by", kw)
dm.AddFieldMappingsAt("updated_by", kw)

// datetime fields — creation/update timestamps for date-range filtering
dm.AddFieldMappingsAt("created_at", dt)
dm.AddFieldMappingsAt("updated_at", dt)
```

Update `allDocFields` to include the four new fields:

```go
var allDocFields = []string{
    "type", "path", "page_id", "space_key", "labels",
    "language", "title", "content", "heading_text", "heading_path_text",
    "heading_level", "line", "mod_time",
    "created_by", "created_at", "updated_by", "updated_at",
}
```

### Step 4: Update `docToMap` in `store.go`

Add a helper to parse a date string for Bleve's datetime field (which requires `time.Time`):

```go
// parseDateString parses an RFC3339 string into time.Time for Bleve datetime fields.
// Returns nil if s is empty or unparseable.
func parseDateString(s string) interface{} {
    if s == "" {
        return nil
    }
    if t, err := time.Parse(time.RFC3339, s); err == nil {
        return t
    }
    return nil
}
```

In `docToMap`, add four new entries to the map (after `mod_time`):

```go
"created_by": d.CreatedBy,
"updated_by": d.UpdatedBy,
"created_at": parseDateString(d.CreatedAt),
"updated_at": parseDateString(d.UpdatedAt),
```

### Step 5: Update `mapToDoc` in `store.go`

Add four new field reads (after the existing `mod_time` block):

```go
if v, ok := fields["created_by"]; ok {
    d.CreatedBy = toString(v)
}
if v, ok := fields["updated_by"]; ok {
    d.UpdatedBy = toString(v)
}
if v, ok := fields["created_at"]; ok {
    if t, err := parseTimeField(v); err == nil {
        d.CreatedAt = t.UTC().Format(time.RFC3339)
    }
}
if v, ok := fields["updated_at"]; ok {
    if t, err := parseTimeField(v); err == nil {
        d.UpdatedAt = t.UTC().Format(time.RFC3339)
    }
}
```

### Step 6: Update `buildQuery` in `store.go`

Add filter clauses after the `HeadingFilter` block:

```go
// CreatedBy / UpdatedBy exact-match filters
if opts.CreatedBy != "" {
    tq := query.NewTermQuery(opts.CreatedBy)
    tq.SetField("created_by")
    musts = append(musts, tq)
}
if opts.UpdatedBy != "" {
    tq := query.NewTermQuery(opts.UpdatedBy)
    tq.SetField("updated_by")
    musts = append(musts, tq)
}

// Date range filters — parse RFC3339; skip malformed values gracefully
if opts.CreatedAfter != "" || opts.CreatedBefore != "" {
    var start, end *time.Time
    if t, err := time.Parse(time.RFC3339, opts.CreatedAfter); err == nil {
        start = &t
    }
    if t, err := time.Parse(time.RFC3339, opts.CreatedBefore); err == nil {
        end = &t
    }
    if start != nil || end != nil {
        incl := true
        drq := query.NewDateRangeInclusiveQuery(start, end, &incl, &incl)
        drq.SetField("created_at")
        musts = append(musts, drq)
    }
}
if opts.UpdatedAfter != "" || opts.UpdatedBefore != "" {
    var start, end *time.Time
    if t, err := time.Parse(time.RFC3339, opts.UpdatedAfter); err == nil {
        start = &t
    }
    if t, err := time.Parse(time.RFC3339, opts.UpdatedBefore); err == nil {
        end = &t
    }
    if start != nil || end != nil {
        incl := true
        drq := query.NewDateRangeInclusiveQuery(start, end, &incl, &incl)
        drq.SetField("updated_at")
        musts = append(musts, drq)
    }
}
```

### Step 7: Handle stale Bleve index (schema migration)

Bleve embeds its mapping at index creation time. An existing index will not have the new fields. Add automatic recreation in `Open()`.

Add a version constant and helper to `store.go`:

```go
// bleveIndexVersion is bumped whenever the index mapping changes.
// An existing index with a different version is deleted and recreated automatically.
const bleveIndexVersion = "v2"

var bleveVersionKey = []byte("confluence-sync:index-version")

func (s *Store) indexVersion() string {
    raw, _ := s.index.GetInternal(bleveVersionKey)
    return string(raw)
}

func (s *Store) setIndexVersion() error {
    return s.index.SetInternal(bleveVersionKey, []byte(bleveIndexVersion))
}
```

Update `Open()`:

```go
func Open(rootDir string) (*Store, error) {
    indexPath := filepath.Join(rootDir, indexSubDir)

    if _, statErr := os.Stat(indexPath); os.IsNotExist(statErr) {
        // Fresh install: create with current mapping.
        idx, err := bleve.New(indexPath, NewMapping())
        if err != nil {
            return nil, fmt.Errorf("blevestore.Open create %q: %w", indexPath, err)
        }
        s := &Store{index: idx}
        if err := s.setIndexVersion(); err != nil {
            _ = idx.Close()
            return nil, fmt.Errorf("blevestore.Open set version: %w", err)
        }
        return s, nil
    }

    // Existing index: check version.
    idx, err := bleve.Open(indexPath)
    if err != nil {
        return nil, fmt.Errorf("blevestore.Open %q: %w", indexPath, err)
    }

    s := &Store{index: idx}
    if s.indexVersion() != bleveIndexVersion {
        // Stale mapping — delete and recreate. The next search/index run will
        // trigger a full reindex automatically (LastIndexedAt returns zero).
        _ = idx.Close()
        if err := os.RemoveAll(indexPath); err != nil {
            return nil, fmt.Errorf("blevestore.Open remove stale index %q: %w", indexPath, err)
        }
        idx, err = bleve.New(indexPath, NewMapping())
        if err != nil {
            return nil, fmt.Errorf("blevestore.Open recreate %q: %w", indexPath, err)
        }
        s = &Store{index: idx}
        if err := s.setIndexVersion(); err != nil {
            _ = idx.Close()
            return nil, fmt.Errorf("blevestore.Open set version on recreated: %w", err)
        }
    }

    return s, nil
}
```

### Step 8: Run the Bleve tests

```bash
go test ./internal/search/blevestore/... -v
```

Expected: all tests PASS.

### Step 9: Run all tests

```bash
go test ./...
```

Expected: all tests pass.

### Step 10: Commit

```bash
git add internal/search/blevestore/mapping.go internal/search/blevestore/store.go internal/search/blevestore/store_test.go
git commit -m "feat(search/bleve): index and filter by created_by, updated_by, created_at, updated_at"
```

---

## Task 5: CLI command

**Files:**
- Modify: `cmd/search.go`
- Modify: `cmd/search_test.go`

### Step 1: Write failing tests

Find `cmd/search_test.go` and add these tests (use the same test helper pattern already in the file — open a store, index docs, call `runSearch` via `cmd.Execute`):

```go
func TestSearch_CreatedByFlag(t *testing.T) {
	repoDir, store := setupSearchTest(t)
	_ = store.Index([]search.Document{
		{
			ID: "page:DEV/a.md", Type: search.DocTypePage,
			Path: "DEV/a.md", SpaceKey: "DEV", Title: "Alice Doc",
			Content: "content", CreatedBy: "alice",
		},
		{
			ID: "page:DEV/b.md", Type: search.DocTypePage,
			Path: "DEV/b.md", SpaceKey: "DEV", Title: "Bob Doc",
			Content: "content", CreatedBy: "bob",
		},
	})

	out := &strings.Builder{}
	cmd := newSearchCmd()
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--created-by", "alice", "--format", "json", "--reindex=false"})

	// Point to repoDir
	_ = os.Chdir(repoDir)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out.String(), "alice") {
		t.Errorf("expected alice in output, got: %s", out.String())
	}
	if strings.Contains(out.String(), "bob") {
		t.Errorf("expected bob NOT in output, got: %s", out.String())
	}
}
```

Look at how existing CLI tests in `cmd/search_test.go` are structured — follow the same pattern exactly (e.g., how they set up the repo root, open the store, etc.).

### Step 2: Run to verify failures

```bash
go test ./cmd/... -run TestSearch_CreatedByFlag -v
```

Expected: FAIL — flag not registered.

### Step 3: Add flag variables and `searchRunOptions` fields

In `newSearchCmd()`, add six new flag variables at the top of the var block:

```go
var (
    // ... existing flags ...
    flagSearchCreatedBy    string
    flagSearchUpdatedBy    string
    flagSearchCreatedAfter  string
    flagSearchCreatedBefore string
    flagSearchUpdatedAfter  string
    flagSearchUpdatedBefore string
)
```

Register the flags in `newSearchCmd()` after the existing `cmd.Flags()` calls:

```go
cmd.Flags().StringVar(&flagSearchCreatedBy, "created-by", "", "Filter to pages created by this user (exact match)")
cmd.Flags().StringVar(&flagSearchUpdatedBy, "updated-by", "", "Filter to pages last updated by this user (exact match)")
cmd.Flags().StringVar(&flagSearchCreatedAfter, "created-after", "", "Filter to pages created on or after this date (YYYY-MM-DD or RFC3339)")
cmd.Flags().StringVar(&flagSearchCreatedBefore, "created-before", "", "Filter to pages created on or before this date")
cmd.Flags().StringVar(&flagSearchUpdatedAfter, "updated-after", "", "Filter to pages updated on or after this date")
cmd.Flags().StringVar(&flagSearchUpdatedBefore, "updated-before", "", "Filter to pages updated on or before this date")
```

Pass them into `runSearch`:

```go
return runSearch(cmd, query, searchRunOptions{
    // ... existing fields ...
    createdBy:    flagSearchCreatedBy,
    updatedBy:    flagSearchUpdatedBy,
    createdAfter:  flagSearchCreatedAfter,
    createdBefore: flagSearchCreatedBefore,
    updatedAfter:  flagSearchUpdatedAfter,
    updatedBefore: flagSearchUpdatedBefore,
})
```

Add to `searchRunOptions`:

```go
type searchRunOptions struct {
    // ... existing fields ...
    createdBy    string
    updatedBy    string
    createdAfter  string
    createdBefore string
    updatedAfter  string
    updatedBefore string
}
```

### Step 4: Add `normalizeDateBound` helper and wire filters in `runSearch`

Add the helper to `cmd/search.go`:

```go
// normalizeDateBound normalizes a user-supplied date string to RFC3339 for store queries.
// YYYY-MM-DD is expanded: endOfDay=true → T23:59:59Z, endOfDay=false → T00:00:00Z.
// Already-valid RFC3339 strings are returned unchanged. Unrecognized formats are passed through.
func normalizeDateBound(s string, endOfDay bool) string {
    if s == "" {
        return ""
    }
    if _, err := time.Parse(time.RFC3339, s); err == nil {
        return s
    }
    if _, err := time.Parse("2006-01-02", s); err == nil {
        if endOfDay {
            return s + "T23:59:59Z"
        }
        return s + "T00:00:00Z"
    }
    return s
}
```

In `runSearch()`, update the `store.Search` call:

```go
results, err := store.Search(search.SearchOptions{
    Query:         query,
    SpaceKey:      opts.space,
    Labels:        opts.labels,
    HeadingFilter: opts.heading,
    Limit:         limit,
    CreatedBy:     opts.createdBy,
    UpdatedBy:     opts.updatedBy,
    CreatedAfter:  normalizeDateBound(opts.createdAfter, false),
    CreatedBefore: normalizeDateBound(opts.createdBefore, true),
    UpdatedAfter:  normalizeDateBound(opts.updatedAfter, false),
    UpdatedBefore: normalizeDateBound(opts.updatedBefore, true),
})
```

### Step 5: Update text output to show byline

Add a `shortDate` helper:

```go
// shortDate returns the YYYY-MM-DD prefix of an RFC3339 string, or the full string
// if it is shorter than 10 characters.
func shortDate(s string) string {
    if len(s) >= 10 {
        return s[:10]
    }
    return s
}
```

In `printSearchResults`, after the header line (the `fmt.Fprintf(out, "%s%s%s\n", ...)` call), add:

```go
// Metadata byline (only when any field is present)
var metaParts []string
if doc.CreatedBy != "" {
    metaParts = append(metaParts, "Created by: "+doc.CreatedBy)
}
if doc.CreatedAt != "" {
    metaParts = append(metaParts, "Created: "+shortDate(doc.CreatedAt))
}
if doc.UpdatedBy != "" {
    metaParts = append(metaParts, "Updated by: "+doc.UpdatedBy)
}
if doc.UpdatedAt != "" {
    metaParts = append(metaParts, "Updated: "+shortDate(doc.UpdatedAt))
}
if len(metaParts) > 0 {
    _, _ = fmt.Fprintf(out, "  %s\n", strings.Join(metaParts, "  "))
}
```

### Step 6: Update `projectResult`

In `projectResult`, add the four new fields to the `standard` case:

```go
case "standard":
    r.Document = search.Document{
        Path:        r.Document.Path,
        Title:       r.Document.Title,
        SpaceKey:    r.Document.SpaceKey,
        Labels:      r.Document.Labels,
        HeadingPath: r.Document.HeadingPath,
        HeadingText: r.Document.HeadingText,
        Line:        r.Document.Line,
        CreatedBy:   r.Document.CreatedBy,
        UpdatedBy:   r.Document.UpdatedBy,
        CreatedAt:   r.Document.CreatedAt,
        UpdatedAt:   r.Document.UpdatedAt,
    }
    return r
```

`minimal` and `full` are unchanged — `minimal` already strips everything except path/heading/line; `full` returns `r` unchanged, which now includes the new fields automatically.

### Step 7: Add `"time"` import if not already present

The `normalizeDateBound` helper uses `time.Parse` and `time.RFC3339`. Check the imports at the top of `cmd/search.go` — `"time"` may already be imported. If not, add it.

### Step 8: Run all cmd tests

```bash
go test ./cmd/... -v
```

Expected: all tests PASS.

### Step 9: Run full test suite

```bash
go test ./...
```

Expected: all tests pass with no failures.

### Step 10: Commit

```bash
git add cmd/search.go cmd/search_test.go
git commit -m "feat(cmd/search): add --created-by, --updated-by, --created-after/before, --updated-after/before flags"
```

---

## Final Verification

```bash
go test ./...
go build ./...
```

Both must succeed with no errors or failures before considering this implementation complete.
