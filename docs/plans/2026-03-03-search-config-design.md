# Design: Search Configuration via `.conf.yaml`

**Date:** 2026-03-03

## Problem

Search behavior (engine choice, result limit, result verbosity) is only configurable via CLI flags. Agents and team members who always want the same settings must repeat flags on every invocation. There is no persistent per-repo configuration for search.

## Goals

1. Allow engine, limit, and result detail to be set in a per-repo config file.
2. CLI flags always win over config file (standard CLI convention).
3. Result detail presets reduce token cost for agent use cases.

## Non-Goals

- No global (~/.conf.yaml) config — per-repo only.
- No Viper or other config framework — plain yaml.v3 already in go.mod.
- Config file does not cover credentials (those stay in .env).

## Config File

**Location:** `<repo-root>/.conf.yaml` (optional, gitignored)

```yaml
search:
  engine: sqlite        # "sqlite" or "bleve"   — default: sqlite
  limit: 20             # max results            — default: 20
  result_detail: full   # "full" | "standard" | "minimal" — default: full
```

All keys are optional. Missing file or missing keys fall back to built-in defaults.

## Architecture

### `internal/config/searchconfig.go` (new)

```go
type SearchConfig struct {
    Engine       string // "sqlite" | "bleve"
    Limit        int
    ResultDetail string // "full" | "standard" | "minimal"
}

// LoadSearchConfig reads <repoRoot>/.conf.yaml and returns a SearchConfig
// with defaults filled in. Returns defaults (no error) if the file is absent.
func LoadSearchConfig(repoRoot string) (SearchConfig, error)
```

Defaults: `Engine="sqlite"`, `Limit=20`, `ResultDetail="full"`.

### Precedence wiring in `cmd/search.go`

Uses `cmd.Flags().Changed()` — the same pattern as `applyHTTPPolicyEnvOverrides`:

```
CLI flag (--engine, --limit, --result-detail)
  > .conf.yaml [search] section
    > built-in default
```

A new `--result-detail` flag is added to the search command (default `""` = "not set by flag").

### Result projection in `cmd/search.go`

```go
func projectResult(r SearchResult, detail string) SearchResult
```

Applied to every result before formatting.

| Preset     | Fields kept |
|------------|-------------|
| `full`     | All fields (current behaviour) |
| `standard` | Path, Title, SpaceKey, Labels, HeadingPath, HeadingText, Line, Snippet, Score |
| `minimal`  | Path, HeadingPath, HeadingText, Line, Snippet |

Fields not in the preset are zeroed (empty string / nil slice / 0).

## Files Changed

| File | Action |
|------|--------|
| `internal/config/searchconfig.go` | New — `SearchConfig` + `LoadSearchConfig()` |
| `internal/config/searchconfig_test.go` | New — unit tests |
| `cmd/search.go` | Add `--result-detail` flag, load config, apply precedence, add `projectResult()` |
| `cmd/search_test.go` | Add tests for precedence and projection |
| `cmd/init.go` | Add `.conf.yaml` to gitignore template |

## Validation

- `LoadSearchConfig` returns error on invalid YAML (not on missing file).
- Invalid `engine` / `result_detail` values are caught at command runtime with a clear message.
- `Limit <= 0` from config is treated as "use default" (20).
