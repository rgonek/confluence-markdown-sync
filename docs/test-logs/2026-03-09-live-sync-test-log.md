# Live Sync Test Log

This document is the historical evidence from the 2026-03-09 live sandbox verification run. The maintained operator procedure now lives in `docs/automation.md` under the live sandbox release checklist, and the documented baseline warning allowlist is enforced by the live E2E suite.

- Date: 2026-03-09
- Repository: `D:\Dev\confluence-markdown-sync`
- Operator: Codex
- Scope: Real pull/push verification against sandbox Confluence spaces `TD2` and `SD2`
- Rule: All live workspace pulls/pushes run outside the repository root

## Environment

- Primary binary source: current repository build of `conf.exe`
- Test workspaces: external temporary directories under the system temp root
- Verification methods:
  - CLI workflow: `init` -> `pull` -> `validate` -> `diff` -> `push` -> `pull`
  - Direct Confluence API reads/writes for ADF and remote-conflict simulation
  - Git state inspection inside each temporary workspace

## Scenario Log

### 1. Baseline setup

- Status: Completed
- Goal: Build the current binary, create external workspaces, and perform initial pulls for `TD2` and `SD2`.
- Expected:
  - `conf init` succeeds in each workspace.
  - `conf pull <SPACE> --yes --non-interactive --skip-missing-assets --force` succeeds.
  - Each workspace contains a managed space directory with `.confluence-state.json`.
- Actual:
  - Built `conf.exe` from current source with `go build -o conf.exe ./cmd/conf`.
  - Created external sandbox root at `C:\Users\rgone\AppData\Local\Temp\conf-live-test-20260309-081114`.
  - `conf init` succeeded in both primary workspaces (`td2-a`, `sd2-a`) and scaffolded `.env` from environment variables.
  - `conf pull TD2 --yes --non-interactive --skip-missing-assets --force` succeeded and created tag `confluence-sync/pull/TD2/20260309T071147Z`.
  - `conf pull SD2 --yes --non-interactive --skip-missing-assets --force` succeeded and created tag `confluence-sync/pull/SD2/20260309T071146Z`.
  - Baseline pull warnings:
    - `TD2`: page `17727489` emitted `UNKNOWN_MEDIA_ID_UNRESOLVED`; stale attachment pruning skipped.
    - `TD2`: `Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md` emitted an unresolved fallback link to `pageId=17530900#Task-list`.
    - `TD2`: `Technical-Documentation/Live-Workflow-Test-2026-03-05/Checklist-and-Diagrams.md` emitted unresolved media reference `UNKNOWN_MEDIA_ID`.
    - `SD2`: `Software-Development/Release-Sandbox-2026-03-05.md` emitted an unresolved fallback link to `pageId=17334539`.

### 2. Page lifecycle and hierarchy

- Status: Completed with findings
- Goal: Create, update, move, and delete pages while exercising folder hierarchy and subpage behavior.
- Expected:
  - New Markdown files without `id` become new Confluence pages.
  - Parent pages with children map to `<Page>/<Page>.md`.
  - Folder/subpage changes round-trip cleanly after pull.
  - Deleted tracked Markdown pages are deleted remotely by push and removed locally on pull.
- Actual:
  - Created a new TD2 parent page `Live Workflow Test 2026-03-09` (`19136521`) with child pages `Checklist and Diagrams 2026-03-09` (`19234817`) and `Disposable Leaf 2026-03-09` (`19267585`).
  - Created a local folder path `API-Folder-2026-03-09/Endpoint-Notes-2026-03-09.md`; push created child page `Endpoint Notes 2026-03-09` (`19333121`) under a compatibility-mode surrogate page `API-Folder-2026-03-09` (`19300353`) instead of a real folder object.
  - Parent pages with children did map locally as `<Page>/<Page>.md`.
  - Deleted the disposable leaf by removing the Markdown file and pushing; the remote page moved to archived state and disappeared from tracked local state after subsequent pulls.
  - Deleted the full TD2 test subtree at the end by removing the tracked Markdown files and pushing the destructive change set.

