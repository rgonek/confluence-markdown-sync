// Package search provides full-text search over a local Confluence Markdown workspace.
package search

import "time"

// DocType enumerates the document types indexed by the search engine.
const (
	DocTypePage    = "page"
	DocTypeSection = "section"
	DocTypeCode    = "code"
)

// Document represents a single indexable unit produced from a Markdown file.
//
// Three document types share this struct:
//   - page    (ID = "page:<relPath>")         — whole-file, frontmatter facets + full body
//   - section (ID = "section:<relPath>:<line>") — heading-anchored slice of body text
//   - code    (ID = "code:<relPath>:<line>")    — fenced code block + heading context
//
// All types carry denormalized frontmatter fields (SpaceKey, Labels, Title, PageID)
// so that filtering never requires a join.
type Document struct {
	// ID is a composite, globally unique key.
	ID string

	// Type is DocTypePage, DocTypeSection, or DocTypeCode.
	Type string

	// Path is the repository-relative path with forward slashes, e.g. "DEV/overview.md".
	Path string

	// PageID is the Confluence page identifier from frontmatter (may be empty for new files).
	PageID string

	// Title is the Confluence page title from frontmatter.
	Title string

	// SpaceKey is the Confluence space key from frontmatter.
	SpaceKey string

	// Labels are Confluence page labels, normalised (lowercase, trimmed, deduplicated).
	Labels []string

	// Content holds the searchable text: full body for page docs, heading-section text for
	// section docs, and raw code content for code docs.
	Content string

	// HeadingPath is the ordered heading hierarchy from root to the section/code block,
	// e.g. ["# Overview", "## Auth Flow", "### Token Refresh"].
	HeadingPath []string

	// HeadingText is the innermost heading label (for section/code docs).
	HeadingText string

	// HeadingLevel is the Markdown heading level (1–6) of HeadingText; 0 for page docs.
	HeadingLevel int

	// Language is the fenced code block info string (e.g. "go", "sql"); empty for non-code docs.
	Language string

	// Line is the 1-based start line in the source file (0 for page docs).
	Line int

	// ModTime is the last modification time of the source file.
	ModTime time.Time
}

// SearchOptions controls a full-text search query.
type SearchOptions struct {
	// Query is the full-text search string. May be empty when listing labels/spaces.
	Query string

	// SpaceKey restricts results to a single Confluence space key. Empty = all spaces.
	SpaceKey string

	// Labels restricts results to pages that carry ALL of the given labels.
	Labels []string

	// HeadingFilter restricts section/code results to those whose HeadingText contains
	// this substring (case-insensitive).
	HeadingFilter string

	// Types restricts results to the given document types. nil = all types.
	Types []string

	// Limit is the maximum number of results to return. 0 = use a sensible default.
	Limit int
}

// SearchResult is a single match returned by Store.Search.
type SearchResult struct {
	// Document is the full indexed document.
	Document Document

	// Score is a backend-specific relevance score (higher = more relevant).
	Score float64

	// Snippet is a short contextual excerpt with the matched terms highlighted.
	Snippet string
}
