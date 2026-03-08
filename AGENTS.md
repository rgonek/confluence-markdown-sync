# AGENTS

## Purpose
This repository builds `conf` (`confluence-sync`), a Go CLI that syncs Confluence pages with local Markdown files.

## Source Of Truth
- Canonical specs:
  - `openspec/project.md`
  - `openspec/specs/*/spec.md`
- Narrative summaries:
  - `docs/specs/prd.md`
  - `docs/specs/technical-spec.md`
  - `docs/specs/README.md`
- If behavior changes, update the canonical specs first, then implement.

## Intended Usages

This project supports two primary sync workflows for agents:

### 1. Human-in-the-Loop (Agent as Writer)
The agent focus on Markdown content; the human runs `conf` commands.
- **Agent Task**: Edit `.md` files, run `conf validate` to check work.
- **Safety**: Do not touch sync-managed frontmatter keys such as `id`, `version`, `created_by`, `created_at`, `updated_by`, or `updated_at`.

### 2. Full Agentic Use (Autonomous Sync)
The agent manages the full sync cycle.
- **Workflow**: `pull` -> `edit` -> `validate` -> `diff` -> `push`.
- **Tests**: Always run `make test` (including the E2E workflow test) before pushing significant changes to `conf` itself.

## Core Invariants
- `push` must always run `validate` before any remote write.

- Immutable frontmatter keys:
  - `id`
- Mutable-by-sync frontmatter keys:
  - `version`
  - `created_by`
  - `created_at`
  - `updated_by`
  - `updated_at`
- User-editable frontmatter keys:
  - `title`
  - `state` (can be `draft` or `current`. Omitted means `current`. Cannot be set back to `draft` once published remotely).
  - `status` (Confluence "Content Status" visual lozenge, e.g., "Ready to review").
  - `labels` (array of strings for Confluence page labels).
- Space identity is stored in `.confluence-state.json` and workspace context, not in frontmatter.
- Remote deletions are hard-deleted locally during `pull` (recovery is via Git history).
- `.confluence-state.json` is local state and must stay gitignored.

## Converter And Hook Requirements
- Forward conversion for `pull`/`diff` uses `github.com/rgonek/jira-adf-converter/converter` and `ConvertWithContext(..., converter.ConvertOptions{SourcePath: ...})`.
- Reverse conversion for `validate`/`push` uses `github.com/rgonek/jira-adf-converter/mdconverter` and `ConvertWithContext(..., mdconverter.ConvertOptions{SourcePath: ...})`.
- `pull`/`diff` run with best-effort resolution (`ErrUnresolved` => diagnostics + fallback output).
- `validate`/`push` run with strict resolution (`ErrUnresolved` => conversion failure).
- `validate` must use the same strict reverse-conversion profile and hook adapters as `push`.
- Hooks return mapping decisions only; sync orchestration owns downloads/uploads and file writes/deletes.
- Diagram contract:
  - PlantUML is supported as a first-class `plantumlcloud` Confluence extension.
  - Mermaid is preserved as fenced code / ADF `codeBlock` content, not a rendered Confluence diagram macro.
  - `validate` should warn before push when Mermaid fences are present so the downgrade is explicit.
- Extension/macro support contract:
  - PlantUML: rendered round-trip support via the custom `plantumlcloud` handler.
  - Mermaid: preserved-but-not-rendered only; keep it as fenced code and expect an ADF `codeBlock` on push.
  - Raw `adf:extension` payloads: best-effort, low-level preservation fallback for extension nodes without a repo-specific handler; not a verified end-to-end round-trip guarantee.
  - Unknown Confluence macros/extensions: not a first-class supported authoring target; they may only survive through best-effort raw ADF preservation, and Confluence can still reject them on push. Validate any such workflow in a sandbox before relying on it.

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
- Commands: `init`, `pull`, `push`, `recover`, `status`, `clean`, `prune`, `validate`, `diff`, `relink`, `version`, `doctor`, `search`.
- `status` reports Markdown page drift only; attachment-only changes should be checked with `git status` or `conf diff`.
- `[TARGET]` parsing rule:
  - Ends with `.md` => file mode.
  - Otherwise => space mode (`SPACE_KEY`).

## Search Command (`conf search`)
- `conf search QUERY [flags]` runs full-text search over local Markdown files.
- Two pluggable backends share the `Store` interface: `--engine sqlite` (default, SQLite FTS5) and `--engine bleve` (Bleve scorch).
- Index lives in `.confluence-search-index/` (gitignored, local-only).
- Index is updated automatically on `pull` (non-fatal) and incrementally on each `search` invocation.
- Key flags:
  - `--space KEY` — filter to a Confluence space.
  - `--label LABEL` — filter by label (repeatable).
  - `--heading TEXT` — restrict to sections under matching headings.
  - `--reindex` — force full rebuild.
  - `--result-detail full|standard|minimal` — control payload size/detail.
  - `--created-by USER` / `--updated-by USER` — filter by creator or last updater.
  - `--created-after DATE` / `--created-before DATE` — bound created timestamps.
  - `--updated-after DATE` / `--updated-before DATE` — bound updated timestamps.
  - `--list-labels` / `--list-spaces` — facet discovery.
  - `--format text|json|auto` — output format (auto: TTY→text, pipe→json).
  - `--limit N` (default 20) — max results.
- Recommended agent workflow: `conf search "term" --format json | <process>` for token-efficient, structured reads.

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
- **NEVER perform real tests (e.g. `conf pull` or `conf push`) targeting real Confluence spaces within the repository root.** This prevents accidental commits of synced Markdown content.
- **Agent Sandbox**: Use a temporary directory *outside* of the repository for full end-to-end integration tests with real data.
- E2E tests must run only against explicit sandbox configuration:
  - `CONF_E2E_SANDBOX_SPACE_KEY` (required for all E2E workflows)
  - `CONF_E2E_CONFLICT_PAGE_ID` (required for conflict workflow coverage)
  - Never hardcode production page IDs or space keys in test code.
- If you must use a subdirectory for small tests, use the `workspace/` or `test-output/` directories (both gitignored).
- **Cleanup**: Always delete test content from `workspace/` or `test-output/` after completing a test session to keep the environment clean.
- Prioritize:

  - Frontmatter/validation unit tests.
  - Pull/push integration tests.
  - Worktree, snapshot-ref, and tag lifecycle tests (including no-op behavior).
  - Round-trip Markdown <-> ADF golden tests.

## Docs Maintenance
- Keep `README.md`, `docs/usage.md`, `docs/automation.md`, and `docs/compatibility.md` aligned with the OpenSpec files.
- Keep this file aligned with `openspec/project.md` and `openspec/specs/*/spec.md`.
