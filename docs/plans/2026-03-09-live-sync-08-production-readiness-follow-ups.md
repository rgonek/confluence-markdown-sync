# Live Sync Follow-Up 08: Production Readiness and Workflow Smoothness

**Goal:** Turn the live `TD2`/`SD2` workflow findings into a concrete hardening plan for production readiness.

**Covers:** live sandbox findings from 2026-03-09, release readiness gaps, operator workflow friction

**Specs to update first if behavior changes:**
- `openspec/specs/push/spec.md`
- `openspec/specs/pull-and-validate/spec.md`
- `openspec/specs/compatibility/spec.md`
- `openspec/specs/recovery-and-maintenance/spec.md`

**Likely files:**
- `internal/confluence/metadata.go`
- `internal/confluence/metadata_test.go`
- `internal/sync/push.go`
- `internal/sync/push_page.go`
- `internal/sync/push_rollback.go`
- `internal/sync/folder_fallback.go`
- `internal/sync/tenant_capabilities.go`
- `internal/sync/status.go`
- `cmd/push.go`
- `cmd/pull.go`
- `cmd/recover.go`
- `cmd/e2e_test.go`
- `README.md`
- `docs/usage.md`
- `docs/automation.md`
- `docs/compatibility.md`

## Production-readiness assessment

Current state is not production-ready.

What worked in the live run:

1. New page creation and follow-up pulls worked on the happy path.
2. Hierarchy creation worked through page-based compatibility fallback.
3. Task lists round-tripped correctly.
4. `plantumlcloud` round-tripped as a Confluence extension.
5. Mermaid was preserved correctly as an ADF `codeBlock`.
6. Attachment upload and file-scoped attachment deletion worked.
7. Remote create/update/delete via API reconciled locally on pull.
8. Conflict detection and recovery artifacts worked.

Why it is not yet production-ready:

1. `status` frontmatter can break a real push after remote mutation has begun.
2. Delete/archive workflow is not reliable enough under long-task stalls.
3. New-page duplicate-title diagnostics are not actionable enough.
4. Concurrent workspace mutation appears unsafe.
5. Space-scoped pushes are too brittle in partially-broken sandbox spaces.

## Findings to address

### P0: Fix content-status writes

Live finding:

- A real push failed after creating a page because Confluence rejected the content-status request with `color in body of content state must be a 6 hex digit color`.

Required outcome:

1. Pushing frontmatter `status` must not emit an invalid color payload.
2. Page create/update with content status must succeed or fail before remote mutation starts.
3. Regression coverage must include live-like create and update flows.

Suggested tasks:

1. Inspect the content-status request builder in `internal/confluence/metadata.go`.
2. Determine whether color should be omitted, normalized, or mapped from lozenge names.
3. Add tests covering known values like `Ready to review`.
4. Add rollback-path coverage for metadata failures after page creation.

### P0: Harden archive/delete workflow

Live finding:

- A page-delete push in `SD2` timed out waiting on archive long task `21692454`, which stayed `ENQUEUED` for more than 2 minutes.

Required outcome:

1. Archive progress and timeout behavior must be clearer and more reliable.
2. The CLI must distinguish “still running remotely” from “definitely failed”.
3. Operators must get precise next steps when Confluence stalls.

Suggested tasks:

1. Audit archive long-task polling and timeout handling.
2. Add a follow-up verification read before classifying the operation as failed.
3. Improve operator guidance around `--archive-task-timeout`.
4. Add E2E coverage for delayed archive completion and stalled-task behavior.

### P1: Improve duplicate-title diagnostics for new pages

Live finding:

- A create attempt failed with `A page with this title already exists`, but a direct current-page lookup by title did not reveal the conflicting page.

Required outcome:

1. New-page title collisions should produce actionable diagnostics.
2. The command should search broader visibility states before giving up.

Suggested tasks:

1. Inspect create-path diagnostics and any pre-create existence checks.
2. Check current, archived, and draft visibility paths where possible.
3. Include conflicting page ID/status/title context in errors when discoverable.
4. Add tests for hidden-collision cases.

### P1: Add workspace locking for mutating commands

Live finding:

