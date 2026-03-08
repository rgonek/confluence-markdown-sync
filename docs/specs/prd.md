# Product Requirements Document

This PRD is a narrative summary of the canonical OpenSpec requirements for `conf`.

For the live behavior contract, see `openspec/project.md` and `openspec/specs/*/spec.md`.
For the narrative technical summary, see [technical-spec.md](/D:/Dev/confluence-markdown-sync/docs/specs/technical-spec.md).

## Product Summary

`conf` is a local-first Go CLI that synchronizes Confluence Cloud pages with a Git-managed Markdown workspace. It exists so humans and agents can author in Markdown, review changes locally, and publish back to Confluence without losing operational safety.

## Problem

Teams want the editing ergonomics, searchability, automation, and history of local Markdown and Git, but they still need Confluence as the published destination. Native Confluence editing does not provide the same local authoring, repo workflows, or agent-friendly interfaces. A sync layer is required, but it must be safe enough that a bad local edit or automation mistake does not silently corrupt remote content.

## Primary Users

- Technical writers and engineers maintaining documentation in Git.
- Product, project, and support teams authoring structured Markdown pages.
- AI agents that draft or refactor Markdown content.
- CI or scheduled automation that validates or syncs docs non-interactively.

## Primary Workflows

### 1. Human-in-the-Loop Authoring

The agent or human edits Markdown locally, then a human runs:

`pull -> edit -> validate -> diff -> push`

This is the default safety model for production content.

### 2. Autonomous Sync

An agent or CI job performs the full workflow, including remote writes, using explicit non-interactive flags and conflict policy settings.

### 3. Local Discovery And Review

Users query and inspect the workspace without mutating Confluence:

- `search` for full-text discovery
- `status` for page drift
- `diff` for local-vs-remote comparison
- `doctor` for workspace consistency

### 4. Recovery And Maintenance

Users recover from failed sync runs or local state drift with:

- `recover`
- `clean`
- `doctor --repair`
- `prune`

## Product Goals

- Markdown is the primary authoring format.
- Confluence remains the publishing system of record.
- `push` never writes remotely without a successful `validate`.
- Local review is first-class via `diff`, `status`, Git history, and structured run reports.
- Failed push runs remain recoverable through retained refs, branches, worktrees, and metadata.
- The product supports both interactive human workflows and scripted automation.
- Search works entirely from the local workspace with no API calls during query execution.

## Non-Goals

- Replacing Confluence with a Git-only publishing workflow.
- Supporting every Confluence macro as a first-class authoring primitive.
- Requiring users to run Git recovery commands manually for normal sync failures.
- Treating raw ADF preservation as a guaranteed, user-friendly authoring format.

## Product Requirements

### Workspace And Setup

- `conf init` must bootstrap a usable workspace, including Git initialization when needed, `.gitignore` updates, `.env` scaffolding, and template helper docs.
- A Git remote must not be required.
- Each synced space must have local state in `.confluence-state.json`, and that state must remain gitignored.

### Content Model

- Markdown files must carry sync metadata in frontmatter.
- `space` is not part of frontmatter. Space identity is derived from workspace context and per-space state.
- `id` is immutable after assignment.
- `version`, `created_by`, `created_at`, `updated_by`, and `updated_at` are sync-managed.
- `state`, `status`, and `labels` are user-editable.
- Unknown extra frontmatter keys should be preserved unless they collide with reserved sync keys.

### Pull

- `pull` must convert remote ADF to Markdown using best-effort resolution.
- Same-space page links must rewrite to relative Markdown links when local targets are known.
- Attachments must be downloaded to deterministic local asset paths.
- Remote deletions must remove tracked local Markdown and asset files.
- Non-fatal degradation must surface as diagnostics instead of silently disappearing.

### Validate

- `validate` must be strict and use the same reverse-conversion profile as `push`.
- Validation must catch frontmatter schema issues, immutable metadata edits, broken link/media resolution, and strict Markdown-to-ADF conversion failures.
- Mermaid content must trigger a warning before push because it is preserved as code, not rendered as a Confluence diagram macro.

### Diff And Status

- `diff` must show what would change between local Markdown and current remote content.
- `status` must report Markdown page drift only; attachment-only drift remains a `git status` / `diff` concern.

### Push

- `push` must always validate before any remote write.
- Push execution must be isolated from the user workspace through a temporary worktree and ephemeral sync branch.
- Push must operate against the full captured workspace state, including uncommitted in-scope changes.
- Conflict policy must be explicit and automatable.
- Successful non-no-op push runs must create audit tags and update the local baseline used for later status/diff/push calculations.
- Failed runs must retain enough state for `recover` to inspect and discard safely later.

### Search

- `search` must index local Markdown only.
- The default backend is SQLite FTS5, with Bleve as an alternative backend.
- Search indexing must happen automatically on `pull` when possible and incrementally during `search`.
- Search must support filters for space, labels, headings, creator/updater, and created/updated dates.

### Recovery And Maintenance

- `recover` must expose retained failed-push artifacts without requiring manual Git inspection.
- `clean` must remove stale sync artifacts that are safe to delete.
- `doctor` must detect state/file/index inconsistencies and offer repair for repairable cases.
- `prune` must delete orphaned local assets only after confirmation or `--yes`.

### Automation

- `pull` and `push` must support `--yes` and `--non-interactive`.
- Safety confirmation is required for destructive or large operations unless explicitly auto-approved.
- `push --dry-run` and `push --preflight` must provide safe pre-write inspection paths.
- Commands that emit structured reports must support machine-readable JSON.

## Acceptance Criteria

- A production-safe user can review and publish a single page or whole space without editing Git internals directly.
- A CI job can validate or push deterministically with explicit flags and no prompts.
- The source-of-truth metadata contract is unambiguous: `space` is not stored in frontmatter.
- Pull, validate, diff, push, search, and recovery behavior can be described from the current implementation without depending on historical plan docs.
