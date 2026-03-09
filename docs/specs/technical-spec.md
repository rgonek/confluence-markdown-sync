# Technical Specification

This document is a narrative technical summary of the canonical OpenSpec requirements for `conf`.

For the live behavior contract, see `openspec/project.md` and `openspec/specs/*/spec.md`.
For product scope and user-facing goals, see [prd.md](/D:/Dev/confluence-markdown-sync/docs/specs/prd.md).

## System Model

`conf` is a Go CLI that synchronizes Confluence Cloud pages and attachments with a local Git workspace:

- forward path: Confluence ADF -> Markdown (`pull`, `diff`)
- reverse path: Markdown -> ADF (`validate`, `push`)
- local discovery path: workspace indexing and query (`search`, `status`, `doctor`)

Local Git is required. A Git remote is optional.

## Workspace Model

### Repository Root

The workspace root is the Git repository root where `conf init` was run or where an existing Git repository is reused.

### Space Directories

Each managed Confluence space lives in its own directory. When `pull` creates a new space directory, it uses a sanitized `Name (KEY)` directory name unless the space was already resolved to an existing tracked directory.

### Local State

Each managed space stores local sync state in `.confluence-state.json`.

State schema:

| Key | Type | Meaning |
|---|---|---|
| `space_key` | string | Canonical Confluence space key for the local space directory |
| `last_pull_high_watermark` | RFC3339 string | High-watermark timestamp used for incremental pull planning |
| `page_path_index` | map[path]pageID | Tracked Markdown path -> Confluence page ID |
| `attachment_index` | map[path]attachmentID | Tracked local asset path -> Confluence attachment ID |
| `folder_path_index` | map[path]folderID | Tracked local folder path -> Confluence folder ID |

Rules:

- The state file is local-only and must remain gitignored.
- `space_key` lives in state, not in frontmatter.
- State paths are normalized to repo-style forward-slash relative paths.

## Markdown Frontmatter Contract

Reserved frontmatter keys:

| Key | Ownership | Notes |
|---|---|---|
| `title` | user-authored | Preferred page title. Push falls back to first H1, then filename stem when absent. Pull writes the remote title here. |
| `id` | sync-owned, immutable after assignment | Empty is allowed for a new page before first push. |
| `version` | sync-owned | Must be `> 0` for existing pages with an `id`. |
| `state` | user-authored | `draft` or `current`. Omitted means `current`. Existing published pages cannot be set back to `draft`. |
| `status` | user-authored | Confluence content-status lozenge. |
| `labels` | user-authored | Normalized to lowercase, trimmed, deduplicated, and sorted. Labels containing whitespace are invalid. |
| `created_by` | sync-owned | Remote author metadata for search/reporting. |
| `created_at` | sync-owned | Remote creation timestamp. |
| `updated_by` | sync-owned | Remote last-updater metadata. |
| `updated_at` | sync-owned | Remote last-updated timestamp. |

Additional rules:

- `space` is not stored in frontmatter.
- Unknown extra keys are preserved unless they collide with reserved sync keys.
- Legacy keys such as `confluence_page_id`, `confluence_space_key`, `confluence_version`, `confluence_last_modified`, and `confluence_parent_page_id` may be parsed for compatibility but are not emitted in normalized output.

## Target Resolution

Commands that accept `[TARGET]` follow one parsing rule:

- ends with `.md` -> file mode
- otherwise -> space mode

When `[TARGET]` is omitted, space context is inferred from the current working directory.

## Command Surface

Top-level commands:

- `init`
- `pull`
- `push`
- `recover`
- `status`
- `clean`
- `prune`
- `validate`
- `diff`
- `relink`
- `version`
- `doctor`
- `search`

Subcommands:

- `init agents`

Structured run reports:

- `pull`, `push`, `validate`, and `diff` support `--report-json`.

## Conversion And Hook Contract

### Forward Conversion

- Used by `pull` and `diff`.
- Implementation: `github.com/rgonek/jira-adf-converter/converter`.
- Call shape: `ConvertWithContext(..., converter.ConvertOptions{SourcePath: ...})`.
- Resolution mode: best effort.
- Behavior on unresolved references: produce warnings/diagnostics and fallback Markdown output rather than fail the run.