### 3. Attachments

- Status: Completed with findings
- Goal: Add and remove referenced assets and verify remote attachment state plus local reconciliation.
- Expected:
  - Referenced local assets upload on push.
  - Deleted/unreferenced assets are removed remotely unless explicitly preserved.
  - Follow-up pull reflects attachment additions/deletions under `assets/<page-id>/`.
- Actual:
  - Added two attachments from local files:
    - `checklist-notes-20260309.txt` uploaded to page `19234817` as attachment `att19464193`.
    - `payload-20260309.json` uploaded to page `19333121` as attachment `att19300371`.
  - Push normalized both attachment paths into `assets/<page-id>/...` and rewrote the local Markdown references accordingly.
  - Direct API verification confirmed the attachments existed remotely.
  - Direct ADF verification showed the attachment references were published as `UNKNOWN_MEDIA_ID`, causing degraded pull output instead of a clean round-trip.
  - Deleted one attachment explicitly by removing the local reference/file and pushing; deleted the second attachment implicitly during page removal cleanup.

### 4. Links

- Status: Completed with findings
- Goal: Verify same-space relative Markdown links and cross-space links.
- Expected:
  - Same-space page links resolve and remain valid after push/pull.
  - Cross-space links preserve an appropriate remote URL/reference without corrupting local content.
- Actual:
  - Same-space Markdown links between the TD2 test pages resolved to Confluence page URLs in published ADF and came back as local relative Markdown links on pull.
  - Cross-space link from TD2 to SD2 was preserved as an absolute Confluence URL using page ID `19103745`.
  - Cross-space links remained readable and preserved content, but pull surfaced them as `unresolved_reference` warnings instead of a lower-severity preserved cross-space diagnostic.

### 5. Rich content

- Status: Completed
- Goal: Validate PlantUML, Mermaid, and Markdown task lists.
- Expected:
  - PlantUML round-trips as the `plantumlcloud` extension and renders remotely.
  - Mermaid emits warnings and is stored in ADF as a `codeBlock` with language `mermaid`.
  - Task lists convert to/from Confluence tasks without data loss.
- Actual:
  - Markdown task lists published as remote ADF `taskList` / `taskItem` nodes and pulled back as checkbox lists.
  - PlantUML published as a Confluence `plantumlcloud` extension. Direct ADF inspection confirmed the extension node and macro parameters were present.
  - Mermaid emitted the expected validation warning and published as an ADF `codeBlock` with `language: mermaid`.
  - Forced pull preserved both PlantUML and Mermaid source in Markdown.

### 6. Remote-change and conflict handling

- Status: Completed with findings
- Goal: Simulate a second user via direct API create/update/delete operations and verify pull/push behavior.
- Expected:
  - Remote updates trigger conflict behavior according to `--on-conflict`.
  - Pull reconciles remote creations, edits, and deletions locally.
  - Manual resolution path leads back to a successful push and clean follow-up pull.
- Actual:
  - Created page `Remote Actor Note 2026-03-09` (`19824641`) directly via API under the TD2 test parent.
  - First incremental pull updated local state but failed to materialize the new Markdown file; a forced pull later created it.
  - Updated page `19824641` directly via API to version `2`; incremental pull missed the change and required a forced pull to reconcile it locally.
  - Deleted page `19824641` directly via API; incremental pull correctly removed the local Markdown file and state entry.
  - In `sd2-b`, made a stale local edit to `Cross Space Target 2026-03-09` (`19103745`), then updated the same page remotely via API to version `3`.
  - `conf push ... --on-conflict=cancel` failed as expected and retained the sync branch plus snapshot refs for recovery.
  - `conf push ... --on-conflict=pull-merge` ran an automatic pull, but it dropped the local edit instead of merging or surfacing a conflict.
  - Manually reapplied a combined result, pushed successfully to remote version `4`, then later deleted the page during cleanup.

## Final Verification

