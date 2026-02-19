# Usage Guide

This guide covers day-to-day usage of `cms`.

## What `cms` does

`cms` synchronizes Confluence pages with local Markdown files.

- `pull` converts Confluence ADF to Markdown.
- `push` converts Markdown back to ADF and updates Confluence.
- `validate` checks a workspace before remote writes.
- `diff` previews local vs remote content.

## Requirements

- Git installed and available in `PATH`
- Confluence Cloud credentials
- Local filesystem access to a workspace directory

## Authentication

`cms` resolves configuration in this order:

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
cms init
```

`init` can:

- initialize Git when missing,
- ensure `.gitignore` entries,
- create `.env` when needed,
- scaffold helper files,
- create an initial commit when a new Git repository is initialized.

## Target Syntax

Many commands accept `[TARGET]`.

- If `[TARGET]` ends with `.md`, `cms` treats it as a file target.
- Otherwise, `cms` treats it as a space target (`SPACE_KEY`).

Examples:

```powershell
cms pull ENG
cms validate ENG
cms push ENG --on-conflict=cancel
cms diff ENG

cms pull .\ENG\Architecture.md
cms validate .\ENG\Architecture.md
cms diff .\ENG\Architecture.md
```

## Command Reference

### `cms pull [TARGET]`

Pulls remote Confluence content into local Markdown.

Highlights:

- best-effort conversion (unresolved references become diagnostics),
- page files follow Confluence hierarchy (parent/child pages become nested directories),
- same-space links rewritten to relative Markdown links,
- attachments downloaded into `assets/<page-id>/<attachment-id>-<filename>`,
- attachment download failures include the owning page ID,
- missing assets can be auto-skipped with `--skip-missing-assets` (`-s`),
- without `-s`, pull asks whether to continue when an attachment download fails,
- remote deletions are hard-deleted locally,
- sync tag created only on non-no-op runs.

### `cms validate [TARGET]`

Runs strict validation of sync invariants.

Checks include:

- frontmatter schema,
- immutable metadata integrity,
- link/asset resolution,
- strict Markdown -> ADF conversion compatibility.

Use this before major pushes or in CI.

### `cms diff [TARGET]`

Shows a local-vs-remote diff.

Highlights:

- fetches remote content,
- converts using best-effort forward conversion,
- compares using `git diff --no-index`,
- supports both file and space targets.

### `cms push [TARGET]`

Publishes local Markdown changes to Confluence.

Highlights:

- always runs `validate` first,
- strict conversion before remote writes,
- isolated sync branch and worktree execution,
- per-page commit metadata with Confluence trailers,
- recovery refs retained on failures.

## Metadata and State

Markdown frontmatter keys:

- immutable keys:
  - `confluence_page_id`
  - `confluence_space_key`
- sync-managed keys:
  - `confluence_version`
  - `confluence_last_modified`
  - `confluence_parent_page_id`

Local state file:

- `.confluence-state.json` (per space, gitignored)

## Typical Team Workflow

```powershell
# 1) Pull latest
cms pull ENG

# 2) Edit markdown locally

# 3) Validate
cms validate ENG

# 4) Review with diff
cms diff ENG

# 5) Push
cms push ENG --on-conflict=cancel
```

## Troubleshooting

- Validation errors on unresolved links/assets: run `cms validate [TARGET]` and fix broken paths or metadata.
- Conflict errors on push: choose `--on-conflict=pull-merge|force|cancel` based on your policy.
- No-op output: there were no in-scope changes to sync.
