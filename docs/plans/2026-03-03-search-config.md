# Search Config Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Allow search engine, result limit, and result detail level to be configured in a per-repo `.conf.yaml` file, with CLI flags always taking precedence.

**Architecture:** New `internal/config/searchconfig.go` reads the `search:` block from `<repo-root>/.conf.yaml` using `gopkg.in/yaml.v3` (already in go.mod). `cmd/search.go` loads config in `runSearch()`, applies "flag wins if `cmd.Flags().Changed()`" precedence (same pattern as `applyHTTPPolicyEnvOverrides`), and projects results through a new `projectResult()` before formatting.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, Cobra (`cmd.Flags().Changed()`), stdlib `os.ReadFile`.

---

## Quick Reference

- Run all tests: `go test ./...` from repo root
- Run specific package: `go test ./internal/config/...` or `go test ./cmd/...`
- Test pattern: table-driven, no Arrange/Act/Assert comments, `t.TempDir()` for file fixtures
- Config package uses external test package (`package config_test`)
- cmd tests use same package (`package cmd`) with `runParallelCommandTest(t)` + `chdirRepo(t, repo)` for integration tests

---

### Task 1: `LoadSearchConfig` — struct and YAML reader

**Files:**
- Create: `internal/config/searchconfig.go`
- Create: `internal/config/searchconfig_test.go`

**Step 1: Write failing tests**

Create `internal/config/searchconfig_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
)

func TestLoadSearchConfig_Defaults(t *testing.T) {
	// Missing file → all defaults.
	cfg, err := config.LoadSearchConfig(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Engine != "sqlite" {
		t.Errorf("Engine = %q; want sqlite", cfg.Engine)
	}
	if cfg.Limit != 20 {
		t.Errorf("Limit = %d; want 20", cfg.Limit)
	}
	if cfg.ResultDetail != "full" {
		t.Errorf("ResultDetail = %q; want full", cfg.ResultDetail)
	}
}

func TestLoadSearchConfig_FullFile(t *testing.T) {
	dir := t.TempDir()
	content := "search:\n  engine: bleve\n  limit: 5\n  result_detail: minimal\n"
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadSearchConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Engine != "bleve" {
		t.Errorf("Engine = %q; want bleve", cfg.Engine)
	}
	if cfg.Limit != 5 {
		t.Errorf("Limit = %d; want 5", cfg.Limit)
	}
	if cfg.ResultDetail != "minimal" {
		t.Errorf("ResultDetail = %q; want minimal", cfg.ResultDetail)
	}
}

func TestLoadSearchConfig_PartialFile(t *testing.T) {
	// Only limit set — engine and result_detail should be defaults.
	dir := t.TempDir()
	content := "search:\n  limit: 50\n"
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadSearchConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Engine != "sqlite" {
		t.Errorf("Engine = %q; want sqlite (default)", cfg.Engine)
	}
	if cfg.Limit != 50 {
		t.Errorf("Limit = %d; want 50", cfg.Limit)
	}
	if cfg.ResultDetail != "full" {
		t.Errorf("ResultDetail = %q; want full (default)", cfg.ResultDetail)
	}
}

func TestLoadSearchConfig_ZeroLimitUsesDefault(t *testing.T) {
	// limit: 0 in file → treat as unset, use default 20.
	dir := t.TempDir()
	content := "search:\n  limit: 0\n"
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadSearchConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Limit != 20 {
		t.Errorf("Limit = %d; want 20 (default when 0)", cfg.Limit)
	}
}

func TestLoadSearchConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".conf.yaml"), []byte(":::bad yaml:::"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadSearchConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/config/... -run TestLoadSearchConfig -v
```

Expected: FAIL with `"config.LoadSearchConfig" undefined`

**Step 3: Implement `internal/config/searchconfig.go`**

```go
package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SearchConfig holds per-repo search preferences loaded from .conf.yaml.
type SearchConfig struct {
	Engine       string // "sqlite" | "bleve" — default "sqlite"
	Limit        int    // max results — default 20
	ResultDetail string // "full" | "standard" | "minimal" — default "full"
}

type confYAML struct {
	Search struct {
		Engine       string `yaml:"engine"`
		Limit        int    `yaml:"limit"`
		ResultDetail string `yaml:"result_detail"`
	} `yaml:"search"`
}

// LoadSearchConfig reads <repoRoot>/.conf.yaml and returns a SearchConfig with
// defaults applied. A missing file is not an error — defaults are returned.
func LoadSearchConfig(repoRoot string) (SearchConfig, error) {
	defaults := SearchConfig{
		Engine:       "sqlite",
		Limit:        20,
		ResultDetail: "full",
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, ".conf.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaults, nil
		}
		return defaults, err
	}

	var raw confYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return defaults, err
	}

	cfg := defaults
	if raw.Search.Engine != "" {
		cfg.Engine = raw.Search.Engine
	}
	if raw.Search.Limit > 0 {
		cfg.Limit = raw.Search.Limit
	}
	if raw.Search.ResultDetail != "" {
		cfg.ResultDetail = raw.Search.ResultDetail
	}
	return cfg, nil
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/... -run TestLoadSearchConfig -v
```

