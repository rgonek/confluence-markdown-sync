# Confluence Markdown Sync CLI Implementation Plan

## 1. Overview
This document outlines the plan for building a CLI tool in Go that synchronizes Confluence pages with a local directory. It converts Confluence's Atlassian Document Format (ADF) JSON to Markdown for local editing and converts Markdown back to ADF for publishing updates to Confluence.

This design uses Git as a local history engine only (no Git remote required). The CLI owns Git operations (branches, worktrees, tags, snapshots), so users should not need to run Git commands directly.

**Binary Name:** `cms`

**Key Libraries:**
- `github.com/rgonek/jira-adf-converter/converter`: Forward conversion (ADF JSON -> Markdown) via `converter.New(converter.Config)` and `ConvertWithContext(...)`, returning `converter.Result{Markdown, Warnings}`.
- `github.com/rgonek/jira-adf-converter/mdconverter`: Reverse conversion (Markdown -> ADF JSON) via `mdconverter.New(mdconverter.ReverseConfig)` and `ConvertWithContext(...)`, returning `mdconverter.Result{ADF, Warnings}`.
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
    -   Convert page ADF to Markdown using `converter.ConvertWithContext(ctx, adfJSON, converter.ConvertOptions{SourcePath: <planned-md-path>})`.
    -   Use `converter.Config{ResolutionMode: converter.ResolutionBestEffort, LinkHook: ..., MediaHook: ...}` so unresolved refs degrade to fallback output with warnings instead of failing pull.
    -   Collect `converter.Result.Warnings` (especially `unresolved_reference`) and surface them as pull diagnostics.
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
    -   **Convert Markdown -> ADF**: Run `mdconverter.ConvertWithContext(ctx, markdown, mdconverter.ConvertOptions{SourcePath: <md-path>})` using `mdconverter.ReverseConfig{ResolutionMode: mdconverter.ResolutionStrict, LinkHook: ..., MediaHook: ...}`. Fail file processing on unresolved refs or invalid hook output.
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
- **Converter Integration (concrete API)**:
    - `pull`/`diff` use `converter.New(converter.Config)` + `ConvertWithContext(ctx, adfJSON, converter.ConvertOptions{SourcePath: ...})`.
    - `validate`/`push` use `mdconverter.New(mdconverter.ReverseConfig)` + `ConvertWithContext(ctx, markdown, mdconverter.ConvertOptions{SourcePath: ...})`.
    - Both directions surface structured warnings (`converter.Result.Warnings`, `mdconverter.Result.Warnings`) that must be translated into CLI diagnostics.
- **Hook Contract (exact signatures)**:
    - Forward link hook: `func(context.Context, converter.LinkRenderInput) (converter.LinkRenderOutput, error)`.
    - Forward media hook: `func(context.Context, converter.MediaRenderInput) (converter.MediaRenderOutput, error)`.
    - Reverse link hook: `func(context.Context, mdconverter.LinkParseInput) (mdconverter.LinkParseOutput, error)`.
    - Reverse media hook: `func(context.Context, mdconverter.MediaParseInput) (mdconverter.MediaParseOutput, error)`.
    - Hook metadata includes typed fields (`PageID`, `SpaceKey`, `AttachmentID`, `Filename`, `Anchor`) plus raw attrs payloads for custom mapping logic.
- **Hook Output Validation (library-enforced)**:
    - Forward handled link output requires non-empty `Href` unless `TextOnly=true`.
    - Forward handled media output requires non-empty `Markdown`.
    - Reverse handled link output requires non-empty `Destination`; `ForceLink` and `ForceCard` are mutually exclusive.
    - Reverse handled media output requires `MediaType` in `{image,file}` and exactly one of `ID` or `URL`.
- **Invocation Notes (library behavior)**:
    - ADF -> Markdown hooks run on link marks, inline cards, then media nodes.
    - Markdown -> ADF detects `mention:` links before link hooks; remaining links then pass through hook/card heuristics.
- **Attachments**:
    - **Pull Planning**: Build `attachment_path_by_id` before conversion.
    - **Download**: CLI scans ADF for media and downloads files to `assets/`.
    - **Storage Pattern**: Use `assets/<page-id>/<attachment-id>-<filename>` to avoid name collisions.
    - **Link Rewrite**: Pull media hook rewrites Markdown image/file references to local relative paths (e.g., `![Image](assets/12345/8899-diagram.png)`).
    - **Push Mapping**: Push media hook resolves local asset paths to `MediaParseOutput{MediaType, ID|URL, Alt}`; sync uploads missing attachments, then writes resolved IDs/URLs back into the outgoing ADF payload.
    - **ADF Mapping**: Push conversion emits ADF `mediaSingle`/`media` nodes with resolved attachment identity (`id` or `url`).
- **Page Links**:
    - **Pull Planning**: Build `page_path_by_id` before converting page content.
    - **Pull Rewrite**: Pull link hook resolves Confluence page links to relative Markdown links (e.g., `[Link](./ChildPage.md)`) using `page_path_by_id` and current page path.
    - **Push Rewrite**: Build `page_id_by_path`, then reverse link hooks resolve local relative links to canonical Confluence destinations before ADF emission.
    - **Anchor Handling**: Preserve in-document fragments while rewriting links in both directions.
- **Resolution Modes**:
    - **Best-Effort** (`pull`, `diff`): use `ResolutionBestEffort`; `ErrUnresolved` falls back to default behavior and emits diagnostics.
    - **Strict** (`validate`, `push`): use `ResolutionStrict`; `ErrUnresolved` fails conversion before any remote write.
