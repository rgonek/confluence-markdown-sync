# Production Readiness Remediation Plan (2026-02-25)

## Goal
Close production-readiness gaps in `conf` before broad rollout.

## Decision Log
- Confirmed invariant: pages with children use `<Page>/<Page>.md`.
- This plan treats that pathing pattern as correct and non-negotiable.

## Exit Criteria
- All P0 items completed and merged.
- CI blocks on failing lint/tests and passes on `main`.
- Docs match implemented CLI behavior and invariants.
- No tracked `.confluence-state.json` files in git history moving forward.

## P0 - Release Blockers

### P0-1: Enforce immutable frontmatter integrity in `validate`
**Problem**
- `validate` currently checks schema + strict conversion but does not enforce immutable key integrity (`id`, `space`) or `current -> draft` restriction.

**Work**
- Wire immutable checks into validation flow by comparing current file frontmatter to prior known mapping/state.
- Ensure validation fails if:
  - existing page `id` changes,
  - existing page `space` changes,
  - existing published page is set from `current` to `draft`.

**Primary files**
- `cmd/validate.go`
- `internal/fs/frontmatter.go`
- `internal/sync/push.go` (guardrails for runtime push path)

**Verification**
- Add/extend tests proving tampered `id`/`space` are blocked.
- Add test proving `current -> draft` is blocked for existing published pages.

---

### P0-2: Restore `.confluence-state.json` invariant
**Status (2026-02-25): ✅ Completed in PR-A (`phase/pr-a-state-invariant-docs`).**

**Problem**
- `.confluence-state.json` must be gitignored, but currently a tracked state file exists and push flow stages state files.

**Work**
- Add `.confluence-state.json` ignore rule at repository root.
- Stop force-staging state file in push commits.
- Remove currently tracked state file(s) from git index while preserving local file.
- Keep state as local operational metadata only.

**Primary files**
- `.gitignore`
- `cmd/push.go`
- repository index cleanup (`git rm --cached ...` in implementation PR)

**Verification**
- `git ls-files "**/.confluence-state.json"` returns no entries.
- Pull/push still function with local state updates.

---

### P0-3: Fix push snapshot/worktree fidelity
**Problem**
- Push snapshot/worktree model must include staged, unstaged, untracked, and deletions in-scope; current flow resets to `HEAD` and only restores untracked stash parent in worktree.

**Work**
- Rework snapshot materialization so worktree push runs against true captured workspace snapshot.
- Preserve and restore in-scope and out-of-scope changes exactly on success/failure paths.
- Remove assumptions that lose staged/unstaged tracked changes.

**Primary files**
- `cmd/push.go`
- `internal/git/*.go`

**Verification**
- Add integration tests for all four change types:
  - staged tracked,
  - unstaged tracked,
  - untracked,
  - deletions.
- Confirm resulting push commits reflect real snapshot content.

---

### P0-4: Make no-op push truly no-op operationally
**Problem**
- No-op push should avoid creating snapshot refs, sync branch, or worktree.

**Work**
- Move no-op detection earlier (before snapshot ref/branch/worktree creation).
- Keep behavior: no commit/merge/tag/refs for no-op runs.

**Primary files**
- `cmd/push.go`

**Verification**
- Test asserts no refs under `refs/confluence-sync/snapshots/...`, no `sync/...` branch, no push tag for no-op.

---

### P0-5: Tighten strict media/link validation behavior
**Problem**
- Strict reverse conversion should fail unresolved assets/links consistently with push behavior; placeholders can mask unresolved states.

**Work**
- Remove placeholder success path for unresolved media in strict mode.
- Ensure only valid `assets/` scoped files are accepted for upload mapping.
- Preserve strict failure semantics on unresolved refs.

**Primary files**
- `internal/sync/hooks.go`
- `internal/sync/push.go`
- `cmd/validate.go`

**Verification**
- Add tests where non-`assets/` references and missing attachment mappings fail validation.

---

### P0-6: Make pull failure cleanup state-safe
**Problem**
- Pull failure cleanup can remove `.confluence-state.json` unexpectedly.

**Work**
- Adjust cleanup to avoid deleting persisted local state blindly.
- Ensure recovery behavior does not destroy state when pull fails.

**Primary files**
- `cmd/pull.go`

**Verification**
- Add failure-path test asserting state file persistence rules.

## P1 - High-Value Hardening

### P1-1: CI gate hardening
- Make lint blocking (remove `continue-on-error` for lint in CI).
- Keep `vet` + tests as required checks.

**Files**
- `.github/workflows/ci.yml`

---

### P1-2: Docs and behavior alignment
**Status (2026-02-25): 🟡 Partially completed in PR-A (`phase/pr-a-state-invariant-docs`).**

- Update docs to reflect actual command set (`agents`, `relink` included).
- Correct CI/build snippet to `go build -o conf ./cmd/conf`.
- Align automation docs with implemented `--on-conflict` behavior, or adjust code to match docs.

**Files**
- `README.md`
- `docs/automation.md`
- `docs/usage.md`

---

### P1-3: Relink confirmation semantics
- Ensure relink confirmation is actually enforced for impactful changes.
- Pass real changed counts into safety confirmation path.

**Files**
- `cmd/relink.go`
- `cmd/automation.go`

## P2 - Release Maturity Improvements

### P2-1: Add targeted coverage gates
- Add package-specific minimums for risk-heavy modules (`cmd`, `internal/sync`, `internal/git`).
- Add first test suite for `internal/git` helpers.

### P2-2: Reduce operational complexity in large command handlers
- Continue decomposition of `cmd/push.go` and `cmd/pull.go` into clearer orchestration helpers.

### P2-3: Add release workflow
- Add explicit build-and-artifact workflow for tagged releases.

## Execution Order
1. P0-2 state invariant restoration.
2. P0-1 immutable validation enforcement.
3. P0-5 strict media/link behavior alignment.
4. P0-3 snapshot/worktree fidelity.
5. P0-4 true no-op fast path.
6. P0-6 pull cleanup safety.
7. P1 and P2 items.

## Suggested PR Breakdown
- PR-A: State invariant + docs quick fixes (`.gitignore`, tracked state cleanup, docs command/build corrections).
- PR-B: Validation integrity (`id`, `space`, state lifecycle) + tests.
- PR-C: Strict media/link behavior + tests.
- PR-D: Push snapshot/worktree/no-op lifecycle hardening + integration tests.
- PR-E: Pull failure cleanup safety + regression tests.
- PR-F: CI/lint gating + release workflow.

## Final Readiness Gate
Before production mark, run:
- `go test ./...`
- `go vet ./...`
- `golangci-lint run ./...`
- CI on clean branch with required checks enforced
