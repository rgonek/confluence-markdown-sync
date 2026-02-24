# Push New Page Improvements — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix all gaps in the push flow for creating new pages: auto-create Confluence folders for orphaned directories, guard against duplicate page creation, improve placeholder handling, and add transactional safety.

**Architecture:** The directory structure is the single source of truth for hierarchy. When a directory has no index page (`Dir/Dir.md`), a Confluence folder is created instead. Folder IDs are tracked in state and used as parent references. The Confluence v2 folder API cannot directly parent pages, so a v1 move endpoint is used as a follow-up step after page creation.

**Tech Stack:** Go, Confluence REST API v2 (`POST /wiki/api/v2/folders`), Confluence REST API v1 (`PUT /wiki/rest/api/content/{id}/move/append/{targetId}`)

**Key Decision — No parentId in frontmatter:** The directory structure already encodes parent relationships. Adding parentId to frontmatter would create a second source of truth that conflicts with the directory layout, produces noisy diffs on pull when pages move, and doesn't help for new pages where the parent doesn't exist yet. The state file (`page_path_index` + new `folder_path_index`) is sufficient for tracking.

**API Constraint:** Confluence Cloud v2 API does not support `parentId` pointing to a folder when creating a page ([CONFCLOUD-79677](https://community.developer.atlassian.com/t/missing-support-to-create-a-page-in-a-folder-via-rest-api/84850)). The workaround is: create page at space root, then use v1 `PUT /wiki/rest/api/content/{pageId}/move/append/{folderId}` to reparent.

---

## Task 1: Add `CreateFolder` and `MovePage` to Confluence client

**Priority:** High (prerequisite for all other tasks)

**Files:**
- Modify: `internal/confluence/types.go` (add `FolderCreateInput`)
- Modify: `internal/confluence/client.go` (add `CreateFolder`, `MovePage` methods)
- Modify: `internal/confluence/types.go:17-34` (add methods to `Service` interface)
- Create: `internal/confluence/client_test.go` (add tests for new methods)

**Step 1: Write failing tests for CreateFolder and MovePage**

Add to `internal/confluence/client_test.go`:

```go
func TestCreateFolder(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost || r.URL.Path != "/wiki/api/v2/folders" {
            t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
        }
        var body map[string]string
        json.NewDecoder(r.Body).Decode(&body)
        if body["spaceId"] != "space-1" || body["title"] != "Policies" {
            t.Fatalf("unexpected body: %v", body)
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{
            "id":      "folder-1",
            "spaceId": "space-1",
            "title":   "Policies",
        })
    }))
    defer srv.Close()

    client := newTestClient(t, srv)
    folder, err := client.CreateFolder(context.Background(), FolderCreateInput{
        SpaceID: "space-1",
        Title:   "Policies",
    })
    if err != nil {
        t.Fatal(err)
    }
    if folder.ID != "folder-1" || folder.Title != "Policies" {
        t.Fatalf("unexpected folder: %+v", folder)
    }
}

func TestMovePage(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPut {
            t.Fatalf("expected PUT, got %s", r.Method)
        }
        expected := "/wiki/rest/api/content/page-1/move/append/folder-1"
        if r.URL.Path != expected {
            t.Fatalf("path = %q, want %q", r.URL.Path, expected)
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{}`))
    }))
    defer srv.Close()

    client := newTestClient(t, srv)
    err := client.MovePage(context.Background(), "page-1", "folder-1")
    if err != nil {
        t.Fatal(err)
    }
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/confluence/ -run "TestCreateFolder|TestMovePage" -v
```

Expected: FAIL (methods don't exist).

**Step 3: Add types**

In `internal/confluence/types.go`, add:
```go
// FolderCreateInput is used to create a new folder.
type FolderCreateInput struct {
    SpaceID  string
    Title    string
    ParentID string // Optional: parent folder ID for nested folders
}
```

**Step 4: Add `CreateFolder` to `client.go`**

```go
// CreateFolder creates a new folder in a Confluence space.
func (c *Client) CreateFolder(ctx context.Context, input FolderCreateInput) (Folder, error) {
    spaceID := strings.TrimSpace(input.SpaceID)
    if spaceID == "" {
        return Folder{}, errors.New("space ID is required")
    }
    title := strings.TrimSpace(input.Title)
    if title == "" {
        return Folder{}, errors.New("folder title is required")
    }

    body := map[string]string{
        "spaceId": spaceID,
        "title":   title,
    }
    if parentID := strings.TrimSpace(input.ParentID); parentID != "" {
        body["parentId"] = parentID
    }

    req, err := c.newRequest(ctx, http.MethodPost, "/wiki/api/v2/folders", nil, body)
    if err != nil {
        return Folder{}, err
    }

    var payload folderDTO
    if err := c.do(req, &payload); err != nil {
        return Folder{}, err
    }
    return payload.toModel(), nil
}
```

**Step 5: Add `MovePage` to `client.go`**

```go
// MovePage moves a page to be a child of the given target (page or folder) using the v1 API.
func (c *Client) MovePage(ctx context.Context, pageID, targetID string) error {
    pid := strings.TrimSpace(pageID)
    tid := strings.TrimSpace(targetID)
    if pid == "" {
        return errors.New("page ID is required")
    }
    if tid == "" {
        return errors.New("target ID is required")
    }

    pathSuffix := fmt.Sprintf("/wiki/rest/api/content/%s/move/append/%s",
        url.PathEscape(pid), url.PathEscape(tid))
    req, err := c.newRequest(ctx, http.MethodPut, pathSuffix, nil, nil)
    if err != nil {
        return err
    }
    return c.do(req, nil)
}
```

**Step 6: Update `Service` interface in `types.go`**

Add to the `Service` interface:
```go
CreateFolder(ctx context.Context, input FolderCreateInput) (Folder, error)
MovePage(ctx context.Context, pageID, targetID string) error
```

**Step 7: Run tests**

```bash
go test ./internal/confluence/ -run "TestCreateFolder|TestMovePage" -v
go test ./internal/confluence/ -v
```

Expected: All PASS.

**Step 8: Commit**

```bash
git add internal/confluence/types.go internal/confluence/client.go internal/confluence/client_test.go
git commit -m "feat: add CreateFolder and MovePage to Confluence client

