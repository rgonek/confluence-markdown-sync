# Refactor Plan: Files with 800+ Lines of Code

**Date:** 2026-02-27  
**Goal:** Break up all Go source files exceeding 800 lines into smaller, focused units with clear single responsibilities. No behaviour changes.

---

## Affected Files (sorted by size)

| File | Lines | Priority |
|------|-------|----------|
| `internal/sync/push.go` | 2,587 | High |
| `internal/confluence/client.go` | 1,772 | High |
| `internal/sync/pull.go` | 1,642 | High |
| `cmd/push_test.go` | 1,632 | Medium |
| `cmd/pull.go` | 1,470 | High |
| `cmd/push.go` | 1,371 | High |
| `cmd/pull_test.go` | 1,303 | Medium |
| `internal/confluence/client_test.go` | 1,151 | Medium |
| `internal/sync/pull_test.go` | 969 | Low |

---

## Guiding Principles

1. **No behaviour changes.** Refactoring only — all existing tests must stay green.
2. **One responsibility per file.** Each extracted file should have a clear, single focus.
3. **Keep package membership.** All extracted files stay in the same Go package as the source.
4. **Test files mirror source files.** When a source file is split, split its test file to match.
5. **Run `make test` after every split** to verify no regressions.

---

## Phase 1 — `internal/sync/push.go` (2,587 lines)

This is the most critical and largest file. Split into 6 focused files:

### Extracted files

| New file | Responsibility | Approx lines |
|----------|---------------|--------------|
| `internal/sync/push.go` | Orchestration entry point: `Push(...)`, `PushOptions`, `PushResult`, `PushRemote` interface | ~300 |
| `internal/sync/push_types.go` | All types and enums: `PushChangeType`, `PushFileChange`, `PushCommitPlan`, `PushConflictPolicy`, `pushRollbackTracker` | ~150 |
| `internal/sync/push_page.go` | Page upsert/delete pipeline: `pushUpsertPage`, `pushDeletePage`, `syncPageMetadata` | ~400 |
| `internal/sync/push_hierarchy.go` | Folder hierarchy: `ensureFolderHierarchy`, `precreatePendingPushPages` | ~250 |
| `internal/sync/push_assets.go` | Asset/attachment pipeline: `BuildStrictAttachmentIndex`, `CollectReferencedAssetPaths`, `PrepareMarkdownForAttachmentConversion`, `migrateReferencedAssetsToPageHierarchy` | ~400 |
| `internal/sync/push_adf.go` | ADF post-processing: `ensureADFMediaCollection`, `walkAndFixMediaNodes` | ~200 |

### Steps

1. Create `push_types.go` — move all struct/enum type declarations out of `push.go`.
2. Create `push_assets.go` — move asset/attachment resolution functions.
3. Create `push_adf.go` — move ADF media node fixup functions.
4. Create `push_hierarchy.go` — move folder hierarchy and pre-create functions.
5. Create `push_page.go` — move individual page upsert/delete and metadata sync.
6. Trim `push.go` down to the orchestration entry point.
7. Run `make test`.

---

## Phase 2 — `internal/confluence/client.go` (1,772 lines)

Split into 5 focused files:

### Extracted files

| New file | Responsibility | Approx lines |
|----------|---------------|--------------|
| `internal/confluence/client.go` | `Client` struct, `ClientConfig`, constructor, `newRequest`, `do`, auth, rate-limit, retry core | ~350 |
| `internal/confluence/client_errors.go` | `APIError`, `decodeAPIErrorMessage`, `mapConfluenceErrorCode`, `confluenceStatusHint`, sentinel errors | ~150 |
| `internal/confluence/client_spaces.go` | `ListSpaces`, `GetSpace` | ~100 |
| `internal/confluence/client_pages.go` | `ListPages`, `GetPage`, `GetFolder`, `CreatePage`, `UpdatePage`, `DeletePage`, `CreateFolder`, `MovePage`, `ArchivePages`, `WaitForArchiveTask`, `getArchiveTaskStatus` | ~600 |
| `internal/confluence/client_attachments.go` | `ListAttachments`, `DownloadAttachment`, `UploadAttachment`, `DeleteAttachment`, attachment ID resolution helpers | ~400 |

### Steps

1. Create `client_errors.go` — move error types and helpers.
2. Create `client_spaces.go` — move space methods.
3. Create `client_attachments.go` — move attachment methods.
4. Create `client_pages.go` — move page/folder/archive methods.
5. Trim `client.go` to core HTTP machinery.
6. Run `make test`.

### Test file split (`client_test.go`, 1,151 lines)

Mirror the source split:

| New file | Tests for |
|----------|-----------|
| `client_test.go` | Core client, auth, user-agent, `GetUser` |
| `client_spaces_test.go` | `ListSpaces`, `GetSpace` |
| `client_pages_test.go` | Page/folder/archive methods |
| `client_attachments_test.go` | Attachment methods |
| `client_errors_test.go` | Error decoding, token-leak security test |

---

## Phase 3 — `internal/sync/pull.go` (1,642 lines)

Split into 5 focused files:

### Extracted files

| New file | Responsibility | Approx lines |
|----------|---------------|--------------|
| `internal/sync/pull.go` | Entry point: `Pull(...)`, `PullOptions`, `PullResult`, `PullRemote` interface, `Progress` interface | ~250 |
| `internal/sync/pull_types.go` | Internal structs used during pull orchestration | ~100 |
| `internal/sync/pull_pages.go` | Page listing, change feed, folder hierarchy: `listAllPages`, `listAllChanges`, `ResolveFolderPathIndex`, `resolveFolderHierarchyFromPages` | ~350 |
| `internal/sync/pull_paths.go` | Path planning: `PlanPagePaths`, `deletedPageIDs`, `movedPageIDs`, `recoverMissingPages` | ~300 |
| `internal/sync/pull_assets.go` | Attachment handling: `collectAttachmentRefs`, `resolveUnknownAttachmentRefsByFilename`, `removeAttachmentsForPage` | ~350 |

