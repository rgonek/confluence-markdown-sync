# confluence-markdown-sync

`cms` is a Go CLI that syncs Confluence pages and attachments with a local Markdown workspace.

## Status
- Delivery plan PR-01 through PR-07 is implemented.
- Current source of truth: `agents/plans/confluence_sync_cli.md`.

## Commands
- `init`
- `pull [TARGET]`
- `push [TARGET]`
- `validate [TARGET]`
- `diff [TARGET]`

`[TARGET]` parsing:
- If it ends with `.md`, it is file mode.
- Otherwise, it is space mode (`SPACE_KEY`).

## Automation Flags
- `pull` and `push`:
  - `--yes`: auto-approves safety confirmations.
  - `--non-interactive`: disables prompts and fails when a required decision is missing.
- `push`:
  - `--on-conflict=pull-merge|force|cancel` sets remote-ahead conflict policy.
  - In `--non-interactive` mode, `--on-conflict` is required.

Safety confirmations are required for high-impact operations (>10 markdown files) and delete operations.

## Authentication
Configuration is resolved in this order:
1. Legacy `CONFLUENCE_*`
2. `ATLASSIAN_*`
3. `.env`

Required values:
- `ATLASSIAN_DOMAIN`
- `ATLASSIAN_EMAIL`
- `ATLASSIAN_API_TOKEN`

## Sync Behavior
- `pull`:
  - Uses best-effort ADF -> Markdown conversion.
  - Rewrites same-space links to relative Markdown paths and preserves anchors.
  - Downloads attachments to `assets/<page-id>/<attachment-id>-<filename>`.
  - Hard-deletes local markdown/assets for remote deletions.
  - Creates scoped commit + `confluence-sync/pull/<SpaceKey>/<UTC timestamp>` tag only on non-no-op runs.
- `push`:
  - Always runs `validate` before remote writes.
  - Uses strict Markdown -> ADF conversion.
  - Runs in an isolated worktree from snapshot refs (`refs/confluence-sync/snapshots/...`) and ephemeral `sync/<SpaceKey>/<UTC timestamp>` branch.
  - Creates per-file commits with Confluence trailers, merges on full success, and tags non-no-op runs with `confluence-sync/push/<SpaceKey>/<UTC timestamp>`.
  - Retains snapshot refs and sync branch on failure for recovery.
- `validate`:
  - Checks frontmatter schema, immutable keys, link/asset resolution, and strict reverse conversion.
- `diff`:
  - Fetches remote page content, converts with best-effort forward conversion, and compares against local markdown using `git diff --no-index`.
  - Supports both file and space mode.

## Core Invariants
- Immutable frontmatter keys:
  - `confluence_page_id`
  - `confluence_space_key`
- Mutable-by-sync keys:
  - `confluence_version`
  - `confluence_last_modified`
  - `confluence_parent_page_id`
- `.confluence-state.json` is local state and must remain gitignored.

## Developer Workflows
Use the top-level `Makefile`:
- `make build`
- `make test`
- `make fmt`
- `make lint`

## Notes
- Git is required locally.
- A Git remote is not required.

## References
- Plan: `agents/plans/confluence_sync_cli.md`
- Agent rules: `AGENTS.md`
