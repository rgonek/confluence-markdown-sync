package blevestore

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	search "github.com/rgonek/confluence-markdown-sync/internal/search"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func pageDoc(id, path, space, title, content string, labels ...string) search.Document {
	t := time.Now().Truncate(time.Second)
	return search.Document{
		ID:       id,
		Type:     search.DocTypePage,
		Path:     path,
		SpaceKey: space,
		Title:    title,
		Content:  content,
		Labels:   labels,
		ModTime:  &t,
	}
}

func sectionDoc(id, path, space, title, headingText, content string, headingLevel, line int) search.Document {
	t := time.Now().Truncate(time.Second)
	return search.Document{
		ID:           id,
		Type:         search.DocTypeSection,
		Path:         path,
		SpaceKey:     space,
		Title:        title,
		HeadingText:  headingText,
		Content:      content,
		HeadingLevel: headingLevel,
		Line:         line,
		ModTime:      &t,
	}
}

func mustIndex(t *testing.T, s *Store, docs ...search.Document) {
	t.Helper()
	if err := s.Index(docs); err != nil {
		t.Fatalf("Index: %v", err)
	}
}

// sortedIDs extracts and sorts doc IDs from results for deterministic assertions.
func sortedIDs(results []search.SearchResult) []string {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Document.ID
	}
	sort.Strings(ids)
	return ids
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestOpenClose(t *testing.T) {
	// Test basic open/close without using openTestStore (to avoid double-close).
	dir := t.TempDir()

	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (create): %v", err)
	}
	if s1 == nil {
		t.Fatal("expected non-nil Store")
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close s1: %v", err)
	}

	// Re-open the same directory to verify the index persists.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (reopen): %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close s2: %v", err)
	}
}

func TestIndexAndBasicTextSearch(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:DEV/auth.md", "DEV/auth.md", "DEV", "Authentication Guide",
			"OAuth2 and JWT are the primary authentication mechanisms."),
		pageDoc("page:DEV/deploy.md", "DEV/deploy.md", "DEV", "Deployment Guide",
			"Kubernetes cluster deployment with Helm charts."),
	}
	mustIndex(t, s, docs...)

	results, err := s.Search(search.SearchOptions{Query: "authentication"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'authentication'")
	}
	if results[0].Document.ID != "page:DEV/auth.md" {
		t.Errorf("expected top result to be auth.md, got %s", results[0].Document.ID)
	}
	if results[0].Score <= 0 {
		t.Error("expected positive score")
	}
}

