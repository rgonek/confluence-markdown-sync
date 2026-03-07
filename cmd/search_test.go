package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/search"
	"github.com/rgonek/confluence-markdown-sync/internal/search/blevestore"
	"github.com/rgonek/confluence-markdown-sync/internal/search/sqlitestore"
)

// --- command structure tests ---

func TestNewSearchCmd_NotNil(t *testing.T) {
	cmd := newSearchCmd()
	if cmd == nil {
		t.Fatal("newSearchCmd returned nil")
	}
}

func TestNewSearchCmd_Use(t *testing.T) {
	cmd := newSearchCmd()
	if !strings.HasPrefix(cmd.Use, "search") {
		t.Errorf("expected Use to start with 'search', got %q", cmd.Use)
	}
}

func TestNewSearchCmd_Flags(t *testing.T) {
	cmd := newSearchCmd()

	expectedFlags := []string{
		"space",
		"label",
		"heading",
		"format",
		"limit",
		"reindex",
		"engine",
		"list-labels",
		"list-spaces",
		"result-detail",
		"created-by",
		"updated-by",
		"created-after",
		"created-before",
		"updated-after",
		"updated-before",
	}

	for _, name := range expectedFlags {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected flag --%s to be registered", name)
		}
	}
}

func TestNewSearchCmd_FlagDefaults(t *testing.T) {
	cmd := newSearchCmd()

	cases := []struct {
		flag     string
		expected string
	}{
		{"format", "auto"},
		{"engine", "sqlite"},
		{"limit", "20"},
		{"space", ""},
		{"heading", ""},
		{"result-detail", ""},
	}

	for _, tc := range cases {
		f := cmd.Flags().Lookup(tc.flag)
		if f == nil {
			t.Errorf("flag --%s not found", tc.flag)
			continue
		}
		if f.DefValue != tc.expected {
			t.Errorf("flag --%s default = %q, want %q", tc.flag, f.DefValue, tc.expected)
		}
	}
}

// --- helper: build a minimal git repo with an indexed space ---