### Reverse Conversion

- Used by `validate` and `push`.
- Implementation: `github.com/rgonek/jira-adf-converter/mdconverter`.
- Call shape: `ConvertWithContext(..., mdconverter.ConvertOptions{SourcePath: ...})`.
- Resolution mode: strict.
- Behavior on unresolved references: fail conversion before any remote write.

### Hook Boundary

- Hooks return mapping decisions only.
- Sync orchestration owns all network and filesystem side effects such as downloads, uploads, file writes, and deletes.

## Extension And Macro Contract

| Feature | Contract |
|---|---|
| PlantUML (`plantumlcloud`) | First-class rendered round-trip support via the custom handler |
| Mermaid | Preserved as fenced code / ADF `codeBlock`, not a rendered Confluence macro |
| Raw `adf:extension` fences | Best-effort preservation only |
| Unknown Confluence macros/extensions | Not a first-class supported authoring target |

`validate` must warn for Mermaid content before push.

## Pull Contract

`pull` behavior:

1. Resolve the target space or page and load config/state.
2. Fetch remote space metadata and normalize the space directory.
3. Estimate impact for confirmation, including delete count and total changed Markdown files.
4. Stash dirty in-scope workspace state unless `--discard-local` is used.
5. Build global link index and run `sync.Pull`.
6. In `sync.Pull`:
   - load all relevant pages for the space
   - probe tenant compatibility modes for folders and content status
   - plan deterministic page paths and attachment paths
   - identify changed pages using the last pull watermark plus overlap window, unless `--force`
   - fetch changed pages, labels, content status, and attachments
   - convert ADF to Markdown with best-effort hooks
   - write Markdown files with normalized frontmatter
   - materialize remotely created pages during incremental pull only after the file write succeeds
   - reconcile remotely updated pages during incremental pull without requiring `--force`
   - delete tracked local files/assets removed remotely
   - update state indexes and watermark
7. Save state, print diagnostics, create a scoped commit and `confluence-sync/pull/<space>/<timestamp>` tag when changes exist.
8. Restore stashed workspace content and repair pulled `version` fields if the stash reintroduced old values.
9. Update the local search index for the pulled space on a best-effort basis.
10. Optionally run targeted cross-space relinking with `pull --relink`.

Pull-specific rules:

- `--force` is valid only for space targets.
- `--skip-missing-assets` turns missing-attachment failures into diagnostics.
- Pull no-ops create no commit and no sync tag.
- Remote deletions are hard-deleted locally.
- Cross-space links stay as readable remote links and emit preserved cross-space diagnostics instead of generic unresolved-reference failures.

## Validate Contract

`validate` must:

- build the space page index and global page index
- detect duplicate page IDs
- validate frontmatter schema
- validate immutable metadata against local state and push baseline
- resolve link/media references with the same strict hook profile used by `push`
- emit Mermaid downgrade warnings

Supported structured round-trip content includes:

- Markdown task lists with preserved checkbox state
- ordinary ISO-like date text that remains plain text unless the source explicitly requested date markup

Validation failure must stop `push` immediately.

## Push Contract

`push` behavior:

1. Resolve target scope, config, and current branch.
2. Determine the comparison baseline from the latest sync tag for the space; if no sync tag exists, fall back to the repository root commit.
3. Support three non-write inspection modes:
   - normal pre-validation before real push
   - `--preflight` for concise change/validation planning
   - `--dry-run` for simulated remote operations without local Git mutation
   - `--preflight` uses the same validation scope and strictness as a real push
4. For a real push:
   - capture the current in-scope workspace state by stashing dirty changes when needed
   - create a snapshot ref at `refs/confluence-sync/snapshots/<space>/<timestamp>`
   - create a sync branch `sync/<space>/<timestamp>`
   - create a temporary worktree under `.confluence-worktrees/`
5. In the worktree:
   - materialize the snapshot
   - compute in-scope changes against the baseline
   - run strict validation on changed files
   - require safety confirmation for destructive or large runs
   - execute `sync.Push`