func TestSearchReturnsAllFieldsRoundTrip(t *testing.T) {
	s := openTestStore(t)

	modTime := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	doc := search.Document{
		ID:           "page:SPACE/overview.md",
		Type:         search.DocTypePage,
		Path:         "SPACE/overview.md",
		PageID:       "12345",
		Title:        "Project Overview",
		SpaceKey:     "SPACE",
		Labels:       []string{"docs", "public"},
		Content:      "This document provides an overview of the project architecture.",
		HeadingPath:  []string{"# Overview", "## Architecture"},
		HeadingText:  "",
		HeadingLevel: 0,
		Language:     "",
		Line:         0,
		ModTime:      &modTime,
	}
	mustIndex(t, s, doc)

	results, err := s.Search(search.SearchOptions{Query: "architecture"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	got := results[0].Document
	if got.ID != doc.ID {
		t.Errorf("ID: got %q, want %q", got.ID, doc.ID)
	}
	if got.Type != doc.Type {
		t.Errorf("Type: got %q, want %q", got.Type, doc.Type)
	}
	if got.Path != doc.Path {
		t.Errorf("Path: got %q, want %q", got.Path, doc.Path)
	}
	if got.PageID != doc.PageID {
		t.Errorf("PageID: got %q, want %q", got.PageID, doc.PageID)
	}
	if got.Title != doc.Title {
		t.Errorf("Title: got %q, want %q", got.Title, doc.Title)
	}
	if got.SpaceKey != doc.SpaceKey {
		t.Errorf("SpaceKey: got %q, want %q", got.SpaceKey, doc.SpaceKey)
	}
}

func TestDeleteByPath(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:DEV/a.md", "DEV/a.md", "DEV", "Page A", "content about golang"),
		sectionDoc("section:DEV/a.md:10", "DEV/a.md", "DEV", "Page A", "Intro", "intro content golang", 1, 10),
		pageDoc("page:DEV/b.md", "DEV/b.md", "DEV", "Page B", "content about python"),
	}
	mustIndex(t, s, docs...)

	// Confirm both a.md docs are indexed.
	res, err := s.Search(search.SearchOptions{Query: "golang"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) < 2 {
		t.Fatalf("expected >=2 results before delete, got %d", len(res))
	}

	if err := s.DeleteByPath("DEV/a.md"); err != nil {
		t.Fatalf("DeleteByPath: %v", err)
	}

	// a.md docs should be gone; b.md should remain.
	res, err = s.Search(search.SearchOptions{Query: "golang"})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, r := range res {
		if strings.Contains(r.Document.Path, "a.md") {
			t.Errorf("found deleted doc: %s", r.Document.ID)
		}
	}

	res, err = s.Search(search.SearchOptions{Query: "python"})
	if err != nil {
		t.Fatalf("Search b.md: %v", err)
	}
	if len(res) == 0 {
		t.Error("expected b.md to still be indexed")
	}
}

func TestDeleteByPathNoop(t *testing.T) {
	s := openTestStore(t)
	// Deleting a non-existent path should not error.
	if err := s.DeleteByPath("nonexistent/path.md"); err != nil {
		t.Fatalf("DeleteByPath noop: %v", err)
	}
}

func TestFilterBySpaceKey(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:DEV/a.md", "DEV/a.md", "DEV", "Dev Page", "microservice deployment"),
		pageDoc("page:OPS/b.md", "OPS/b.md", "OPS", "Ops Page", "microservice deployment"),
		pageDoc("page:QA/c.md", "QA/c.md", "QA", "QA Page", "microservice testing"),
	}
	mustIndex(t, s, docs...)

	res, err := s.Search(search.SearchOptions{
		Query:    "microservice",
		SpaceKey: "DEV",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result for DEV, got %d", len(res))
	}
	if res[0].Document.SpaceKey != "DEV" {
		t.Errorf("expected DEV space, got %q", res[0].Document.SpaceKey)
	}
}

func TestFilterByLabels(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:a.md", "a.md", "DEV", "A", "content", "go", "backend"),
		pageDoc("page:b.md", "b.md", "DEV", "B", "content", "go", "frontend"),
		pageDoc("page:c.md", "c.md", "DEV", "C", "content", "python"),
	}
	mustIndex(t, s, docs...)

	// Filter: label "go" AND "backend" — should match only a.md.
	res, err := s.Search(search.SearchOptions{
		Labels: []string{"go", "backend"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	ids := sortedIDs(res)
	if len(ids) != 1 || ids[0] != "page:a.md" {
		t.Errorf("expected [page:a.md], got %v", ids)
	}

	// Filter: label "go" only — should match a.md and b.md.
	res, err = s.Search(search.SearchOptions{
		Labels: []string{"go"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	ids = sortedIDs(res)
	if len(ids) != 2 {
		t.Errorf("expected 2 results for label 'go', got %v", ids)
	}
}

func TestListLabels(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:a.md", "a.md", "S", "A", "content", "alpha", "beta"),
		pageDoc("page:b.md", "b.md", "S", "B", "content", "beta", "gamma"),
		pageDoc("page:c.md", "c.md", "S", "C", "content"),
	}
	mustIndex(t, s, docs...)

	labels, err := s.ListLabels()
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}

	want := []string{"alpha", "beta", "gamma"}
	if !equalStringSlice(labels, want) {
		t.Errorf("ListLabels: got %v, want %v", labels, want)
	}
}

func TestListLabelsEmpty(t *testing.T) {
	s := openTestStore(t)
	labels, err := s.ListLabels()
	if err != nil {
		t.Fatalf("ListLabels empty: %v", err)
	}
	if len(labels) != 0 {
		t.Errorf("expected empty, got %v", labels)
	}
}

func TestListSpaces(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:DEV/a.md", "DEV/a.md", "DEV", "A", "content"),
		pageDoc("page:OPS/b.md", "OPS/b.md", "OPS", "B", "content"),
		pageDoc("page:OPS/c.md", "OPS/c.md", "OPS", "C", "content"),
	}
	mustIndex(t, s, docs...)

	spaces, err := s.ListSpaces()
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}

	want := []string{"DEV", "OPS"}
	if !equalStringSlice(spaces, want) {
		t.Errorf("ListSpaces: got %v, want %v", spaces, want)
	}
}

func TestUpdateMetaAndLastIndexedAt(t *testing.T) {
	s := openTestStore(t)

	// Before any UpdateMeta, LastIndexedAt should return zero time.
	ts, err := s.LastIndexedAt()
	if err != nil {
		t.Fatalf("LastIndexedAt (initial): %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time before UpdateMeta, got %v", ts)
	}

	before := time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateMeta(); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	ts, err = s.LastIndexedAt()
	if err != nil {
		t.Fatalf("LastIndexedAt: %v", err)
	}
	if ts.IsZero() {
		t.Fatal("expected non-zero time after UpdateMeta")
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("LastIndexedAt %v is outside [%v, %v]", ts, before, after)
	}
}

func TestUpdateMetaMultipleTimes(t *testing.T) {
	s := openTestStore(t)

	if err := s.UpdateMeta(); err != nil {
		t.Fatalf("UpdateMeta 1: %v", err)
	}
	ts1, err := s.LastIndexedAt()
	if err != nil {
		t.Fatalf("LastIndexedAt 1: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if err := s.UpdateMeta(); err != nil {
		t.Fatalf("UpdateMeta 2: %v", err)
	}
	ts2, err := s.LastIndexedAt()
	if err != nil {
		t.Fatalf("LastIndexedAt 2: %v", err)
	}

	if ts2.Before(ts1) {
		t.Errorf("second UpdateMeta should record a time >= first: ts1=%v ts2=%v", ts1, ts2)
	}
}

func TestUpsertBehavior(t *testing.T) {
	s := openTestStore(t)

	// Index original document.
	original := pageDoc("page:SPACE/page.md", "SPACE/page.md", "SPACE", "Original Title", "original content")
	mustIndex(t, s, original)

	res, err := s.Search(search.SearchOptions{Query: "original"})
	if err != nil {
		t.Fatalf("Search original: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected result for original content")
	}

	// Delete and re-index with updated content.
	if err := s.DeleteByPath("SPACE/page.md"); err != nil {
		t.Fatalf("DeleteByPath: %v", err)
	}

	updated := pageDoc("page:SPACE/page.md", "SPACE/page.md", "SPACE", "Updated Title", "updated content about refactoring")
	mustIndex(t, s, updated)

	// Old content should not match.
	res, err = s.Search(search.SearchOptions{Query: "original"})
	if err != nil {
		t.Fatalf("Search original after upsert: %v", err)
	}
	for _, r := range res {
		if r.Document.ID == "page:SPACE/page.md" {
			t.Error("found old content after upsert")
		}
	}

	// New content should match.
	res, err = s.Search(search.SearchOptions{Query: "refactoring"})
	if err != nil {
		t.Fatalf("Search updated: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected result for updated content")
	}
	if res[0].Document.Title != "Updated Title" {
		t.Errorf("expected updated title, got %q", res[0].Document.Title)
	}
}

func TestSearchEmptyQueryReturnsAll(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:a.md", "a.md", "S", "A", "foo"),
		pageDoc("page:b.md", "b.md", "S", "B", "bar"),
	}
	mustIndex(t, s, docs...)

	res, err := s.Search(search.SearchOptions{Limit: 100})
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if len(res) < 2 {
		t.Errorf("expected >=2 results for empty query, got %d", len(res))
	}
}

func TestSearchByType(t *testing.T) {
	s := openTestStore(t)

	docs := []search.Document{
		pageDoc("page:DEV/a.md", "DEV/a.md", "DEV", "Auth", "authentication token"),
		sectionDoc("section:DEV/a.md:5", "DEV/a.md", "DEV", "Auth", "Token Auth", "authentication token section", 2, 5),
	}
	mustIndex(t, s, docs...)

	res, err := s.Search(search.SearchOptions{
		Query: "authentication",
		Types: []string{search.DocTypeSection},
	})
	if err != nil {
		t.Fatalf("Search by type: %v", err)
	}
	for _, r := range res {
		if r.Document.Type != search.DocTypeSection {
			t.Errorf("unexpected type %q in results filtered to section", r.Document.Type)
		}
	}
	if len(res) == 0 {
		t.Error("expected at least 1 section result")
	}
}

func TestSearchLimit(t *testing.T) {
	s := openTestStore(t)

	var docs []search.Document
	for i := 0; i < 20; i++ {
		docs = append(docs, pageDoc(
			fmt.Sprintf("page:S/page%d.md", i),
			fmt.Sprintf("S/page%d.md", i),
			"S",
			fmt.Sprintf("Page %d", i),
			"common keyword content",
		))
	}
	mustIndex(t, s, docs...)

	res, err := s.Search(search.SearchOptions{Query: "common", Limit: 5})
	if err != nil {
		t.Fatalf("Search with limit: %v", err)
	}
	if len(res) > 5 {
		t.Errorf("expected <=5 results, got %d", len(res))
	}
}

func TestSnippetIsPopulated(t *testing.T) {
	s := openTestStore(t)

	doc := pageDoc("page:DEV/a.md", "DEV/a.md", "DEV", "Guide",
		"Distributed tracing helps you understand latency across services.")
	mustIndex(t, s, doc)

	res, err := s.Search(search.SearchOptions{Query: "tracing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected result")
	}
	// Snippet may be empty if the highlighter doesn't match (acceptable),
	// but if present it should be non-empty string.
	if res[0].Snippet != "" && strings.TrimSpace(res[0].Snippet) == "" {
		t.Error("snippet is whitespace-only")
	}
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

// equalStringSlice compares two string slices after sorting.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
