# AGENTS

## Purpose
This repository builds `cms` (`confluence-sync`), a Go CLI that syncs Confluence pages with local Markdown files.

## Source Of Truth
- Primary plan: `agents/plans/confluence_sync_cli.md`
- If implementation details are unclear, update the plan first, then implement.

## Intended Usages

This project supports two primary sync workflows for agents:

### 1. Human-in-the-Loop (Agent as Writer)
The agent focus on Markdown content; the human runs `cms` commands.
- **Agent Task**: Edit `.md` files, run `cms validate` to check work.
- **Safety**: Do not touch `id`, `space`, or `version` in frontmatter.

### 2. Full Agentic Use (Autonomous Sync)
The agent manages the full sync cycle.
- **Workflow**: `pull` -> `edit` -> `validate` -> `diff` -> `push`.
- **Tests**: Always run `make test` (including the E2E workflow test) before pushing significant changes to `cms` itself.

## Core Invariants
- `push` must always run `validate` before any remote write.

- Immutable frontmatter keys:
  - `id`
  - `space`
- Mutable-by-sync frontmatter keys:
  - `version`
- User-editable frontmatter keys:
  - `status` (can be `draft` or `current`. Omitted means `current`. Cannot be set back to `draft` once published remotely).
- Remote deletions are hard-deleted locally during `pull` (recovery is via Git history).
- `.confluence-state.json` is local state and must stay gitignored.

## Converter And Hook Requirements
- Forward conversion for `pull`/`diff` uses `github.com/rgonek/jira-adf-converter/converter` and `ConvertWithContext(..., converter.ConvertOptions{SourcePath: ...})`.
- Reverse conversion for `validate`/`push` uses `github.com/rgonek/jira-adf-converter/mdconverter` and `ConvertWithContext(..., mdconverter.ConvertOptions{SourcePath: ...})`.
- `pull`/`diff` run with best-effort resolution (`ErrUnresolved` => diagnostics + fallback output).
- `validate`/`push` run with strict resolution (`ErrUnresolved` => conversion failure).
- `validate` must use the same strict reverse-conversion profile and hook adapters as `push`.
- Hooks return mapping decisions only; sync orchestration owns downloads/uploads and file writes/deletes.

## Git Workflow Requirements
- `push` uses an ephemeral sync branch: `sync/<SpaceKey>/<UTC timestamp>`.
- `push` runs in an isolated temporary worktree.
- `push` captures in-scope workspace state in hidden snapshot refs: `refs/confluence-sync/snapshots/<SpaceKey>/<UTC timestamp>`.
- `push` keeps per-file commits, then merges the sync branch on full success.
- `push` restores out-of-scope local workspace state exactly (`staged`, `unstaged`, `untracked`, deletions) after successful merge.
- Successful non-no-op sync runs create annotated tags:
  - `confluence-sync/pull/<SpaceKey>/<UTC timestamp>`
  - `confluence-sync/push/<SpaceKey>/<UTC timestamp>`
- No-op `pull`/`push` runs create no commit/merge and no sync tag.
- Failed `push` runs keep sync branch and snapshot refs for CLI-guided recovery.
- Push commits include structured trailers:
  - `Confluence-Page-ID`
  - `Confluence-Version`
  - `Confluence-Space-Key`
  - `Confluence-URL`

## Validation Requirements
`validate [TARGET]` must check:
- Frontmatter schema.
- Immutable key integrity.
- Link and asset resolution.
- Markdown to ADF conversion.
- Strict reverse conversion behavior aligned with `push` hook/profile settings.

Validation failures must stop `push` immediately.

## Command Model
- Commands: `init`, `pull`, `push`, `validate`, `diff`.
- `[TARGET]` parsing rule:
  - Ends with `.md` => file mode.
  - Otherwise => space mode (`SPACE_KEY`).

## Developer Tooling Requirements
- Keep a top-level `Makefile` in the repository.
- `Makefile` should provide common local workflows (at minimum: `build`, `test`, and `lint`/`fmt`).
- Keep `Makefile` targets aligned with current CLI behavior and CI usage.

## Interactivity And Automation Requirements
- `pull` and `push` support `--yes` and `--non-interactive`.
- `pull` supports `--skip-missing-assets`, `--force` (`-f`) for full-space refresh, and `--discard-local` to overwrite local changes.
- `push` supports `--on-conflict=pull-merge|force|cancel` for non-interactive conflict policy.
- `push` supports `--dry-run` to print simulated API requests and ADF output without modifying local or remote state.
- `pull` provides interactive conflict resolution (Keep Both/Remote/Local) when automatic merge fails.
- `push` with `--on-conflict=pull-merge` automatically triggers `pull` on version conflicts.

- `--yes` auto-approves safety confirmations but does not choose remote-ahead conflict strategy.
- `--non-interactive` must fail fast when a required decision is missing.
- Safety confirmation is required when an operation affects more than 10 markdown files or includes delete operations.

## Testing Expectations
- Add or update tests for any changed invariant.
- Prioritize:
  - Frontmatter/validation unit tests.
  - Pull/push integration tests.
  - Worktree, snapshot-ref, and tag lifecycle tests (including no-op behavior).
  - Round-trip Markdown <-> ADF golden tests.

## Docs Maintenance
- Keep `README.md` aligned with current plan and command behavior.
- Keep this file aligned with `agents/plans/confluence_sync_cli.md`.