- Primary workspaces `td2-a` and `sd2-a` ended with `git status --short` clean.
- `conf status TD2` reported no local drift, no remote drift, and no version drift.
- `conf status SD2` reported no local drift, no remote drift, and no version drift.
- Local test artifacts were removed from both primary workspaces after cleanup.
- Direct API probes for deleted test pages `19136521` and `19103745` returned `status=archived`, confirming the current delete behavior is archival rather than hard purge.

## Findings

### F-001 Baseline pull exposes unresolved remote references in sandbox content

- Type: Existing content/data issue
- Severity: Medium
- Reproduction:
  1. Run `conf pull TD2 --yes --non-interactive --skip-missing-assets --force`.
  2. Observe `UNKNOWN_MEDIA_ID_UNRESOLVED` and unresolved fallback link warnings for existing pages.
  3. Run `conf pull SD2 --yes --non-interactive --skip-missing-assets --force`.
  4. Observe unresolved fallback link warnings for existing pages.
- Expected:
  - Sandbox seed content should be internally consistent, or at minimum the warning set should be known and documented ahead of test execution.
- Actual:
  - Real pull succeeds, but baseline state already contains unresolved links/media that complicate signal when evaluating new regressions.
- Proposed resolution:
  - Clean up or recreate sandbox seed pages so live verification starts from a warning-free baseline, or maintain a documented allowlist of known sandbox warnings.

### F-002 `conf diff <new-file.md>` is not usable before first push

- Type: UX friction
- Severity: Medium
- Reproduction:
  1. Create a brand-new Markdown file in a managed space without an `id`.
  2. Run `conf validate <file>`; it succeeds.
  3. Run `conf diff <file>`.
- Expected:
  - A new file should have a preview path equivalent to "local file vs no remote page yet", or the command should redirect users toward `push --preflight`.
- Actual:
  - `conf diff` exits with `target file ... missing id`.
- Proposed resolution:
  - Support a "new page" diff mode in file scope, or emit a more actionable message that explicitly recommends `conf push <file> --preflight`.

### F-003 Folder API compatibility fallback is hiding a tenant/platform failure during push

- Type: Architectural / platform compatibility issue
- Severity: Medium
- Reproduction:
  1. Push a page in `SD2`.
  2. Observe warning log `folder_list_unavailable_falling_back_to_pages`.
  3. Inspect the underlying error for `GET /wiki/api/v2/folders?space-id=...`.
- Expected:
  - Folder capability probing should be reliable, or the operator should get a clearer surfaced explanation that the tenant folder API is broken/unavailable.
- Actual:
  - Push succeeds with diagnostic `FOLDER_COMPATIBILITY_MODE`, while the log reveals a raw `500 Internal Server Error` from Confluence’s folders endpoint.
- Proposed resolution:
  - Keep the fallback, but promote the underlying remote failure into structured diagnostics/reporting so live test logs and CI can distinguish "expected capability not present" from "tenant endpoint is currently unhealthy."

### F-004 Pushed attachment links resolve to remote `UNKNOWN_MEDIA_ID` ADF despite successful uploads

- Type: Functional bug
- Severity: High
- Reproduction:
  1. Create a new Markdown page that links to a local file attachment.
  2. Run `conf push`.
  3. Confirm the attachment upload succeeds and the file exists via `GET /wiki/api/v2/pages/{pageId}/attachments`.
  4. Fetch the page ADF via `GET /wiki/api/v2/pages/{pageId}?body-format=atlas_doc_format`.
- Expected:
  - The published ADF should reference the uploaded attachment using its real attachment/media identity so the attachment renders or links correctly.
- Actual:
  - The ADF contains `mediaInline` with `id: "UNKNOWN_MEDIA_ID"` and `__fileName: "Invalid file id - <attachment-id>"` even though the attachment exists remotely (`att19464193`, `att19300371` in this run).
- Proposed resolution:
  - Fix the reverse media/attachment conversion path so post-upload ADF is rebuilt with the resolved attachment IDs, then add a live-style regression test that asserts the remote ADF no longer contains `UNKNOWN_MEDIA_ID` after push.

