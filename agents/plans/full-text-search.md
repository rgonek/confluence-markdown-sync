# Plan: Full-Text Search with Bleve + SQLite FTS5 (Dual Backend)

## Context

AI agents using `conf` are token-expensive during reads because grep/ripgrep has no awareness of document structure. The Atlassian MCP wins on reads because it returns targeted, structured results. Adding `conf search` with Goldmark (markdown AST) and a pluggable search backend gives agents heading-anchored, faceted search — the "search-then-read" pattern that makes MCP efficient, but locally with zero API calls.

We implement **two backends** (Bleve and SQLite FTS5) behind a shared interface to evaluate which works better in practice, then drop the loser.

## Architecture

```
cmd/search.go                    -- CLI command, output formatting
       |
internal/search/
  parser.go                      -- Goldmark AST → sections, code blocks
  document.go                    -- Shared document types
  store.go                       -- Store interface
  indexer.go                     -- Orchestrates file walking + store calls
       |
       ├── blevestore/store.go   -- Bleve scorch implementation
       └── sqlitestore/store.go  -- SQLite + FTS5 implementation
```

### Store Interface

```go
type Store interface {
    Index(docs []Document) error         // Upsert documents for a file
    DeleteByPath(relPath string) error   // Remove all docs for a file
    Search(opts SearchOptions) ([]SearchResult, error)
    ListLabels() ([]string, error)       // All unique labels with counts
    ListSpaces() ([]string, error)       // All unique space keys
    UpdateMeta() error                   // Mark index timestamp
    LastIndexedAt() (time.Time, error)   // Read index timestamp
    Close() error
}
```

Both backends implement this. The `Indexer` orchestrates file walking and calls `Store` methods — it never touches Bleve or SQLite directly.

### Document Model (shared, 3 types)

| Doc Type | ID Pattern | Purpose |
|----------|-----------|---------|
| `page` | `page:<relPath>` | Full file: frontmatter facets + full body text |
| `section` | `section:<relPath>:<line>` | Heading-anchored section: heading hierarchy + section content |
| `code` | `code:<relPath>:<line>` | Fenced code block: language tag + content + heading context |

All types carry denormalized frontmatter fields (`space_key`, `labels`, `title`, `page_id`) for zero-join filtering.

```go
type Document struct {
    ID           string   // Composite ID
    Type         string   // "page", "section", "code"
    Path         string   // Relative path (forward slashes)
    PageID       string
    Title        string
    SpaceKey     string
    Labels       []string
    Content      string   // Body (page), section text, or code content
    HeadingPath  []string // Heading hierarchy for sections/code
    HeadingText  string   // Innermost heading text
    HeadingLevel int
    Language     string   // Code block language
    Line         int      // 1-based start line
    ModTime      time.Time
}
```

## File Layout

```
internal/search/
  document.go                     -- Document struct, SearchResult, Match types
  store.go                        -- Store interface definition
  parser.go                       -- ParseMarkdownStructure() Goldmark AST walker
  parser_test.go
  indexer.go                      -- Indexer: file walking, Store orchestration
  indexer_test.go
  search_testhelpers_test.go      -- Shared test helpers

  blevestore/
    store.go                      -- Bleve Store implementation
    store_test.go
    mapping.go                    -- Bleve index mapping/schema

  sqlitestore/
    store.go                      -- SQLite+FTS5 Store implementation
    store_test.go
    schema.go                     -- DDL statements, migration

cmd/
  search.go                       -- newSearchCmd(), runSearch(), formatters
  search_test.go
```

## Implementation Phases

### Phase 1: Shared Types + Goldmark Parser
**Files:** `internal/search/document.go`, `internal/search/store.go`, `internal/search/parser.go`, `internal/search/parser_test.go`

Zero external dependencies beyond Goldmark (already in go.mod).

**`ParseMarkdownStructure(source []byte) ParseResult`** walks the Goldmark AST:
1. Collect all `*ast.Heading` nodes (level, text, line) and `*ast.FencedCodeBlock` nodes
2. Build sections with heading stack: pop entries when same-or-higher level heading arrives
3. Map code blocks to enclosing heading context by line position

**Reuse pattern from:** `internal/sync/assets.go:34` — `goldmark.New().Parser().Parse(text.NewReader(source))` + `ast.Walk`

**Line numbers:** Convert Goldmark byte offsets (`node.Lines().At(0).Start`) to 1-based lines by counting `\n` in `source[:offset]`.