CreateFolder uses v2 POST /wiki/api/v2/folders.
MovePage uses v1 PUT /wiki/rest/api/content/{id}/move/append/{targetId}
to reparent pages under folders (v2 doesn't support folder parentId)."
```

---

## Task 2: Add `FolderPathIndex` to state and `PushRemote` interface

**Priority:** High (prerequisite for folder creation during push)

**Files:**
- Modify: `internal/fs/state.go` (add `FolderPathIndex` to `SpaceState`)
- Modify: `internal/sync/push.go:26-42` (add `CreateFolder`, `MovePage` to `PushRemote` interface)
- Modify: `cmd/dry_run_remote.go` (add no-op implementations for new interface methods)

**Step 1: Add FolderPathIndex to SpaceState**

In `internal/fs/state.go`, add to `SpaceState`:
```go
type SpaceState struct {
    LastPullHighWatermark string            `json:"last_pull_high_watermark,omitempty"`
    PagePathIndex        map[string]string  `json:"page_path_index,omitempty"`
    AttachmentIndex      map[string]string  `json:"attachment_index,omitempty"`
    FolderPathIndex      map[string]string  `json:"folder_path_index,omitempty"` // dir path -> folder ID
}
```

Update `NewSpaceState()` and `normalize()` to initialize it:
```go
func NewSpaceState() SpaceState {
    return SpaceState{
        PagePathIndex:   map[string]string{},
        AttachmentIndex: map[string]string{},
        FolderPathIndex: map[string]string{},
    }
}
```

**Step 2: Add methods to PushRemote**

In `internal/sync/push.go`, add to `PushRemote` interface:
```go
CreateFolder(ctx context.Context, input confluence.FolderCreateInput) (confluence.Folder, error)
MovePage(ctx context.Context, pageID, targetID string) error
```

**Step 3: Add no-op implementations to dryRunPushRemote**

In `cmd/dry_run_remote.go`:
```go
func (d *dryRunPushRemote) CreateFolder(ctx context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
    fmt.Fprintf(d.out, "[DRY-RUN] Would create folder %q in space %s\n", input.Title, input.SpaceID)
    return confluence.Folder{ID: "dry-run-folder", Title: input.Title, SpaceID: input.SpaceID}, nil
}

func (d *dryRunPushRemote) MovePage(ctx context.Context, pageID, targetID string) error {
    fmt.Fprintf(d.out, "[DRY-RUN] Would move page %s under %s\n", pageID, targetID)
    return nil
}
```

**Step 4: Update fake push remotes in tests**

Add no-op implementations of `CreateFolder` and `MovePage` to all fake/mock `PushRemote` implementations in test files.

**Step 5: Run tests**

```bash
go test ./...
```

Expected: All pass.

**Step 6: Commit**

```bash
git add internal/fs/state.go internal/sync/push.go cmd/dry_run_remote.go
git commit -m "feat: add FolderPathIndex to state and folder methods to PushRemote"
```

---

## Task 3: Auto-create Confluence folders for orphaned directories during push

**Priority:** High (the main feature — Gap #1)

**Files:**
- Modify: `internal/sync/push.go:539-567` (enhance `resolveParentIDFromHierarchy`)
- Modify: `internal/sync/push.go:365-398` (call folder creation when parent is missing)
- Create: `internal/sync/push_test.go` (add tests for folder auto-creation)

**Step 1: Write failing test**

Add to `internal/sync/push_test.go` or create a new test:

```go
func TestPush_CreatesFolder_WhenParentIndexMissing(t *testing.T) {
    // Setup: spaceDir with "Policies/NewPage.md" (has space key, no id)
    // No "Policies/Policies.md" exists
    // Push should:
    //   1. Detect no parent page for "Policies" directory
    //   2. Create a Confluence folder named "Policies"
    //   3. Create the page at space root
    //   4. Move the page under the folder
    //   5. Store folder ID in state.FolderPathIndex["Policies"]
}
```

The test should use a fake PushRemote that records CreateFolder and MovePage calls.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPush_CreatesFolder_WhenParentIndexMissing" -v
```

Expected: FAIL.

**Step 3: Implement folder auto-creation**

The approach:

**3a: Add a `folderIDByPath` map to the Push function scope**

In `Push()` (line 121), alongside `pageIDByPath`, initialize:
```go
folderIDByPath := make(map[string]string)
// Populate from state
for dirPath, folderID := range state.FolderPathIndex {
    folderIDByPath[normalizeRelPath(dirPath)] = folderID
}
```

**3b: Enhance `resolveParentIDFromHierarchy` to accept folder index**

Change signature to:
```go
func resolveParentIDFromHierarchy(
    relPath, pageID, fallbackParentID string,
    pageIDByPath PageIndex,
    folderIDByPath map[string]string,
) (parentID string, parentIsFolder bool)
```

Add folder lookup after page lookup fails:
```go
// After checking candidatePath in pageIDByPath (line 553)...
// Also check if a folder exists for this directory
folderID := strings.TrimSpace(folderIDByPath[normalizeRelPath(currentDir)])
if folderID != "" {
    return folderID, true
}
```

**3c: Create folder in `pushUpsertPage` when parent is missing**

In `pushUpsertPage` (around line 368), after resolving parent:
```go
resolvedParentID, parentIsFolder := resolveParentIDFromHierarchy(relPath, "", fallbackParentID, pageIDByPath, folderIDByPath)

// If no parent found and we're in a subdirectory, create folder hierarchy
if resolvedParentID == "" {
    dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
    if dirPath != "" && dirPath != "." {
        resolvedParentID, err = ensureFolderHierarchy(ctx, remote, space.ID, dirPath, folderIDByPath)
        if err != nil {
            return PushCommitPlan{}, fmt.Errorf("create folder for %s: %w", relPath, err)
        }
        parentIsFolder = true
    }
}
```

**3d: Implement `ensureFolderHierarchy`**

```go
// ensureFolderHierarchy creates Confluence folders for each segment of dirPath
// that doesn't already exist, returning the deepest folder's ID.
func ensureFolderHierarchy(
    ctx context.Context,
    remote PushRemote,
    spaceID string,
    dirPath string,
    folderIDByPath map[string]string,
) (string, error) {
    segments := strings.Split(filepath.ToSlash(dirPath), "/")
    var currentParentID string
    var currentPath string

    for _, seg := range segments {
        if currentPath == "" {
            currentPath = seg
        } else {
            currentPath = currentPath + "/" + seg
        }
        normalized := normalizeRelPath(currentPath)

        // Check if folder already exists in our index
        if existingID := folderIDByPath[normalized]; existingID != "" {
            currentParentID = existingID
            continue
        }

        // Create the folder
        input := confluence.FolderCreateInput{
            SpaceID: spaceID,
            Title:   seg,
        }
        if currentParentID != "" {
            input.ParentID = currentParentID
        }

        folder, err := remote.CreateFolder(ctx, input)
        if err != nil {
            return "", fmt.Errorf("create folder %q: %w", seg, err)
        }

        folderIDByPath[normalized] = folder.ID
        currentParentID = folder.ID
    }

    return currentParentID, nil
}
```

**3e: Move page under folder after creation**

After creating the page (line 381), if `parentIsFolder`:
```go
created, err := remote.CreatePage(ctx, confluence.PageUpsertInput{
    SpaceID: space.ID,
    // Don't set ParentPageID — v2 API can't parent to folder
    Title:   title,
    Status:  targetState,
    BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
})
if err != nil {
    return PushCommitPlan{}, fmt.Errorf("create page for %s: %w", relPath, err)
}

// Move page under folder (v2 can't parent to folder directly)
if parentIsFolder && resolvedParentID != "" {
    if err := remote.MovePage(ctx, created.ID, resolvedParentID); err != nil {
        return PushCommitPlan{}, fmt.Errorf("move page %s under folder: %w", relPath, err)
    }
}
```

**3f: Persist folder index in state**

At the end of `Push()`, save the folder index:
```go
state.FolderPathIndex = folderIDByPath
```

**Step 4: Run tests**

```bash
go test ./internal/sync/ -v
go test ./cmd/ -v -count=1
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add internal/sync/push.go internal/sync/push_test.go
git commit -m "feat: auto-create Confluence folders for orphaned directories on push

When a directory has no index page (Dir/Dir.md), a Confluence folder
is created instead. Pages are moved under the folder using the v1
move endpoint (v2 doesn't support folder parentId). Folder IDs are
tracked in state.FolderPathIndex for reuse across pushes."
```

---

## Task 4: Guard against deleted ID field creating duplicate pages

**Priority:** High (Gap #2)

**Files:**
- Modify: `internal/sync/push.go:183-225` (add guard in change processing loop)
- Add test: `internal/sync/push_test.go`

**Step 1: Write failing test**

```go
func TestPush_ErrorsWhenIDRemovedFromExistingPage(t *testing.T) {
    // Setup: state.PagePathIndex has "doc.md" -> "page-123"
    // File "doc.md" has frontmatter with space key but NO id field
    // Change type is Modify (not Add)
    // Push should error: "id field was removed from doc.md (was page-123)"
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPush_ErrorsWhenIDRemovedFromExistingPage" -v
```

Expected: FAIL.

**Step 3: Add guard in `pushUpsertPage`**

After reading frontmatter (line 319), before the `if pageID != ""` branch:

```go
pageID := strings.TrimSpace(doc.Frontmatter.ID)

// Guard: detect removed ID from previously synced page
if pageID == "" {
    if existingID := strings.TrimSpace(state.PagePathIndex[normalizeRelPath(relPath)]); existingID != "" {
        return PushCommitPlan{}, fmt.Errorf(
            "%s was previously synced as page %s but the id field was removed from frontmatter; "+
                "restore the id field or delete the state entry to create a new page",
            relPath, existingID,
        )
    }
}
```

**Step 4: Run tests**

```bash
go test ./internal/sync/ -v
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add internal/sync/push.go internal/sync/push_test.go
git commit -m "fix: error when id field removed from previously synced page

Prevents accidental duplicate page creation when a user removes
the id from frontmatter of an existing synced file."
```

---

## Task 5: Replace placeholder content with empty document

**Priority:** Medium (Gap #3)

**Files:**
- Modify: `internal/sync/push.go:380`

**Step 1: Replace placeholder**

Change line 380 from:
```go
BodyADF: []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Initial sync..."}]}]}`),
```

To an empty document:
```go
BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
```

This creates a blank page instead of one with "Initial sync..." text, so if the update step fails, users see a blank page rather than confusing placeholder text.

**Step 2: Run tests**

```bash
go test ./internal/sync/ -v
go test ./cmd/ -v -count=1
```

Expected: All pass. If any test asserts on the placeholder text, update the assertion.

**Step 3: Commit**

```bash
git add internal/sync/push.go
git commit -m "fix: use empty ADF document for placeholder page creation

Prevents confusing 'Initial sync...' text appearing on Confluence
if the content update step fails after page creation."
```

---

## Task 6: Add warning when parent hierarchy cannot be fully resolved

**Priority:** Medium (helps debug hierarchy issues)

**Files:**
- Modify: `internal/sync/push.go` (add `PushDiagnostic` type and collect warnings)
- Modify: `internal/sync/push.go:94-98` (add `Diagnostics` to `PushResult`)

**Step 1: Add PushDiagnostic type**

```go
// PushDiagnostic is a non-fatal warning during push.
type PushDiagnostic struct {
    Path    string
    Code    string
    Message string
}
```

Add to `PushResult`:
```go
type PushResult struct {
    State       fs.SpaceState
    Commits     []PushCommitPlan
    Diagnostics []PushDiagnostic
}
```

**Step 2: Emit diagnostic when folder auto-creation happens**

In the folder creation path (Task 3), before creating the folder:
```go
diagnostics = append(diagnostics, PushDiagnostic{
    Path:    relPath,
    Code:    "auto_created_folder",
    Message: fmt.Sprintf("no index page found for directory %q; created Confluence folder instead", dirPath),
})
```

**Step 3: Display diagnostics in cmd/push.go**

After the push result is returned, print diagnostics:
```go
for _, diag := range result.Diagnostics {
    fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message)
}
```

**Step 4: Run tests**

```bash
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add internal/sync/push.go cmd/push.go
git commit -m "feat: emit diagnostics when folders are auto-created during push

Warns users when a directory has no index page and a Confluence
folder is created instead, so they can add an index page if desired."
```

---

## Task 7: Pull-side folder awareness — map Confluence folders to local directories

**Priority:** Medium (ensures round-trip consistency)

**Files:**
- Modify: `internal/sync/pull.go` (persist folder IDs in state during pull)

**Step 1: Save folder index during pull**

The pull flow already resolves folder hierarchies via `resolveFolderHierarchyFromPages` (pull.go:642). The `folderByID` map is available. Persist it to state:

In `Pull()`, after folder resolution:
```go
// Build folder path index for state persistence
folderPathIndex := make(map[string]string)
for _, folder := range folderByID {
    // Reconstruct the local directory path from folder hierarchy
    folderPath := buildFolderLocalPath(folder, folderByID)
    if folderPath != "" {
        folderPathIndex[normalizeRelPath(folderPath)] = folder.ID
    }
}
result.State.FolderPathIndex = folderPathIndex
```

**Step 2: Implement `buildFolderLocalPath`**

Walk up the folder chain to build the local directory path:
```go
func buildFolderLocalPath(folder confluence.Folder, folderByID map[string]confluence.Folder) string {
    var segments []string
    current := folder
    for {
        segments = append([]string{fs.SanitizePathSegment(current.Title)}, segments...)
        if current.ParentID == "" || strings.EqualFold(current.ParentType, "space") {
            break
        }
        parent, exists := folderByID[current.ParentID]
        if !exists {
            break
        }
        current = parent
    }
    return filepath.ToSlash(filepath.Join(segments...))
}
```

**Step 3: Run tests**

```bash
go test ./internal/sync/ -v
go test ./...
```

Expected: All pass.

**Step 4: Commit**

```bash
git add internal/sync/pull.go
git commit -m "feat: persist Confluence folder IDs in state during pull

Enables push to reuse existing folder IDs instead of creating
duplicates when auto-creating folders for orphaned directories."
```

---

## Execution Order and Dependencies

```
Task 1 (CreateFolder + MovePage client methods)
  │
Task 2 (FolderPathIndex in state, PushRemote interface)
  │
Task 3 (Auto-create folders during push) ── depends on Tasks 1, 2
  │
Task 4 (Guard deleted ID field) ── independent
  │
Task 5 (Empty placeholder content) ── independent
  │
Task 6 (Push diagnostics for folders) ── depends on Task 3
  │
Task 7 (Pull-side folder awareness) ── depends on Task 2
```

| # | Task | Priority | Depends On |
|---|------|----------|------------|
| 1 | Add CreateFolder + MovePage to client | High | — |
| 2 | Add FolderPathIndex to state + PushRemote | High | — |
| 3 | Auto-create folders for orphaned dirs | High | 1, 2 |
| 4 | Guard against deleted ID field | High | — |
| 5 | Replace placeholder with empty doc | Medium | — |
| 6 | Push diagnostics for auto-created folders | Medium | 3 |
| 7 | Pull-side folder awareness | Medium | 2 |

Tasks 1, 2, 4, and 5 can start in parallel.
Task 3 is the main feature and requires 1 + 2.
Tasks 6 and 7 are follow-ups after 3.
