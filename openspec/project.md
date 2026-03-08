# Project Context

## Summary

`conf` is a Go CLI for synchronizing Confluence Cloud pages with a local Git-managed Markdown workspace.

The project is local-first:

- humans and agents author Markdown locally
- Confluence remains the publishing destination
- local Git provides history, review, and recovery

## Canonical Spec Layout

The canonical behavior contract lives in `openspec/specs/*/spec.md`.

Narrative docs such as `README.md`, `docs/usage.md`, `docs/automation.md`, `docs/compatibility.md`, and `docs/specs/*` are secondary summaries and operator guides. They must stay aligned with the OpenSpec files.

Implementation plans under `agents/plans/*.md` are historical delivery plans and backlog notes, not the live product contract.

## Domain Terms

- Workspace root: the local Git repository root used by `conf`
- Space directory: the directory that stores one managed Confluence space
- State file: `.confluence-state.json` in a space directory
- Baseline: the latest successful sync tag used to compare later local changes
- Snapshot ref: a retained hidden Git ref used during failed push recovery
- Sync branch: the ephemeral `sync/<space>/<timestamp>` branch used during push

## Technical Constraints

- Local Git is required.
- A Git remote is optional.
- Forward conversion uses the Jira ADF converter in best-effort mode.
- Reverse conversion uses the Markdown-to-ADF converter in strict mode.
- `push` MUST validate before any remote write.

## Metadata Rules

- Space identity is derived from workspace context and `.confluence-state.json`, not frontmatter.
- `id` is the only immutable frontmatter key after assignment.
- `version`, `created_by`, `created_at`, `updated_by`, and `updated_at` are sync-managed.
- `state`, `status`, `labels`, and `title` are user-authored.

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

Subcommand:

- `init agents`
