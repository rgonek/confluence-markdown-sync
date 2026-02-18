# Confluence Markdown Sync CLI Implementation Plan

## 1. Overview
This document outlines the plan for building a CLI tool in Go that synchronizes Confluence pages with a local directory. It converts Confluence's Atlassian Document Format (ADF) JSON to Markdown for local editing and converts Markdown back to ADF for publishing updates to Confluence.

This design uses Git as a local history engine only (no Git remote required). The CLI owns Git operations (branches, worktrees, tags, snapshots), so users should not need to run Git commands directly.

**Binary Name:** `cms`

**Key Libraries:**
- `github.com/rgonek/jira-adf-converter`: Handles ADF <-> Markdown conversion.
- `github.com/spf13/cobra`: For CLI command structure.
- `github.com/spf13/viper`: For configuration management (environment variables and .env support).
- `gopkg.in/yaml.v3`: For parsing Markdown frontmatter.

## 2. Architecture & Design

### 2.1 Authentication
Authentication will be handled via environment variables or a local `.env` file:
- `ATLASSIAN_DOMAIN`: The base URL of the Atlassian instance (e.g., `https://your-domain.atlassian.net`).
- `ATLASSIAN_EMAIL`: The user's email address.
- `ATLASSIAN_API_TOKEN`: The API token generated from Atlassian account settings.

Compatibility and precedence:
- `cms` may accept legacy `CONFLUENCE_*` variables for backward compatibility.
- Resolution order: `CONFLUENCE_*` (if set) -> `ATLASSIAN_*` -> `.env` file -> error.

### 2.2 Data Mapping & Storage
- **Directory Structure**:
  - **Root (`XXX`)**: The directory where `cms init` is run. Contains `.git`.
  - **Space Directory (`XXX/<SpaceKey>`)**: All pages for a space reside here.
- **File Format**: Markdown (`.md`) with Frontmatter.
- **Title Source of Truth**: Frontmatter `title` field. Fallback to first H1 header if missing.
- **Frontmatter (required fields)**:
  - `confluence_page_id`: Stable page ID for update/delete operations.
  - `confluence_space_key`: Confluence space key for validation.
  - `confluence_version`: Last synced remote version.
  - `confluence_last_modified`: Last synced remote modified timestamp.
  - `confluence_parent_page_id`: Optional parent ID for hierarchy rebuild.
- **Frontmatter Mutability Rules**:
  - Immutable: `confluence_page_id`, `confluence_space_key`.
  - Mutable by sync only: `confluence_version`, `confluence_last_modified`, `confluence_parent_page_id`.
  - Manual or AI edits to immutable keys fail validation.
- **State**:
    - **Per-Space State File**: `XXX/<SpaceKey>/.confluence-state.json`.
    - **State Keys**:
      - `last_pull_high_watermark`: RFC3339 remote-modified timestamp high-watermark.
      - `page_path_index`: Map of local path -> page ID (used for local delete detection).
      - `attachment_index`: Map of local asset path -> attachment ID (used for rename/delete handling).
    - **Git Ignore**: Added to `.gitignore` automatically.

### 2.3 Workflow

#### 2.3.0 Git Operating Model
- Git is required locally, but no Git remote is required.
- All Git operations are CLI-managed; users interact through `cms` commands only.
- `init` behaves as follows:
    - If no git repo exists: `git init -b main`.
    - If git repo exists: Use current branch.
    - Prompts for environment variables if missing and creates `.env`.
    - Generates `AGENTS.md` and `README.md`.
- Recovery and cleanup flows must be provided by CLI behavior/messages rather than manual Git instructions.

#### 2.3.1 Context Detection
- **Root Context**: If CWD is `XXX`, commands accept `[TARGET]` (`SPACE_KEY` or `.md` file path).
- **Space Context**: If CWD is `XXX/<SpaceKey>`, commands infer space from the directory name.