- **Side-Effect Boundary**:
    - Hooks return mapping decisions only; sync orchestration owns network/filesystem side effects (downloads/uploads/file writes/deletes).

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
- **Converter Profile Match**: `validate` uses the same strict reverse-conversion profile as `push` (`mdconverter.ResolutionStrict` + same link/media hook adapters) so push behavior is predictable.
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

## 4. Delivery Plan (PR-by-PR)

### 4.0 Merge Rules (apply to every PR)
- [ ] Keep invariants intact: `push` always gates on `validate`, immutable frontmatter keys remain enforced, and no unresolved strict conversion reaches remote writes.
- [ ] Keep docs aligned in the same PR (`README.md`, `AGENTS.md`, and this plan when behavior changes).
- [ ] Add or update tests for changed behavior before merge.
- [ ] Keep each PR independently reviewable and shippable.

### PR-01 - CLI Bootstrap, Init, Config, and Tooling
Checklist:
- [ ] Initialize module, command tree (`cobra`), and shared target parser (`.md` => file mode, else space mode).
- [ ] Implement config loading (`ATLASSIAN_*`, compatibility `CONFLUENCE_*`, and `.env` support).
- [ ] Implement `init` command (git checks/bootstrap, `.env`, `.gitignore`, template docs).
- [ ] Wire shared automation flags (`--yes`, `--non-interactive`) and push conflict flag parsing (`--on-conflict`).
- [ ] Add top-level `Makefile` (`build`, `test`, `lint`/`fmt`).
Done criteria:
- [ ] `cms --help` shows all planned commands and flags.
- [ ] `init` can bootstrap a fresh repo on `main` and initialize config files.
- [ ] Unit tests cover target parsing and config precedence.

### PR-02 - Confluence Client and Local Data Model Foundation
Checklist:
- [x] Create `confluence` package with page/space/change APIs and archive/delete endpoints.
- [x] Create filesystem/state layer: frontmatter read/write, path sanitization, `.confluence-state.json` IO.
- [x] Implement immutable frontmatter key checks and schema validation primitives.
Done criteria:
- [x] Unit tests cover frontmatter schema, immutable key protection, and state persistence.
- [x] Client interfaces are stable enough for `pull`/`push` orchestration.

### PR-03 - Converter Adapter + Hook Profiles + `validate`
Checklist:
- [x] Integrate `converter` and `mdconverter` with internal adapter constructors.
- [x] Implement forward profile (`best_effort`) for `pull`/`diff` and reverse profile (`strict`) for `validate`/`push`.
- [x] Pass `ConvertOptions{SourcePath: ...}` through all conversion entrypoints.
- [x] Implement hook adapters (link/media both directions) and warning-to-diagnostic mapping.
- [x] Implement `validate [TARGET]` with strict reverse conversion + hook parity with planned `push` profile.
Done criteria:
- [x] `validate` fails on strict unresolved refs and immutable-key edits.
- [x] Unit tests cover unresolved behavior (`best_effort` vs `strict`) and hook-output validation constraints.


### PR-04 - `pull` End-to-End (Best-Effort Conversion)
Checklist:
- [ ] Implement incremental fetch (watermark + overlap), deterministic path planning (`page_path_by_id`, `attachment_path_by_id`), and conversion flow.
- [ ] Implement link/media rewrite behavior, anchor preservation, attachment download, and delete reconciliation.
- [ ] Implement scoped stash/restore flow, no-op detection, scoped commit creation, and pull sync tagging.
Done criteria:
- [ ] Integration tests verify pull rewrites, unresolved diagnostics fallback, delete reconciliation, and watermark updates.
- [ ] Integration tests verify stash restore behavior and tag creation only on non-no-op pulls.

### PR-05 - `push` v1 (Functional Sync Loop on Clean Workspace)
Checklist:
- [ ] Implement in-scope change detection and mandatory pre-push `validate` gate.
- [ ] Build `page_id_by_path` / `attachment_id_by_path` lookup maps and strict reverse conversion before writes.
- [ ] Implement conflict policy handling (`pull-merge|force|cancel`), page update/archive flow, attachment upload/delete flow.
- [ ] Implement per-file commits with structured trailers and no-op push short-circuit.
Done criteria:
- [ ] Integration tests verify strict unresolved failures happen before remote writes.
- [ ] Integration tests verify conflict-policy behavior and push commit trailer format.

### PR-06 - `push` v2 (Isolated Worktree + Snapshot/Recovery Model)
Checklist:
- [ ] Implement hidden snapshot refs (`refs/confluence-sync/snapshots/...`) for in-scope workspace capture.
- [ ] Implement ephemeral sync branch + temporary worktree lifecycle.
- [ ] Include staged/unstaged/untracked/deleted workspace changes in push snapshots.
- [ ] Implement merge-on-success, cleanup-on-success, and retain-on-failure behavior for recovery refs.
- [ ] Restore out-of-scope workspace state exactly after successful merge and create non-no-op push sync tags.
Done criteria:
- [ ] Integration tests verify snapshot/worktree lifecycle, failure retention, and success cleanup.
- [ ] Integration tests verify out-of-scope workspace preservation and no-op push (no refs/merge/tag).

### PR-07 - `diff`, Hardening, and Final Test Matrix
Checklist:
- [ ] Implement `diff [TARGET]` with best-effort remote conversion and scoped comparison.
- [ ] Finalize non-interactive behavior (`--yes`, `--non-interactive`, `--on-conflict`) across pull/push.
- [ ] Add/finish round-trip golden tests and end-to-end integration scenarios (including no-git-remote environment).
- [ ] Refresh docs to final behavior (`README.md`, `AGENTS.md`, plan notes).
Done criteria:
- [ ] `diff` works in both file and space modes.
- [ ] Full CI test matrix passes with all invariants covered.
- [ ] Docs describe the implemented behavior without plan drift.

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