Expected: All 5 tests PASS

**Step 5: Commit**

```bash
git add internal/config/searchconfig.go internal/config/searchconfig_test.go
git commit -m "feat(config): add SearchConfig + LoadSearchConfig for .conf.yaml"
```

---

### Task 2: `projectResult` — field projection by detail level

**Files:**
- Modify: `cmd/search.go` (add `projectResult` function)
- Modify: `cmd/search_test.go` (add projection tests)

**Step 1: Write failing tests**

Add to `cmd/search_test.go`:

```go
// --- projectResult tests ---

func TestProjectResult_Full(t *testing.T) {
	r := search.SearchResult{
		Document: search.Document{
			ID:          "page:foo.md",
			Type:        search.DocTypePage,
			Path:        "DEV/foo.md",
			PageID:      "123",
			Title:       "Foo",
			SpaceKey:    "DEV",
			Labels:      []string{"a", "b"},
			Content:     "body text",
			HeadingPath: []string{"## H2"},
			HeadingText: "H2",
			HeadingLevel: 2,
			Language:    "go",
			Line:        42,
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
			ID:          "page:foo.md",
			Type:        search.DocTypePage,
			Path:        "DEV/foo.md",
			PageID:      "123",
			Title:       "Foo",
			SpaceKey:    "DEV",
			Labels:      []string{"a"},
			Content:     "body text",
			HeadingPath: []string{"## H2"},
			HeadingText: "H2",
			HeadingLevel: 2,
			Language:    "go",
			Line:        10,
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
			ID:          "section:foo.md:12",
			Type:        search.DocTypeSection,
			Path:        "DEV/foo.md",
			PageID:      "123",
			Title:       "Foo",
			SpaceKey:    "DEV",
			Labels:      []string{"a"},
			Content:     "body text",
			HeadingPath: []string{"## H2"},
			HeadingText: "H2",
			HeadingLevel: 2,
			Line:        12,
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
```

**Step 2: Run tests to verify they fail**

```bash
go test ./cmd/... -run TestProjectResult -v
```

Expected: FAIL with `"projectResult" undefined`

**Step 3: Add `projectResult` to `cmd/search.go`**

Add after `printSearchStringList` (at the end of the file):

```go
// projectResult returns a copy of r with fields zeroed out based on detail level.
// "full" returns r unchanged. "standard" drops Content, ID, PageID, Type, Language,
// HeadingLevel, ModTime. "minimal" keeps only Path, HeadingPath, HeadingText, Line,
// Snippet. Unknown values fall back to "full".
func projectResult(r search.SearchResult, detail string) search.SearchResult {
	switch detail {
	case "standard":
		r.Document = search.Document{
			Path:        r.Document.Path,
			Title:       r.Document.Title,
			SpaceKey:    r.Document.SpaceKey,
			Labels:      r.Document.Labels,
			HeadingPath: r.Document.HeadingPath,
			HeadingText: r.Document.HeadingText,
			Line:        r.Document.Line,
		}
		// Score and Snippet left on r as-is.
		return r
	case "minimal":
		r.Document = search.Document{
			Path:        r.Document.Path,
			HeadingPath: r.Document.HeadingPath,
			HeadingText: r.Document.HeadingText,
			Line:        r.Document.Line,
		}
		r.Score = 0
		return r
	default: // "full" and unknown values
		return r
	}
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./cmd/... -run TestProjectResult -v
```

Expected: All 4 tests PASS

**Step 5: Commit**

```bash
git add cmd/search.go cmd/search_test.go
git commit -m "feat(search): add projectResult for full/standard/minimal detail levels"
```

---

### Task 3: Wire config into `runSearch` + add `--result-detail` flag

**Files:**
- Modify: `cmd/search.go` (add flag, import config package, load config, apply precedence, apply projection)
- Modify: `cmd/search_test.go` (extend flag list test, add config-from-file test)

**Step 1: Write failing tests**

In `cmd/search_test.go`, extend the existing `TestNewSearchCmd_Flags` test to include `result-detail`:

```go
// Update the expectedFlags slice in TestNewSearchCmd_Flags to add:
//   "result-detail",
```

