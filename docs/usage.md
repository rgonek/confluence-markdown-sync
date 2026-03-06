# Usage Guide

This guide covers day-to-day usage of `conf`.

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
- page files follow Confluence hierarchy (folders and parent/child pages become nested directories),
- pages that have children are written as `<Page>/<Page>.md` so they are distinguishable from folders,
- same-space links rewritten to relative Markdown links,
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
- focuses on Markdown page files only.

Attachment-only changes are intentionally excluded from `conf status`. Use `git status` or `conf diff` when you need asset visibility.

### `conf diff [TARGET]`

Shows a local-vs-remote diff.

Highlights:

- fetches remote content,
- converts using best-effort forward conversion,
- includes synced frontmatter parity such as `state`, `status`, and `labels`,
- strips read-only author/timestamp metadata so the diff stays focused on actionable drift,
- compares using `git diff --no-index`,
- supports both file and space targets.

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
- strict conversion before remote writes,
- isolated sync branch and worktree execution,
- per-page commit metadata with Confluence trailers,
- recovery refs retained on failures,
- archive deletes require long-task completion (`--archive-task-timeout`, `--archive-task-poll-interval`),
- `--preflight` for a concise local push plan (change summary + validation) without remote writes.

### `conf search QUERY`

Full-text search over local Markdown files.

Highlights:

- index is built automatically on first use and updated incrementally,
- two backends available: `--engine sqlite` (default, SQLite FTS5) and `--engine bleve`,
- index stored in `.confluence-search-index/` (local-only, gitignored),
- index updated automatically after each `conf pull` (non-fatal),
- results grouped by file with heading context and snippets,
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
```

## Metadata and State

Markdown frontmatter keys:

- immutable keys:
  - `id`
  - `space`
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

## Diagram Support

- PlantUML: supported as a first-class Confluence extension through `plantumlcloud`, with round-trip preservation in pull and push.
- Mermaid: preserved as fenced code in Markdown and as ADF `codeBlock` content with language `mermaid` in Confluence. It does not render as a Mermaid macro, and `conf validate` warns before push so the downgrade is explicit.

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
- No-op output: there were no in-scope changes to sync.
