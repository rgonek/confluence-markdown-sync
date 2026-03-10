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
  - Created external sandbox root at `C:\Users\rgone\AppData\Local\Temp\conf-live-test-20260310-165055`.
  - Initialized four external workspaces: `td2-main`, `td2-remote`, `sd2-main`, and `sd2-remote`.
  - `conf pull TD2 --yes --non-interactive --skip-missing-assets --force` succeeded in both TD2 workspaces and created tag `confluence-sync/pull/TD2/20260310T155147Z`.
  - `conf pull SD2 --yes --non-interactive --skip-missing-assets --force` succeeded in both SD2 workspaces and created tag `confluence-sync/pull/SD2/20260310T155146Z`.
  - Pulled managed directories:
    - `Technical Documentation (TD2)`
    - `Software Development (SD2)`
  - Baseline warnings were limited to pre-existing unresolved-reference diagnostics already present in sandbox content:
    - `Technical-Documentation/Live-Workflow-Test-2026-03-05/Endpoint-Notes.md`
    - `Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md`
    - `Software-Development/Release-Sandbox-2026-03-05.md`

### 2. Page lifecycle and hierarchy

- Status: Completed with findings
- Goal: Create, update, move, and delete pages while exercising folder hierarchy and subpage behavior.
- Expected:
  - New Markdown files without `id` become new Confluence pages.
  - Parent pages with children map to `<Page>/<Page>.md`.
  - Folder/subpage changes round-trip cleanly after pull.
  - Deleted tracked Markdown pages are archived remotely and removed locally on pull.
- Actual:
  - Initial TD2 push with long Windows paths failed before remote writes:
    - Recovery selector: `TD2/20260310T155550Z`
    - Error: snapshot restoration in the main worktree failed with `Filename too long` while re-applying untracked files from the retained snapshot stash.
    - Confirmed the retained sync branch had no new push commits; discarded the retained recovery run before retrying.
  - Retried with short local slugs under `Technical-Documentation/LWT-20260310-1655/` while preserving descriptive page titles in frontmatter.
  - Successful push created TD2 pages:
    - `25591809` `Live Workflow Test 2026-03-10 1655`
    - `25624577` `Checklist and Diagrams 2026-03-10 1655`
    - `25657345` `Disposable Leaf 2026-03-10 1655`
    - `25493512` `Endpoint Notes 2026-03-10 1655`
  - Successful push also created Confluence folder `Technical-Documentation/LWT-20260310-1655/API` as folder `25690113`.
  - Follow-up pulls in `td2-remote` materialized the hierarchy using title-based paths:
    - `Technical-Documentation/Live-Workflow-Test-2026-03-10-1655/Live-Workflow-Test-2026-03-10-1655.md`
    - `Technical-Documentation/Live-Workflow-Test-2026-03-10-1655/Checklist-and-Diagrams-2026-03-10-1655.md`
    - `Technical-Documentation/Live-Workflow-Test-2026-03-10-1655/API/Endpoint-Notes-2026-03-10-1655.md`
  - Deleted tracked page `Disposable Leaf 2026-03-10 1655` by removing the Markdown file locally and pushing.
  - Final pull in `td2-main` emitted `PAGE_PATH_MOVED` notes and renamed the short local slugs into the stable title-based paths expected from pull.

### 3. Attachments

- Status: Completed
- Goal: Add and remove referenced assets and verify remote attachment state plus local reconciliation.
- Expected:
  - Referenced local assets upload on push.
  - Deleted or unreferenced assets are removed remotely.
  - Follow-up pull reflects attachment additions/deletions under `assets/<page-id>/`.
- Actual:
  - Added referenced local attachments before first successful TD2 push:
    - `notes.txt` on page `25624577`
    - `payload.json` on page `25493512`
  - Push normalized both references into `assets/<page-id>/...` and updated the Markdown:
    - `assets/25624577/notes.txt`
    - `assets/25493512/payload.json`
  - Push uploaded remote attachments:
    - `att25821185` / file ID `32744db8-030c-4f7d-8c06-3b08667a9e73` for `notes.txt`
    - `att25985025` / file ID `f5c4a51f-9ba3-44db-b364-1571b0a643d6` for `payload.json`
  - Direct API read of checklist page `25624577` confirmed the generated ADF referenced the real attachment media/file identity `32744db8-030c-4f7d-8c06-3b08667a9e73`.
  - Removed the endpoint attachment by deleting both the Markdown reference and `assets/25493512/payload.json`, then pushed successfully.
  - Push emitted `[ATTACHMENT_DELETED] assets/25493512/payload.json: deleted stale attachment att25985025`.
  - Direct API verification after deletion showed no remaining attachments on page `25493512`.

### 4. Links

- Status: Completed with findings
- Goal: Verify same-space relative Markdown links and cross-space links.
- Expected:
  - Same-space page links resolve and remain valid after push/pull.
  - Cross-space links preserve an appropriate remote reference without corrupting local content.
- Actual:
  - Same-space Markdown links between the TD2 test pages published successfully and pulled back as relative Markdown links in `td2-remote`.
  - Created SD2 page `Cross Space Target 2026-03-10 1655` (`25460737`) and linked to it from TD2 page `25591809`.
  - Added the backlink from SD2 page `25460737` to TD2 page `25591809`.
  - Direct API reads confirmed the generated ADF for:
    - TD2 parent page `25591809` contains a Confluence link mark referencing page ID `25460737`.
    - SD2 page `25460737` contains a Confluence link mark referencing page ID `25591809`.
  - Pulls in both `td2-remote` and `sd2-remote` preserved the cross-space links as readable absolute Confluence URLs.
  - Pulls in both directions also emitted `unresolved_reference` warnings for those valid preserved cross-space links.

