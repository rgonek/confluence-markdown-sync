# Live Workflow Polish Follow-up Plan

## Objective

Capture the non-blocking but high-value workflow, diagnostics, and operator-experience improvements noticed during the March 5, 2026 live TD2/SD2 validation run.

## Relationship To Other Plans

- This plan complements `agents/plans/2026-03-05-live-workflow-findings-remediation.md`.
- The remediation plan covers production blockers.
- This plan covers workflow smoothness, clarity, and maintainability improvements that should follow once the blocking correctness issues are under control.

## Implementation Progress

- [x] Batch 1 completed: items 1, 2, 5, and 7 are closed with regression coverage on this branch.
- [x] Batch 2 completed: items 3, 6, 12, and 13 are closed with regression coverage on this branch.
- [x] Batch 3 completed: items 10, 11, and 18 are closed, and follow-up hardening landed for items 6, 12, and 13.
- [x] Batch 4 completed: items 4 and 9 are closed with warning-taxonomy regression coverage and explicit extension-support documentation updates.
- [x] Item 8 was re-verified as already complete on this branch.
- [ ] Remaining items: 14, 15, 16, and 17.

## Improvements

### 1. Non-Interactive `init` Should Respect Existing Environment Auth

#### Problem

`conf init` still prompts whenever `.env` is missing, even if `ATLASSIAN_DOMAIN`, `ATLASSIAN_EMAIL`, and `ATLASSIAN_API_TOKEN` are already present in the environment.

#### Plan

- Update `cmd/init.go` so `ensureDotEnv` first checks resolved config inputs from the environment.
- If all required auth values are already available, scaffold `.env` directly without prompting.
- Preserve prompting only when values are genuinely missing.
- Keep interactive behavior unchanged for human-driven onboarding.

#### Validation

- Add command tests for:
  - env-backed non-interactive init with no `.env`
  - partially populated env that still requires prompting
  - existing `.env` unchanged path

### 2. Clarify `status` Semantics Around Asset Drift

#### Problem

`conf status` can report a clean space while Git still shows deleted or modified asset files. That makes it look more complete than it really is.

#### Plan

- Decide whether `status` should:
  - remain Markdown/page-focused only, or
  - gain explicit asset drift reporting
- If status remains page-focused:
  - document that clearly in command help and docs
  - surface a short note when asset drift exists but is excluded
- If asset drift is added:
  - report added/modified/deleted assets separately from Markdown pages
  - avoid conflating local asset filesystem changes with remote attachment drift unless both are actually computed

#### Validation

- Add command tests covering spaces with:
  - page-only changes
  - asset-only changes
  - mixed page and asset changes

### 3. Improve `diff` Readability For Metadata Drift

#### Problem

`conf diff` is useful, but labels and other synced frontmatter differences are not always easy to interpret as metadata drift versus content drift.

#### Plan

- Review how frontmatter is rendered in diff output for:
  - labels
  - created/updated metadata
  - status/state metadata
- Consider a lightweight metadata summary ahead of the textual diff for file mode and space mode.
- Ensure labels are rendered deterministically and comparisons are easy to scan.

#### Validation

- Add diff tests for:
  - label-only changes
  - metadata-only remote changes
  - content-only changes
  - combined metadata and content changes

### 4. Tighten Warning Taxonomy And Signal Quality

#### Problem

Warnings currently blur together:

- acceptable preserved absolute Confluence links
- degraded-but-tolerable pull output
- actually broken references that need user action

#### Plan

- Introduce clearer warning classes for:
  - preserved external/cross-space links
  - unresolved but safely degraded references
  - broken strict-path references that block push
- Review pull and diff warnings so preserved absolute links do not look like failures.
- Make diagnostics more actionable by including whether a warning requires user intervention.

#### Validation

- Add tests proving:
  - preserved absolute Confluence links do not emit misleading unresolved warnings
  - real unresolved references still emit actionable diagnostics

### 5. Improve Push Summaries For Attachment Activity

#### Problem

Push summaries center on page counts and diagnostics, but attachment creation, deletion, and preservation are not surfaced clearly enough for operators.

#### Plan