#### 2.3.2 Pull (Confluence -> Local)
**Workflow with CLI-Managed Git Stash:**
1.  **Context Check**: Determine Space (Flag or Directory).
2.  **Git Scope**: Operations restricted to `XXX/<SpaceKey>`.
3.  **Pre-check**: Run `git status --porcelain -- <SpaceDir>`.
    -   If dirty: Run `git stash push --include-untracked -m "Auto-stash <SpaceKey> <UTC timestamp>" -- <SpaceDir>` and capture stash ref.
4.  **Fetch and Plan Conversion**:
    -   Read `last_pull_high_watermark` from `.confluence-state.json`.
    -   Fetch pages/attachments modified since `(last_pull_high_watermark - overlap_window)` (or all if missing).
    -   Capture `pull_started_at` (server timestamp) before processing results.
    -   Track `max_remote_modified_at` from fetched entities.
    -   Build a deterministic pre-conversion page path map `page_path_by_id` (page ID -> planned Markdown path) before rendering any page content.
    -   Build planned attachment path map `attachment_path_by_id` (attachment ID -> planned local asset path).
    -   Convert page ADF to Markdown using link/media resolver hooks that receive current page context and mapping indexes.
    -   Pull link rewrite rule: same-space page ID with known target path => rewrite to relative Markdown path from the current file (preserve `#fragment` anchors).
    -   For external/cross-space links, keep absolute links.
    -   For unresolved same-space page links, keep original absolute link and emit diagnostics.
    -   Update/Create files in `XXX/<SpaceKey>` and download new/updated attachments to `assets/<page-id>/<attachment-id>-<filename>`.
5.  **Reconcile Deletes**:
    -   Compare remote page/attachment IDs vs local `page_path_index` and `attachment_index`.
    -   For remote deletions, hard-delete local files/assets (no trash folder) and update indexes.
6.  **Persist State**:
    -   Write updated indexes and set `last_pull_high_watermark = max(max_remote_modified_at, pull_started_at)` only after successful file reconciliation.
7.  **Commit (if changed)**:
    -   Run `git add <SpaceDir>`.
    -   If scoped staged changes exist, run `git commit -m "Sync from Confluence: [Space] (v[NewVersion])"`.
    -   If no scoped staged changes exist, treat as no-op pull (no commit, no tag).
8.  **Restore**:
    -   If stashed: CLI runs `git stash apply --index <stash-ref>` and then `git stash drop <stash-ref>`.
    -   If apply conflicts, stop, report conflicted files, and keep stash entry for CLI-driven recovery (no user Git commands required).
9.  **Tag Sync Point**:
    -   Create annotated tag: `confluence-sync/pull/<SpaceKey>/<UTC timestamp>` only when the pull created a commit.

#### 2.3.3 Push (Local -> Confluence)
**Change Detection (Git-based, includes uncommitted workspace, per-file commits grouped via sync branch):**
1.  **Capture and Validate Workspace Snapshot**:
    -   **Stash**: Run `git stash push` on target files to clean workspace (Stash-Merge-Pop strategy).
    -   Capture current workspace state for target scope (`staged`, `unstaged`, `untracked`, and deletions).
    -   Capture out-of-scope workspace state so it can be restored exactly after merge.
    -   Run `cms validate [TARGET]` against that captured state.
    -   Abort `push` if validation fails (and restore stash).
2.  **Identify Files**: Determine changed files from the workspace snapshot.
    -   If no in-scope files changed, exit success as a no-op (`push` creates no snapshot commit, no sync branch/worktree, and no tag).
3.  **Create Internal Snapshot Commit**:
    -   Create an internal snapshot commit from in-scope workspace state and store it under hidden ref `refs/confluence-sync/snapshots/<SpaceKey>/<UTC timestamp>`.
    -   Snapshot commits are local-only internals and are not added to user-visible branch history.
4.  **Create Ephemeral Sync Branch**: `sync/<SpaceKey>/<UTC timestamp>` from the snapshot ref.
5.  **Run in Isolated Worktree**:
    -   Create temporary worktree for the sync branch (e.g., `.confluence-worktrees/<SpaceKey>-<UTC timestamp>`).
    -   Execute sync operations in that worktree to avoid touching the user's active working tree.
