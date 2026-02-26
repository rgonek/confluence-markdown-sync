# Self-Healing Sync and Deterministic Hierarchy

## Status
**Proposed Date**: 2026-02-26  
**Target Version**: 0.2.x  
**Owner**: Agentic Core
**Execution Status**: Complete (6/6 sections implemented)

## Implementation Progress
- [x] 1. State File Self-Healing
- [x] 2. Deterministic Hierarchy (Page vs. Folder)
- [x] 3. Complete Removal of Space Key from Frontmatter
- [x] 4. Deletion Warnings for Dirty Worktrees
- [x] 5. Sync Inspection (`conf status`)
- [x] 6. Workspace Recovery (`conf clean`)

## Change Log
- 2026-02-26 (Step 1): Removed `space` from frontmatter write-path and schema requirements. Push/validate now ignore `space` frontmatter mismatches, and file-target space resolution now uses state/directory context instead of requiring `space:`.
- 2026-02-26 (Step 2): Added conflict-marker detection in state loading and automatic pull-time state healing (remote page + local ID rebuild). Added explicit dirty-worktree deletion warnings when pull detects remote deletions that overlap local markdown edits.
- 2026-02-26 (Step 3): Made hierarchy parenting deterministic by preferring nearest index pages (`X/X.md`) over folders, updated folder precreation to use index-parent awareness, and added index-driven folder collapse/reparent behavior diagnostics during push.
- 2026-02-26 (Step 4): Added `conf status` (alias: `conf status`) to inspect local not-pushed markdown changes, remote not-pulled page drift, pending additions/deletions, and max tracked version drift without mutating workspace or remote.
- 2026-02-26 (Step 5): Added `conf clean` (alias: `conf clean`) to recover interrupted sync sessions by switching off `sync/*` branches, removing stale `.confluence-worktrees/*`, pruning snapshot refs, and normalizing readable state files.

---

## 1. State File Self-Healing

### 1.1 Problem: Corruption via Git Conflicts
When `conf` performs sync operations, it uses ephemeral branches and internal Git merges. If two users change the space structure or metadata simultaneously, `.confluence-state.json` may experience a Git merge conflict. Git inserts conflict markers (`<<<<<<<`, `=======`, `>>>>>>>`), which cause the Go JSON parser to fail with a cryptic error on any subsequent command.

### 1.2 Solution: Detection and Auto-Rebuild
- **Conflict Detection**: Modify the state loader to check for Git conflict markers (`<<<<<<<`) when a JSON parsing error occurs.
- **Warning & Auto-Rebuild**: Display a warning: "Git conflict detected in `.confluence-state.json`. Rebuilding state from Confluence and local IDs..."
- **Reconstruction Logic**: 
  1. Fetch all pages from the remote space.
  2. Scan the local space directory for all `.md` files.
  3. Extract `id` from the frontmatter of each file.
  4. Match remote IDs to local paths to reconstruct the `page_path_index`.
  5. Rebuild the `folder_path_index` from the remote hierarchy.
  6. Overwrite the corrupted `.confluence-state.json` with the healed version.

### 1.3 Reproduction Steps
1. `conf pull TD` (v1)
2. Manually add conflict markers to `.confluence-state.json`:
   ```json
   <<<<<<< HEAD
   "page_path_index": { "foo.md": "123" }
   =======
   "page_path_index": { "bar.md": "456" }
   >>>>>>> sync/branch
   ```
3. Run `conf pull TD`.
4. **Verification**: Tool should detect the markers, warn, rebuild the state, and finish the pull successfully.

---

## 2. Deterministic Hierarchy (Page vs. Folder)

### 2.1 Problem: Non-Deterministic Mapping
The tool supports using a markdown file (e.g., `Parent/Parent.md`) as the parent for other files in its directory. However, if the directory is processed *before* the index file is mapped or pushed, the tool may create a "Folder" object in Confluence instead of using the Page. Once a Folder is created, it persists even if an Index file is later provided.

### 2.2 Solution: Hierarchy Planning and Normalization
- **Hierarchy Planning**: Before creating any folders or pages in `push`, scan the entire set of changes to identify "Index Files" (`X/X.md`).
- **Hierarchy Mapping**: If a file is in folder `X/`, and `X/X.md` is present (either in the current change set or already tracked), prioritize its ID as the parent.
- **Folder Cleanup**: If a Folder object exists for `X` but an Index Page `X/X.md` is now being pushed, the tool should offer to "collapse" the folder and move child pages to the new Index Page.