- Expand push summary output to report:
  - attachments uploaded
  - attachments deleted
  - attachments preserved
  - attachment operations skipped due to safety fallbacks
- Keep the summary concise, but ensure attachment-affecting pushes are visibly different from text-only pushes.

#### Validation

- Add summary coverage for:
  - upload-only push
  - delete-only push
  - mixed page + attachment push
  - keep-orphan-assets behavior

### 6. Reduce Folder Fallback Warning Noise

#### Problem

Confluence returned repeated HTTP 500 responses for `GET /wiki/api/v2/folders`, but fallback behavior worked. The warnings are currently too noisy for an operator who only needs to know that fallback mode was engaged.

#### Plan

- De-duplicate repeated folder API fallback warnings within a single run.
- Include one concise explanation that fallback-to-pages mode is active.
- Preserve detailed response info in verbose or debug logs only.

#### Validation

- Add logging tests ensuring repeated folder-list failures produce one high-signal operator warning, not repeated noise.

### 7. Extend `clean` To Handle Stale `sync/*` Branches

#### Problem

`conf clean` removes snapshot refs but does not remove stale `sync/*` branches from failed runs.

#### Plan

- Extend cleanup logic so safe stale `sync/*` branches are pruned when:
  - current branch is not one of them
  - no linked worktree remains
  - recovery refs are gone or marked safe to remove
- Improve summary output so branch cleanup is explicit.

#### Validation

- Add tests for:
  - stale sync branches cleaned successfully
  - active sync branch preserved
  - no-op cleanup on already-clean repo

### 8. Normalize Paths More Aggressively On Windows

#### Problem

State and hierarchy indexes showed inconsistent separator styles during the live run, especially in `folder_path_index`.

#### Plan

- Audit path normalization in:
  - state serialization
  - path indexes
  - hierarchy planners
  - diagnostics
- Standardize on a single normalized slash style for persisted state files.
- Add regression coverage for mixed-separator inputs on Windows paths.

#### Validation

- Add tests for:
  - `page_path_index` normalization
  - `folder_path_index` normalization
  - mixed slash inputs during pull/push/state save cycles

### 9. Document Extension And Macro Support Explicitly

#### Problem

The user-facing support contract for extensions is not explicit enough. The live run showed real differences between PlantUML and Mermaid support.

#### Plan

- Add a support matrix to docs covering at least:
  - PlantUML
  - Mermaid
  - raw ADF extension preservation
  - unknown Confluence macros/extensions
- Clarify whether each item is:
  - rendered round-trip support
  - preserved-but-not-rendered
  - unsupported

#### Validation

- Update `README.md`, `docs/usage.md`, and `AGENTS.md` where applicable.
- Ensure docs align with actual tested behavior.

### 10. Add A Repeatable Live Sandbox Smoke-Test Workflow

#### Problem

The real validation workflow is possible, but it is still too manual and easy to execute inconsistently.

#### Plan

- Create a documented sandbox smoke-test workflow for explicit non-production spaces.
- Include:
  - workspace bootstrap
  - pull/edit/validate/diff/push/pull cycle
  - conflict simulation steps
  - cleanup expectations
- Consider a helper script or automation entrypoint that runs the safe parts end to end against sandbox-configured spaces only.

#### Validation

- Provide a reproducible operator runbook.
- Optionally add a gated manual automation target for sandbox-only live verification.

### 11. Add Tenant Capability Detection And Adaptive Fallbacks

#### Problem

Some Confluence APIs behaved differently than expected during the live run, especially folder APIs and metadata-related paths. Today, `conf` discovers those gaps reactively during a push or pull instead of choosing a stable execution mode up front.

#### Plan

- Add lightweight tenant capability probing for features that materially affect sync behavior, such as:
  - folders API reliability
  - content-status metadata support
  - any other known optional or flaky API paths
- Cache capability results for the duration of a run.
- Use those results to choose execution modes deliberately before mutation starts.
- Surface a concise operator summary when `conf` enters degraded or compatibility mode.

#### Validation

- Add tests that simulate unsupported or flaky endpoints and verify deterministic fallback mode selection.
- Ensure capability probing never causes remote writes.

