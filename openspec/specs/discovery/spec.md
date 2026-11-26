# Discovery Specification

## Purpose

Define the non-mutating discovery and inspection capabilities of `conf`: `status`, `diff`, `relink`, and `search`.

## Requirements

### Requirement: Status reports page drift only

The system SHALL make `status` a high-level Markdown page drift view rather than an attachment inventory.

#### Scenario: Attachment-only changes are excluded from status

- GIVEN the workspace contains only attachment drift and no Markdown page drift
- WHEN the user runs `conf status`
- THEN the system SHALL not report those attachment-only changes as page drift
- AND the user SHALL need `git status` or `conf diff` for asset inspection

### Requirement: Diff compares local Markdown to current remote content

The system SHALL provide a best-effort remote comparison without mutating local or remote state.

#### Scenario: Diff fetches and converts remote content

- GIVEN the user runs `conf diff`
- WHEN the command builds the comparison
- THEN the system SHALL fetch the relevant remote content
- AND the system SHALL convert it with best-effort forward conversion
- AND the system SHALL compare it with local Markdown using `git diff --no-index`

#### Scenario: Planned page moves are surfaced before content diff

- GIVEN pull planning would move one or more tracked Markdown paths
- WHEN `conf diff` renders the comparison
- THEN the system SHALL report those planned path moves explicitly

### Requirement: Relink rewrites absolute Confluence URLs to local paths

The system SHALL rewrite local Markdown links when the target page is managed locally.

#### Scenario: Managed Confluence link becomes relative Markdown link

- GIVEN a Markdown file contains an absolute Confluence URL that points to a page managed in the same repository
- WHEN `conf relink` runs
- THEN the system SHALL rewrite the URL to a relative Markdown path

#### Scenario: Unresolvable link is left unchanged

- GIVEN a Markdown file contains an absolute Confluence URL that cannot be mapped to a managed local page
- WHEN `conf relink` runs
- THEN the system SHALL leave the link unchanged

### Requirement: Search is local-only

The system SHALL provide full-text search over the local workspace without making Confluence API calls at query time.

#### Scenario: Search runs against the local index

- GIVEN the local search index exists or can be built
- WHEN the user runs `conf search`
- THEN the system SHALL answer the query from the local index only

### Requirement: Search backends and index storage

The system SHALL support multiple local index backends behind a shared contract.

#### Scenario: SQLite is the default backend

- GIVEN the user does not pass `--engine`
- WHEN `conf search` opens the index
- THEN the system SHALL use the SQLite FTS5 backend by default

#### Scenario: Index storage is local-only

- GIVEN search indexing is enabled for the workspace
- WHEN the index is created or updated
- THEN the system SHALL store it under `.confluence-search-index/`

### Requirement: Search indexing lifecycle

The system SHALL keep the search index reasonably current without making pull fail if indexing fails.

#### Scenario: Pull updates search index best-effort

- GIVEN a pull run completes successfully with scoped changes
- WHEN post-pull indexing runs
- THEN the system SHALL attempt to refresh the affected space in the search index
- AND pull SHALL continue even if search indexing fails

#### Scenario: Search updates incrementally by default

- GIVEN a prior search index exists
- WHEN the user runs `conf search` without `--reindex`
- THEN the system SHALL perform an incremental update before searching

#### Scenario: Reindex forces a full rebuild

- GIVEN the user passes `--reindex`
- WHEN `conf search` runs
- THEN the system SHALL rebuild the index from all discovered managed spaces

### Requirement: Search filters and output

The system SHALL support structured querying and automation-friendly output.

#### Scenario: Search supports metadata filters

- GIVEN the user passes `--space`, `--label`, `--heading`, `--created-by`, `--updated-by`, or date-window filters
- WHEN `conf search` executes
- THEN the system SHALL apply those filters to the indexed documents

#### Scenario: Automatic format follows the output stream

- GIVEN the user leaves `--format` as `auto`
- WHEN `conf search` writes to a terminal
- THEN the system SHALL emit text
- AND it SHALL emit TOON when output is redirected or piped
- AND explicit `--format text`, `--format toon`, and `--format json` SHALL override detection
- AND reindex progress SHALL stay off structured stdout

#### Scenario: Invalid output options are rejected

- GIVEN `--format` or `--result-detail` has an unrecognized value
- WHEN `conf search` validates the options
- THEN it SHALL report the invalid value and list the valid values before opening the index
- AND it SHALL exit with status 2

#### Scenario: Standard detail is the default

- GIVEN neither `--result-detail` nor `search.result_detail` in `.conf.yaml` is set
- WHEN `conf search` returns matches
- THEN it SHALL include path, title, space, labels, heading context, line, snippet, and author and date metadata when available
- AND raw content SHALL be available with `full` in TOON and JSON
- AND an explicit result detail flag SHALL take precedence over the workspace configuration

#### Scenario: Counts reflect all matching indexed documents

- GIVEN the query or filters match more indexed documents than the result limit
- WHEN `conf search` renders results as text or TOON
- THEN it SHALL report `count: N of M total` and give a rerun command that retains filters and raises the limit to M
- AND TOON SHALL emit nonempty results as a table with comma-separated labels and heading paths joined by ` > ` in their respective cells
- AND explicit JSON SHALL retain its results array

#### Scenario: Empty results include context and next steps

- GIVEN no indexed documents match the query and filters
- WHEN `conf search` renders results as text or TOON
- THEN it SHALL report zero results with the query and active filters
- AND it SHALL suggest ways to discover spaces and labels or rebuild the index

#### Scenario: Facet discovery supports TOON

- WHEN the user lists labels or spaces with `--format toon`
- THEN `conf search` SHALL emit a count and the corresponding array
- AND it SHALL explicitly report when the array is empty
