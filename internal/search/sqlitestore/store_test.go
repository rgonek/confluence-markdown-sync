package sqlitestore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/search"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleDocs() []search.Document {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	return []search.Document{
		{
			ID:       "page:DEV/overview.md",
			Type:     search.DocTypePage,
			Path:     "DEV/overview.md",
			PageID:   "123456",
			Title:    "Security Overview",
			SpaceKey: "DEV",
			Labels:   []string{"architecture", "security"},
			Content:  "This page covers the security architecture and OAuth2 flows.",
			ModTime:  &now,
		},
		{
			ID:           "section:DEV/overview.md:5",
			Type:         search.DocTypeSection,
			Path:         "DEV/overview.md",
			PageID:       "123456",
			Title:        "Security Overview",
			SpaceKey:     "DEV",
			Labels:       []string{"architecture", "security"},
			Content:      "OAuth2 flows use PKCE to prevent interception attacks.",
			HeadingText:  "OAuth2 Flow",
			HeadingLevel: 2,
			HeadingPath:  []string{"# Security Overview", "## OAuth2 Flow"},
			Line:         5,
			ModTime:      &now,
		},
		{
			ID:           "code:DEV/overview.md:12",
			Type:         search.DocTypeCode,
			Path:         "DEV/overview.md",
			PageID:       "123456",
			Title:        "Security Overview",
			SpaceKey:     "DEV",
			Labels:       []string{"architecture", "security"},
			Content:      "func refreshToken(token string) error { return nil }",
			HeadingText:  "Token Refresh",
			HeadingLevel: 3,
			HeadingPath:  []string{"# Security Overview", "## OAuth2 Flow", "### Token Refresh"},
			Language:     "go",
			Line:         12,
			ModTime:      &now,
		},
		{
			ID:       "page:OPS/deploy.md",
			Type:     search.DocTypePage,
			Path:     "OPS/deploy.md",
			PageID:   "654321",
			Title:    "Deployment Guide",
			SpaceKey: "OPS",
			Labels:   []string{"ops", "deployment"},
			Content:  "How to deploy the application to production.",
			ModTime:  &now,
		},
	}
}

func TestStore_IndexAndSearch(t *testing.T) {
	s := newTestStore(t)
	docs := sampleDocs()

	if err := s.Index(docs); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{Query: "OAuth2"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for 'OAuth2'")
	}
}

func TestStore_SearchStripsSpecialCharacters(t *testing.T) {
	s := newTestStore(t)
	docs := sampleDocs()

	docs = append(docs, search.Document{
		ID:       "page:OPS/events.md",
		Type:     search.DocTypePage,
		Path:     "OPS/events.md",
		PageID:   "777777",
		Title:    "Events API",
		SpaceKey: "OPS",
		Content:  "POST /v2/events endpoint details and payloads.",
	})

	if err := s.Index(docs); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{Query: "POST /v2/events"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for query with special characters")
	}
}

func TestNormalizeFTSQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "slashes",
			input: "POST /v2/events",
			want:  "POST v2 events",
		},
		{
			name:  "hyphen",
			input: "Onboarding to On-Call guide",
			want:  "Onboarding to On Call guide",
		},
		{
			name:  "punctuation",
			input: "auth:token (refresh)",
			want:  "auth token refresh",
		},
		{
			name:  "dots and quotes",
			input: `"v2.0" endpoint`,
			want:  "v2 0 endpoint",
		},
		{
			name:  "underscore",
			input: "api_events_v2",
			want:  "api events v2",
		},
		{
			name:    "only symbols",
			input:   "/-()",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeFTSQuery(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeFTSQuery: %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeFTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStore_DeleteByPath(t *testing.T) {
	s := newTestStore(t)
	docs := sampleDocs()

	if err := s.Index(docs); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if err := s.DeleteByPath("DEV/overview.md"); err != nil {
		t.Fatalf("DeleteByPath: %v", err)
	}

	results, err := s.Search(search.SearchOptions{Query: "OAuth2"})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, r := range results {
		if r.Document.Path == "DEV/overview.md" {
			t.Errorf("expected DEV/overview.md to be deleted, but it still appears in results")
		}
	}
}

func TestStore_FilterBySpace(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{SpaceKey: "OPS"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for OPS space")
	}
	for _, r := range results {
		if r.Document.SpaceKey != "OPS" {
			t.Errorf("expected SpaceKey=OPS, got %q", r.Document.SpaceKey)
		}
	}
}

func TestStore_FilterByLabel(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{Labels: []string{"deployment"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for label 'deployment'")
	}
	for _, r := range results {
		found := false
		for _, l := range r.Document.Labels {
			if l == "deployment" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("result %s does not have label 'deployment': %v", r.Document.ID, r.Document.Labels)
		}
	}
}

func TestStore_ListLabels(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	labels, err := s.ListLabels()
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}

	expected := map[string]bool{
		"architecture": true,
		"security":     true,
		"ops":          true,
		"deployment":   true,
	}
	for _, l := range labels {
		delete(expected, l)
	}
	if len(expected) > 0 {
		remaining := make([]string, 0, len(expected))
		for k := range expected {
			remaining = append(remaining, k)
		}
		t.Errorf("missing labels: %v (got %v)", remaining, labels)
	}
}

func TestStore_ListSpaces(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	spaces, err := s.ListSpaces()
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}

	found := map[string]bool{}
	for _, sp := range spaces {
		found[sp] = true
	}
	if !found["DEV"] {
		t.Errorf("expected space DEV in list, got %v", spaces)
	}
	if !found["OPS"] {
		t.Errorf("expected space OPS in list, got %v", spaces)
	}
}

func TestStore_UpdateMetaAndLastIndexedAt(t *testing.T) {
	s := newTestStore(t)

	// Before any update, LastIndexedAt returns zero.
	zero, err := s.LastIndexedAt()
	if err != nil {
		t.Fatalf("LastIndexedAt (pre-update): %v", err)
	}
	if !zero.IsZero() {
		t.Errorf("expected zero time before UpdateMeta, got %v", zero)
	}

	before := time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateMeta(); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	ts, err := s.LastIndexedAt()
	if err != nil {
		t.Fatalf("LastIndexedAt: %v", err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("LastIndexedAt %v out of expected range [%v, %v]", ts, before, after)
	}
}

func TestStore_Upsert(t *testing.T) {
	s := newTestStore(t)

	doc := search.Document{
		ID:       "page:DEV/test.md",
		Type:     search.DocTypePage,
		Path:     "DEV/test.md",
		SpaceKey: "DEV",
		Title:    "Original Title",
		Content:  "original content",
	}

	if err := s.Index([]search.Document{doc}); err != nil {
		t.Fatalf("first Index: %v", err)
	}

	doc.Title = "Updated Title"
	doc.Content = "updated content"
	if err := s.Index([]search.Document{doc}); err != nil {
		t.Fatalf("second Index (upsert): %v", err)
	}

	results, err := s.Search(search.SearchOptions{Query: "updated"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Document.ID == "page:DEV/test.md" {
			found = true
			if r.Document.Title != "Updated Title" {
				t.Errorf("expected 'Updated Title', got %q", r.Document.Title)
			}
		}
	}
	if !found {
		t.Error("updated document not found in search results")
	}
}

func TestStore_Limit(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results with Limit=2, got %d", len(results))
	}
}

func TestStore_TypeFilter(t *testing.T) {
	s := newTestStore(t)
	if err := s.Index(sampleDocs()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(search.SearchOptions{Types: []string{search.DocTypeCode}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Document.Type != search.DocTypeCode {
			t.Errorf("expected type %q, got %q", search.DocTypeCode, r.Document.Type)
		}
	}
}

func TestStore_OpenCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "subdir")
	dbPath := filepath.Join(dir, "search.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open with nested dir: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected db file to exist at %s: %v", dbPath, err)
	}
}

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

	// created_at between 2024-02-01 and 2024-10-01 => bob-doc (2024-03-20) and charlie-doc (2024-09-10)
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