### 2.3 Reproduction Steps
1. Create `Parent/Child.md` (no `Parent/Parent.md`).
2. `conf push TD`. (Verified: Folder "Parent" created).
3. Create `Parent/Parent.md`.
4. `conf push TD`.
5. **Verification**: Tool should identify the new Index File and potentially reparent `Child.md` under the new `Parent` page in Confluence.

---

## 3. Complete Removal of Space Key from Frontmatter

### 3.1 Problem: Redundant and Brittle Metadata
The `space:` key is currently required in every markdown file. This is redundant (it exists in `.confluence-state.json` and in the command-line arguments) and causes validation errors when moving files between spaces.

### 3.2 Solution: Stop Reading and Writing Space Metadata
- **Remove from Writing**: Modify the markdown writer (used in `pull` and `push` ID write-backs) to omit the `space` field entirely.
- **Ignore during Reading**: Modify the validator and push logic to ignore the `space` field if it exists in the frontmatter.
- **Source of Truth**: Always determine the space key from the CLI target space and the tracked `.confluence-state.json` for that directory.

### 3.3 Reproduction Steps
1. Take an existing markdown file with `space: TD`.
2. Run `conf pull TD`.
3. **Verification**: The `space: TD` line should be removed from the local markdown file by the tool.
4. Add `space: WRONG_SPACE` manually to a file.
5. Run `conf push TD`.
6. **Verification**: The push should succeed, ignoring the incorrect key.

---

## 4. Deletion Warnings for Dirty Worktrees

### 4.1 Problem: "Ghost" Files after Moves/Deletions
When a page is renamed or deleted remotely, the local `pull` command tries to delete the old file. If the file has uncommitted changes, Git's merge logic skips the deletion for safety. Currently, this happens silently, leaving the user with an "orphan" file.

### 4.2 Solution: Explicit Warnings
- **Log Warning**: If a remote deletion is detected during `pull` but the file has uncommitted local changes, log:
  `WARNING: Skipped local deletion of 'path/to/page.md' because it contains uncommitted edits. Please resolve manually or run with --discard-local.`

### 4.3 Reproduction Steps
1. Sync a page remotely.
2. Modify the file locally without committing.
3. Trash the page remotely via Confluence API/UI.
4. Run `conf pull`.
5. **Verification**: Tool should not delete the file and should print the warning message.

---

## 5. Sync Inspection (`conf status`)

### 5.1 Problem: Lack of Visibility
Users cannot quickly see a high-level summary of the sync state (local vs. remote) without running a full `diff` or `push --dry-run`.

### 5.2 Solution: New Inspection Command
- **Command**: `conf status [TARGET]`
- **Output**:
  - List of locally modified files (not yet pushed).
  - List of remotely modified files (not yet pulled).
  - Version drift (e.g., "Local state is 3 versions behind remote space").
  - Summary of pending additions/deletions.

### 5.3 Reproduction Steps
1. Modify one file locally and one page remotely.
2. Run `conf status`.
3. **Verification**: Tool should accurately list both the local change and the remote change.

---

## 6. Workspace Recovery (`conf clean`)

### 6.1 Problem: Hanging Sync State
If a `push` or `pull` crashes or is force-interrupted, the user might be left on an ephemeral `sync/` branch with a "dirty" working directory or hidden snapshot refs.

### 6.2 Solution: Recovery Utility
- **Command**: `conf clean`
- **Actions**:
  1. Identify the current branch. If it's a `sync/` branch, offer to return to the original branch (e.g., `master`).
  2. Clean up any stale worktrees in `.confluence-worktrees/`.
  3. Identify and offer to delete stale `refs/confluence-sync/snapshots/` refs.
  4. Ensure the `.confluence-state.json` is in a consistent state.

### 6.3 Reproduction Steps
1. Start a `conf push` and terminate the process abruptly mid-execution.
2. Run `conf clean`.
3. **Verification**: Tool should detect the hanging sync branch and offer to restore the workspace to a clean state.