### F-005 `push --preflight` validation scope does not match `validate` for space deletes

- Type: Functional / safety bug
- Severity: High
- Reproduction:
  1. Delete a page file that is still linked from another page in the same space scope.
  2. Run `conf validate <SPACE>`; it fails on the unresolved link.
  3. Run `conf push <SPACE> --preflight`.
- Expected:
  - Preflight validation should fail on the same unresolved link because a real push is required to validate before remote writes.
- Actual:
  - `validate TD2` failed on `Live-Workflow-Test-2026-03-09.md` due to the broken `Disposable-Leaf-2026-03-09.md` link, but `push TD2 --preflight` reported `Validation successful`.
- Proposed resolution:
  - Make preflight use the exact same validation scope/profile as `validate` and real `push`, especially when deletions can invalidate links in unchanged files.

### F-006 Incremental pull can update `page_path_index` without writing the new remote page file

- Type: Functional bug
- Severity: High
- Reproduction:
  1. Create a new child page directly in Confluence via API under an already-managed parent.
  2. Run `conf pull <SPACE>` incrementally.
  3. Inspect `.confluence-state.json`, `git ls-files`, and the workspace directory.
- Expected:
  - The new remote page should be written to disk and tracked in Git/state consistently.
- Actual:
  - `page_path_index` gained `Technical-Documentation/Live-Workflow-Test-2026-03-09/Remote-Actor-Note-2026-03-09.md -> 19824641`, but no markdown file was written and the pull commit only touched unrelated files.
- Proposed resolution:
  - Add a live/integration regression test for remote-only page creation under an existing parent and verify state/index updates only occur after the file write succeeds.

### F-007 Incremental pull missed a direct remote page update that a forced pull later reconciled

- Type: Functional bug
- Severity: High
- Reproduction:
  1. Create or pull a managed page locally.
  2. Update that page directly in Confluence via API (version increments remotely).
  3. Run `conf pull <SPACE>` without `--force`.
- Expected:
  - Incremental pull should detect the newer remote version and update the local markdown/frontmatter.
- Actual:
  - `conf pull TD2` reported `all remote updates were outside the target scope (no-op)` even though page `19824641` had moved from version `1` to `2` remotely. The local file stayed stale until a forced pull.
- Proposed resolution:
  - Investigate the incremental change planning / in-scope filtering path for remotely updated pages beneath already-managed parents, and add regression coverage for API-side updates after the initial pull.

### F-008 `--on-conflict=pull-merge` dropped the local edit instead of preserving or surfacing a merge

- Type: Functional bug
- Severity: High
- Reproduction:
  1. Pull a page into a second workspace.
  2. Make a local edit.
  3. Update the same page remotely via Confluence API.
  4. Run `conf push <file> --on-conflict=pull-merge --yes --non-interactive`.
- Expected:
  - The workflow should preserve the local edit via a clean merge, conflict markers, or at minimum a recoverable stash that the operator can inspect.
- Actual:
  - The command ran an automatic pull, printed `Discarding local changes (dropped stash stash@{0})`, updated the file to remote version `3`, and lost the local edit entirely.
- Proposed resolution:
  - Treat local change loss during pull-merge as a correctness failure: preserve the stash until the operator confirms the outcome, or restore conflict markers / merged content instead of silently dropping local edits.

### F-009 Plain ISO-like date text round-tripped to the wrong calendar date

- Type: Functional bug
- Severity: High
- Reproduction:
  1. Push Markdown containing plain body text `2026-03-09`.
  2. Pull the page back after remote round-trip.
  3. Compare the original body text with the pulled Markdown.
- Expected:
  - Plain text dates should remain plain text unless the author explicitly requested a date macro/node.
- Actual:
  - In the SD2 page `Cross Space Target 2026-03-09` (`19103745`), the sentence `This page acts as the cross-space target for the TD2 live workflow test on 2026-03-09.` came back as `... on 2024-10-04.` after push/pull.
