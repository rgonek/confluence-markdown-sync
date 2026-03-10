# Usage Guide

This guide covers day-to-day usage of `conf`.

> **Beta** — `conf` is under active development. Core workflows are tested against live tenants, but edge cases remain. Pin a specific version for production use.

## What `conf` does

`conf` synchronizes Confluence pages with local Markdown files.

- `pull` converts Confluence ADF to Markdown.
- `push` converts Markdown back to ADF and updates Confluence.
- `status` inspects Markdown page drift against the last sync baseline and current remote state.
- `validate` checks a workspace before remote writes.
- `diff` previews local vs remote content.
- `init agents` scaffolds an `AGENTS.md` file for AI-assisted authoring.
- `relink` rewrites absolute Confluence links to local relative Markdown links.
- `search` indexes and queries local Markdown files with full-text search (zero API calls).

## Requirements

- Git installed and available in `PATH`
- Confluence Cloud credentials
- Local filesystem access to a workspace directory

## Authentication

`conf` resolves configuration in this order:

1. `CONFLUENCE_*`
2. `ATLASSIAN_*`
3. `.env`

Required values:

- `ATLASSIAN_DOMAIN` (example: `https://your-domain.atlassian.net`)
- `ATLASSIAN_EMAIL`
- `ATLASSIAN_API_TOKEN`

Example `.env`:

```dotenv
ATLASSIAN_DOMAIN=https://your-domain.atlassian.net
ATLASSIAN_EMAIL=you@example.com
ATLASSIAN_API_TOKEN=your-token
```

## Workspace Setup

Create or enter your repository folder and run:

```powershell
conf init
```

`init` can:

- initialize Git when missing,
- ensure `.gitignore` entries,
- create `.env` when needed,
- scaffold `.env` directly from already-set `ATLASSIAN_*` / `CONFLUENCE_*` variables without prompting,
- scaffold helper files,
- create an initial commit when a new Git repository is initialized.

## Target Syntax

Many commands accept `[TARGET]`.

- If `[TARGET]` ends with `.md`, `conf` treats it as a file target.
- Otherwise, `conf` treats it as a space target (`SPACE_KEY`).

Examples:

```powershell
conf pull ENG
conf validate ENG
conf push ENG --on-conflict=cancel
conf diff ENG

conf pull .\ENG\Architecture.md
conf validate .\ENG\Architecture.md
conf diff .\ENG\Architecture.md
```

## Command Reference

### `conf pull [TARGET]`

Pulls remote Confluence content into local Markdown.

Highlights:

- best-effort conversion (unresolved references become diagnostics),
- diagnostics distinguish preserved cross-space links (`note`), degraded-but-pullable fallbacks, and broken references left as fallback output,
- page files follow Confluence hierarchy (folders and parent/child pages become nested directories),
- pages that have children are written as `<Page>/<Page>.md` so they are distinguishable from folders,
- incremental pulls reconcile remote page creates, updates, and deletes without requiring `--force`,
- leaf-page title renames can keep the existing Markdown path when the effective parent directory is unchanged,
- pages that own subtree directories move when their self-owned directory segment changes,
- hierarchy moves and ancestor/path-segment sanitization changes move the Markdown file and emit `PAGE_PATH_MOVED` notes with old/new paths,
- same-space links rewritten to relative Markdown links,
- cross-space links preserved as readable remote URLs/references instead of being rewritten to local Markdown paths,
- attachments downloaded into `assets/<page-id>/<attachment-id>-<filename>`,
- `--force` (`-f`) forces a full-space refresh (all tracked pages are re-pulled even when incremental changes are empty),
- attachment download failures include the owning page ID,
- missing assets can be auto-skipped with `--skip-missing-assets` (`-s`),
- without `-s`, pull asks whether to continue when an attachment download fails,
- remote deletions are hard-deleted locally,
- sync tag created only on non-no-op runs.

### `conf validate [TARGET]`

Runs strict validation of sync invariants.

Checks include:

- frontmatter schema,
- immutable metadata integrity,
- link/asset resolution,
- strict Markdown -> ADF conversion compatibility.

Validation also emits non-fatal compatibility warnings for content that will sync successfully but will not render as a first-class Confluence feature. Today that includes Mermaid fenced code blocks, which are preserved as ADF `codeBlock` nodes instead of diagram macros.

Use this before major pushes or in CI.

### `conf status [TARGET]`

Shows a high-level sync summary for Markdown pages.

Highlights:

- compares local Markdown drift against the last sync baseline,
- checks whether tracked remote pages are ahead, missing, or newly added,
- surfaces planned tracked-page path relocations that would happen on the next pull,
- focuses on Markdown page files only.