### 12. Strengthen `doctor` For Semantic Sync Corruption

#### Problem

`doctor` currently focuses on structural consistency, but it does not detect some meaningful semantic corruption cases that surfaced in the live run, such as unresolved media placeholders or hierarchy shape drift.

#### Plan

- Extend `doctor` to detect:
  - `UNKNOWN_MEDIA_ID` placeholders
  - unresolved embedded-content placeholders
  - stale `sync/*` recovery branches
  - hierarchy layout that violates documented parent-page conventions
  - other known degraded round-trip states
- Classify findings by severity and whether `--repair` can safely fix them.
- Keep repairs conservative; diagnostics are better than destructive auto-fixes.

#### Validation

- Add `doctor` tests for each known degraded state found in the live run.
- Add command coverage for both report-only and `--repair` modes.

### 13. Add A Guided Recovery Flow For Failed Pushes

#### Problem

Failed pushes leave retained refs and branches for safety, but there is no guided recovery workflow beyond partial cleanup. That makes recovery more manual than it should be.

#### Plan

- Introduce a `conf recover` flow, or expand `clean` substantially, so users can:
  - inspect retained failed sync runs
  - see why a run failed
  - choose to retry, discard, or clean retained recovery artifacts
- Tie recovery output to existing snapshot refs, sync branches, and worktree metadata.
- Keep recovery non-destructive by default.

#### Validation

- Add integration tests for:
  - failed push recovery inspection
  - cleanup of abandoned recovery artifacts
  - safe handling when the current branch is itself a recovery branch

### 14. Add Structured Run Reports

#### Problem

Live verification is currently hard to automate consistently because command output is human-readable but not easy to compare mechanically across runs.

#### Plan

- Add an optional machine-readable run report output such as `--report-json` for:
  - `pull`
  - `push`
  - `validate`
  - `diff`
- Include:
  - diagnostics
  - mutated files/pages
  - attachment operations
  - fallback modes entered
  - recovery artifacts created
  - timing and run IDs
- Keep human-readable output unchanged by default.

#### Validation

- Add golden-style tests for report shape stability.
- Ensure JSON reports remain usable in both success and failure paths.

### 15. Define A Clear Path Stability And Rename Policy

#### Problem

Path churn is still somewhat implicit. Renames, hierarchy changes, and sanitized title changes can move files in ways that are not always obvious to operators and can break local links or expectations.

#### Plan

- Define and document how `conf` handles:
  - title changes
  - page moves
  - folder moves
  - sanitization-driven path changes
- Consider whether alias tracking, rename diagnostics, or path-history metadata are needed.
- Ensure path changes are visible in `diff`, `pull`, and status/recovery diagnostics.

#### Validation

- Add tests for:
  - title rename round-trips
  - hierarchy move round-trips
  - sanitized path changes on pull

### 16. Strengthen Push Preflight And Release Messaging

#### Problem

Some failures are still surprising because capability mismatches or degraded behavior are only discovered during execution. Also, the current maturity level should be communicated more explicitly.

#### Plan

- Expand preflight so it can optionally report:
  - remote capability concerns
  - exact planned page and attachment mutations
  - known degraded modes before write execution
- Review release docs, README language, and versioning guidance so the product is clearly labeled beta until blocker workstreams are done.
- Keep maturity messaging aligned with actual tested behavior.

#### Validation

- Add preflight coverage for degraded-mode reporting.
- Update release-facing docs to match the current maturity contract.

### 17. Align Generated `AGENTS.md` With Actual Workflow And Documentation Strategy

#### Problem

The generated `AGENTS.md` scaffolding is no longer fully aligned with the current codebase, the live-tested behavior, or the desired documentation process:

- it still splits usage into human-in-the-loop and autonomous modes, even though one general workflow is sufficient
- it still refers to `space` as a normal frontmatter key users must not edit, which is stale relative to the current frontmatter model
- technical templates overstate support for Mermaid and relative cross-space links
- it does not explain the intended direction that generated Specs/PRDs should become the working source of truth for feature behavior and product intent

#### Plan

- Update generated workspace and space-level `AGENTS.md` templates in:
  - `cmd/init.go`
  - `cmd/agents.go`