6.  **Process Loop**: For each changed file:
    -   **Conflict Check**: Compare Remote Version vs Local Frontmatter.
    -   **Upload Assets**: If new images are referenced, upload to Confluence.
    -   **Update/Delete**:
        -   Modified/Added `.md`: PUT page update to Confluence.
        -   Deleted `.md`: Archive remote page by default using `page_path_index` (`--hard-delete` optional).
    -   **Post-Update**:
        -   Update local file Frontmatter (Version/Modified timestamp) and local state indexes.
        -   **Git Commit**:
            -   `git add <file> assets/`.
            -   `git commit -m "Sync [Page] to Confluence"`.
7.  **Finalize**:
    -   If all page operations succeed, merge sync branch back into the original branch.
    -   **Pop Stash**: Run `git stash pop` to restore user's uncommitted edits. If Frontmatter conflicts occur (due to version update), leave standard Git conflict markers for user to resolve.
    -   Create annotated tag: `confluence-sync/push/<SpaceKey>/<UTC timestamp>` only when a merge commit is created.
    -   If any page operation fails, do not merge and keep sync branch + snapshot ref for CLI-guided recovery.
    -   If out-of-scope state restoration conflicts, stop, report impacted paths, and keep recovery refs.
8.  **Cleanup**:
    -   Remove temporary worktree after success or failure.
    -   On full success, delete sync branch and hidden snapshot ref.
    -   On failure, retain sync branch and hidden snapshot ref for recovery.
9.  **Note**: `push` does NOT update `last_pull_high_watermark`.

#### 2.3.4 Attachment & Link Handling
- **Converter Hook Contract (library-level, runtime callbacks)**:
    - Conversion exposes link/media resolver hooks in both directions (ADF->Markdown and Markdown->ADF).
    - Hooks receive current document context plus mapping metadata and may rewrite targets or defer to default behavior.
    - Hooks do not perform network/filesystem side effects; sync orchestration owns downloads/uploads/file writes.
- **Attachments**:
    - **Pull Planning**: Build `attachment_path_by_id` before conversion.
    - **Download**: CLI scans ADF for media and downloads files to `assets/`.
    - **Storage Pattern**: Use `assets/<page-id>/<attachment-id>-<filename>` to avoid name collisions.
    - **Link Rewrite**: Pull media hook rewrites Markdown image/file references to local relative paths (e.g., `![Image](assets/12345/8899-diagram.png)`).
    - **Push Mapping**: Push media hook resolves local asset paths to attachment IDs (or upload intents), then sync performs upload/delete and updates `attachment_index`.
    - **ADF Mapping**: Push conversion emits Confluence `ri:attachment` references.
- **Page Links**:
    - **Pull Planning**: Build `page_path_by_id` before converting page content.
    - **Pull Rewrite**: Pull link hook resolves `ri:page` links to relative Markdown links (e.g., `[Link](./ChildPage.md)`) using `page_path_by_id` and current page path.
    - **Push Rewrite**: Build `page_id_by_path`, then resolve local relative links to Confluence page IDs and emit `ri:page` in ADF.
    - **Anchor Handling**: Preserve in-document fragments while rewriting links in both directions.
- **Resolution Modes**:
    - **Best-Effort** (`pull`, `diff`): unresolved refs fall back to original link/asset representation and emit diagnostics.
    - **Strict** (`validate`, `push`): unresolved local/space refs fail conversion before any remote write.

#### 2.3.5 Git Integration Enhancements
- **Smart .gitignore**: `init` adds `.DS_Store`, `*.tmp`, `.confluence-state.json`, `.env`, `cms.exe`, etc.
- **Diff Command**: `cms diff [TARGET]` fetches remote, converts to MD, and runs `git diff --no-index` (`.md` suffix => file mode, otherwise space mode).
- **Rich Commits**:
    - Subject: `Sync "[Page Title]" to Confluence (v[Version])`
    - Body: `Page ID: [ID]\nURL: [URL]`
    - Trailers:
      - `Confluence-Page-ID: [ID]`
      - `Confluence-Version: [VERSION]`
      - `Confluence-Space-Key: [SPACE_KEY]`
      - `Confluence-URL: [URL]`
