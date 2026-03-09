# Live Sync Follow-Up 07: Sandbox Baseline and Release Checklist

**Goal:** Reduce baseline-noise risk, codify the release checklist, and ensure the expanded live-sandbox E2E coverage is treated as a production-readiness gate.

**Covers:** `F-001`, P2 items `1`, `2`, `3`, E2E item `20`

**Specs to update first if behavior changes:**
- `openspec/project.md`
- `openspec/specs/pull-and-validate/spec.md`
- `openspec/specs/push/spec.md`

**Likely files:**
- `cmd/e2e_test.go`
- `Makefile`
- `README.md`
- `docs/automation.md`
- `docs/usage.md`
- `docs/compatibility.md`
- `docs/test-logs/2026-03-09-live-sync-test-log.md`
- `docs/specs/README.md`
- `docs/specs/prd.md`
- `docs/specs/technical-spec.md`

## Required outcomes

1. Baseline live-sandbox warnings are either cleaned up or explicitly allowlisted/documented.
2. The live-sync verification flow becomes a repeatable release checklist.
3. The expanded live-sandbox E2E suite is wired into the repo workflow and documented as a release gate.
4. Cleanup parity is verified at the end of the workflow.

## Suggested implementation order

### Task 1: Define baseline warning policy

1. Decide whether to clean the sandbox seed content or codify an allowlist.
2. Document the expected baseline warnings precisely if cleanup is not immediate.

### Task 2: Promote the log into a checklist

1. Convert the one-off live test log into an operator-ready release procedure.
2. Include setup, workflow, failure triage, cleanup, and expected artifacts.

### Task 3: Wire release gating and cleanup checks

1. Ensure `make test` or documented release workflow includes the expanded E2E coverage path.
2. Add or document cleanup-parity verification:
   - `git status` clean
   - `conf status` clean
   - temporary content removed

## Verification

1. `make test`
2. `make lint`
3. Review docs for alignment with updated OpenSpec text

## Commit

Use one section commit, for example:

`docs(release): codify live sync checklist and sandbox baseline policy`
