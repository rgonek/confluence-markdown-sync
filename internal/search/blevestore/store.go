package blevestore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bleve "github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"

	search "github.com/rgonek/confluence-markdown-sync/internal/search"
)

// Compile-time interface check.
var _ search.Store = (*Store)(nil)

const (
	// indexSubDir is the path under rootDir where the Bleve index is stored.
	indexSubDir = ".confluence-search-index/bleve"

	// defaultSearchLimit is the result limit used when SearchOptions.Limit == 0.
	defaultSearchLimit = 50

	// facetSize is the maximum number of facet values returned for ListLabels/ListSpaces.
	facetSize = 10000
)

// Store is a search.Store backed by Bleve (scorch engine).
type Store struct {
	index bleve.Index
}

// Open opens (or creates) the Bleve index rooted at rootDir.
// The index is stored at <rootDir>/.confluence-search-index/bleve/.
func Open(rootDir string) (*Store, error) {
	indexPath := filepath.Join(rootDir, indexSubDir)

	var idx bleve.Index
	var err error

	if _, statErr := os.Stat(indexPath); os.IsNotExist(statErr) {
		m := NewMapping()
		idx, err = bleve.New(indexPath, m)
	} else {
		idx, err = bleve.Open(indexPath)
	}
	if err != nil {
		return nil, fmt.Errorf("blevestore.Open %q: %w", indexPath, err)
	}

	return &Store{index: idx}, nil
}

// ---------------------------------------------------------------------------
// search.Store implementation
// ---------------------------------------------------------------------------

// Index upserts a batch of documents.
// The caller is expected to call DeleteByPath before re-indexing a path.
func (s *Store) Index(docs []search.Document) error {
	b := s.index.NewBatch()
	for _, d := range docs {
		if err := b.Index(d.ID, docToMap(d)); err != nil {
			return fmt.Errorf("blevestore.Index %q: %w", d.ID, err)
		}
	}
	if err := s.index.Batch(b); err != nil {
		return fmt.Errorf("blevestore.Index batch: %w", err)
	}
	return nil
}

// DeleteByPath removes all indexed documents whose path field equals relPath.
func (s *Store) DeleteByPath(relPath string) error {
	tq := query.NewTermQuery(relPath)
	tq.SetField("path")

	req := bleve.NewSearchRequestOptions(tq, 10000, 0, false)
	req.Fields = []string{}

	res, err := s.index.Search(req)
	if err != nil {
		return fmt.Errorf("blevestore.DeleteByPath search %q: %w", relPath, err)
	}

	b := s.index.NewBatch()
	for _, hit := range res.Hits {
		b.Delete(hit.ID)
	}
	if b.Size() == 0 {
		return nil
	}
	if err := s.index.Batch(b); err != nil {
		return fmt.Errorf("blevestore.DeleteByPath batch delete %q: %w", relPath, err)
	}
	return nil
}

// Search executes a full-text query against the index.
func (s *Store) Search(opts search.SearchOptions) ([]search.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	q := buildQuery(opts)

	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = allDocFields
	req.Highlight = bleve.NewHighlight()
	req.Highlight.AddField("content")
	req.Highlight.AddField("title")
	req.Highlight.AddField("heading_text")

	bleveRes, err := s.index.Search(req)
	if err != nil {
		return nil, fmt.Errorf("blevestore.Search: %w", err)
	}

	results := make([]search.SearchResult, 0, len(bleveRes.Hits))
	for _, hit := range bleveRes.Hits {
		doc, err := mapToDoc(hit.ID, hit.Fields)
		if err != nil {
			continue
		}
		snippet := extractSnippet(hit.Fragments)
		results = append(results, search.SearchResult{
			Document: doc,
			Score:    hit.Score,
			Snippet:  snippet,
		})
	}
	return results, nil
}

// ListLabels returns all distinct label values present in the index, sorted.
func (s *Store) ListLabels() ([]string, error) {
	return s.listFacetTerms("labels")
}

// ListSpaces returns all distinct space key values present in the index, sorted.
func (s *Store) ListSpaces() ([]string, error) {
	return s.listFacetTerms("space_key")
}