### Phase 2a: Bleve Backend
**Files:** `internal/search/blevestore/mapping.go`, `internal/search/blevestore/store.go`, `internal/search/blevestore/store_test.go`

**Dependency:** `go get github.com/blevesearch/bleve/v2`

**Index location:** `<rootDir>/.confluence-search-index/bleve/`

**Field mapping:**
- **keyword** (exact match): `type`, `path`, `page_id`, `space_key`, `labels`, `language`
- **text** (standard analyzer): `title`, `content`, `heading_text`, `heading_path`
- **numeric**: `heading_level`, `line`
- **date**: `mod_time`

**Search query construction:**
- Text: `DisjunctionQuery` across `content` (boost 2.0), `heading_text` (1.5), `title` (1.0)
- Filters: `TermQuery` on keyword fields
- Combined: `ConjunctionQuery`
- Result aggregation: group hits by `path`, sort by top score

### Phase 2b: SQLite FTS5 Backend
**Files:** `internal/search/sqlitestore/schema.go`, `internal/search/sqlitestore/store.go`, `internal/search/sqlitestore/store_test.go`

**Dependency:** `go get modernc.org/sqlite` (pure Go, no CGo — works on Windows without gcc)

**Index location:** `<rootDir>/.confluence-search-index/search.db`

**Schema:**
```sql
CREATE TABLE IF NOT EXISTS documents (
    id         TEXT PRIMARY KEY,
    type       TEXT NOT NULL,           -- 'page', 'section', 'code'
    path       TEXT NOT NULL,
    page_id    TEXT,
    title      TEXT,
    space_key  TEXT,
    labels     TEXT,                     -- JSON array: '["arch","security"]'
    content    TEXT,
    heading_path TEXT,                   -- JSON array: '["## Foo","### Bar"]'
    heading_text TEXT,
    heading_level INTEGER,
    language   TEXT,
    line       INTEGER,
    mod_time   TEXT
);

CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path);
CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(type);
CREATE INDEX IF NOT EXISTS idx_documents_space ON documents(space_key);

-- FTS5 virtual table for full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
    title,
    content,
    heading_text,
    content=documents,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
    INSERT INTO documents_fts(rowid, title, content, heading_text)
    VALUES (new.rowid, new.title, new.content, new.heading_text);
END;
-- (similar for UPDATE/DELETE triggers)

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);
```

**Search query:**
```sql
SELECT d.*, fts.rank
FROM documents_fts fts
JOIN documents d ON d.rowid = fts.rowid
WHERE documents_fts MATCH ?
  AND d.type IN ('section', 'code')
  AND (? = '' OR d.space_key = ?)
  AND (? = '' OR EXISTS (
    SELECT 1 FROM json_each(d.labels) WHERE json_each.value = ?
  ))
ORDER BY fts.rank
LIMIT ?
```

**Label listing:** `SELECT DISTINCT j.value FROM documents, json_each(documents.labels) j ORDER BY j.value`

### Phase 3: Indexer (shared orchestration)
**Files:** `internal/search/indexer.go`, `internal/search/indexer_test.go`

The `Indexer` operates on the `Store` interface — backend-agnostic.

```go
type Indexer struct {
    store   Store
    rootDir string
}

func NewIndexer(store Store, rootDir string) *Indexer
func (ix *Indexer) Reindex() (int, error)              // Full reindex
func (ix *Indexer) IndexSpace(spaceDir, spaceKey string) (int, error)
func (ix *Indexer) IncrementalUpdate() (int, error)     // Mtime-based delta
func (ix *Indexer) Close() error
```

**Per-file indexing flow:**
1. `fs.ReadMarkdownDocument(absPath)` — get frontmatter + body
2. `ParseMarkdownStructure(body)` — get sections + code blocks
3. `store.DeleteByPath(relPath)` — remove old documents
4. Build `[]Document` (1 page + N sections + M code blocks)
5. `store.Index(docs)` — insert all

**File walking:** Reuse the standard skip pattern (`assets/`, `.`-prefixed dirs, `.md` only) from `internal/sync/index.go`. Discover spaces via `fs.FindAllStateFiles()`.

### Phase 4: CLI Command
**Files:** `cmd/search.go`, `cmd/search_test.go`

**Command:** `conf search QUERY [flags]`

