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
