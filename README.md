# confluence-markdown-sync

`cms` (working name: `confluence-sync`) is a planned Go CLI for syncing Confluence pages and attachments with a local Markdown workspace.

## Status
- Planning and design are tracked in `agents/plans/confluence_sync_cli.md`.
- **PR-01 (Bootstrap)** is implemented: Go module, cobra command tree, config loading, `init` command, automation flags, and `Makefile`.
- **PR-02 (Client + Local Data Model Foundation)** is implemented: Confluence client package (`spaces/pages/changes`, archive/delete endpoints), frontmatter read/write and validation primitives, path sanitization, and `.confluence-state.json` persistence.
- **PR-03 (Converter + Validate)** is implemented: strict/best-effort converter profiles, link/media hooks, and `validate [TARGET]`.
- **PR-04 (`pull` End-to-End)** is implemented: incremental pull orchestration, best-effort conversion with diagnostics, attachment download/delete reconciliation, scoped stash/restore, scoped commit/no-op detection, and pull sync tags.
- PR-05 through PR-07 are in progress per the delivery plan.

## Planned Commands
- `init`
- `pull [TARGET]`
- `push [TARGET]`
- `validate [TARGET]`
- `diff [TARGET]`

`[TARGET]` parsing:
- If it ends with `.md`, it is treated as a file path.
- Otherwise, it is treated as a `SPACE_KEY`.

Automation flags (planned):
- `pull` and `push`: `--yes`, `--non-interactive`
- `push`: `--on-conflict=pull-merge|force|cancel`

## Authentication
Environment variables:
- `ATLASSIAN_DOMAIN`
- `ATLASSIAN_EMAIL`
- `ATLASSIAN_API_TOKEN`

Compatibility and precedence (planned):
- `confluence-sync` will continue accepting legacy `CONFLUENCE_*` variables.
- Resolution order: `CONFLUENCE_*` (if set) -> `ATLASSIAN_*` -> `.env` file -> error.

## Planned Converter Libraries and Hook System
- Forward conversion (`pull`, `diff`) uses `github.com/rgonek/jira-adf-converter/converter`:
  - `converter.New(converter.Config)`
  - `ConvertWithContext(ctx, adfJSON, converter.ConvertOptions{SourcePath: ...})`
  - `converter.Result{Markdown, Warnings}`
- Reverse conversion (`validate`, `push`) uses `github.com/rgonek/jira-adf-converter/mdconverter`:
  - `mdconverter.New(mdconverter.ReverseConfig)`
  - `ConvertWithContext(ctx, markdown, mdconverter.ConvertOptions{SourcePath: ...})`
  - `mdconverter.Result{ADF, Warnings}`
- Runtime hook surfaces (planned):
  - Forward: `LinkRenderHook`, `MediaRenderHook`
  - Reverse: `LinkParseHook`, `MediaParseHook`
- Resolution behavior (planned):
  - `pull` and `diff` use `best_effort` (`ErrUnresolved` -> warning + fallback output).
  - `validate` and `push` use `strict` (`ErrUnresolved` -> conversion failure).
- Hook responsibilities:
  - Hooks return mapping decisions only.
  - Network and filesystem side effects (uploads, downloads, file writes/deletes) stay in sync orchestration.
- `validate` and `push` share the same strict reverse-conversion profile and hook adapters.

## Planned Developer Tooling
- A top-level `Makefile` will be included for common local workflows.
- Initial targets will cover at least `build`, `test`, and `lint`/`fmt`.

## Sync Behavior
- `pull` (implemented):
  - Fetches incremental changes using `last_pull_high_watermark` with an overlap window.
  - Rewrites same-space page links to relative Markdown links (anchors preserved).
  - Rewrites/downloads referenced attachments to `assets/<page-id>/<attachment-id>-<filename>`.
  - Reconciles remote deletions by hard-deleting local markdown/files and attachment assets.
  - Uses scoped `git stash` restore and creates scoped commits + pull sync tag only for non-no-op runs.
- `push` (planned):
  - Always runs `validate` before any remote writes.
  - Converts Markdown -> ADF with strict link/media hooks; unresolved references fail before remote writes.
  - Captures an internal snapshot under `refs/confluence-sync/snapshots/<SpaceKey>/<UTC timestamp>`.
  - Uses an ephemeral sync branch and isolated `git worktree`.
  - Creates per-file commits and merges on full success.
  - Restores out-of-scope local workspace state exactly after successful merge.
  - Keeps sync branch and snapshot refs on failure for CLI-guided recovery.
  - Creates push sync tag only for successful non-no-op runs.

## AI-Safe Guardrails
- Immutable frontmatter keys:
  - `confluence_page_id`
  - `confluence_space_key`
- `validate` fails if immutable metadata is edited.
- `push` aborts on validation failure.

## Git Integration
- Annotated tags for successful non-no-op runs:
  - `confluence-sync/pull/<SpaceKey>/<UTC timestamp>`
  - `confluence-sync/push/<SpaceKey>/<UTC timestamp>`
- No-op `pull`/`push` runs create no sync tag.
- Push commit trailers include:
  - `Confluence-Page-ID`
  - `Confluence-Version`
  - `Confluence-Space-Key`
  - `Confluence-URL`

## Project Layout (Planned)
```text
confluence-markdown-sync/
  cmd/
  internal/
  Makefile
  main.go
```

## References
- Implementation plan: `agents/plans/confluence_sync_cli.md`
- Agent implementation rules: `AGENTS.md`
