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
	ID string `json:"id,omitempty"`

	// Type is DocTypePage, DocTypeSection, or DocTypeCode.
	Type string `json:"type,omitempty"`

	// Path is the repository-relative path with forward slashes, e.g. "DEV/overview.md".
	Path string `json:"path,omitempty"`

	// PageID is the Confluence page identifier from frontmatter (may be empty for new files).
	PageID string `json:"page_id,omitempty"`

	// Title is the Confluence page title from frontmatter.
	Title string `json:"title,omitempty"`

	// SpaceKey is the Confluence space key from frontmatter.
	SpaceKey string `json:"space_key,omitempty"`

	// Labels are Confluence page labels, normalised (lowercase, trimmed, deduplicated).
	Labels []string `json:"labels,omitempty"`

	// Content holds the searchable text: full body for page docs, heading-section text for
	// section docs, and raw code content for code docs.
	Content string `json:"content,omitempty"`

	// HeadingPath is the ordered heading hierarchy from root to the section/code block,
	// e.g. ["# Overview", "## Auth Flow", "### Token Refresh"].
	HeadingPath []string `json:"heading_path,omitempty"`

	// HeadingText is the innermost heading label (for section/code docs).
	HeadingText string `json:"heading_text,omitempty"`

	// HeadingLevel is the Markdown heading level (1–6) of HeadingText; 0 for page docs.
	HeadingLevel int `json:"heading_level,omitempty"`

	// Language is the fenced code block info string (e.g. "go", "sql"); empty for non-code docs.
	Language string `json:"language,omitempty"`

	// Line is the 1-based start line in the source file (0 for page docs).
	Line int `json:"line,omitempty"`

	// ModTime is the last modification time of the source file.
	ModTime *time.Time `json:"mod_time,omitempty"`

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
}

// SearchResult is a single match returned by Store.Search.
type SearchResult struct {
	// Document is the full indexed document.
	Document Document `json:"document"`

	// Score is a backend-specific relevance score (higher = more relevant).
	Score float64 `json:"score,omitempty"`

	// Snippet is a short contextual excerpt with the matched terms highlighted.
	Snippet string `json:"snippet,omitempty"`
}