### 5. Rich content

- Status: Completed
- Goal: Validate PlantUML, Mermaid, and Markdown task lists.
- Expected:
  - PlantUML round-trips as the `plantumlcloud` extension and renders remotely.
  - Mermaid warns and is stored in ADF as a `codeBlock` with `language: mermaid`.
  - Task lists convert to/from Confluence tasks without data loss.
- Actual:
  - `conf validate TD2` warned as expected that Mermaid fences will be preserved as Confluence code blocks rather than rendered Mermaid macros.
  - Direct API read of TD2 checklist page `25624577` confirmed the published ADF contains:
    - `taskList` / `taskItem` nodes for the Markdown task list
    - `plantumlcloud` extension payload for the PlantUML block
    - `codeBlock` with `language: mermaid` for the Mermaid fence
  - Follow-up pull in `td2-remote` preserved:
    - Markdown task list checkbox syntax
    - PlantUML fenced `puml` block wrapped in the managed `adf-extension`
    - Mermaid fenced code block

### 6. Remote-change and conflict handling

- Status: Completed
- Goal: Simulate a second user via direct API create/update/delete operations and verify pull/push behavior.
- Expected:
  - Remote updates trigger conflict behavior according to `--on-conflict`.
  - Pull reconciles remote creations, edits, and deletions locally.
  - Manual or automatic resolution leads back to a successful push and clean follow-up pull.
- Actual:
  - Direct API create in TD2 created page `Remote Actor Note 2026-03-10 1655` (`25821191`) under parent page `25591809`.
  - Incremental `conf pull TD2` in `td2-remote` created the corresponding local Markdown file at:
    - `Technical-Documentation/Live-Workflow-Test-2026-03-10-1655/Remote-Actor-Note-2026-03-10-1655.md`
  - Direct API update moved page `25821191` to version `2` with changed body content; the next incremental pull updated the local Markdown accordingly.
  - Direct API delete archived page `25821191`; the next incremental pull removed the local Markdown file.
  - In `sd2-remote`, created a stale local edit to page `25460737` while the same page was updated directly via API to version `4`.
  - `conf push ... --on-conflict=cancel --yes --non-interactive` failed as expected with:
    - local version `3`
    - remote version `4`
    - retained recovery selector `SD2/20260310T160424Z`
  - `conf push ... --on-conflict=pull-merge --yes --non-interactive` pulled remote version `4`, then failed fast because non-interactive mode could not choose a conflict resolution, leaving the file in `UU` state with conflict markers.
  - Manually resolved the conflict by combining the remote API sentence with the stale local sentence, `git add`-ed the file, and pushed successfully to version `5`.
  - Final SD2 pull in `sd2-main` confirmed the conflict-resolved remote content was now the local source of truth.

## Findings

### F-001 Cross-space links still surface as `unresolved_reference` warnings on pull

- Type: UX / diagnostics issue
- Severity: Medium
- Reproduction:
  1. Create a valid cross-space link from TD2 to an SD2 page, or the reverse.
  2. Push the page successfully.
  3. Pull the originating space.
- Expected:
  - The preserved cross-space URL should either be silent or produce a dedicated low-severity preserved cross-space diagnostic.
- Actual:
  - Pull succeeds and preserves the readable Confluence URL, but emits `unresolved_reference` warnings for the target page URL in both directions.
- Proposed resolution:
  - Route preserved cross-space links through a dedicated diagnostic path so valid cross-space references are distinguishable from genuinely unresolved content.

### F-002 Windows path-length failure in push snapshot/worktree restoration for new untracked files

- Type: Windows compatibility / push recovery bug
- Severity: High
- Reproduction:
  1. In a Windows workspace, create a new nested TD2 subtree with long title-derived directory and file names.
  2. Run `conf push TD2 --on-conflict=cancel --yes --non-interactive`.
  3. Let push snapshot the untracked files and attempt to materialize the snapshot back into the main worktree.
- Expected:
  - Push either succeeds fully or fails cleanly without breaking snapshot restoration for long but otherwise valid workspace paths.
- Actual:
  - Push aborted before remote writes with:
    - `materialize snapshot in worktree`
    - `git stash apply --index ... failed`
    - `unable to create file ... Filename too long`
  - The retained recovery branch contained no push commits, proving the failure happened during local snapshot/worktree restoration.
- Proposed resolution:
  - Short term: enable or document long-path-safe Git handling on Windows in the push snapshot path, or fail earlier with a preflight path-length diagnostic before mutating Git state.
  - Longer term: reduce reliance on stash materialization for untracked files in long nested workspaces, or materialize snapshots in a long-path-tolerant temp root.

## Final Verification

- `td2-main` and `sd2-main` both ended with `git status --short` clean.
- `conf status TD2` reported no local drift, no remote drift, and no version drift.
- `conf status SD2` reported no local drift, no remote drift, and no version drift.
- Follow-up `conf pull TD2 --yes --non-interactive` and `conf pull SD2 --yes --non-interactive` were both no-ops after the final reconciliation pulls.
- `td2-main` final pull renamed the short local authoring slugs into title-based stable pull paths, and the resulting workspace matched remote state.
- Published sandbox pages intentionally remain in `TD2` and `SD2` as synchronized test artifacts:
  - TD2 parent `25591809`
  - TD2 checklist `25624577`
  - TD2 endpoint `25493512`
  - SD2 cross-space target `25460737`