func setupSearchTestRepo(t *testing.T) (repoRoot string, store search.Store) {
	t.Helper()

	repo := t.TempDir()
	setupGitRepo(t, repo)

	// Create a space directory with one Markdown file.
	spaceDir := filepath.Join(repo, "DOCS")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	mdContent := `---
id: "123"
title: OAuth Security Overview
space: DOCS
labels:
  - security
  - architecture
---

# OAuth2 Flow

Token refresh happens every 15 minutes using PKCE.

## Token Refresh

Refresh tokens are rotated every 15 minutes using PKCE extension.
`
	if err := os.WriteFile(filepath.Join(spaceDir, "overview.md"), []byte(mdContent), 0o600); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	// Write minimal state file so indexer can discover the space.
	stateContent := `{"space_key":"DOCS","pages":{}}`
	stateFile := filepath.Join(spaceDir, ".confluence-state.json")
	if err := os.WriteFile(stateFile, []byte(stateContent), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Open a real SQLite store for this test repo.
	indexDir := filepath.Join(repo, searchIndexDir)
	dbPath := filepath.Join(indexDir, "search.db")
	s, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlitestore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return repo, s
}

// --- resolveSearchFormat tests ---

func TestResolveSearchFormat_Explicit(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"text", "text"},
		{"json", "json"},
	}
	for _, tc := range cases {
		got := resolveSearchFormat(tc.input, new(bytes.Buffer))
		if got != tc.expected {
			t.Errorf("resolveSearchFormat(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestResolveSearchFormat_AutoPipe(t *testing.T) {
	// A bytes.Buffer is not a TTY — should resolve to "json".
	got := resolveSearchFormat("auto", new(bytes.Buffer))
	if got != "json" {
		t.Errorf("resolveSearchFormat(auto, non-tty) = %q, want json", got)
	}
}

// --- printSearchResults tests ---

func TestPrintSearchResults_TextEmpty(t *testing.T) {
	out := new(bytes.Buffer)
	if err := printSearchResults(out, nil, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No results found") {
		t.Errorf("expected 'No results found', got %q", out.String())
	}
}

func TestPrintSearchResults_TextFormat(t *testing.T) {
	results := []search.SearchResult{
		{
			Document: search.Document{
				Type:     search.DocTypePage,
				Path:     "DEV/security/overview.md",
				Title:    "Security Overview",
				Labels:   []string{"architecture", "security"},
				SpaceKey: "DEV",
			},
			Snippet: "refresh tokens are rotated every 15 minutes using PKCE",
		},
	}

	out := new(bytes.Buffer)
	if err := printSearchResults(out, results, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "DEV/security/overview.md") {
		t.Errorf("expected path in output, got %q", got)
	}
	if !strings.Contains(got, "Security Overview") {
		t.Errorf("expected title in output, got %q", got)
	}
	if !strings.Contains(got, "architecture") {
		t.Errorf("expected label in output, got %q", got)
	}
	if !strings.Contains(got, "PKCE") {
		t.Errorf("expected snippet in output, got %q", got)
	}
}

func TestPrintSearchResults_JSONFormat(t *testing.T) {
	results := []search.SearchResult{
		{
			Document: search.Document{
				Type:     search.DocTypePage,
				Path:     "DEV/overview.md",
				Title:    "Overview",
				SpaceKey: "DEV",
			},
			Score: 1.5,
		},
	}

	out := new(bytes.Buffer)
	if err := printSearchResults(out, results, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded []search.SearchResult
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, out.String())
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 result, got %d", len(decoded))
	}
	if decoded[0].Document.Path != "DEV/overview.md" {
		t.Errorf("expected path DEV/overview.md, got %q", decoded[0].Document.Path)
	}
}

// --- printSearchStringList tests ---

func TestPrintSearchStringList_Text(t *testing.T) {
	out := new(bytes.Buffer)
	if err := printSearchStringList(out, []string{"alpha", "beta", "gamma"}, "text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	for _, item := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(got, item) {
			t.Errorf("expected %q in output, got %q", item, got)
		}
	}
}

func TestPrintSearchStringList_JSON(t *testing.T) {
	out := new(bytes.Buffer)
	if err := printSearchStringList(out, []string{"alpha", "beta"}, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded []string
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, out.String())
	}
	if len(decoded) != 2 || decoded[0] != "alpha" {
		t.Errorf("unexpected decoded value: %v", decoded)
	}
}

// --- openSearchStore tests ---

func TestOpenSearchStore_UnknownEngine(t *testing.T) {
	_, err := openSearchStore("badengine", t.TempDir())
	if err == nil {
		t.Fatal("expected error for unknown engine")
	}
	if !strings.Contains(err.Error(), "badengine") {
		t.Errorf("error should mention engine name, got: %v", err)
	}
}

func TestOpenSearchStore_SQLite(t *testing.T) {
	repo := t.TempDir()
	store, err := openSearchStore("sqlite", repo)
	if err != nil {
		t.Fatalf("unexpected error opening sqlite store: %v", err)
	}
	defer func() { _ = store.Close() }()
}

// --- --list-labels integration test ---

func TestRunSearch_ListLabels(t *testing.T) {
	runParallelCommandTest(t)

	repo, store := setupSearchTestRepo(t)

	// Pre-index a document with known labels.
	docs := []search.Document{
		{
			ID:       "page:DOCS/overview.md",
			Type:     search.DocTypePage,
			Path:     "DOCS/overview.md",
			SpaceKey: "DOCS",
			Labels:   []string{"security", "architecture"},
			Content:  "Token refresh happens every 15 minutes.",
		},
	}
	if err := store.Index(docs); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := store.UpdateMeta(); err != nil {
		t.Fatalf("update meta: %v", err)
	}

	// Change to the repo dir so gitRepoRoot() works.
	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--list-labels", "--format", "text", "--engine", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "security") {
		t.Errorf("expected 'security' in list-labels output, got %q", got)
	}
	if !strings.Contains(got, "architecture") {
		t.Errorf("expected 'architecture' in list-labels output, got %q", got)
	}
}

