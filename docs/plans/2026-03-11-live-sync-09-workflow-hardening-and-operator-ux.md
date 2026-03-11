# Live Sync Follow-Up 09: Workflow Hardening and Operator UX

**Goal:** Turn the March 11 live-sync findings into concrete production-hardening work for hierarchy correctness, canonical path reconciliation, pre-push inspection, conflict UX, and attachment visibility.

**Covers:** `F-001`, `F-002`, `F-003` from the March 11 live test log, plus operator smoothness gaps confirmed during the real `TD2` / `SD2` run

**Specs to update first if behavior changes:**
- `openspec/specs/push/spec.md`
- `openspec/specs/pull-and-validate/spec.md`
- `openspec/specs/compatibility/spec.md`
- `openspec/specs/workspace/spec.md`
- `openspec/specs/recovery-and-maintenance/spec.md`

**Likely files:**
- `internal/sync/push_hierarchy.go`
- `internal/sync/folder_fallback.go`
- `internal/sync/tenant_capabilities.go`
- `internal/sync/pull_paths.go`
- `internal/sync/pull_pages.go`
- `internal/sync/status.go`
- `cmd/diff.go`
- `cmd/diff_pages.go`
- `cmd/push.go`
- `cmd/pull.go`
- `cmd/report.go`
- `cmd/inline_status.go`
- `cmd/progress.go`
- `cmd/e2e_test.go`
- `internal/sync/push_hierarchy_test.go`
- `internal/sync/pull_paths_test.go`
- `internal/sync/status_test.go`
- `docs/usage.md`
- `docs/automation.md`
- `docs/compatibility.md`

## Implementation status

- [x] Specs updated for canonical pull paths, explicit folder downgrade handling, and non-interactive merge policy requirements.
- [x] Canonical pull paths now reconcile authored slugs to the canonical hierarchy, and `validate` blocks duplicate directory-backed folder titles across a space.
- [x] `conf diff` now previews brand-new pages, and `conf status --attachments` reports attachment-only drift plus orphaned local assets.
- [x] Push now requires explicit non-interactive merge resolution, stops before mutating the main workspace for `--merge-resolution=fail`, and never silently downgrades folders into pages.
- [x] Added unit/integration coverage for canonical-path reconciliation, explicit folder fallback handling, merge-resolution behavior, create preview, and attachment-aware status.
- [x] Added live E2E coverage for new-page diff preview, attachment-aware status, and non-interactive `pull-merge` fail-closed behavior.

## Required outcomes

1. A local directory that represents a folder must never silently become a Confluence page unless the operator explicitly opts into that semantic downgrade.
2. Workspaces that authored short slugs must reconcile to the same canonical pull path as fresh workspaces, or the product must explicitly define and document stable author-path behavior.
3. `conf diff` for new pages must show a useful create preview instead of failing because the file has no `id`.
4. Non-interactive conflict handling must be deterministic and must not surprise-mutate the main workspace into an unresolved merge without an explicit policy.
5. `conf status` must be able to report attachment-only drift so operators can answer “is this synced?” from one command.
6. Interactive TTY workflows may be richer, but must remain optional and must never block automation or `--non-interactive` usage.

## Findings to address

### P0: Fail closed on folder-creation fallback

Live finding:

- During the March 11 TD2 push, the CLI attempted `POST /wiki/api/v2/folders`, received HTTP `400`, switched to compatibility mode, and created a page named `API` instead of preserving a pure folder node.
- The current push path in `internal/sync/push_hierarchy.go` treats any folder-related `APIError` as ignorable and falls back to `CreatePage(...)` for the directory segment.
- The tenant behavior appears to enforce folder-title uniqueness across the whole space, not just among siblings. That means `/test` and `/API/test` cannot both exist as folders if they share the same title.

Required outcome:

1. Folder creation errors must be classified narrowly.
2. “Folder becomes page” must not happen silently.
3. Broken or incomplete local state should fail explicitly instead of triggering a broad compatibility path.
4. Validation must detect space-wide folder-title conflicts before push mutates remote state.
5. Any semantic downgrade from folder to page-with-subpages must be explicit and interactive.

Suggested tasks:

1. Audit `shouldIgnoreFolderHierarchyError` and all call sites that currently treat any `APIError` as fallback-worthy.
2. Split the current behavior into:
   - unsupported capability / endpoint unavailable
   - transient upstream failure
   - semantic conflict such as duplicate title or bad parent
3. Add validation coverage for duplicate folder titles at space scope, even when the local directories are under different parent paths.
4. Report that validation result as a hard error explaining that Confluence folder titles must be unique across the space.
5. In interactive push mode only, offer an explicit fallback choice:
   - keep failing and rename/restructure locally
   - convert the conflicting directory segment into a page with subpages
6. If the operator accepts fallback, perform the conversion intentionally and mirror it both:
   - remotely, by creating a page and placing child pages under it
   - locally, by rewriting the workspace into the page-with-subpages shape
7. In `--non-interactive`, fail closed instead of falling back.
8. Add E2E regressions for:
   - validation error on duplicate folder title
   - interactive acceptance of page fallback
   - non-interactive refusal to downgrade semantics

### P0: Reconcile canonical paths across workspaces

Live finding:

