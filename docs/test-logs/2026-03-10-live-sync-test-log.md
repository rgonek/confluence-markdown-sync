# Live Sync Test Log

- Date: 2026-03-10
- Repository: `D:\Dev\confluence-markdown-sync`
- Operator: Codex
- Scope: Real pull/push verification against sandbox Confluence spaces `TD2` and `SD2`
- Rule: All live workspace pulls/pushes run outside the repository root

## Environment

- Primary binary source: current repository build of `conf.exe`
- Verification methods:
  - CLI workflow: `init` -> `pull` -> `validate` -> `diff` -> `push` -> `pull`
  - Direct Confluence API reads/writes for ADF, attachments, and remote-actor simulation
  - Git state inspection inside temporary workspaces

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
  - Created external sandbox root at `C:\Users\rgone\AppData\Local\Temp\conf-live-test-20260310-150804`.
  - Initialized four external workspaces: `td2-main`, `sd2-main`, `td2-remote`, and `sd2-remote`.
  - `conf pull TD2 --yes --non-interactive --skip-missing-assets --force` succeeded in both TD2 workspaces and created tags `confluence-sync/pull/TD2/20260310T140837Z` and `confluence-sync/pull/TD2/20260310T141551Z`.
  - `conf pull SD2 --yes --non-interactive --skip-missing-assets --force` succeeded in both SD2 workspaces and created tags `confluence-sync/pull/SD2/20260310T140836Z` and `confluence-sync/pull/SD2/20260310T141552Z`.
  - Baseline warnings remained limited to pre-existing unresolved reference noise in sandbox seed content plus cross-space warnings for the test pages created during this run.

### 2. Page lifecycle and hierarchy

- Status: Completed
- Goal: Create, update, move, and delete pages while exercising folder hierarchy and subpage behavior.
- Expected:
  - New Markdown files without `id` become new Confluence pages.
  - Parent pages with children map to `<Page>/<Page>.md`.
  - Folder/subpage changes round-trip cleanly after pull.
  - Deleted tracked Markdown pages are archived remotely and removed locally on pull.
- Actual:
  - Created TD2 parent page `Live Workflow Test 2026-03-10` (`24313920`) from local Markdown.
  - Created TD2 child pages `Checklist and Diagrams 2026-03-10` (`22970490`) and `Disposable Leaf 2026-03-10` (`23691325`) from local Markdown.
  - Created nested folder-path child `API-Folder-2026-03-10/Endpoint-Notes-2026-03-10.md`; push published `Endpoint Notes 2026-03-10` (`23724205`) plus the compatibility-mode surrogate page `API-Folder-2026-03-10` (`23068840`).
  - Follow-up pulls preserved the hierarchy as:
    - `Live-Workflow-Test-2026-03-10/Live-Workflow-Test-2026-03-10.md`
    - `Live-Workflow-Test-2026-03-10/Checklist-and-Diagrams-2026-03-10.md`
    - `Live-Workflow-Test-2026-03-10/API-Folder-2026-03-10/API-Folder-2026-03-10.md`
    - `Live-Workflow-Test-2026-03-10/API-Folder-2026-03-10/Endpoint-Notes-2026-03-10.md`
  - Deleted existing page `Disposable Leaf 2026-03-10` by removing the tracked Markdown file and pushing; the page was archived remotely and removed locally on pull.
  - Deleted the full TD2 test subtree at the end by removing the tracked Markdown files and pushing; direct API verification confirmed all four pages were archived remotely.

### 3. Attachments

- Status: Completed
- Goal: Add and remove referenced assets and verify remote attachment state plus local reconciliation.
- Expected:
  - Referenced local assets upload on push.
  - Deleted or unreferenced assets are removed remotely.
  - Follow-up pull reflects attachment additions/deletions under `assets/<page-id>/`.
- Actual:
  - Added two referenced local attachments before first push:
    - `checklist-notes-20260310.txt` on page `22970490`
    - `payload-20260310.json` on page `23724205`
  - Push normalized both asset paths into `assets/<page-id>/...` and rewrote the Markdown references accordingly.
  - Direct API verification confirmed remote attachments:
    - `att22478900 checklist-notes-20260310.txt`
    - `att23855221 payload-20260310.json`
  - Direct ADF verification confirmed the published attachments used real `mediaInline` identities inside `contentId-<page-id>` collections; the prior `UNKNOWN_MEDIA_ID` failure did not reproduce.
  - Removed the payload attachment by deleting the local file/reference and pushing; push emitted `[ATTACHMENT_DELETED] assets/23724205/payload-20260310.json: deleted stale attachment att23855221`.
  - Final cleanup archived the remaining checklist attachment during page removal; push emitted `[ATTACHMENT_DELETED] assets/22970490/att22478900-checklist-notes-20260310.txt: deleted attachment att22478900 during page removal`.

### 4. Links

- Status: Completed with findings
- Goal: Verify same-space relative Markdown links and cross-space links.
- Expected:
  - Same-space page links resolve and remain valid after push/pull.
  - Cross-space links preserve an appropriate remote reference without corrupting local content.
