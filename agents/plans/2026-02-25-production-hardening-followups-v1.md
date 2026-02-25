# Production Hardening Follow-up Plan (Excluding CI Sandbox E2E)

## Objective

Close the remaining production-readiness gaps identified in the recent review, excluding improvement #1 (adding sandbox E2E execution to CI). This plan focuses on stronger failure compensation, safer archive semantics, higher relink correctness, stricter label hygiene, and better operational observability.

## Explicitly Out Of Scope

- Add sandbox E2E workflow job in CI (the previously listed improvement #1).

## Assumptions

- Command model (`init`, `pull`, `push`, `validate`, `diff`, `relink`) remains unchanged.
- Backward compatibility for existing markdown/state layouts is required.
- Confluence API behavior can vary by tenant; all remote operations must stay fail-safe.
- Safety and diagnosability are prioritized over throughput.

## Workstream A: Stronger Push Compensation For Post-Update Failures (Improvement #2)

### Problem

`push` can still leave partial remote state when page content update succeeds but a later step fails (for example metadata sync), because rollback currently targets metadata, uploaded attachments, and created pages, not content reversion of existing pages.

### Plan

- Capture a pre-mutation snapshot for existing pages before `UpdatePage` mutates content:
  - current page title
  - parent ID
  - status/lifecycle state
  - ADF body
- Extend rollback tracker in `internal/sync/push.go` to include a content restore phase for existing pages.
- Add best-effort content restore that updates the page back to the captured snapshot using the current remote head version rules.
- Emit explicit diagnostics for content rollback outcomes:
  - `ROLLBACK_PAGE_CONTENT_RESTORED`
  - `ROLLBACK_PAGE_CONTENT_FAILED`
- Keep current behavior for newly created pages (hard delete on rollback).

### Validation

- Add tests that force failures after successful `UpdatePage` and verify content rollback attempt and diagnostics.
- Verify no rollback attempts happen in dry-run mode.

## Workstream B: Verify Archive Completion Before Finalizing Local State (Improvement #3)

### Problem

`ArchivePages` returns an async task ID, but push delete flow currently treats archive submission as success without polling task completion.

### Plan

- Add archive task polling support in `internal/confluence/client.go` via long-task endpoint handling.
- Extend confluence interfaces/types with archive task status model.
- Update delete path in `internal/sync/push.go` so local state/index removal and commit planning occur only after archive task succeeds.
- Add configurable timeout/backoff (defaults + env/flag wiring if needed).
- Emit clear diagnostics/errors for timeout and failure:
  - `ARCHIVE_TASK_TIMEOUT`
  - `ARCHIVE_TASK_FAILED`

### Validation

- Unit tests for task polling success/failure/timeout paths.
- Integration tests proving local delete commit is blocked when archive does not complete.

## Workstream C: Replace Regex Relink With Markdown AST Rewriter (Improvement #4)

### Problem

Current relink implementation uses a regex over raw markdown, which can rewrite links in edge cases incorrectly and does not robustly handle markdown constructs.

### Plan

- Reimplement `internal/sync/relink.go` using a markdown parser/AST traversal (Goldmark-based).
- Rewrite only actual link destinations (not code blocks/spans or non-link text).
- Preserve anchors/fragments and retain original text around links.
- Keep dry-run behavior and statistics unchanged from the CLI perspective.

### Validation

- Add relink tests for:
  - inline code and fenced code containing URL-like text
  - links with anchors and titles
  - escaped/nested bracket content
  - documents with no rewritable links
- Ensure existing command-level relink tests still pass.

## Workstream D: Harden Label Validation And Normalization (Improvement #5)

### Problem

Label handling is permissive and can allow empty/duplicate/poorly normalized values, increasing push churn and metadata inconsistencies.

### Plan

- Introduce a single canonical label normalization utility used by validate and push paths.
- Enforce schema constraints in `internal/fs/frontmatter.go`:
  - no empty labels after trim
  - no whitespace-containing labels
  - deterministic dedupe
- Apply normalized labels in metadata sync and rollback comparison logic in `internal/sync/push.go`.
- Improve validation error messages to identify offending labels clearly.
- Update docs (`README.md`, `docs/usage.md`) with exact label rules.

### Validation

- Add unit tests for normalization and invalid-label rejection.
- Add push metadata tests showing no-op behavior for equivalent label sets.

## Workstream E: Versioned User-Agent And Run-Correlation Logging (Improvement #6)

### Problem

HTTP user agent defaults to `conf/dev`, and logs do not carry a stable per-run correlation key.

### Plan

- Build a versioned UA string from CLI version in `cmd/confluence_client.go` and pass through `ClientConfig.UserAgent`.
- Preserve token/header redaction guarantees (no auth leakage in debug logs).
- Generate a per-command run ID at command start (`pull`, `push`, `diff`, `validate`) and include it in structured logs.
- Ensure retry/rate-limit logs include run ID for easier incident tracing.

### Validation

- Add tests for UA propagation and non-leak logging behavior.
- Add command tests asserting run ID is attached to key lifecycle log lines.

## Cross-Cutting Test/Quality Requirements

- Add/adjust tests for every changed invariant (unit + integration where applicable).
- Run:
  - `go test ./...`
  - `go vet ./...`
  - `go run ./tools/coveragecheck`
  - `go run ./tools/gofmtcheck`
- Keep coverage gates passing for `./cmd`, `./internal/sync`, `./internal/git`.

## Delivery Slices

1. Archive verification and async task polling.
2. Push content compensation rollback.
3. Label normalization/validation hardening.
4. AST-based relink engine.
5. Versioned UA and run-correlation logging.

Each slice should be mergeable independently with tests and docs updates.

## Implementation Progress

- [x] Slice 1: Archive verification and async task polling.
  - Added long-task polling support in the Confluence client with success/failure/timeout handling.
  - Updated push delete flow to block local state mutation until archive completion is confirmed.
  - Added diagnostics for `ARCHIVE_TASK_TIMEOUT` and `ARCHIVE_TASK_FAILED`.
  - Added unit coverage for polling behavior and integration coverage for delete blocking semantics.
- [x] Slice 2: Push content compensation rollback.
  - Added existing-page content snapshots (title, parent, status, ADF) before mutation.
  - Added rollback content restore for post-update failures with `ROLLBACK_PAGE_CONTENT_RESTORED` / `ROLLBACK_PAGE_CONTENT_FAILED` diagnostics.
  - Ensured rollback is skipped in dry-run mode and added tests for both restore and dry-run behavior.
- [x] Slice 3: Label normalization/validation hardening.
  - Added canonical label normalization in `internal/fs/frontmatter.go` (trim/lowercase/dedupe/sort).
  - Strengthened schema validation for empty and whitespace-containing labels with clearer error messages.
  - Applied normalized labels in push metadata sync + rollback comparisons and added no-op equivalence tests.
  - Updated `README.md` and `docs/usage.md` with exact label rules.
- [x] Slice 4: AST-based relink engine.
  - Replaced regex relink matching with Goldmark AST traversal to plan true link destination rewrites.
  - Added a destination-span markdown rewriter that updates only link destination tokens while preserving surrounding text.
  - Added coverage for code spans/fences, anchors + titles, escaped/nested labels, and no-op documents.
- [ ] Slice 5: Versioned UA and run-correlation logging.

## Verification Criteria

- Existing-page push failures after content update show explicit content rollback diagnostics.
- Delete/archival pushes do not finalize local state unless archive completion is confirmed.
- Relink rewrites only true markdown links and skips code text safely.
- Invalid/empty/duplicate labels are blocked by validation, and equivalent label sets do not churn on push.
- HTTP telemetry includes versioned UA and per-run correlation fields without leaking credentials.

## Risks And Mitigations

1. **Confluence long-task semantics vary by tenant**  
   Mitigation: fail safe on unknown states/timeouts, with actionable diagnostics.

2. **Content rollback can fail under concurrent edits**  
   Mitigation: best-effort revert with clear failure diagnostics and recovery guidance.

3. **AST relink implementation may change markdown formatting unexpectedly**  
   Mitigation: use targeted destination rewrites and preserve untouched source spans.

4. **Stricter label rules may break existing content unexpectedly**  
   Mitigation: clear validate errors and documented migration behavior.

5. **Additional logging fields may increase noise**  
   Mitigation: keep fields structured and concise; gate verbose details behind debug level.