- Replace the split workflow sections with one general recommended workflow:
  - `pull -> edit -> validate -> diff -> push`
  - mention that humans may still review or approve specific steps, but do not model that as a separate mode
- Remove stale frontmatter guidance and align template language with the real model:
  - `id` remains immutable
  - `version` remains sync-managed
  - `state`, `status`, and `labels` remain user-editable
  - do not present `space` as a normal active frontmatter field
- Add a concise support-contract note or link to docs covering:
  - same-space links
  - cross-space links
  - attachments
  - PlantUML
  - Mermaid
  - hierarchy behavior
- Add explicit guidance that, based on the current codebase and existing plans, new Specs/PRDs should be generated and maintained as the intended source of truth, or at minimum the closest maintained product-definition artifact until the larger planning/doc system is consolidated.
- Ensure generated `AGENTS.md` points readers to the primary plan and any future Specs/PRDs when behavior or requirements are unclear.

#### Validation

- Add golden-style tests for generated `AGENTS.md` output.
- Verify generated templates do not mention the old split workflow model.
- Verify generated templates align with current frontmatter behavior and current documented support boundaries.

### 18. Harden `conf search` Correctness, Backend Parity, And Documentation

#### Problem

The `search` feature is useful, but it still has several correctness and productization gaps:

- the current source exposes `search`, but the checked-in `conf.exe` binary is stale and does not
- README and usage docs are not fully aligned with the implemented flags and behavior
- `--reindex` emits plain-text progress output even in JSON mode, which is unfriendly for automation
- post-`pull` search index updates currently hardcode SQLite instead of respecting configured backend choice
- the indexing model appears vulnerable to stale hits when Markdown files are deleted locally, because existing indexed paths are only purged when the file is still encountered during indexing

#### Plan

- Fix release/build hygiene so shipped binaries always match the current command set.
- Align `README.md`, `docs/usage.md`, and generated `AGENTS.md` with the current `search` feature set, including:
  - command availability
  - engine selection
  - filter semantics
  - `--result-detail`
  - local-only / zero-API-call behavior
- Change search progress reporting so machine-readable output stays clean:
  - send progress to stderr, or
  - suppress it unless explicitly requested
- Make post-`pull` index updates honor the configured search backend instead of always writing SQLite state.
- Add indexed-path deletion reconciliation so removed Markdown files do not remain searchable after incremental updates or pull-triggered partial indexing.
- Review backend parity between SQLite and Bleve for:
  - query results
  - label/space facets
  - date filters
  - incremental update semantics
- Add stronger release coverage so command registration drift is caught before shipping.

#### Validation

- Add tests for:
  - deleted Markdown file removed from search results after update
  - JSON output remains valid when `--reindex` is used
  - configured Bleve backend is respected by post-`pull` indexing
  - README/help/docs stay aligned with command registration
- Add parity-oriented tests where SQLite and Bleve should return equivalent results for the same fixture set.

## Suggested Order

1. `init` env-aware bootstrap
2. warning taxonomy cleanup
3. push summary improvements
4. `clean` sync-branch cleanup
5. Windows path normalization
6. `status` asset semantics
7. `diff` metadata clarity
8. extension support matrix docs
9. live sandbox smoke-test workflow
10. folder fallback warning de-duplication
11. tenant capability detection
12. stronger `doctor`
13. guided recovery flow
14. structured run reports
15. path stability policy
16. preflight and release messaging
17. generated `AGENTS.md` alignment and Specs/PRDs guidance
18. `search` correctness, backend parity, and documentation

## Success Criteria

- Operators can bootstrap a workspace non-interactively when credentials are already present in env vars.
- `status`, `diff`, and warning output better reflect what is actually happening without misleading clean/dirty signals.
- Push summaries clearly report attachment activity.
- Cleanup removes all safe stale recovery artifacts, including stale sync branches.
- Persisted state uses stable normalized paths on Windows.
- Docs clearly explain extension support behavior and sandbox verification workflow.
- `doctor` can identify meaningful degraded states from real failed round-trips.
- Operators have a guided recovery path after failed pushes.
- Commands can emit structured run reports for live verification and automation.
- Preflight makes degraded modes and risky capabilities visible before remote writes start.
- Generated `AGENTS.md` scaffolding reflects one general workflow, current product constraints, and the intended Specs/PRDs documentation direction.
- `search` behaves consistently across backends, does not leave stale deleted-file hits behind, and remains automation-friendly in JSON mode.

