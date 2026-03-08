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

#### Scenario: Piped output defaults to JSON

- GIVEN `conf search` output is not a TTY
- WHEN the user leaves `--format` as `auto`
- THEN the system SHALL emit JSON output