- **Asset Tracking**: `push` automatically runs `git add` on referenced images in `assets/`.
- **Grouped Push Runs**: Use an ephemeral sync branch so per-file commits stay granular while the run is merged as one unit on success.
- **Isolated Sync Execution**: Use `git worktree` for push runs to avoid mutating the user's active working tree.
- **Workspace Snapshot Pushes**: Include uncommitted (`staged`, `unstaged`, `untracked`, deletions) local changes in push runs via an internal snapshot commit.
- **Internal Snapshot Refs**: Store snapshot commits under `refs/confluence-sync/snapshots/...`; clean them on full success and retain them on failure for recovery.
- **Out-of-Scope Preservation**: After successful push merge, restore all out-of-scope local workspace changes exactly as they were before push.
- **Local-Only Git**: No Git remote is required; the CLI does not depend on Git fetch/push operations.
- **No-Manual-Git UX**: All recovery and cleanup paths are surfaced through CLI flows/messages.
- **Sync Tags**: Create annotated tags only for successful non-no-op sync runs (runs that produce a pull commit or push merge commit).

#### 2.3.6 Interactivity
- **Space Selection**: If `pull` is run without args in root, fetch spaces and show interactive list (using `charmbracelet/huh` or similar).
- **Conflict Resolution**: If `push` detects remote is ahead, prompt user: `[P]ull & Merge`, `[F]orce Overwrite`, `[C]ancel`.
- **Automation Flags**:
    - `--yes`: Auto-approve confirmation prompts (for example, bulk-change or delete confirmations). Does not auto-resolve version conflicts.
    - `--non-interactive`: Disable prompts; fail fast when a required decision is missing.
    - `push --on-conflict=pull-merge|force|cancel`: Non-interactive equivalent for remote-ahead conflict decisions.
- **Safety Confirmation**: If `pull` or `push` affects >10 files or performs remote/local deletes, prompt for confirmation `[y/N]`; `--yes` auto-approves, and `--non-interactive` without `--yes` fails.

#### 2.3.7 Validation Gate
- **`validate` Command**: Checks frontmatter schema, immutable key integrity, link/asset resolution, and Markdown -> ADF conversion.
- **Pre-Push Requirement**: `push` always runs `validate` first.
- **Failure Output**: Returns machine-readable and human-readable diagnostics (file path, field/error, remediation hint).

#### 2.3.8 Developer Tooling
- Provide a top-level `Makefile` for common local workflows (build, test, lint/format, and CLI smoke checks).
- Keep `Makefile` targets aligned with the implemented command set and CI expectations.

## 3. CLI Commands

| Command | Arguments | Description |
| :--- | :--- | :--- |
| `init` | none | Checks git installed. Initializes local repo on branch `main` if needed (or uses current branch), creates `.gitignore` (ignoring `.confluence-state.json`, `.env`), verifies config/prompts for env vars, creates `AGENTS.md` and `README.md`. |
| `pull` | `[TARGET]` | Pulls entire space or a single file. If `TARGET` ends with `.md`, treat as file path; otherwise treat as `SPACE_KEY`. Commits changes, updates state watermark, and manages dirty workspace restoration without requiring user Git commands. |
| `push` | `[TARGET]` | Pushes all changes in space or one file. If `TARGET` ends with `.md`, treat as file path; otherwise treat as `SPACE_KEY`. Includes uncommitted local changes through an internal workspace snapshot. |
| `validate` | `[TARGET]` | Validates sync invariants before push: frontmatter schema, immutable key integrity, links/assets, and Markdown->ADF conversion. |
| `diff` | `[TARGET]` | Shows file- or space-scoped diff against Confluence. If `TARGET` ends with `.md`, treat as file path; otherwise treat as `SPACE_KEY`. |

Automation support for `pull`/`push`: `--yes`, `--non-interactive`; `push` additionally supports `--on-conflict=pull-merge|force|cancel`.

## 4. Implementation Steps

