# Design: Frontmatter Author/Date Search Indexing

## Context

The search index currently captures `space_key`, `labels`, `title`, and `page_id` from YAML
frontmatter. The fields `created_by`, `created_at`, `updated_by`, and `updated_at` — already
parsed by `internal/fs/frontmatter.go` and written by sync operations — are not indexed.
This design extends the index to include them and adds six CLI filter flags.

## Approach: Denormalize into all document types

Following the existing architecture, all four fields are stored on every document row (page,
section, code) for zero-join filtering — identical to how `space_key`, `labels`, and `title`
are already denormalized. No JOINs, no separate table, no subqueries.

## Data Model

### `Document` struct (`internal/search/document.go`)

Four new fields added:

```go
CreatedBy string `json:"created_by,omitempty"`
CreatedAt string `json:"created_at,omitempty"` // ISO8601, e.g. "2024-11-15T10:30:00Z"
UpdatedBy string `json:"updated_by,omitempty"`
UpdatedAt string `json:"updated_at,omitempty"` // ISO8601
```

### `SearchOptions` (`internal/search/document.go`)

Six new filter fields:

```go
CreatedBy    string // exact match
UpdatedBy    string // exact match
CreatedAfter  string // ISO8601, inclusive lower bound
CreatedBefore string // ISO8601, inclusive upper bound
UpdatedAfter  string
UpdatedBefore string
```

## Storage Backends

### SQLite (`internal/search/sqlitestore/schema.go`)

Four new TEXT columns on `documents`:

```sql
created_by   TEXT NOT NULL DEFAULT '',
created_at   TEXT NOT NULL DEFAULT '',
updated_by   TEXT NOT NULL DEFAULT '',
updated_at   TEXT NOT NULL DEFAULT ''
```

Two new indexes for exact-match author filtering:

```sql
CREATE INDEX IF NOT EXISTS idx_documents_created_by ON documents(created_by);
CREATE INDEX IF NOT EXISTS idx_documents_updated_by ON documents(updated_by);
```

Date ranges use ISO8601 lexicographic comparison (strings sort correctly as text).
FTS5 virtual table and triggers are unchanged — these fields are filter-only, not full-text indexed.

Schema migration: `ALTER TABLE documents ADD COLUMN IF NOT EXISTS` statements run on store open,
before the main DDL, to handle existing index databases.

### Bleve (`internal/search/blevestore/mapping.go`)

```
created_by, updated_by  → keyword fields (TermQuery, exact match)
created_at, updated_at  → datetime fields (DateRangeQuery)
```

## Indexer (`internal/search/indexer.go`)

`indexFile()` already reads `fm := mdDoc.Frontmatter`. The four fields are propagated to all
document rows in the three `append` blocks (page, section, code):

```go
CreatedBy: fm.CreatedBy,
CreatedAt: fm.CreatedAt,
UpdatedBy: fm.UpdatedBy,
UpdatedAt: fm.UpdatedAt,
```

No other changes to indexer logic.

## CLI Command (`cmd/search.go`)

Six new flags:

| Flag | Type | Description |
|------|------|-------------|
| `--created-by` | string | Filter to pages created by this user (exact match) |
| `--updated-by` | string | Filter to pages last updated by this user (exact match) |
| `--created-after` | string | Created on or after this date (YYYY-MM-DD or RFC3339) |
| `--created-before` | string | Created on or before this date |
| `--updated-after` | string | Updated on or after this date |
| `--updated-before` | string | Updated on or before this date |

Wired through `searchRunOptions` → `SearchOptions` using the existing
`cmd.Flags().Changed()` override pattern.

**Text output** adds a byline when author/date fields are present:

```
DEV/security/overview.md - Security Overview [architecture, security]
  Created by: john.doe  Updated by: jane.smith  Updated: 2024-11-15
  ## OAuth2 Flow > ### Token Refresh (line 87)
    ...refresh tokens are rotated every 15 minutes...
```

**`projectResult()`** — `standard` preset includes the four new fields; `minimal` strips them
(consistent with minimal keeping only location fields: path, heading, line).

## Testing

- **`indexer_test.go`** — assert all four fields populated on page/section/code doc types
- **`sqlitestore/store_test.go`** — test each new filter: exact author match, date range bounds,
  empty string = no filter applied
- **`blevestore/store_test.go`** — same filter coverage for Bleve backend
- **`cmd/search_test.go`** — CLI tests exercising the six new flags end-to-end

## Files Changed

| File | Change |
|------|--------|
| `internal/search/document.go` | Add 4 fields to `Document`; add 6 fields to `SearchOptions` |
| `internal/search/indexer.go` | Populate 4 fields in `indexFile()` |
| `internal/search/sqlitestore/schema.go` | 4 new columns, 2 new indexes, migration stmts |
| `internal/search/sqlitestore/store.go` | Read/write new columns; apply new filters in Search() |
| `internal/search/sqlitestore/store_test.go` | New filter test cases |
| `internal/search/blevestore/mapping.go` | 4 new field mappings |
| `internal/search/blevestore/store.go` | Populate/read new fields; apply new filters in Search() |
| `internal/search/blevestore/store_test.go` | New filter test cases |
| `cmd/search.go` | 6 new flags, text output byline, projectResult updates |
| `cmd/search_test.go` | CLI flag tests |