Attachment-only changes are intentionally excluded from `conf status`. Use `git status` for local asset changes or `conf diff` for attachment-aware remote inspection. There is no attachment-aware `conf status` mode yet.

### `conf diff [TARGET]`

Shows a local-vs-remote diff.

Highlights:

- fetches remote content,
- converts using best-effort forward conversion,
- reports planned Markdown path moves before showing the diff so hierarchy-driven renames are explicit,
- includes synced frontmatter parity such as `state`, `status`, and `labels`,
- strips read-only author/timestamp metadata so the diff stays focused on actionable drift,
- compares using `git diff --no-index`,
- supports both file and space targets,
- requires an `id` for file-mode remote comparison; for a brand-new local file without `id`, use `conf push <file> --preflight` instead.

### `conf init agents [TARGET]`

Scaffolds an `AGENTS.md` file in a managed space directory.

Highlights:

- supports multiple templates via `--type`,
- preserves existing `AGENTS.md` (no overwrite),
- works against a space key or explicit target directory.

### `conf relink [TARGET]`

Converts absolute Confluence links in Markdown to relative local links when targets are managed in the repo.

Highlights:

- supports global or targeted relink,
- dry-runs each scope before prompting,
- applies only when links can be resolved from local state/index.

### `conf push [TARGET]`

Publishes local Markdown changes to Confluence.

Highlights:

- always runs `validate` first,
- preflights frontmatter `status` values before any remote page mutation so invalid or unavailable content-status writes fail early,
- strict conversion before remote writes,
- isolated sync branch and worktree execution,
- repository-scoped workspace lock prevents concurrent `pull`/`push` runs in the same repo,
- per-page commit metadata with Confluence trailers,
- recovery refs retained on failures,
- failed pushes print concrete `recover`, branch inspection, and cleanup commands for the retained run,
- space-scoped push, `--preflight`, and `--dry-run` validate the full target space whenever there are in-scope changes,
- `--preflight` uses the same validation scope and strictness as a real push,
- `--on-conflict=pull-merge` restores local edits before running `pull` and preserves them via merge results, conflict markers, or retained recovery state instead of silently discarding them,
- when `--on-conflict=pull-merge` stops after a conflict-preserving pull, the CLI prints explicit next steps to resolve files, `git add` them, and rerun push,
- removing tracked Markdown pages archives the corresponding remote page and follow-up pull removes it from tracked local state,
- tracked page removals are previewed and summarized as remote archive operations rather than hard deletes,
- remote archive operations require long-task completion (`--archive-task-timeout`, `--archive-task-poll-interval`), and timeout handling now performs a follow-up verification read so the CLI can distinguish "still running remotely" from a confirmed archive,
- `--preflight` for a concise local push plan (change summary + validation) without remote writes.

### `conf search QUERY`

Full-text search over local Markdown files.

Highlights:

- index is built automatically on first use and updated incrementally,
- two backends available: `--engine sqlite` (default, SQLite FTS5) and `--engine bleve`,
- index stored in `.confluence-search-index/` (local-only, gitignored),
- index updated automatically after each `conf pull` (non-fatal),
- deleted Markdown paths are removed from the index during incremental updates and post-pull space refreshes,
- results grouped by file with heading context and snippets,
- metadata filters support creator/updater and created/updated date windows,
- `--result-detail` controls whether JSON/text results include full document payloads or a smaller projection,
- `--format auto` defaults to text on TTY, JSON when piped.

Key flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--space KEY` | | Filter to a specific Confluence space |
| `--label LABEL` | | Filter by label (repeatable) |
| `--heading TEXT` | | Restrict to sections under matching headings |
| `--limit N` | 20 | Maximum number of results |
| `--reindex` | false | Force full index rebuild |
| `--engine` | sqlite | Backend: `sqlite` or `bleve` |
| `--list-labels` | false | List all indexed labels and exit |
| `--list-spaces` | false | List all indexed spaces and exit |
| `--format` | auto | Output format: `text`, `json`, or `auto` |
| `--result-detail` | full | Result verbosity: `full`, `standard`, or `minimal` |
| `--created-by USER` | | Filter to pages created by this user |
| `--updated-by USER` | | Filter to pages last updated by this user |
| `--created-after DATE` | | Filter to pages created on or after a date (`YYYY-MM-DD` or RFC3339) |
| `--created-before DATE` | | Filter to pages created on or before a date |
| `--updated-after DATE` | | Filter to pages updated on or after a date |
| `--updated-before DATE` | | Filter to pages updated on or before a date |

Examples:

```powershell
# Basic search
conf search "oauth token refresh"

