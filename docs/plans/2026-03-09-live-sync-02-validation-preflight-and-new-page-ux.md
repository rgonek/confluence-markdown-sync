# Live Sync Follow-Up 02: Validation, Preflight, and New-Page UX

**Goal:** Make `validate`, `push --preflight`, and real `push` use the same validation scope, and provide a usable preview path for brand-new files without `id`.

**Covers:** `F-002`, `F-005`, E2E items `1`, `8`

**Specs to update first if behavior changes:**
- `openspec/specs/push/spec.md`
- `openspec/specs/pull-and-validate/spec.md`

**Likely files:**
- `cmd/push.go`
- `cmd/validate.go`
- `cmd/diff.go`
- `cmd/diff_pages.go`
- `cmd/push_test.go`
- `cmd/validate_test.go`
- `cmd/diff_test.go`
- `cmd/diff_extra_test.go`
- `cmd/e2e_test.go`
- `README.md`
- `docs/usage.md`

## Required outcomes

1. `push --preflight` fails whenever real `push` would fail validation.
2. Space-scoped deletions that break links in otherwise unchanged files are caught consistently.
3. `conf diff <new-file.md>` either supports new-page preview or emits explicit guidance toward the supported preview path.
4. Tests lock the parity requirement in place.

## Suggested implementation order

### Task 1: Unify validation scope

1. Compare `validate`, preflight, and real push target expansion.
2. Refactor to a shared scope/planning helper instead of maintaining separate logic.
3. Ensure delete-driven broken links are evaluated before any remote write.

### Task 2: Fix new-page diff/preflight UX

1. Inspect file-scoped diff behavior for missing `id`.
2. Choose one path:
   - implement new-page diff mode, or
   - keep diff strict but emit an actionable message that points to `push --preflight`
3. Update docs and tests to match the chosen behavior.

### Task 3: Add regression tests

1. Command tests for validation/preflight parity.
2. Command or E2E tests for a broken-link scenario introduced by deleting a referenced page.
3. Tests for brand-new file behavior:
   - `validate` succeeds
   - `diff` supports preview or emits the intended guidance

## Verification

1. `go test ./cmd/... -run "Validate|Push|Diff"`
2. `make test`

## Commit

Use one section commit, for example:

`fix(push): align preflight validation with real push`
