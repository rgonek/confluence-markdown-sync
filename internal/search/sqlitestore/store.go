package sqlitestore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/rgonek/confluence-markdown-sync/internal/search"
	_ "modernc.org/sqlite" // SQLite driver registration
)

const (
	// defaultSearchLimit is used when SearchOptions.Limit is 0.
	defaultSearchLimit = 20
)

// Store is a search.Store implementation backed by SQLite + FTS5.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dbPath and applies all DDL migrations.
// The directory containing dbPath is created if it does not exist.
func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // index dirs are intentionally group-readable
		return nil, fmt.Errorf("sqlitestore: create directory %s: %w", dir, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open %s: %w", dbPath, err)
	}

	// SQLite performs best with a single writer; cap pool to avoid locking issues.
	db.SetMaxOpenConns(1)

	if err := applyDDL(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitestore: apply schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Index upserts all documents for a single source file.
// It wraps all inserts in a transaction for atomicity.
func (s *Store) Index(docs []search.Document) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlitestore.Index begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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

	stmt, err := tx.Prepare(query)
	if err != nil {
		return fmt.Errorf("sqlitestore.Index prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range docs {
		d := &docs[i]
		labelsJSON, err := marshalJSON(d.Labels)
		if err != nil {
			return fmt.Errorf("sqlitestore.Index marshal labels: %w", err)
		}
		headingPathJSON, err := marshalJSON(d.HeadingPath)
		if err != nil {
			return fmt.Errorf("sqlitestore.Index marshal heading_path: %w", err)
		}
		modTimeStr := ""
		if d.ModTime != nil {
			modTimeStr = d.ModTime.UTC().Format(time.RFC3339)
		}
		_, err = stmt.Exec(
			d.ID, d.Type, d.Path, d.PageID, d.Title, d.SpaceKey,
			labelsJSON, d.Content, headingPathJSON, d.HeadingText,
			d.HeadingLevel, d.Language, d.Line, modTimeStr,
			d.CreatedBy, d.CreatedAt, d.UpdatedBy, d.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("sqlitestore.Index exec for %s: %w", d.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitestore.Index commit: %w", err)
	}
	return nil
}

// DeleteByPath removes all indexed documents whose Path equals relPath.
func (s *Store) DeleteByPath(relPath string) error {
	_, err := s.db.Exec(`DELETE FROM documents WHERE path = ?`, relPath)
	if err != nil {
		return fmt.Errorf("sqlitestore.DeleteByPath %s: %w", relPath, err)
	}
	return nil
}

// Search executes a full-text query and returns ranked results.
func (s *Store) Search(opts search.SearchOptions) ([]search.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	// Build the WHERE clause and argument list dynamically.
	var (
		whereClauses []string
		args         []any
	)

	if opts.Query != "" {
		safeQuery, err := normalizeFTSQuery(opts.Query)
		if err != nil {
			return nil, fmt.Errorf("sqlitestore.Search query normalize: %w", err)
		}
		whereClauses = append(whereClauses, "documents_fts MATCH ?")
		args = append(args, safeQuery)
	}

	if opts.SpaceKey != "" {
		whereClauses = append(whereClauses, "d.space_key = ?")
		args = append(args, opts.SpaceKey)
	}

	for _, label := range opts.Labels {
		whereClauses = append(whereClauses, `EXISTS (
            SELECT 1 FROM json_each(d.labels) WHERE json_each.value = ?
        )`)
		args = append(args, label)
	}

	if opts.HeadingFilter != "" {
		whereClauses = append(whereClauses, "d.heading_text LIKE ?")
		args = append(args, "%"+opts.HeadingFilter+"%")
	}

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

	if len(opts.Types) > 0 {
		placeholders := strings.Repeat("?,", len(opts.Types))
		placeholders = strings.TrimSuffix(placeholders, ",")
		whereClauses = append(whereClauses, fmt.Sprintf("d.type IN (%s)", placeholders))
		for _, t := range opts.Types {
			args = append(args, t)
		}
	}

	args = append(args, limit)

	var baseQuery string
	if opts.Query != "" {
		whereExpr := strings.Join(whereClauses, " AND ")
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
	} else {
		whereExpr := ""
		if len(whereClauses) > 0 {
			whereExpr = "WHERE " + strings.Join(whereClauses, " AND ")
		}
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
	}

	rows, err := s.db.Query(baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore.Search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []search.SearchResult
	for rows.Next() {
		var (
			doc        search.Document
			labelsJSON string
			hpathJSON  string
			modTimeStr string
			score      float64
			snippet    string
		)
		if err := rows.Scan(
			&doc.ID, &doc.Type, &doc.Path, &doc.PageID, &doc.Title,
			&doc.SpaceKey, &labelsJSON, &doc.Content, &hpathJSON,
			&doc.HeadingText, &doc.HeadingLevel, &doc.Language, &doc.Line,
			&modTimeStr, &doc.CreatedBy, &doc.CreatedAt, &doc.UpdatedBy, &doc.UpdatedAt,
			&score, &snippet,
		); err != nil {
			return nil, fmt.Errorf("sqlitestore.Search scan: %w", err)
		}

		if err := json.Unmarshal([]byte(labelsJSON), &doc.Labels); err != nil {
			doc.Labels = nil
		}
		if err := json.Unmarshal([]byte(hpathJSON), &doc.HeadingPath); err != nil {
			doc.HeadingPath = nil
		}
		if modTimeStr != "" {
			if t, err := time.Parse(time.RFC3339, modTimeStr); err == nil {
				doc.ModTime = &t
			}
		}

		results = append(results, search.SearchResult{
			Document: doc,
			Score:    score,
			Snippet:  snippet,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitestore.Search rows: %w", err)
	}
	return results, nil
}

// ListLabels returns all distinct label values present in the index, sorted.
func (s *Store) ListLabels() ([]string, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT j.value
FROM documents, json_each(documents.labels) j
WHERE j.value != ''
ORDER BY j.value`)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore.ListLabels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, fmt.Errorf("sqlitestore.ListLabels scan: %w", err)
		}
		labels = append(labels, label)
	}
	return labels, rows.Err()
}

// ListSpaces returns all distinct space key values present in the index, sorted.
func (s *Store) ListSpaces() ([]string, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT space_key
FROM documents
WHERE space_key != ''
ORDER BY space_key`)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore.ListSpaces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var spaces []string
	for rows.Next() {
		var space string
		if err := rows.Scan(&space); err != nil {
			return nil, fmt.Errorf("sqlitestore.ListSpaces scan: %w", err)
		}
		spaces = append(spaces, space)
	}
	return spaces, rows.Err()
}

// ListPathsBySpace returns all distinct indexed source paths for spaceKey, sorted.
func (s *Store) ListPathsBySpace(spaceKey string) ([]string, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT path
FROM documents
WHERE space_key = ?
ORDER BY path`, spaceKey)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore.ListPathsBySpace: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("sqlitestore.ListPathsBySpace scan: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// UpdateMeta records the current UTC timestamp as the last-indexed-at time.
func (s *Store) UpdateMeta() error {
	ts := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
INSERT INTO meta(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		metaKeyLastIndexedAt, ts)
	if err != nil {
		return fmt.Errorf("sqlitestore.UpdateMeta: %w", err)
	}
	return nil
}

// LastIndexedAt returns the time recorded by the most recent successful UpdateMeta call.
// Returns the zero time.Time and a nil error if no meta has been recorded yet.
func (s *Store) LastIndexedAt() (time.Time, error) {
	var ts string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaKeyLastIndexedAt).Scan(&ts)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlitestore.LastIndexedAt: %w", err)
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlitestore.LastIndexedAt parse time: %w", err)
	}
	return t, nil
}

// — helpers —

func applyDDL(db *sql.DB) error {
	// Apply the base DDL first (creates tables if they don't exist).
	if _, err := db.Exec(DDL); err != nil {
		return err
	}
	// Then run column migrations for existing databases.
	if err := addColumnsIfMissing(db, "documents", map[string]string{
		"created_by": "TEXT NOT NULL DEFAULT ''",
		"created_at": "TEXT NOT NULL DEFAULT ''",
		"updated_by": "TEXT NOT NULL DEFAULT ''",
		"updated_at": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	return nil
}

// addColumnsIfMissing adds columns to a table only if they are not already present.
// This is a safe alternative to ALTER TABLE ... ADD COLUMN IF NOT EXISTS for SQLite
// versions that do not support that syntax.
func addColumnsIfMissing(db *sql.DB, table string, columns map[string]string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("pragma table_info %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan table_info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for col, def := range columns {
		if existing[col] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, def)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s: %w", col, err)
		}
	}
	return nil
}

func marshalJSON(v any) (string, error) {
	if v == nil {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func normalizeFTSQuery(raw string) (string, error) {
	sanitized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return ' '
	}, raw)
	tokens := strings.Fields(sanitized)
	if len(tokens) == 0 {
		return "", fmt.Errorf("query contains no searchable tokens")
	}
	return strings.Join(tokens, " "), nil
}