### Steps

1. Create `pull_types.go` — move internal type declarations.
2. Create `pull_assets.go` — move attachment/media resolution.
3. Create `pull_paths.go` — move path planning and deletion helpers.
4. Create `pull_pages.go` — move listing and hierarchy resolution.
5. Trim `pull.go` to the entry point.
6. Run `make test`.

### Test file split (`pull_test.go`, 969 lines)

| New file | Tests for |
|----------|-----------|
| `pull_test.go` | Core orchestration, incremental, force-full, draft recovery |
| `pull_paths_test.go` | `PlanPagePaths` variants, folder hierarchy fallback |
| `pull_assets_test.go` | Asset resolution, unknown media ID, skip-missing-assets |

---

## Phase 4 — `cmd/pull.go` (1,470 lines)

Split into 4 focused files:

### Extracted files

| New file | Responsibility | Approx lines |
|----------|---------------|--------------|
| `cmd/pull.go` | Command definition, flags, `runPull` entry point | ~200 |
| `cmd/pull_state.go` | State loading and healing: `loadPullStateWithHealing`, `rebuildStateFromConfluenceAndLocal` | ~250 |
| `cmd/pull_stash.go` | Git stash lifecycle and conflict resolution: `stashScopeIfDirty`, `applyAndDropStash`, `handlePullConflict`, `applyPullConflictChoice`, `fixPulledVersionsAfterStashRestore` | ~400 |
| `cmd/pull_context.go` | Target resolution and impact estimation: `resolveInitialPullContext`, `estimatePullImpactWithSpace`, `cleanupFailedPullScope` | ~300 |

### Steps

1. Create `pull_context.go`.
2. Create `pull_state.go`.
3. Create `pull_stash.go`.
4. Trim `cmd/pull.go` to the command entry point.
5. Run `make test`.

### Test file split (`cmd/pull_test.go`, 1,303 lines)

| New file | Tests for |
|----------|-----------|
| `cmd/pull_test.go` | Core run-pull lifecycle, tag creation, no-op |
| `cmd/pull_stash_test.go` | Stash restore, Keep Both conflict, stash-with-discard-local |
| `cmd/pull_state_test.go` | State healing, corrupt state recovery |
| `cmd/pull_context_test.go` | Force flag, safety confirmations, non-interactive gating |

---

## Phase 5 — `cmd/push.go` (1,371 lines)

Split into 4 focused files:

### Extracted files

| New file | Responsibility | Approx lines |
|----------|---------------|--------------|
| `cmd/push.go` | Command definition, flags, `runPush` entry point | ~200 |
| `cmd/push_worktree.go` | Worktree and snapshot lifecycle: `runPushInWorktree`, merge, tag, snapshot ref management | ~350 |
| `cmd/push_stash.go` | Stash management: `restorePushStash`, `restoreTrackedPathsFromStash`, `restoreUntrackedPathsFromStashParent` | ~250 |
| `cmd/push_changes.go` | Change collection and dry-run: `collectSyncPushChanges`, `collectPushChangesForTarget`, `collectGitChangesWithUntracked`, `gitPushBaselineRef`, `prepareDryRunSpaceDir`, `copyDirTree`, `toSyncPushChanges`, `toSyncConflictPolicy`, `runPushDryRun`, `runPushPreflight` | ~450 |

### Steps

1. Create `push_stash.go`.
2. Create `push_changes.go`.
3. Create `push_worktree.go`.
4. Trim `cmd/push.go` to the entry point.
5. Run `make test`.

### Test file split (`cmd/push_test.go`, 1,632 lines)

| New file | Tests for |
|----------|-----------|
| `cmd/push_test.go` | Core lifecycle, trailers, state file tracking, no-op |
| `cmd/push_conflict_test.go` | Conflict policies, pull-merge stash restore |
| `cmd/push_dryrun_test.go` | Dry-run and preflight mode |
| `cmd/push_stash_test.go` | Stash restore, out-of-scope preservation, failure retention |

---

## Phase 6 — Verification and Cleanup

1. Run `make test` — all tests must pass.
2. Run `make lint` — no new lint warnings.
3. Run `make build` — binary builds cleanly.
4. Confirm no file in the repository exceeds 800 lines (with a short script or manual count).
5. Update any import paths or cross-references if needed (should not be required since all splits stay in the same package).

---

## Execution Order

Execute phases sequentially. Each phase must be independently verified with `make test` before starting the next.

```
Phase 1: internal/sync/push.go        (highest risk — largest, most coupled)
Phase 2: internal/confluence/client.go (medium risk — clear method boundaries)
Phase 3: internal/sync/pull.go        (medium risk — mirrors push structure)
Phase 4: cmd/pull.go                  (lower risk — mostly orchestration glue)
Phase 5: cmd/push.go                  (lower risk — mostly orchestration glue)
Phase 6: Verification and cleanup
```

Test files are split in the same phase as their corresponding source file.

---

## Risk Notes

- **Phase 1** is the highest-risk split because `push.go` has many inter-function dependencies (the rollback tracker is passed through several layers). Carefully audit function signatures when moving to `push_page.go` and `push_hierarchy.go`.
- **Circular imports** cannot occur since all splits stay within the same package.
- **Unexported helpers** shared across multiple new files remain accessible since they are in the same package — no visibility changes are needed.