## P2 Backlog

These items are worth doing, but should remain behind the blocker remediation plan and the main polish follow-ups.

### 19. Add Release Gating With Live Sandbox Smoke Tests

#### Problem

Current quality signals are still too synthetic to fully protect releases from workflow regressions that only appear against live Confluence tenants.

#### Plan

- Require an explicit sandbox live smoke-test check before promoting release candidates.
- Keep it gated to sandbox-configured spaces only.
- Separate release-blocking live checks from ordinary developer CI to avoid accidental production-space execution.

### 20. Add Upgrade And Migration Coverage For Older Workspaces

#### Problem

Fixes to state files, hierarchy layout, and metadata handling may unintentionally break existing user workspaces created by older versions.

#### Plan

- Add migration fixtures for older `.confluence-state.json` and markdown layouts.
- Verify pull/push/status/doctor behavior stays safe after upgrade.
- Document any migration semantics if automatic normalization changes persisted files.

### 21. Make `--dry-run` Closer To Real Execution

#### Problem

`--dry-run` is useful, but it should validate more of the real execution path so operators can trust it as a genuine preflight.

#### Plan

- Validate final payload shape, attachment mutation plan, and cleanup plan in dry-run mode.
- Show the exact remote operations that would occur, including page/archive/attachment changes.
- Preserve the guarantee that no local or remote state is mutated.

### 22. Add Read-Only Inspection For Recovery Artifacts

#### Problem

Even before a full `recover` workflow exists, operators need an easy way to inspect failed-run artifacts without dropping into Git internals.

#### Plan

- Add a read-only inspection command or submode to list:
  - retained `sync/*` branches
  - snapshot refs
  - failed run timestamps
  - associated failure reasons when available

### 23. Improve No-Op Explainability

#### Problem

No-op runs succeed quietly, but they often do not explain why nothing changed, which makes troubleshooting harder.

#### Plan

- Improve no-op output for `pull`, `push`, and `clean` so it states why the command was a no-op.
- Distinguish cases such as:
  - no local changes
  - no remote changes
  - changes existed but were intentionally skipped

### 24. Add Performance And Scale Tests

#### Problem

Live validation covered correctness, but not scale. Large spaces, pagination stress, and attachment-heavy pages may still expose bottlenecks or edge-case failures.

#### Plan

- Add scale-oriented tests for:
  - larger page counts
  - attachment-heavy pages
  - long pagination chains
  - rate-limit and retry pressure

### 25. Strengthen Destructive Operation Previews

#### Problem

Archive/delete pushes should make the exact destructive target set obvious before execution.

#### Plan

- Expand preflight and confirmation flows to show exact pages and attachments that will be archived or deleted.
- Keep summaries concise, but make destructive targets explicit.

### 26. Add A Feature/Tenant Compatibility Matrix

#### Problem

Operators need a clearer understanding of what behavior is guaranteed, what is best-effort, and what depends on tenant capability.

#### Plan

- Publish a compatibility matrix covering:
  - core sync features
  - macro/extension support
  - tenant capability dependencies
  - degraded fallback modes

### 27. Add Changelog Discipline For Sync Semantics

#### Problem

Behavior changes in sync semantics are especially important to operators, but they are easy to lose in generic release notes.

#### Plan

- Track user-visible sync behavior changes explicitly in changelog or release-note guidance.
- Highlight changes to:
  - hierarchy rules
  - attachment handling
  - validation strictness
  - cleanup/recovery semantics

### 28. Add Sanitized Golden Live Fixtures

#### Problem

Synthetic fixtures are not catching enough real-world edge cases from Confluence content.

#### Plan

- Build a sanitized fixture corpus from real pulled pages.
- Use it for round-trip, pull, push, and diff regression tests.
- Keep private or tenant-specific details removed while preserving structure that triggered bugs in the live run.