### Phase 1: Setup & Configuration
- [ ] Initialize Go module: `go mod init github.com/rgonek/confluence-markdown-sync`.
- [ ] Setup `cobra` for CLI commands.
- [ ] Implement `config` package to read environment variables (`ATLASSIAN_*` as canonical, with `CONFLUENCE_*` compatibility aliases) and `.env`.
- [ ] Implement `init` command:
    - [ ] Git check.
    - [ ] Repo bootstrap (new `main` or existing).
    - [ ] Env var prompting and `.env` creation.
    - [ ] `.gitignore` creation.
    - [ ] `AGENTS.md` creation (AI rules).
    - [ ] `README.md` creation (User guide + Windows env instructions).
- [ ] Add target parser: `.md` suffix => file mode, otherwise space mode.
- [ ] Add git capability checks: `worktree` support and tag creation permissions (local-only; no remote required).
- [ ] Add `validate` command wiring and shared target resolution.
- [ ] Add shared automation flags for `pull`/`push`: `--yes`, `--non-interactive`.
- [ ] Add push conflict policy flag parsing/validation: `--on-conflict=pull-merge|force|cancel`.
- [ ] Add top-level `Makefile` with standard developer targets (at minimum: `build`, `test`, and `lint`/`fmt`).

### Phase 2: Confluence Client
- [ ] Create `confluence` package.
- [ ] Implement `Client` struct with `GetPage(id)`, `GetSpacePages(spaceKey)`, `UpdatePage(id, content)`.
- [ ] Handle Confluence API V2 (if available) or V1 for content body storage format (ADF).
- [ ] Implement `GetSpaceChanges(spaceKey, timestamp)` for incremental sync.
- [ ] Implement page archive API (`ArchivePage`) and optional hard-delete API (`DeletePage`).
- [ ] Implement attachment delete API (`DeleteAttachment`).

### Phase 3: Content Conversion
- [ ] Integrate `github.com/rgonek/jira-adf-converter`.
- [ ] Implement `converter` package.
- [ ] Add converter adapter hooks for link/media resolution in both directions.
- [ ] Define hook context payloads (current page path, space key, link/media attributes) and fallback behavior.
- [ ] Add conversion modes (`best-effort` and `strict`) with structured unresolved-reference diagnostics.
- [ ] `ToMarkdown(adfJSON) -> (string, error)`
- [ ] `ToADF(markdown) -> (jsonNode, error)`

### Phase 4: File System Operations
- [ ] Implement `filesystem` package.
- [ ] `SavePage(pageData, path)`: Handles frontmatter injection and file writing.
- [ ] `LoadPage(path)`: Reads file, separates frontmatter from content.
- [ ] Implement path sanitization logic for filenames.
- [ ] Implement frontmatter schema validation (required `confluence_page_id`, `confluence_version`, etc.).
- [ ] Implement `State` management (read/write `.confluence-state.json`, including watermark and indexes).
- [ ] Implement immutable frontmatter key checks (`confluence_page_id`, `confluence_space_key`).

### Phase 5: Synchronization Logic (`pull`)
- [ ] Implement logic to build the page tree from flat API response.
- [ ] Recursive function to traverse tree and create directory structure.
- [ ] Handle collisions (e.g., two pages with same name).
- [ ] Implement high-watermark fetch with overlap window and `max_remote_modified_at` tracking.
- [ ] Build deterministic `page_path_by_id` before converting any page content.
- [ ] Build deterministic `attachment_path_by_id` before converting media references.
- [ ] Convert ADF -> Markdown using resolver hooks to rewrite page/media references per current page path.
- [ ] Preserve link anchors and normalize relative path rendering during pull rewrites.
- [ ] Emit diagnostics for unresolved same-space links/assets in pull best-effort mode.
- [ ] Implement CLI-managed Git Stash/Commit/Restore workflow with stash-ref tracking, conflict-safe restore, and user-facing recovery without manual Git.
- [ ] Implement Attachment download & Link rewriting.
- [ ] Implement remote deletion reconciliation for pages/assets and index updates.
- [ ] Implement no-op pull detection (no commit/no tag when nothing changed in scope).
- [ ] Implement creation of annotated pull sync tags for non-no-op pulls.

