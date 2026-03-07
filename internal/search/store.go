package search

import "time"

// Store is the backend-agnostic interface that all search index implementations must satisfy.
//
// The Indexer uses Store exclusively — it never calls Bleve or SQLite APIs directly.
// This enables transparent A/B comparison between the two backends.
type Store interface {
	// Index upserts all documents for a single source file.
	// Callers should call DeleteByPath first to replace existing content atomically.
	Index(docs []Document) error

	// DeleteByPath removes all indexed documents whose Path equals relPath.
	DeleteByPath(relPath string) error

	// Search executes a full-text query and returns ranked results.
	Search(opts SearchOptions) ([]SearchResult, error)

	// ListLabels returns all distinct label values present in the index, sorted.
	ListLabels() ([]string, error)

	// ListSpaces returns all distinct space key values present in the index, sorted.
	ListSpaces() ([]string, error)

	// ListPathsBySpace returns distinct indexed source paths for a space.
	ListPathsBySpace(spaceKey string) ([]string, error)

	// UpdateMeta records the current UTC timestamp as the last-indexed-at time.
	UpdateMeta() error

	// LastIndexedAt returns the time recorded by the most recent successful UpdateMeta call.
	// Returns the zero time.Time and a nil error if no meta has been recorded yet.
	LastIndexedAt() (time.Time, error)

	// Close releases resources held by the store.
	Close() error
}
