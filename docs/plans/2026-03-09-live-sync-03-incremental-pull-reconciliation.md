# Live Sync Follow-Up 03: Incremental Pull Reconciliation

**Goal:** Fix incremental pull so remote create/update/delete events reconcile locally without requiring `--force`.

**Covers:** `F-006`, `F-007`, E2E items `2`, `14`, `15`, `16`

**Specs to update first if behavior changes:**
- `openspec/specs/pull-and-validate/spec.md`

**Likely files:**
- `internal/sync/pull.go`
- `internal/sync/pull_pages.go`
- `internal/sync/pull_paths.go`
- `internal/sync/index.go`
- `internal/sync/pull_test.go`
- `internal/sync/pull_paths_test.go`
- `internal/sync/pull_hierarchy_issue_test.go`
- `internal/sync/workstream_d_hierarchy_test.go`
- `cmd/pull.go`
- `cmd/e2e_test.go`
- `README.md`
- `docs/usage.md`

## Required outcomes

1. Incremental pull writes new remote pages to disk before mutating tracked state.
2. Incremental pull updates existing pages when remote versions advance.
3. Incremental pull removes locally tracked pages/assets after remote deletion/archive reconciliation.
4. Hierarchy creation and child placement remain deterministic under incremental planning.

## Suggested implementation order

### Task 1: Fix remote-create materialization

1. Trace how changed remote pages are selected after the watermark.
2. Verify the page write path and state/index mutation order.
3. Ensure `page_path_index` only changes after the local file write succeeds.

### Task 2: Fix remote-update detection

1. Inspect the overlap-window planning and in-scope filtering path.
2. Ensure remote version changes beneath managed parents are not filtered out as no-op.

### Task 3: Lock delete reconciliation

1. Confirm incremental delete/archive handling removes tracked local files and state entries.
2. Add regression coverage if delete already works only accidentally.

### Task 4: Add regression tests

1. Hierarchy creation round-trip E2E:
   - create parent, child, and nested folder-like child path
   - push
   - force-pull
   - assert local layout and remote ancestry
2. Incremental remote create E2E.
3. Incremental remote update E2E.
4. Incremental remote delete E2E.

## Verification

1. `go test ./internal/sync/... -run Pull`
2. `go test ./cmd/... -run Pull`
3. `make test`

## Commit

Use one section commit, for example:

`fix(pull): reconcile incremental remote create and update events`