6. `sync.Push` must:
   - resolve page, folder, and attachment identity maps
   - handle remote version conflicts according to `pull-merge`, `force`, or `cancel`
   - preserve local edits during `pull-merge` via merge results, conflict markers, or explicit recoverable state instead of silently discarding them
   - convert Markdown to ADF strictly
   - upload missing assets
   - update and create remote content as required
   - archive remote pages when tracked Markdown page deletions are pushed
   - delete remote attachments when tracked attachment deletions are pushed and not suppressed by `--keep-orphan-assets`
   - sync labels and content status where supported
   - emit rollback diagnostics when a partial failure needs recovery work
7. Finalize Git state:
   - create one commit per pushed page with Confluence trailers
   - merge the sync branch back into the original branch
   - tag successful non-no-op runs as `confluence-sync/push/<space>/<timestamp>`
   - restore the user stash, warning if conflicts remain
   - save updated local state
8. On failure:
   - keep snapshot ref and sync branch
   - record recovery metadata under `.git/confluence-recovery/`
   - leave cleanup to `recover` or `clean`

Push-specific rules:

- `push` always validates before any remote write.
- `--preflight` and `--dry-run` are mutually exclusive.
- Space-wide pushes default to `pull-merge` if `--on-conflict` is omitted.
- Single-file pushes require an explicit conflict policy or an interactive choice.
- `--keep-orphan-assets` suppresses deletion of unreferenced remote attachments.
- Archive operations respect `--archive-task-timeout` and `--archive-task-poll-interval`.

## Status, Diff, Relink, And Search

### `status`

- Reports Markdown page drift only.
- Uses the current workspace plus remote state.
- Surfaces tracked page path moves planned by the next pull.

### `diff`

- Fetches remote content.
- Converts with best-effort forward conversion.
- Shows local-vs-remote changes using `git diff --no-index`.
- Emits planned page-path move notes before the diff when hierarchy changes are involved.

### `relink`

- Rewrites absolute Confluence URLs in Markdown to relative local links when the target page is managed in the repository.
- Can operate globally or focus on a space/space directory target.

### `search`

- Stores the local index in `.confluence-search-index/`.
- Supports SQLite FTS5 (`sqlite`, default) and Bleve (`bleve`) backends.
- Reindexes fully with `--reindex` and otherwise updates incrementally.
- Indexes page, section, and code-block documents with path, title, labels, page ID, and author/timestamp metadata.

## Recovery And Maintenance Commands

### `recover`

- Lists retained failed-push artifacts.
- Can discard a specific run or all safe retained runs.
- Must never discard the current recovery branch or a run that still has an active linked worktree.

### `clean`

- Removes stale worktrees, stale snapshot refs, safe stale sync branches, and normalizes readable state files.
- Alias: `repair`.

### `doctor`

- Checks consistency between `.confluence-state.json`, Markdown files, and Git state.
- Detects missing files, ID mismatches, duplicate/tracked-state issues, unresolved conflict markers, degraded placeholders, and hierarchy layout issues.
- `--repair` fixes repairable cases only.

### `prune`

- Deletes orphaned local assets under `assets/` after confirmation or `--yes`.

## Safety And Automation Rules

- `pull`, `push`, `prune`, and destructive `recover`/`clean` flows require confirmation unless auto-approved.
- `--yes` auto-approves safety prompts but does not choose a push conflict strategy.
- `--non-interactive` must fail fast when a required decision is missing.
- Safety confirmation is required when an operation affects more than 10 Markdown files or includes delete operations.

## Git And Audit Model

- Local Git history is the operational audit log.
- Successful non-no-op runs create annotated sync tags.
- Failed push runs retain recovery refs/branches and metadata for later inspection.
- Push commits include:
  - `Confluence-Page-ID`
  - `Confluence-Version`
  - `Confluence-Space-Key`
  - `Confluence-URL`

## Compatibility And Fallback Modes

Important degraded modes already implemented:

- folder API unavailable -> page-based hierarchy fallback
- content-status API unavailable -> skip content-status sync for the run
- Mermaid -> preserved as code block
- unresolved best-effort references during pull/diff -> diagnostics plus fallback output

See [docs/compatibility.md](/D:/Dev/confluence-markdown-sync/docs/compatibility.md) for the operator-facing matrix.