- Actual:
  - Same-space Markdown links between the TD2 test pages resolved to Confluence page URLs in published ADF and pulled back as local relative Markdown links.
  - Created SD2 page `Cross Space Target 2026-03-10` (`22184048`) and linked to it from TD2.
  - Updated the SD2 page with a back-link to TD2 after the TD2 parent page existed.
  - Cross-space links preserved content and remained readable, but pulls surfaced them as `unresolved_reference` warnings instead of a lower-severity preserved cross-space diagnostic.

### 5. Rich content

- Status: Completed
- Goal: Validate PlantUML, Mermaid, and Markdown task lists.
- Expected:
  - PlantUML round-trips as the `plantumlcloud` extension and renders remotely.
  - Mermaid warns and is stored in ADF as a `codeBlock` with `language: mermaid`.
  - Task lists convert to/from Confluence tasks without data loss.
- Actual:
  - Markdown task lists published as remote ADF `taskList` / `taskItem` nodes and pulled back as checkbox lists.
  - PlantUML published as a Confluence `plantumlcloud` extension; direct ADF verification confirmed the `plantumlcloud` macro payload.
  - Mermaid emitted the expected `MERMAID_PRESERVED_AS_CODEBLOCK` warning during validate/push and published as an ADF `codeBlock` with language `mermaid`.
  - Follow-up pulls preserved PlantUML source and Mermaid fences in Markdown.

### 6. Remote-change and conflict handling

- Status: Completed
- Goal: Simulate a second user via direct API create/update/delete operations and verify pull/push behavior.
- Expected:
  - Remote updates trigger conflict behavior according to `--on-conflict`.
  - Pull reconciles remote creations, edits, and deletions locally.
  - Manual or automatic resolution leads back to a successful push and clean follow-up pull.
- Actual:
  - Created page `Remote Actor Note 2026-03-10` (`22577297`) directly via API under TD2 parent page `24313920`.
  - Incremental pull in `td2-remote` created the local Markdown file and state entry for the remote-created page.
  - Updated page `22577297` directly via API to version `2`; incremental pull updated frontmatter/body locally and committed the change.
  - Deleted page `22577297` directly via API; incremental pull removed the local Markdown file and state entry.
  - In `sd2-remote`, made a stale local edit to `Cross Space Target 2026-03-10` (`22184048`), then updated the same page remotely via API to version `4`.
  - `conf push ... --on-conflict=cancel --yes --non-interactive` failed as expected, retained the sync branch plus snapshot refs, and printed recovery instructions.
  - `conf push ... --on-conflict=pull-merge --yes --non-interactive` attempted automatic pull-merge, preserved the conflict in Git with `UU` state and conflict markers, and failed fast because non-interactive mode could not choose keep-local / keep-website / keep-both.
  - Manually resolved the conflict by combining local and remote content, `git add`-ed the file, and pushed successfully to remote version `5`.

## Final Verification

- `td2-main` and `sd2-main` both ended with `git status --short` clean.
- `conf status TD2` reported no local drift, no remote drift, and no version drift.
- `conf status SD2` reported no local drift, no remote drift, and no version drift.
- Follow-up `conf pull TD2 --yes --non-interactive` and `conf pull SD2 --yes --non-interactive` were both no-ops after cleanup pushes.
- `rg --files` confirmed the 2026-03-10 test pages were absent from both main workspaces after final pull.
- Direct API reads confirmed archived remote status for:
  - `24313920` `Live Workflow Test 2026-03-10`
  - `22970490` `Checklist and Diagrams 2026-03-10`
  - `23724205` `Endpoint Notes 2026-03-10`
  - `23068840` `API-Folder-2026-03-10`
  - `22184048` `Cross Space Target 2026-03-10`

## Findings

### F-001 Cross-space links still surface as `unresolved_reference` warnings on pull

- Type: UX / diagnostics issue
- Severity: Medium
- Reproduction:
  1. Create a valid cross-space link from TD2 to an SD2 page.
  2. Push successfully.
  3. Pull the originating space.
- Expected:
  - The link should be preserved and reported, at most, as a dedicated cross-space preservation note.
- Actual:
  - Pull succeeds and preserves the readable absolute Confluence URL, but emits `unresolved_reference` warnings for the cross-space page IDs.
- Proposed resolution:
  - Restore or tighten the dedicated cross-space diagnostic path so valid preserved cross-space links are distinguishable from genuinely unresolved content.

### F-002 Folder hierarchy writes still rely on compatibility fallback because the Confluence folders endpoint returns `500`

- Type: Platform compatibility / operator visibility issue
- Severity: Medium
- Reproduction:
  1. Push a page in `TD2` or `SD2`.
  2. Observe the compatibility diagnostic and warning log.
  3. Inspect the underlying request to `GET /wiki/api/v2/folders?space-id=...`.
- Expected:
  - Either folder writes use the tenant capability directly, or the operator gets a structured explanation that the tenant endpoint is unhealthy.
- Actual:
  - Push succeeds via page-based hierarchy fallback, while logs show `GET ... /folders ... status 500: Internal Server Error`.
- Proposed resolution:
  - Keep the fallback, but surface the upstream `500` more prominently in structured diagnostics/reporting so automation can distinguish tenant-health issues from expected capability absence.

## Notes

- Prior live-run failures around remote attachment media IDs, incremental remote create/update reconciliation, and `--on-conflict=pull-merge` dropping local edits did not reproduce on 2026-03-10.