- Proposed resolution:
  - Audit the Markdown->ADF conversion path for automatic date-node coercion, and add a round-trip test asserting that ordinary ISO date strings remain unchanged unless the source uses an explicit date extension/markup.

## Follow-Up Plan

## Execution Split For New Sessions

Run these plans in order. Each plan is scoped so a fresh session can implement it, update specs first when behavior changes, add tests for changed invariants, commit once at the end of the section, and stop cleanly for the next session.

1. `docs/plans/2026-03-09-live-sync-01-attachment-publication.md`
2. `docs/plans/2026-03-09-live-sync-02-validation-preflight-and-new-page-ux.md`
3. `docs/plans/2026-03-09-live-sync-03-incremental-pull-reconciliation.md`
4. `docs/plans/2026-03-09-live-sync-04-conflict-recovery-and-operator-guidance.md`
5. `docs/plans/2026-03-09-live-sync-05-round-trip-fidelity-and-link-diagnostics.md`
6. `docs/plans/2026-03-09-live-sync-06-compatibility-delete-semantics-and-status.md`
7. `docs/plans/2026-03-09-live-sync-07-sandbox-baseline-and-release-checklist.md`

### P0: Blockers before production release

1. Fix attachment publication so remote ADF references real uploaded attachment IDs instead of `UNKNOWN_MEDIA_ID`.
2. Make `validate`, `push --preflight`, and real `push` use the same validation scope and strictness.
3. Fix incremental pull so remote page create/update events always materialize and reconcile locally without requiring `--force`.
4. Make `--on-conflict=pull-merge` lossless: preserve local edits via merge, conflict markers, or explicit recoverable state.
5. Fix unintended ISO-date coercion in body text round-trips.

### P1: High-value workflow and operator improvements

1. Improve cross-space link handling so preserved cross-space URLs are not reported as generic unresolved-reference warnings.
2. Clarify delete semantics in CLI output and docs: current behavior archives pages remotely instead of purging them.
3. Surface folder API capability failure more explicitly when compatibility mode is activated by upstream `500` responses.
4. Add a usable preview path for new pages, either by supporting `conf diff <new-file.md>` or by redirecting operators toward `push --preflight`.
5. Improve failed-push recovery UX by printing the exact next-step commands for retained sync branches/snapshot refs.
6. Consider an attachment-aware `conf status` mode so asset drift can be checked without switching tools.

### P2: Sandbox and release-process improvements

1. Keep sandbox seed content warning-free, or maintain an explicit allowlist of known baseline warnings so regressions stay visible.
2. Gate production-readiness on passing live-sandbox E2E coverage for the critical write-path scenarios below.
3. Promote the live test log into a repeatable release checklist so manual verification and automated verification stay aligned.

### Extracted baseline allowlist carried forward into automation

1. `TD2`: page `17727489` with `UNKNOWN_MEDIA_ID_UNRESOLVED`.
2. `TD2`: `Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md` unresolved fallback link to `pageId=17530900#Task-list`.
3. `TD2`: `Technical-Documentation/Live-Workflow-Test-2026-03-05/Checklist-and-Diagrams.md` unresolved media fallback containing `UNKNOWN_MEDIA_ID`.
4. `SD2`: `Software-Development/Release-Sandbox-2026-03-05.md` unresolved fallback link to `pageId=17334539`.

## E2E Automation Plan

### Existing automated E2E coverage in `cmd/e2e_test.go`

1. `TestWorkflow_ConflictResolution`
2. `TestWorkflow_PushAutoPullMerge`
3. `TestWorkflow_AgenticFullCycle`
4. `TestWorkflow_MermaidPushPreservesCodeBlock`
5. `TestWorkflow_PushDryRunNonMutating`
6. `TestWorkflow_PullDiscardLocal`

### Required additional automated E2E coverage

1. New page preflight and diff UX:
   - Create a brand-new Markdown file without `id`.
   - Assert `validate` succeeds.
   - Assert either `diff` supports new pages or emits the intended actionable guidance.

