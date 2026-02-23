# Cross-Space Link Resolution Plan (`conf relink`)

## 1. Overview
This document outlines the plan for implementing cross-space link resolution in the `conf` CLI. The goal is to allow absolute Confluence URLs in Markdown files to be replaced with local relative paths even when they point to pages in different spaces, provided those spaces are also managed in the local repository.

## 2. Design

### 2.1 Repository-Wide Indexing
Currently, the tool operates primarily on a per-space basis. To support cross-space links, we need a global view of all pages in the repository.

- **Global Page Index**: A mapping of `PageID -> AbsoluteLocalPath`.
- **Discovery**: The tool will scan the entire repository for `.confluence-state.json` files to discover managed spaces and their page paths.
- **Components**:
    - `internal/fs/state.go`: Add `FindAllStateFiles(root string)` to discover state files.
    - `internal/sync/index.go`: Add `BuildGlobalPageIndex(root string)` to aggregate paths from all discovered spaces.

### 2.2 The Relink Engine
A utility to identify and transform absolute Confluence URLs into local relative links.

- **Target**: `internal/sync/relink.go` (New File)
- **Logic**:
    - **Regex Matching**: Match Markdown links `[text](url)`.
    - **ID Extraction**: Extract the Confluence `PageID` from the absolute URL (using the `ExtractPageID` utility).
    - **Relative Pathing**: If the `PageID` exists in the `GlobalPageIndex`, calculate the relative path from the source file to the target local Markdown file.
    - **Anchors**: Fragments (e.g., `#Section-Name`) from the original URL are preserved and appended to the new relative path.

### 2.3 Command: `conf relink [TARGET]`
A new command to perform the link resolution.

- **Behavior**:
    - **Targeted Mode** (`conf relink space2`):
        1. Identify all `PageID`s belonging to `space2`.
        2. Scan **all other spaces** (space1, space3, etc.) for absolute links pointing to those IDs.
        3. Group findings by space and prompt the user:
           `Found X absolute links in 'space1' pointing to 'space2'. Update 'space1'? [y/N]`
    - **Global Mode** (no target):
        1. Scan all managed spaces for **any** absolute links that can be resolved via the `GlobalPageIndex`.
        2. Prompt space-by-space before applying changes.

### 2.4 Pull Integration: `conf pull [TARGET] --relink`
Automatically trigger the relink logic after a successful pull to clean up references in other spaces.

- **Flag**: `--relink` (shorthand `-r`).
- **Logic**: If set, and the pull succeeds, it triggers the "Targeted Mode" of the relink command for the space that was just pulled.

## 3. Implementation Steps

### Step 1: Core Utilities & Indexing
- [ ] Export `ExtractPageID` in `internal/sync/hooks.go`.
- [ ] Add `FindAllStateFiles` to `internal/fs/state.go`.
- [ ] Add `GlobalPageIndex` and `BuildGlobalPageIndex` to `internal/sync/index.go`.

### Step 2: Relink Engine
- [ ] Create `internal/sync/relink.go`.
- [ ] Implement `ResolveLinksInFile(path, GlobalPageIndex)`.
- [ ] Implement `ResolveLinksInSpace(spaceDir, GlobalPageIndex, targetPageIDs)`.

### Step 3: CLI Commands
- [ ] Create `cmd/relink.go` with the `relink` command and confirmation UI.
- [ ] Add `--relink` / `-r` flag to `cmd/pull.go`.
- [ ] Register `relink` command in `cmd/root.go`.

## 4. Verification Plan
- **Unit Tests**:
    - Verify `GlobalPageIndex` aggregates correctly across multiple mock space directories.
    - Verify `ResolveLinksInFile` handles various URL formats, anchors, and relative path calculations.
- **Integration Tests**:
    - Simulate a repository with two spaces (`space1` and `space2`).
    - Pull `space1` which contains absolute links to `space2`.
    - Pull `space2`.
    - Run `conf relink space2` and verify `space1` files are updated with relative paths.
    - Verify `conf pull space2 --relink` performs the same action.