- A page authored locally as `Software-Development/XT-20260311-0712.md` stayed pinned to that path in the original workspace even after successful pulls and a force pull.
- Fresh workspaces pulled the same page as `Software-Development/Cross-Space-Target-2026-03-11-0712.md`.

Required outcome:

1. The sync engine must have one coherent rule for canonical local paths.
2. Original authoring workspaces and fresh pull workspaces must converge to the same path shape, unless specs deliberately say otherwise.

Suggested tasks:

1. Audit how `pull` computes canonical paths versus how `push` preserves tracked paths in `.confluence-state.json`.
2. Decide the product rule explicitly:
   - canonical pull paths always win, or
   - author-created paths are stable and preserved
3. Update specs first to reflect that decision.
4. Add regression tests covering:
   - local short slug then push then pull
   - fresh workspace pull of the same page
   - force pull reconciliation

### P1: Unify new-page `diff` with preflight

Live finding:

- `conf diff` on a brand-new file failed with “file has no id” and told the operator to use `conf push --preflight`.

Required outcome:

1. `diff` must remain useful for new pages.
2. Operators should not have to switch mental models between `diff` and `preflight` for the simplest create flow.

Suggested tasks:

1. Detect “new page with no id” early in `diff`.
2. Reuse the existing preflight planning pipeline.
3. Render a create preview that includes:
   - operation type
   - resolved parent
   - canonical target path
   - attachment operations
   - ADF summary or optional raw ADF view
4. Keep machine-readable reporting aligned between `diff --report-json` and `push --preflight --report-json`.

### P1: Make non-interactive conflict handling explicit

Live finding:

- `--on-conflict=pull-merge --non-interactive` performed a real pull, updated the working tree, and then stopped with conflict markers in `UU` state because no merge choice could be made automatically.

Required outcome:

1. Non-interactive conflict behavior must be deterministic.
2. The default non-interactive path should fail safely without surprising workspace mutation.
3. Operators who do want automatic conflict handling must opt into an explicit merge policy.

Suggested tasks:

1. Add an explicit merge-resolution policy for non-interactive use, for example:
   - `fail`
   - `keep-local`
   - `keep-remote`
   - `keep-both`
2. If no policy is provided, stop before mutating the main workspace.
3. Move the exploratory pull-merge attempt into an isolated temp worktree.
4. If a merge conflict remains unresolved, present artifacts and next steps without leaving the main workspace half-merged.
5. Extend recovery metadata and JSON reporting with conflict-state detail.

### P1: Add attachment-aware status reporting

Live finding:

- `conf status` explicitly reported clean page drift while attachment work still required `git status` or `conf diff`.

Required outcome:

1. Operators need one command that answers whether the workspace is fully synced, not just markdown/page synced.
2. Attachment-only drift must be visible without dropping into Git internals.

Suggested tasks:

1. Extend `status` to summarize:
   - local attachment additions
   - local attachment deletions
   - remote attachment additions
   - remote attachment deletions
   - orphaned local assets
2. If full attachment inspection is too expensive for the default path, add `conf status --attachments` and make the default output point to it explicitly.
3. Add unit and E2E coverage for attachment-only drift where page markdown is unchanged.

### P2: Use Bubble Tea for richer interactive flows, but keep it optional

Observation:

- The repo already depends on `bubbletea`, `bubbles`, and `lipgloss`.
- That makes richer TTY-only workflows practical without adding a new UI stack.

Required outcome:

1. Interactive flows should be easier for humans.
2. Automation and `--non-interactive` behavior must remain plain, deterministic, and scriptable.

Suggested tasks:

1. Evaluate a TTY-only diff / preflight viewer for:
   - new-page create preview
   - attachment operations
   - hierarchy changes
2. Evaluate a conflict-resolution picker for interactive `pull` / `push` runs:
   - keep local
   - keep remote
   - keep both
3. Evaluate a folder-fallback confirmation flow for interactive `push` runs when validation or preflight detects a folder-title conflict:
   - show the conflicting local path and the Confluence constraint
   - explain that the fallback changes semantics from folder to page
   - require explicit confirmation before any local or remote rewrite
4. Evaluate a recovery browser for retained runs from `conf recover`.
5. Keep all Bubble Tea flows behind TTY detection and/or an explicit `--interactive-ui` flag.
6. Preserve a plain-text fallback for every interactive surface.

## Suggested implementation order

### Task 1: Fix semantic correctness

1. Remove or narrow push-side page fallback for folder creation.
2. Add regression coverage proving directory-backed folder paths stay folder-backed.
3. Decide and codify canonical path rules.

### Task 2: Fix operator correctness signals

1. Reconcile canonical paths during pull.
2. Add attachment-aware status reporting.
3. Align machine-readable reports with the new behavior.

### Task 3: Improve operator smoothness

1. Unify new-page diff and preflight.
2. Add explicit non-interactive merge policy handling.
3. Add optional Bubble Tea interactive views for human-in-the-loop usage.

## Verification

1. `go test ./internal/sync/... -run "Folder|Hierarchy|Path|Status"`
2. `go test ./cmd/... -run "Diff|Push|Pull|Recover|Status"`
3. `make test`
4. `make test-e2e`

## Commit

Use one section commit, for example:

`docs(plan): capture workflow hardening and operator ux follow-ups`