// --- --list-spaces integration test ---

func TestRunSearch_ListSpaces(t *testing.T) {
	runParallelCommandTest(t)

	repo, store := setupSearchTestRepo(t)

	docs := []search.Document{
		{
			ID:       "page:DOCS/page.md",
			Type:     search.DocTypePage,
			Path:     "DOCS/page.md",
			SpaceKey: "DOCS",
			Content:  "some content",
		},
	}
	if err := store.Index(docs); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := store.UpdateMeta(); err != nil {
		t.Fatalf("update meta: %v", err)
	}

	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--list-spaces", "--format", "text", "--engine", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "DOCS") {
		t.Errorf("expected 'DOCS' in list-spaces output, got %q", got)
	}
}

// --- query no-results graceful output ---

func TestRunSearch_NoResults(t *testing.T) {
	runParallelCommandTest(t)

	repo, store := setupSearchTestRepo(t)

	if err := store.UpdateMeta(); err != nil {
		t.Fatalf("update meta: %v", err)
	}

	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"xyzzy_no_such_term", "--format", "text", "--engine", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No results found") {
		t.Errorf("expected 'No results found', got %q", out.String())
	}
}

// --- missing query error ---

func TestRunSearch_MissingQuery(t *testing.T) {
	runParallelCommandTest(t)

	repo, _ := setupSearchTestRepo(t)
	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	// No QUERY arg, no --list-labels, no --list-spaces
	cmd.SetArgs([]string{"--engine", "sqlite"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when query is missing")
	}
}

// --- bleve engine opens successfully ---

func TestOpenSearchStore_Bleve(t *testing.T) {
	store, err := openSearchStore("bleve", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error opening bleve store: %v", err)
	}
	defer func() { _ = store.Close() }()
}

func TestOpenSearchStore_BleveUsesRepoIndexRoot(t *testing.T) {
	repo := t.TempDir()

	store, err := openSearchStore("bleve", repo)
	if err != nil {
		t.Fatalf("unexpected error opening bleve store: %v", err)
	}
	defer func() { _ = store.Close() }()

	expectedIndexDir := filepath.Join(repo, searchIndexDir, "bleve")
	if _, err := os.Stat(expectedIndexDir); err != nil {
		t.Fatalf("expected Bleve index directory %s to exist: %v", expectedIndexDir, err)
	}

	nestedIndexDir := filepath.Join(expectedIndexDir, searchIndexDir, "bleve")
	if _, err := os.Stat(nestedIndexDir); err == nil {
		t.Fatalf("expected no nested Bleve index directory, but found %s", nestedIndexDir)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat nested Bleve index directory: %v", err)
	}
}

// --- projectResult tests ---

func TestProjectResult_Full(t *testing.T) {
	r := search.SearchResult{
		Document: search.Document{
			ID:           "page:foo.md",
			Type:         search.DocTypePage,
			Path:         "DEV/foo.md",
			PageID:       "123",
			Title:        "Foo",
			SpaceKey:     "DEV",
			Labels:       []string{"a", "b"},
			Content:      "body text",
			HeadingPath:  []string{"## H2"},
			HeadingText:  "H2",
			HeadingLevel: 2,
			Language:     "go",
			Line:         42,
		},
		Score:   1.5,
		Snippet: "...body...",
	}

	got := projectResult(r, "full")
	if got.Document.Content != "body text" {
		t.Errorf("full: Content stripped unexpectedly")
	}
	if got.Document.SpaceKey != "DEV" {
		t.Errorf("full: SpaceKey stripped unexpectedly")
	}
	if got.Score != 1.5 {
		t.Errorf("full: Score stripped unexpectedly")
	}
}

func TestProjectResult_Standard(t *testing.T) {
	r := search.SearchResult{
		Document: search.Document{
			ID:           "page:foo.md",
			Type:         search.DocTypePage,
			Path:         "DEV/foo.md",
			PageID:       "123",
			Title:        "Foo",
			SpaceKey:     "DEV",
			Labels:       []string{"a"},
			Content:      "body text",
			HeadingPath:  []string{"## H2"},
			HeadingText:  "H2",
			HeadingLevel: 2,
			Language:     "go",
			Line:         10,
		},
		Score:   2.0,
		Snippet: "...snippet...",
	}

	got := projectResult(r, "standard")

	// kept fields
	if got.Document.Path != "DEV/foo.md" {
		t.Errorf("standard: Path missing")
	}
	if got.Document.Title != "Foo" {
		t.Errorf("standard: Title missing")
	}
	if got.Document.SpaceKey != "DEV" {
		t.Errorf("standard: SpaceKey missing")
	}
	if len(got.Document.Labels) != 1 {
		t.Errorf("standard: Labels missing")
	}
	if len(got.Document.HeadingPath) != 1 {
		t.Errorf("standard: HeadingPath missing")
	}
	if got.Document.HeadingText != "H2" {
		t.Errorf("standard: HeadingText missing")
	}
	if got.Document.Line != 10 {
		t.Errorf("standard: Line missing")
	}
	if got.Snippet != "...snippet..." {
		t.Errorf("standard: Snippet missing")
	}
	if got.Score != 2.0 {
		t.Errorf("standard: Score missing")
	}

	// stripped fields
	if got.Document.Content != "" {
		t.Errorf("standard: Content should be stripped, got %q", got.Document.Content)
	}
	if got.Document.ID != "" {
		t.Errorf("standard: ID should be stripped")
	}
	if got.Document.PageID != "" {
		t.Errorf("standard: PageID should be stripped")
	}
	if got.Document.Language != "" {
		t.Errorf("standard: Language should be stripped")
	}
}

func TestProjectResult_Minimal(t *testing.T) {
	r := search.SearchResult{
		Document: search.Document{
			ID:           "section:foo.md:12",
			Type:         search.DocTypeSection,
			Path:         "DEV/foo.md",
			PageID:       "123",
			Title:        "Foo",
			SpaceKey:     "DEV",
			Labels:       []string{"a"},
			Content:      "body text",
			HeadingPath:  []string{"## H2"},
			HeadingText:  "H2",
			HeadingLevel: 2,
			Line:         12,
		},
		Score:   1.0,
		Snippet: "...match...",
	}

	got := projectResult(r, "minimal")

	// kept fields
	if got.Document.Path != "DEV/foo.md" {
		t.Errorf("minimal: Path missing")
	}
	if len(got.Document.HeadingPath) != 1 {
		t.Errorf("minimal: HeadingPath missing")
	}
	if got.Document.HeadingText != "H2" {
		t.Errorf("minimal: HeadingText missing")
	}
	if got.Document.Line != 12 {
		t.Errorf("minimal: Line missing")
	}
	if got.Snippet != "...match..." {
		t.Errorf("minimal: Snippet missing")
	}

	// stripped fields
	if got.Document.Title != "" {
		t.Errorf("minimal: Title should be stripped")
	}
	if got.Document.SpaceKey != "" {
		t.Errorf("minimal: SpaceKey should be stripped")
	}
	if len(got.Document.Labels) != 0 {
		t.Errorf("minimal: Labels should be stripped")
	}
	if got.Document.Content != "" {
		t.Errorf("minimal: Content should be stripped")
	}
	if got.Score != 0 {
		t.Errorf("minimal: Score should be stripped")
	}
}

func TestProjectResult_UnknownDetailFallsBackToFull(t *testing.T) {
	r := search.SearchResult{
		Document: search.Document{
			Path:    "DEV/foo.md",
			Content: "body text",
		},
		Score: 9.9,
	}
	got := projectResult(r, "bogus")
	if got.Document.Content != "body text" {
		t.Errorf("unknown detail: should fall back to full, Content stripped")
	}
}

func TestRunSearch_ConfigFileEngine(t *testing.T) {
	runParallelCommandTest(t)

	repo, store := setupSearchTestRepo(t)

	// Pre-index with UpdateMeta so incremental update skips reindex.
	if err := store.UpdateMeta(); err != nil {
		t.Fatalf("update meta: %v", err)
	}
	_ = store.Close()

	// Write a .conf.yaml specifying engine=sqlite (valid, should not error).
	confContent := "search:\n  engine: sqlite\n  limit: 5\n  result_detail: minimal\n"
	if err := os.WriteFile(filepath.Join(repo, ".conf.yaml"), []byte(confContent), 0o600); err != nil {
		t.Fatal(err)
	}

	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	// No --engine flag — should be read from .conf.yaml.
	cmd.SetArgs([]string{"--list-spaces", "--format", "text"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command error: %v", err)
	}
}

func TestRunSearch_ReindexJSONFormatWritesOnlyJSON(t *testing.T) {
	runParallelCommandTest(t)

	repo, _ := setupSearchTestRepo(t)
	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	errOut := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"PKCE", "--reindex", "--format", "json", "--engine", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var decoded []search.SearchResult
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("expected valid JSON search output, got error %v\nstdout: %s\nstderr: %s", err, out.String(), errOut.String())
	}
	if len(decoded) == 0 {
		t.Fatal("expected reindex+search to return at least one result")
	}
}

func TestUpdateSearchIndexForSpace_UsesConfiguredBleveBackend(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "DOCS")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".conf.yaml"), []byte("search:\n  engine: bleve\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	writeMarkdown := `---
id: "123"
title: Searchable Page
space: DOCS
---

Bleve backend should index this content.
`
	if err := os.WriteFile(filepath.Join(spaceDir, "page.md"), []byte(writeMarkdown), 0o600); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	stateContent := `{"space_key":"DOCS","pages":{}}`
	if err := os.WriteFile(filepath.Join(spaceDir, ".confluence-state.json"), []byte(stateContent), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if err := updateSearchIndexForSpace(repo, spaceDir, "DOCS", new(bytes.Buffer)); err != nil {
		t.Fatalf("updateSearchIndexForSpace: %v", err)
	}

	bleveStore, err := blevestore.Open(repo)
	if err != nil {
		t.Fatalf("open blevestore: %v", err)
	}
	defer func() { _ = bleveStore.Close() }()

	results, err := bleveStore.Search(search.SearchOptions{Query: "Bleve backend"})
	if err != nil {
		t.Fatalf("search bleve index: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected document to be indexed in configured Bleve backend")
	}

	sqlitePath := filepath.Join(repo, searchIndexDir, "search.db")
	if _, err := os.Stat(sqlitePath); err == nil {
		t.Fatalf("expected SQLite index %s to remain absent when Bleve is configured", sqlitePath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat sqlite index: %v", err)
	}
}

func TestSearchRegistrationAndDocsAlignment(t *testing.T) {
	hasSearchCommand := false
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "search" {
			hasSearchCommand = true
			break
		}
	}
	if !hasSearchCommand {
		t.Fatal("expected root command to register search subcommand")
	}

	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	usage, err := os.ReadFile(filepath.Join("..", "docs", "usage.md"))
	if err != nil {
		t.Fatalf("read usage guide: %v", err)
	}
	agents, err := os.ReadFile(filepath.Join("..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS guide: %v", err)
	}

	if !strings.Contains(string(readme), "`search QUERY`") {
		t.Fatal("expected README to mention search in its command guidance")
	}

	requiredFlagDocs := []string{
		"--result-detail",
		"--created-by",
		"--updated-by",
		"--created-after",
		"--created-before",
		"--updated-after",
		"--updated-before",
	}
	for _, flag := range requiredFlagDocs {
		if !strings.Contains(string(usage), flag) {
			t.Fatalf("expected docs/usage.md to document %s", flag)
		}
		if !strings.Contains(string(agents), flag) {
			t.Fatalf("expected AGENTS.md to document %s", flag)
		}
	}
}

// --- author/date filter tests ---

func TestSearch_CreatedByFlag(t *testing.T) {
	runParallelCommandTest(t)

	repo, store := setupSearchTestRepo(t)

	docs := []search.Document{
		{
			ID: "page:DEV/a.md", Type: search.DocTypePage,
			Path: "DEV/a.md", SpaceKey: "DEV", Title: "Alice Doc",
			Content: "content", CreatedBy: "alice",
		},
		{
			ID: "page:DEV/b.md", Type: search.DocTypePage,
			Path: "DEV/b.md", SpaceKey: "DEV", Title: "Bob Doc",
			Content: "content", CreatedBy: "bob",
		},
	}
	if err := store.Index(docs); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := store.UpdateMeta(); err != nil {
		t.Fatalf("update meta: %v", err)
	}

	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--created-by", "alice", "--format", "json", "--engine", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "alice") {
		t.Errorf("expected alice in output, got: %s", got)
	}
	if strings.Contains(got, "bob") {
		t.Errorf("expected bob NOT in output, got: %s", got)
	}
}

func TestSearch_UpdatedByFlag(t *testing.T) {
	runParallelCommandTest(t)

	repo, store := setupSearchTestRepo(t)

	docs := []search.Document{
		{
			ID: "page:DEV/x.md", Type: search.DocTypePage,
			Path: "DEV/x.md", SpaceKey: "DEV", Title: "X",
			Content: "content", UpdatedBy: "carol",
		},
		{
			ID: "page:DEV/y.md", Type: search.DocTypePage,
			Path: "DEV/y.md", SpaceKey: "DEV", Title: "Y",
			Content: "content", UpdatedBy: "dave",
		},
	}
	if err := store.Index(docs); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := store.UpdateMeta(); err != nil {
		t.Fatalf("update meta: %v", err)
	}

	chdirRepo(t, repo)

	cmd := newSearchCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--updated-by", "carol", "--format", "json", "--engine", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "carol") {
		t.Errorf("expected carol in output, got: %s", got)
	}
	if strings.Contains(got, "dave") {
		t.Errorf("expected dave NOT in output, got: %s", got)
	}
}

// --- normalizeDateBound unit tests ---

func TestNormalizeDateBound_Empty(t *testing.T) {
	if got := normalizeDateBound("", false); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestNormalizeDateBound_RFC3339Passthrough(t *testing.T) {
	s := "2024-06-15T12:00:00Z"
	if got := normalizeDateBound(s, false); got != s {
		t.Errorf("expected %q unchanged, got %q", s, got)
	}
}

func TestNormalizeDateBound_DateStart(t *testing.T) {
	got := normalizeDateBound("2024-06-15", false)
	want := "2024-06-15T00:00:00Z"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeDateBound_DateEnd(t *testing.T) {
	got := normalizeDateBound("2024-06-15", true)
	want := "2024-06-15T23:59:59Z"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizeDateBound_UnknownPassthrough(t *testing.T) {
	s := "not-a-date"
	if got := normalizeDateBound(s, false); got != s {
		t.Errorf("expected %q passed through, got %q", s, got)
	}
}

// --- shortDate unit tests ---

func TestShortDate_RFC3339(t *testing.T) {
	got := shortDate("2024-06-15T12:00:00Z")
	if got != "2024-06-15" {
		t.Errorf("got %q, want 2024-06-15", got)
	}
}

func TestShortDate_ShortString(t *testing.T) {
	got := shortDate("2024")
	if got != "2024" {
		t.Errorf("short string: got %q, want %q", got, "2024")
	}
}