- Parallel pulls against the same workspace produced an invalid-repo-style failure until rerun sequentially.

Required outcome:

1. `pull` and `push` must not run concurrently against the same workspace.
2. Operators should get a clear lock/conflict message instead of incidental git or filesystem failures.

Suggested tasks:

1. Add a repo-scoped lock for mutating commands.
2. Fail fast with a clear message if another sync is active.
3. Document the constraint in automation docs.

### P1: Improve non-interactive conflict guidance

Live finding:

- `--on-conflict=pull-merge --non-interactive` preserved edits via conflict markers, but still failed after pull because no keep-local/keep-remote/keep-both decision could be made automatically.

Required outcome:

1. Non-interactive conflict outcomes should be easier to understand and recover from.
2. The CLI should clearly state what was preserved and what the operator must do next.

Suggested tasks:

1. Review messaging after `pull-merge` stops on unresolved file conflicts.
2. Print explicit “resolve file, git add, rerun push” instructions.
3. Consider a clearer machine-readable report for automation.

### P2: Make compatibility fallback more visible

Live finding:

- Folder API calls repeatedly returned HTTP 500, and the workflow silently relied on page-based compatibility mode after a warning line.

Required outcome:

1. Capability fallback must be obvious in summaries and docs.
2. Operators should know whether they are seeing unsupported behavior or a tenant outage.

Suggested tasks:

1. Promote fallback cause to the final push summary.
2. Persist fallback reason in structured JSON reports.
3. Document the operational meaning in `docs/compatibility.md`.

### P2: Reduce space-wide validation blast radius

Live finding:

- A space-scoped deletion attempt in `TD2` was blocked by unrelated unresolved links elsewhere in the same sandbox space.

Required outcome:

1. Operators need a clearer path for narrow destructive changes in imperfect spaces.
2. The product should either keep strict whole-space validation with very explicit guidance or provide a safer scoped alternative.

Suggested tasks:

1. Re-evaluate whether all space-scoped pushes must validate every in-scope page for all mutation types.
2. If behavior stays the same, improve operator messaging and docs.
3. If behavior changes, update specs first and add regression coverage.

### P2: Improve attachment-path churn messaging

Live finding:

- Local source assets were normalized into `assets/<page-id>/...`, which is correct but causes visible path churn on first push/pull.

Required outcome:

1. Operators should understand asset relocation before it happens.
2. Diagnostics should explain whether the rename is expected and stable.

Suggested tasks:

1. Review `ATTACHMENT_PATH_NORMALIZED` wording.
2. Mention first-push asset relocation more clearly in docs.
3. Add examples showing pre-push vs post-pull asset layout.

### P2: Add a sandbox health preflight story

Live finding:

- Several failures were caused or amplified by tenant behavior: folder API 500s, archive long-task stalls, and possibly hidden title collisions.

Required outcome:

1. Operators should be able to assess tenant/sandbox health before a risky push.

Suggested tasks:

1. Evaluate a lightweight `doctor` or `push --preflight` enhancement that checks folder capability, archive responsiveness, and common API health signals.
2. Surface environment constraints distinctly from local-content validation failures.

## Suggested implementation order

### Task 1: Fix release-blocking push bugs

1. Fix content-status payload generation.
2. Harden archive/delete long-task handling.
3. Add targeted regression coverage for both.

### Task 2: Improve diagnostics and recovery UX

1. Fix duplicate-title diagnostics.
2. Improve non-interactive conflict instructions.
3. Add workspace locking for mutating commands.

### Task 3: Improve operator smoothness

1. Promote compatibility fallback visibility.
2. Clarify attachment normalization and scoped-vs-space validation behavior.
3. Consider sandbox-health checks in preflight/doctor flows.

## Verification

1. `go test ./internal/confluence/... -run "Metadata|Archive|Title"`
2. `go test ./internal/sync/... -run "Push|Rollback|Folder|Capability"`
3. `go test ./cmd/... -run "Push|Pull|Recover|Doctor"`
4. `make test`
5. `make test-e2e`

## Commit

Use one section commit, for example:

`docs(plan): capture live production-readiness follow-ups`