// ListPathsBySpace returns all distinct indexed source paths for spaceKey, sorted.
func (s *Store) ListPathsBySpace(spaceKey string) ([]string, error) {
	const pageSize = 1000

	var q query.Query = query.NewMatchAllQuery()
	if spaceKey != "" {
		spaceQuery := query.NewTermQuery(spaceKey)
		spaceQuery.SetField("space_key")
		q = query.NewConjunctionQuery([]query.Query{q, spaceQuery})
	}

	seen := map[string]struct{}{}
	for from := 0; ; from += pageSize {
		req := bleve.NewSearchRequestOptions(q, pageSize, from, false)
		req.Fields = []string{"path"}

		res, err := s.index.Search(req)
		if err != nil {
			return nil, fmt.Errorf("blevestore.ListPathsBySpace(%q): %w", spaceKey, err)
		}

		for _, hit := range res.Hits {
			if path := toString(hit.Fields["path"]); path != "" {
				seen[path] = struct{}{}
			}
		}

		if len(res.Hits) < pageSize {
			break
		}
	}

	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// metaKey is the internal key used to persist the last-indexed-at timestamp
// via Bleve's internal key-value store (independent of the document mapping).
var metaKey = []byte("confluence-sync:last-indexed-at")

// UpdateMeta records the current UTC timestamp as the last-indexed-at time.
func (s *Store) UpdateMeta() error {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.index.SetInternal(metaKey, []byte(ts)); err != nil {
		return fmt.Errorf("blevestore.UpdateMeta: %w", err)
	}
	return nil
}

// LastIndexedAt returns the time recorded by the most recent UpdateMeta call.
// Returns the zero time.Time with nil error if no meta has been recorded yet.
func (s *Store) LastIndexedAt() (time.Time, error) {
	raw, err := s.index.GetInternal(metaKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("blevestore.LastIndexedAt: %w", err)
	}
	if len(raw) == 0 {
		return time.Time{}, nil
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("blevestore.LastIndexedAt parse %q: %w", raw, err)
	}
	return ts, nil
}

// Close releases resources held by the store.
func (s *Store) Close() error {
	return s.index.Close()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// docToMap converts a search.Document to a flat map for Bleve indexing.
func docToMap(d search.Document) map[string]interface{} {
	m := map[string]interface{}{
		"type":          d.Type,
		"path":          d.Path,
		"page_id":       d.PageID,
		"title":         d.Title,
		"space_key":     d.SpaceKey,
		"content":       d.Content,
		"heading_text":  d.HeadingText,
		"heading_level": float64(d.HeadingLevel),
		"language":      d.Language,
		"line":          float64(d.Line),
		"mod_time": func() interface{} {
			if d.ModTime != nil {
				return *d.ModTime
			}
			return nil
		}(),
		"heading_path_text": strings.Join(d.HeadingPath, " / "),
		"created_by":        d.CreatedBy,
		"updated_by":        d.UpdatedBy,
		"created_at":        parseDateString(d.CreatedAt),
		"updated_at":        parseDateString(d.UpdatedAt),
	}

	// Index labels as a multi-valued field so Bleve creates one term per label.
	if len(d.Labels) > 0 {
		labels := make([]interface{}, len(d.Labels))
		for i, l := range d.Labels {
			labels[i] = l
		}
		m["labels"] = labels
	}

	return m
}

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

// mapToDoc reconstructs a search.Document from a Bleve hit's Fields map.
func mapToDoc(id string, fields map[string]interface{}) (search.Document, error) {
	d := search.Document{ID: id}

	if v, ok := fields["type"]; ok {
		d.Type = toString(v)
	}
	if v, ok := fields["path"]; ok {
		d.Path = toString(v)
	}
	if v, ok := fields["page_id"]; ok {
		d.PageID = toString(v)
	}
	if v, ok := fields["title"]; ok {
		d.Title = toString(v)
	}
	if v, ok := fields["space_key"]; ok {
		d.SpaceKey = toString(v)
	}
	if v, ok := fields["content"]; ok {
		d.Content = toString(v)
	}
	if v, ok := fields["heading_text"]; ok {
		d.HeadingText = toString(v)
	}
	if v, ok := fields["language"]; ok {
		d.Language = toString(v)
	}
	if v, ok := fields["heading_level"]; ok {
		d.HeadingLevel = toInt(v)
	}
	if v, ok := fields["line"]; ok {
		d.Line = toInt(v)
	}
	if v, ok := fields["mod_time"]; ok {
		if t, err := parseTimeField(v); err == nil {
			d.ModTime = &t
		}
	}
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
	if v, ok := fields["labels"]; ok {
		d.Labels = toStringSlice(v)
	}
	if v, ok := fields["heading_path_text"]; ok {
		joined := toString(v)
		if joined != "" {
			d.HeadingPath = strings.Split(joined, " / ")
		}
	}

	return d, nil
}

// buildQuery constructs a Bleve query from SearchOptions.
func buildQuery(opts search.SearchOptions) query.Query {
	var musts []query.Query

	// Full-text part — disjunction across content/heading_text/title with boosts.
	if opts.Query != "" {
		var textQueries []query.Query

		addMatch := func(field string, boost float64) {
			mq := query.NewMatchQuery(opts.Query)
			mq.SetField(field)
			mq.SetBoost(boost)
			textQueries = append(textQueries, mq)
		}

		addMatch("content", 2.0)
		addMatch("heading_text", 1.5)
		addMatch("title", 1.0)

		dis := query.NewDisjunctionQuery(textQueries)
		dis.SetMin(1)
		musts = append(musts, dis)
	}

	// SpaceKey filter.
	if opts.SpaceKey != "" {
		tq := query.NewTermQuery(opts.SpaceKey)
		tq.SetField("space_key")
		musts = append(musts, tq)
	}

	// Labels filter — every requested label must appear.
	for _, label := range opts.Labels {
		tq := query.NewTermQuery(label)
		tq.SetField("labels")
		musts = append(musts, tq)
	}

	// HeadingFilter.
	if opts.HeadingFilter != "" {
		mq := query.NewMatchQuery(opts.HeadingFilter)
		mq.SetField("heading_text")
		musts = append(musts, mq)
	}

	// CreatedBy / UpdatedBy exact-match filters.
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

	// Date range filters — parse RFC3339; skip malformed values gracefully.
	if opts.CreatedAfter != "" || opts.CreatedBefore != "" {
		var start, end time.Time
		var hasStart, hasEnd bool
		if t, err := time.Parse(time.RFC3339, opts.CreatedAfter); err == nil {
			start, hasStart = t, true
		}
		if t, err := time.Parse(time.RFC3339, opts.CreatedBefore); err == nil {
			end, hasEnd = t, true
		}
		if hasStart || hasEnd {
			incl := true
			var startPtr, endPtr *time.Time
			if hasStart {
				startPtr = &start
			}
			if hasEnd {
				endPtr = &end
			}
			drq := buildDateRangeQuery("created_at", startPtr, endPtr, incl)
			musts = append(musts, drq)
		}
	}
	if opts.UpdatedAfter != "" || opts.UpdatedBefore != "" {
		var start, end time.Time
		var hasStart, hasEnd bool
		if t, err := time.Parse(time.RFC3339, opts.UpdatedAfter); err == nil {
			start, hasStart = t, true
		}
		if t, err := time.Parse(time.RFC3339, opts.UpdatedBefore); err == nil {
			end, hasEnd = t, true
		}
		if hasStart || hasEnd {
			incl := true
			var startPtr, endPtr *time.Time
			if hasStart {
				startPtr = &start
			}
			if hasEnd {
				endPtr = &end
			}
			drq := buildDateRangeQuery("updated_at", startPtr, endPtr, incl)
			musts = append(musts, drq)
		}
	}

	// Types filter.
	if len(opts.Types) > 0 {
		typeQueries := make([]query.Query, len(opts.Types))
		for i, t := range opts.Types {
			tq := query.NewTermQuery(t)
			tq.SetField("type")
			typeQueries[i] = tq
		}
		if len(typeQueries) == 1 {
			musts = append(musts, typeQueries[0])
		} else {
			dis := query.NewDisjunctionQuery(typeQueries)
			dis.SetMin(1)
			musts = append(musts, dis)
		}
	}

	switch len(musts) {
	case 0:
		return query.NewMatchAllQuery()
	case 1:
		return musts[0]
	default:
		return query.NewConjunctionQuery(musts)
	}
}

// buildDateRangeQuery builds a Bleve DateRangeQuery for the given field.
// start and end are optional (*time.Time); nil means "unbounded" (uses the
// Bleve-compatible min/max sentinel values). inclusive applies to both ends.
func buildDateRangeQuery(field string, start, end *time.Time, inclusive bool) query.Query {
	s := query.MinRFC3339CompatibleTime
	e := query.MaxRFC3339CompatibleTime
	if start != nil {
		s = *start
	}
	if end != nil {
		e = *end
	}
	incl := inclusive
	drq := query.NewDateRangeInclusiveQuery(s, e, &incl, &incl)
	drq.SetField(field)
	return drq
}

// listFacetTerms runs a match-all query with a facet on field and returns the
// distinct term values, sorted alphabetically.
func (s *Store) listFacetTerms(field string) ([]string, error) {
	q := query.NewMatchAllQuery()
	req := bleve.NewSearchRequestOptions(q, 0, 0, false)
	req.AddFacet(field, bleve.NewFacetRequest(field, facetSize))

	res, err := s.index.Search(req)
	if err != nil {
		return nil, fmt.Errorf("blevestore.listFacetTerms(%q): %w", field, err)
	}

	facet, ok := res.Facets[field]
	if !ok || facet == nil || facet.Terms == nil {
		return []string{}, nil
	}

	terms := facet.Terms.Terms()
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if t.Term != "" {
			out = append(out, t.Term)
		}
	}
	sort.Strings(out)
	return out, nil
}

// extractSnippet picks the first available fragment from a hit.
func extractSnippet(fragments map[string][]string) string {
	for _, field := range []string{"content", "title", "heading_text"} {
		if frags, ok := fragments[field]; ok && len(frags) > 0 {
			return frags[0]
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Type conversion helpers
// ---------------------------------------------------------------------------

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func toStringSlice(v interface{}) []string {
	switch val := v.(type) {
	case string:
		if val == "" {
			return nil
		}
		return []string{val}
	case []interface{}:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return val
	default:
		return nil
	}
}

// parseTimeField parses a time value from a Bleve stored field.
// Bleve stores datetime fields as RFC3339 strings.
func parseTimeField(v interface{}) (time.Time, error) {
	switch val := v.(type) {
	case time.Time:
		return val, nil
	case string:
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999Z",
		} {
			if t, err := time.Parse(layout, val); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("cannot parse time %q", val)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return time.Time{}, fmt.Errorf("cannot marshal time value: %w", err)
		}
		var t time.Time
		if err := json.Unmarshal(b, &t); err != nil {
			return time.Time{}, fmt.Errorf("cannot unmarshal time value: %w", err)
		}
		return t, nil
	}
}
