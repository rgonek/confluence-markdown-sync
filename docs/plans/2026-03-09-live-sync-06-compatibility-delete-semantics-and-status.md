# Live Sync Follow-Up 06: Compatibility, Delete Semantics, and Status

**Goal:** Surface folder fallback causes clearly, align delete wording with actual remote archive behavior, and add an attachment-aware status path if feasible.

**Covers:** `F-003`, P1 items `2`, `3`, `6`, E2E items `3`, `7`

**Specs to update first if behavior changes:**
- `openspec/specs/compatibility/spec.md`
- `openspec/specs/push/spec.md`

**Likely files:**
- `internal/sync/folder_fallback.go`
- `internal/sync/tenant_capabilities.go`
- `internal/sync/push_folder_logging.go`
- `internal/sync/push_folder_logging_test.go`
- `cmd/status.go`
- `internal/sync/status.go`
- `cmd/status_test.go`
- `cmd/push.go`
- `cmd/e2e_test.go`
- `README.md`
- `docs/compatibility.md`
- `docs/usage.md`
- `docs/automation.md`

## Required outcomes

1. Folder fallback diagnostics distinguish unsupported capability from upstream endpoint failure.
2. CLI/docs describe current delete behavior accurately as archive semantics if that remains true.
3. If implemented, `status` gains a documented asset-drift mode or equivalent attachment-aware inspection path.

## Suggested implementation order

### Task 1: Improve folder fallback diagnostics

1. Trace capability probing and warning emission.
2. Preserve compatibility fallback but surface the underlying cause clearly.
3. Add tests covering unsupported-vs-upstream-failure cases.

### Task 2: Align delete semantics

1. Confirm whether current remote delete behavior is archive-only.
2. Update command wording, diagnostics, and docs to match actual behavior.
3. Add E2E coverage that asserts the intended remote state.

### Task 3: Evaluate attachment-aware status

1. Inspect whether current status plumbing can surface attachment drift without large redesign.
2. If feasible, add a status mode or flag.
3. Otherwise, document a narrower operator workflow and capture the feature gap explicitly.

## Verification

1. `go test ./internal/sync/... -run "Folder|Status"`
2. `go test ./cmd/... -run "Status|Push"`
3. `make test`

## Commit

Use one section commit, for example:

`feat(compat): surface folder fallback cause and archive semantics`