# Filter by space and label
conf search "deploy pipeline" --space DEV --label ci

# Restrict to sections under matching headings
conf search "token" --heading "Authentication"

# Facet discovery
conf search --list-labels --format json
conf search --list-spaces

# Agent-friendly (piped → JSON automatically)
conf search "security review" --format json | ConvertFrom-Json

# Use metadata filters and smaller result payloads
conf search "oauth" --created-by alice --updated-after 2024-01-01 --result-detail minimal
```

## Metadata and State

Markdown frontmatter keys:

- `space` is not stored in frontmatter; space identity comes from workspace context and `.confluence-state.json`.
- immutable keys:
  - `id`
- sync-managed keys:
  - `version`
  - `created_by`
  - `created_at`
  - `updated_by`
  - `updated_at`
- user-editable keys:
  - `state` (lifecycle: `draft` | `current`)
  - `status` (visual lozenge: e.g., "Ready to review")
  - `labels` (list of strings): each label must be non-empty after trim and must not contain whitespace; labels are normalized to lowercase and de-duplicated/sorted before sync operations

Local state file:

- `.confluence-state.json` (per space, gitignored)

## Extension and Macro Support

For a full breakdown of which features depend on optional tenant APIs and what
fallback behavior applies when those APIs are unavailable, see
[docs/compatibility.md](compatibility.md).

| Item | Support level | Markdown / ADF behavior | Notes |
|------|---------------|-------------------------|-------|
| Markdown task lists | Native round-trip support | Push writes Confluence task nodes and pull restores checkbox lists. | Checked/unchecked state should survive push/pull round-trips. |
| PlantUML (`plantumlcloud`) | Rendered round-trip support | Pull/diff use the custom extension handler to turn the Confluence macro into a managed `adf-extension` wrapper with a `puml` code body; validate/push rebuild the same Confluence extension. | This is the only first-class extension handler registered by `conf`. |
| Mermaid | Preserved but not rendered | Markdown keeps ` ```mermaid ` fences; push writes an ADF `codeBlock` with language `mermaid` instead of a Confluence diagram macro. | `conf validate` warns with `MERMAID_PRESERVED_AS_CODEBLOCK`, and push surfaces the same warning before writing. |
| Plain ISO-like date text | Text-preserving round-trip | Ordinary body text such as `2026-03-09` stays plain text through push/pull unless the source explicitly requests date markup. | Date-looking text must not be silently coerced into a different calendar date or implicit macro. |
| Raw ADF extension preservation | Best-effort preservation only | When an extension node has no repo-specific handler, pull/diff can preserve it as a raw ```` ```adf:extension ```` JSON fence that validate/push can pass back through with minimal interpretation. | Treat this as a low-level escape hatch, not as a rendered or human-friendly authoring format. It is not a verified end-to-end round-trip contract; validate in a sandbox before relying on it. |
| Unknown Confluence macros/extensions | Unsupported as a first-class feature | `conf` does not add custom behavior for unknown macros beyond whatever best-effort raw ADF preservation may be possible for some remote payloads. | If Confluence rejects an unknown or uninstalled macro, push can still fail. Do not assume rendered round-trip support unless a handler is documented explicitly, and sandbox-validate any workflow that depends on this path. |

Practical guidance:

- Use PlantUML when the page must keep rendering as a Confluence diagram macro.
- Use Mermaid only when preserving the source as code is acceptable.
- Keep raw `adf:extension` fences unchanged if you need best-effort preservation of an unhandled extension node, and test that workflow in a sandbox before using it in a real space.
- Do not treat unknown macros/extensions as supported authoring targets just because they may survive a pull in raw ADF form.

## Typical Team Workflow

```powershell
# 1) Pull latest
conf pull ENG

# 2) Edit markdown locally

# 3) Validate
conf validate ENG

# 4) Review with diff
conf diff ENG

# 5) Push
conf push ENG --on-conflict=cancel
```

## Troubleshooting

- Validation errors on unresolved links/assets: run `conf validate [TARGET]` and fix broken paths or metadata.
- Conflict errors on push: choose `--on-conflict=pull-merge|force|cancel` based on your policy.
- `another sync command is already mutating this repository`: wait for the active `pull`/`push` to finish, or inspect `.git/confluence-sync.lock.json` if you suspect a stale lock.
- `ATTACHMENT_PATH_NORMALIZED`: the first push may relocate referenced local assets into `assets/<page-id>/...`; that rename is expected and stable after the next pull.
- No-op output: there were no in-scope changes to sync.