**Flags:**
| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--space` | string | "" | Filter to specific space key |
| `--label` | []string | nil | Filter by label (repeatable) |
| `--heading` | string | "" | Filter to sections under matching headings |
| `--format` | string | auto | `text` or `json` (auto: TTY→text, pipe→json) |
| `--limit` | int | 20 | Max results |
| `--reindex` | bool | false | Force full reindex before searching |
| `--engine` | string | "sqlite" | Backend: `bleve` or `sqlite` (for A/B testing) |
| `--list-labels` | bool | false | List all indexed labels and exit |
| `--list-spaces` | bool | false | List all indexed spaces and exit |

**`runSearch` flow:**
1. `gitRepoRoot()`
2. Open store based on `--engine` flag
3. Create `Indexer` with store
4. If `--reindex`: full reindex, else: incremental update
5. If `--list-labels`: `store.ListLabels()` → format → exit
6. If `--list-spaces`: `store.ListSpaces()` → format → exit
7. `store.Search(opts)` → format output

**Text output:**
```
DEV/security/overview.md - Security Overview [architecture, security]
  ## OAuth2 Flow > ### Token Refresh (line 87)
    ...refresh tokens are rotated every 15 minutes using PKCE...
```

**JSON output:** `[]SearchResult` with `json.Encoder.SetIndent("", "  ")`

**Registration:** Add `newSearchCmd()` to `rootCmd.AddCommand(...)` in `cmd/root.go:98`

### Phase 5: Integration Hooks
**Modified files:** `cmd/pull.go`, `cmd/clean.go`, `cmd/init.go`

**5a. Post-pull indexing** (`cmd/pull.go` after line 336):
```go
if err := updateSearchIndexForSpace(repoRoot, pullCtx.spaceDir, pullCtx.spaceKey, out); err != nil {
    _, _ = fmt.Fprintf(out, "warning: search index update failed: %v\n", err)
}
```
Non-fatal — a failed index update never fails a pull.

**5b. Clean command** (`cmd/clean.go` after line 124):
Remove search index directories as part of clean artifacts.

**5c. Gitignore** (`cmd/init.go`):
- Add `.confluence-search-index/` to `gitignoreContent` template (line 15)
- Add `.confluence-search-index/` to `ensureGitignore()` entries (line 221)

**5d. Repo `.gitignore`** — add `.confluence-search-index/`

### Phase 6: Documentation
- Update `AGENTS.md` with `conf search` command reference
- Update `docs/usage.md` with search docs

## Implementation Order

1. **Phase 1** — Shared types + Goldmark parser (testable immediately, no new deps)
2. **Phase 2b** — SQLite backend first (simpler, pure Go, faster to get working)
3. **Phase 3** — Indexer (uses SQLite backend for initial testing)
4. **Phase 4** — CLI command (end-to-end working with SQLite)
5. **Phase 5** — Integration hooks
6. **Phase 2a** — Bleve backend (add second backend, compare)
7. **Phase 6** — Documentation + decide which backend to keep

## Critical Files Reference

| File | Action |
|------|--------|
| `internal/fs/frontmatter.go` | Reuse `ReadMarkdownDocument()`, `ReadFrontmatter()`, `NormalizeLabels()` |
| `internal/fs/state.go` | Reuse `FindAllStateFiles()`, `LoadState()` |
| `internal/sync/assets.go:34` | Reference Goldmark AST walk pattern |
| `internal/sync/index.go` | Replicate WalkDir skip logic |
| `cmd/root.go:98` | Add `newSearchCmd()` |
| `cmd/pull.go:336` | Insert post-pull indexing hook |
| `cmd/clean.go:124` | Insert search index cleanup |
| `cmd/init.go:15,221` | Add `.confluence-search-index/` to gitignore |

## Verification

1. **Unit tests:** `make test` — all new tests pass
2. **Smoke test both backends:**
   - `conf search "term" --engine sqlite` vs `conf search "term" --engine bleve`
   - Compare: speed, result quality, index size
3. **Facet discovery:**
   - `conf search --list-labels --format json` → verify all labels returned
   - `conf search --list-spaces --format json` → verify all spaces returned
4. **Incremental:** Edit a file → `conf search "term"` → verify only changed file reindexed
5. **Post-pull:** `conf pull SPACE` → verify "Updated search index" message
6. **Clean:** `conf clean` → verify index removed
7. **Pipe:** `conf search "term" | head` → verify auto-JSON format
8. **Init:** `conf init` → verify `.gitignore` includes `.confluence-search-index/`