And update `TestNewSearchCmd_FlagDefaults` to add:

```go
// Add to cases slice:
//   {"result-detail", ""},
```

Add a new integration test for config file loading:

```go
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
```

**Step 2: Run tests to verify they fail**

```bash
go test ./cmd/... -run "TestNewSearchCmd_Flags|TestNewSearchCmd_FlagDefaults|TestRunSearch_ConfigFileEngine" -v
```

Expected: `TestNewSearchCmd_Flags` and `TestNewSearchCmd_FlagDefaults` FAIL because `result-detail` flag does not exist yet.

**Step 3: Update `cmd/search.go`**

3a. Add `flagSearchResultDetail` to the var block and register the flag:

```go
// In the var block inside newSearchCmd():
flagSearchResultDetail string

// After the other cmd.Flags() calls:
cmd.Flags().StringVar(&flagSearchResultDetail, "result-detail", "", `Result verbosity: "full" (default), "standard", or "minimal"`)
```

3b. Add `resultDetail` to `searchRunOptions`:

```go
type searchRunOptions struct {
	// ... existing fields ...
	resultDetail string
}
```

3c. Pass `resultDetail` in the `RunE` closure:

```go
return runSearch(cmd, query, searchRunOptions{
	// ... existing fields ...
	resultDetail: flagSearchResultDetail,
})
```

3d. Add `config` import and load config + precedence at the top of `runSearch()`, after `gitRepoRoot()`:

```go
import "github.com/rgonek/confluence-markdown-sync/internal/config"

// In runSearch(), after repoRoot is resolved:
searchCfg, err := config.LoadSearchConfig(repoRoot)
if err != nil {
    return fmt.Errorf("search: load config: %w", err)
}

engine := searchCfg.Engine
if cmd.Flags().Changed("engine") {
    engine = opts.engine
}

limit := searchCfg.Limit
if cmd.Flags().Changed("limit") {
    limit = opts.limit
}

detail := searchCfg.ResultDetail
if cmd.Flags().Changed("result-detail") {
    detail = opts.resultDetail
}
```

3e. Replace `opts.engine` and `opts.limit` with the resolved `engine` and `limit` throughout `runSearch()`:

- `openSearchStore(engine, repoRoot)` (was `opts.engine`)
- `Limit: limit` in `SearchOptions` (was `opts.limit`)

3f. Apply projection before `printSearchResults`:

```go
projected := make([]search.SearchResult, len(results))
for i, r := range results {
    projected[i] = projectResult(r, detail)
}
return printSearchResults(out, projected, format)
```

**Step 4: Run all tests to verify they pass**

```bash
go test ./cmd/... -v 2>&1 | tail -20
```

Expected: All tests PASS

**Step 5: Commit**

```bash
git add cmd/search.go cmd/search_test.go
git commit -m "feat(search): load search config from .conf.yaml with CLI flag precedence"
```

---

### Task 4: Add `.conf.yaml` to gitignore template

**Files:**
- Modify: `cmd/init.go`

**Step 1: Update `gitignoreContent` constant**

In `cmd/init.go`, add `.conf.yaml` to the template string (line 15 area):

```go
const gitignoreContent = `# Confluence Markdown Sync
.confluence-state.json
.confluence-search-index/
.conf.yaml
.env
// ... rest unchanged
```

**Step 2: Update `ensureGitignore` entries slice**

In `ensureGitignore()` at line ~229, add `.conf.yaml` to the entries slice:

```go
for _, entry := range []string{".confluence-state.json", ".confluence-search-index/", ".conf.yaml", ".env"} {
```

**Step 3: Run tests**

```bash
go test ./cmd/... -run TestInit -v
```

Expected: PASS (init tests verify gitignore behaviour)

**Step 4: Run full test suite**

```bash
go test ./...
```

Expected: All tests PASS

**Step 5: Commit**

```bash
git add cmd/init.go
git commit -m "feat(init): add .conf.yaml to gitignore template"
```

---

## Verification Checklist

After all tasks:

1. `go test ./...` — all pass
2. Create a `.conf.yaml` in a test repo:
   ```yaml
   search:
     engine: sqlite
     limit: 5
     result_detail: minimal
   ```
3. `conf search "oauth"` — verify only 5 results, minimal fields (path + heading + snippet)
4. `conf search "oauth" --limit 50` — verify CLI flag overrides config (50 results)
5. `conf search "oauth" --result-detail standard` — verify standard fields returned
6. Delete `.conf.yaml` — verify defaults still work (`full`, 20 results, `sqlite`)
7. `conf init` — verify `.conf.yaml` appears in generated `.gitignore`