2. Full hierarchy creation round-trip:
   - Create a parent page, child page, and nested folder-like child path in a sandbox space.
   - Push, force-pull, and assert the exact local path layout after round-trip.
   - Assert remote parent/child relationships by direct API reads.

3. Folder capability fallback behavior:
   - Run against a tenant or injected environment where folder APIs are unavailable.
   - Assert the operator receives structured diagnostics that distinguish compatibility mode from upstream endpoint failure.
   - Assert the resulting local and remote hierarchy shape is deterministic.

4. Attachment upload correctness:
   - Create pages with both file-like and image-like local attachments.
   - Push, verify attachments exist remotely, then fetch page ADF directly.
   - Assert the ADF references real attachment/media IDs and does not contain `UNKNOWN_MEDIA_ID`.

5. Attachment pull round-trip:
   - After the upload test above, force-pull the space.
   - Assert the Markdown still points to local `assets/<page-id>/...` paths rather than degraded `Media: UNKNOWN_MEDIA_ID` fallback output.

6. Attachment deletion:
   - Remove an attachment reference and the asset file locally.
   - Push, verify the remote attachment is deleted, then pull and assert the local asset path is gone from both disk and state.

7. Page delete semantics:
   - Delete a tracked Markdown page locally and push.
   - Assert the remote page ends in the intended state (`archived` today, or purge if behavior changes).
   - Assert the CLI wording and structured diagnostics match the actual remote behavior.

8. Validation/preflight parity:
   - Create a broken-link scenario caused by deleting one file while another unchanged file still references it.
   - Assert `validate`, `push --preflight`, and real `push` all fail consistently before any remote write.

9. Cross-space link preservation:
   - Push a page containing a link to a managed page in another sandbox space.
   - Force-pull and assert the link is preserved and the diagnostic category is the intended preserved-cross-space outcome.

10. Task list round-trip:
   - Push Markdown task lists with checked and unchecked items.
   - Assert remote ADF contains `taskList` / `taskItem`.
   - Pull again and assert checkbox state is preserved exactly.

11. PlantUML round-trip:
   - Push a `plantumlcloud` Markdown block.
   - Assert remote ADF contains the expected extension node and macro metadata.
   - Pull again and assert the Markdown extension block remains intact.

12. Mermaid warning and round-trip:
   - Push Mermaid content.
   - Assert validation warning text is emitted.
   - Assert remote ADF contains a `codeBlock` with `language: mermaid`.
   - Pull again and assert Mermaid fenced code is preserved.

13. Plain date text stability:
   - Push ordinary body text containing ISO-like dates such as `2026-03-09`.
   - Pull again and assert the text is unchanged, not converted into a different calendar date or a date macro.

14. Incremental pull for remote create:
   - Create a new page directly via Confluence API under an already-managed parent.
   - Run incremental `pull`.
   - Assert the Markdown file is written, Git captures it, and state only updates if the file write succeeds.

15. Incremental pull for remote update:
   - Update an existing managed page directly via API.
   - Run incremental `pull`.
   - Assert frontmatter version and body both update without requiring `--force`.

16. Incremental pull for remote delete:
   - Delete/archive a managed page directly via API.
   - Run incremental `pull`.
   - Assert the local Markdown file and state entry are removed.

17. Conflict policy `cancel`:
   - Reproduce a remote-ahead conflict.
   - Assert push fails before remote write and leaves snapshot refs plus sync branch for recovery.

18. Conflict policy `pull-merge` data preservation:
   - Reproduce a local edit plus remote edit on the same file.
   - Run `push --on-conflict=pull-merge`.
   - Assert the local edit survives via merge, conflict markers, or an explicit retained recovery artifact; never allow silent loss.

19. Recovery command flow:
   - After an intentionally failed push, run the recovery workflow.
   - Assert the documented recovery path can inspect and clean up retained branches/refs safely.

20. End-to-end cleanup parity:
   - Create a temporary subtree, attachments, and cross-space target pages.
   - Delete them at the end of the test.
   - Force-pull and assert `git status` plus `conf status` are clean in the primary workspace.
