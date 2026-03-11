# Live Sync Follow-Up 04: Conflict Recovery and Operator Guidance

**Goal:** Make `--on-conflict=pull-merge` lossless, keep `cancel` recovery artifacts reliable, and improve post-failure recovery guidance.

**Covers:** `F-008`, P1 item `5`, E2E items `17`, `18`, `19`

**Specs to update first if behavior changes:**
- `openspec/specs/push/spec.md`
- `openspec/specs/recovery-and-maintenance/spec.md`

**Likely files:**
- `cmd/push.go`
- `cmd/push_stash.go`
- `cmd/pull_stash.go`
- `cmd/recover.go`
- `cmd/push_conflict_test.go`
- `cmd/push_stash_test.go`
- `cmd/recover_test.go`
- `cmd/push_recovery_metadata_test.go`
- `cmd/e2e_test.go`
- `README.md`
- `docs/automation.md`
- `docs/usage.md`

## Required outcomes

1. `--on-conflict=pull-merge` never silently drops local edits.
2. The command preserves edits via merge result, conflict markers, or explicit recoverable state.
3. `--on-conflict=cancel` reliably retains sync branch and snapshot refs.
4. Failed-push output tells the operator exactly what to run next.

## Suggested implementation order

### Task 1: Audit stash and conflict flow

1. Trace how local edits are captured before conflict-triggered pull.
2. Identify where the stash is discarded today.
3. Change the flow so the stash is only dropped after a successful, reviewed outcome.

### Task 2: Improve recovery UX

1. Inspect retained metadata and current CLI messaging.
2. Print concrete follow-up commands for `recover`, branch inspection, and cleanup.

### Task 3: Add regression tests

1. Unit/command tests for stash preservation and non-destructive conflict handling.
2. E2E for `--on-conflict=cancel`.
3. E2E for `--on-conflict=pull-merge` ensuring local edits survive.
4. E2E for recovery command flow after an intentional failed push.

## Verification

1. `go test ./cmd/... -run "Conflict|Recover|Stash"`
2. `make test`

## Commit

Use one section commit, for example:

`fix(push): preserve local edits during pull-merge conflicts`