### Phase 6: Synchronization Logic (`push`)
- [ ] Implement logic to detect changed files from workspace snapshot state (including staged/unstaged/untracked/deleted).
- [ ] Implement no-op push short-circuit (no snapshot/sync-branch/worktree/tag when nothing changed in scope).
- [ ] Implement internal workspace snapshot commit creation under hidden refs (`refs/confluence-sync/snapshots/...`).
- [ ] Implement ephemeral sync branch lifecycle (create, per-file commit, merge-on-success, keep-on-failure).
- [ ] Implement temporary `git worktree` lifecycle for push runs.
- [ ] Run `validate` as mandatory pre-push gate against snapshot content.
- [ ] Build `page_id_by_path`/`attachment_id_by_path` lookup maps before Markdown -> ADF conversion.
- [ ] Run reverse conversion with strict resolver hooks so unresolved relative links/assets fail before remote writes.
- [ ] Update loop for `push` (conflict detection).
- [ ] Implement Attachment upload & Link resolution using reverse resolver hook outputs.
- [ ] Implement local deletion handling (archive/delete remote pages and attachments via indexes).
- [ ] Implement per-file Git Commit after push.
- [ ] Add structured commit trailers to push commits.
- [ ] Implement out-of-scope workspace state capture/restore across successful push merge.
- [ ] Implement snapshot/sync-branch retention on failure and cleanup on full success.
- [ ] Implement creation of annotated push sync tags for non-no-op push runs.
- [ ] Implement active workspace refresh to merged push result without requiring manual Git commands.
- [ ] Implement `diff` command.

### Phase 7: Testing & Validation
- [ ] Unit tests: frontmatter parsing/validation, target parsing, path collision handling.
- [ ] Unit tests: immutable frontmatter edits are rejected.
- [ ] Unit tests: resolver hook fallback vs strict-mode failures for unresolved links/media.
- [ ] Integration tests: `init` creates new repositories on `main` (never `master`).
- [ ] Integration tests: pull watermark correctness, delete reconciliation, stash restore, push sync-branch merge flow.
- [ ] Integration tests: pull rewrites same-space page links to relative paths via `page_path_by_id` and preserves anchors.
- [ ] Integration tests: pull unresolved same-space references emit diagnostics and keep fallback links.
- [ ] Integration tests: worktree creation/cleanup and failure-path branch retention.
- [ ] Integration tests: sync tag creation only on non-no-op runs and trailer presence/format.
- [ ] Integration tests: `push` fails fast when `validate` reports invariant violations.
- [ ] Integration tests: validate/push strict mode fails unresolved relative links/assets before remote writes.
- [ ] Integration tests: `push` includes unstaged/untracked/deleted workspace changes.
- [ ] Integration tests: hidden snapshot refs are cleaned on success and retained on failure.
- [ ] Integration tests: push preserves out-of-scope dirty workspace state (`staged`/`unstaged`/`untracked`/deletions).
- [ ] Integration tests: `--yes`, `--non-interactive`, and `--on-conflict` produce deterministic non-interactive behavior.
- [ ] Integration tests: full pull/push flows succeed with no Git remote configured.
- [ ] Integration tests: recovery paths provide CLI-only guidance (no manual Git commands required).
- [ ] Golden tests: Markdown <-> ADF round-trip for representative Confluence content, including link/media resolver scenarios.
- [ ] End-to-end dry-run mode (`--dry-run`) verification for destructive actions.

## 5. Directory Structure
```
confluence-markdown-sync/
├── cmd/
│   ├── root.go
│   ├── pull.go
│   ├── push.go
│   ├── init.go
│   ├── validate.go
│   └── diff.go
├── internal/
│   ├── config/       # Env var loading
│   ├── confluence/   # API client
│   ├── converter/    # Markdown <-> ADF logic
│   ├── fs/           # File system & Frontmatter handling
│   ├── git/          # Git command wrappers
│   └── sync/         # Core synchronization logic (Tree building)
├── Makefile
├── go.mod
├── go.sum
└── main.go
```
