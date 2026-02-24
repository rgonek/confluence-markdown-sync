# Pull Command Production Readiness — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix all 16 new issues found in the pull command analysis to make `conf pull` production-grade — safe under Confluence Cloud rate limits, cancellation-aware, and robust against partial failures.

**Architecture:** 12 actionable tasks ordered by priority. Critical fixes target data corruption and API abuse. Warning fixes improve safety and correctness. Suggestion fixes improve UX and maintainability. Each task is independently committable and testable.

**Tech Stack:** Go 1.24, `sync/errgroup`, `context`, `os`, `io`, `path/filepath`, `bufio`

**Issue Coverage Map:**

| Analysis # | Issue | Task |
|------------|-------|------|
| 1 | N+1 API call storm for page details | Task 1 |
| 2 | `time.Sleep` in retry ignores context | Task 2 |
| 3 | Orphaned partial files on download failure | Task 3 |
| 4 | Duplicate page listing (estimate + pull) | Task 4 |
| 5 | `handlePullConflict` scopes checkout to `.` | Task 5 |
| 6 | No max-iterations guard in `listAllPages` | Task 6 |
| 7 | Unbounded user cache growth | N/A (accepted, <100 users typical) |
| 8 | Silent status/label fetch failures overwrite data | Task 7 |
| 9 | Space key discovery fragility | Task 8 |
| 10 | `handlePullConflict` bypasses `out` writer | Task 9 |
| 11 | No sub-step progress for page fetches | Task 10 |
| 12 | Individual API calls for draft recovery | N/A (accepted, correctness over speed) |
| 13 | Opaque attachment path structure | N/A (changing breaks state compat) |
| 14 | Empty markdown parent dirs not cleaned up | Task 11 |
| 15 | No integration test for `runPull` | N/A (deferred — requires git fixture harness) |
| 16 | Silent ADF parse failure | Task 12 |

---

## Task 1: Add bounded concurrency for page detail fetches

**Priority:** Critical
**Analysis Issue:** #1

**Why:** For every changed page, `Pull()` makes 3 sequential API calls: `GetPage`, `GetContentStatus`, `GetLabels`. For a 500-page initial pull this is 1,500+ serial HTTP calls. With bounded concurrency (e.g., 5 workers), this becomes ~300 batches — a 5x speedup while staying under Confluence rate limits.

**Files:**
- Modify: `internal/sync/pull.go:216-261` (the page detail fetch loop)
- Create: `internal/sync/pull_concurrent_test.go` (test bounded fetch)

**Step 1: Write failing test**

Create `internal/sync/pull_concurrent_test.go`:

```go
package sync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPull_BoundedConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32

	pages := make([]confluence.Page, 20)
	pagesByID := make(map[string]confluence.Page, 20)
	for i := range pages {
		id := fmt.Sprintf("page-%d", i)
		pages[i] = confluence.Page{
			ID:      id,
			SpaceID: "space-1",
			Title:   fmt.Sprintf("Page %d", i),
			BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
		}
		pagesByID[id] = pages[i]
	}

	fake := &fakePullRemote{
		space:       confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages:       pages,
		pagesByID:   pagesByID,
		attachments: map[string][]byte{},
		getPageHook: func() {
			cur := currentConcurrent.Add(1)
			defer currentConcurrent.Add(-1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		},
	}

	_, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	peak := maxConcurrent.Load()
	if peak > int32(pullPageFetchConcurrency) {
		t.Fatalf("peak concurrency = %d, want <= %d", peak, pullPageFetchConcurrency)
	}
	if peak < 2 {
		t.Fatalf("peak concurrency = %d, expected concurrent fetches", peak)
	}
}
```

This requires adding a `getPageHook func()` field to `fakePullRemote` and calling it in `GetPage`.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPull_BoundedConcurrency" -v
```

Expected: FAIL (compile error — `pullPageFetchConcurrency` and `getPageHook` don't exist).

**Step 3: Add concurrency constant and implement bounded fetch**

In `internal/sync/pull.go`, add constant:
```go
const (
	pullPageFetchConcurrency = 5
)
```

Replace the serial loop at lines 216-261 with a bounded concurrent fetch using `sync/errgroup`:

```go
import "golang.org/x/sync/errgroup"
```

```go
type fetchedPage struct {
	page        confluence.Page
	status      string
	statusErr   error
	labels      []string
	labelsErr   error
}

g, gctx := errgroup.WithContext(ctx)
g.SetLimit(pullPageFetchConcurrency)

fetchResults := make([]fetchedPage, len(changedPageIDs))
for i, pageID := range changedPageIDs {
	i, pageID := i, pageID
	g.Go(func() error {
		page, err := remote.GetPage(gctx, pageID)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) {
				return nil // Skip deleted pages
			}
			return fmt.Errorf("fetch page %s: %w", pageID, err)
		}

		status, statusErr := remote.GetContentStatus(gctx, pageID)
		labels, labelsErr := remote.GetLabels(gctx, pageID)

		fetchResults[i] = fetchedPage{
			page:      page,
			status:    status,
			statusErr: statusErr,
			labels:    labels,
			labelsErr: labelsErr,
		}

		if opts.Progress != nil {
			opts.Progress.Add(1)
		}
		return nil
	})
}

if err := g.Wait(); err != nil {
	return PullResult{}, err
}

changedPages := make(map[string]confluence.Page, len(changedPageIDs))
for _, result := range fetchResults {
	if result.page.ID == "" {
		continue // Skipped (not found)
	}
	page := result.page

	if result.statusErr != nil {
		diagnostics = append(diagnostics, PullDiagnostic{
			Path:    page.ID,
			Code:    "CONTENT_STATUS_FETCH_FAILED",
			Message: fmt.Sprintf("fetch content status for page %s: %v", page.ID, result.statusErr),
		})
	} else {
		page.ContentStatus = result.status
	}

	if result.labelsErr != nil {
		diagnostics = append(diagnostics, PullDiagnostic{
			Path:    page.ID,
			Code:    "LABELS_FETCH_FAILED",
			Message: fmt.Sprintf("fetch labels for page %s: %v", page.ID, result.labelsErr),
		})
	} else {
		page.Labels = result.labels
	}

	changedPages[page.ID] = page
	if page.Version > maxVersion {
		maxVersion = page.Version
	}
	if page.LastModified.After(maxRemoteModified) {
		maxRemoteModified = page.LastModified
	}
}
```

**Step 4: Add `getPageHook` to fakePullRemote in test**

In `pull_test.go`, add to `fakePullRemote`:
```go
getPageHook func()
```

In `GetPage`:
```go
func (f *fakePullRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	if f.getPageHook != nil {
		f.getPageHook()
	}
	// ... existing code
}
```

**Step 5: Run `go get` for errgroup if needed**

```bash
go get golang.org/x/sync/errgroup
go mod tidy
```

**Step 6: Run tests**

```bash
go test ./internal/sync/ -run "TestPull_BoundedConcurrency" -v
go test ./internal/sync/ -v
```

Expected: All PASS.

**Step 7: Commit**

```bash
git add internal/sync/pull.go internal/sync/pull_concurrent_test.go go.mod go.sum
git commit -m "perf: add bounded concurrency for page detail fetches during pull

Fetches page details (GetPage, GetContentStatus, GetLabels) with up to
5 concurrent workers instead of sequentially. Reduces pull time by ~5x
for large spaces while staying within Confluence rate limits."
```

---

## Task 2: Replace `time.Sleep` with context-aware wait in attachment retry

**Priority:** Critical
**Analysis Issue:** #2

**Why:** The attachment download retry at `pull.go:353-375` uses `time.Sleep` which blocks even after Ctrl+C. If a download is stalled, users cannot cancel during the retry wait.

**Files:**
- Modify: `internal/sync/pull.go:353-375`
- Add test: `internal/sync/pull_test.go`

**Step 1: Write failing test**

Add to `internal/sync/pull_test.go`:

```go
func TestPull_AttachmentRetryCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	_ = os.MkdirAll(spaceDir, 0o755)

	downloadCount := 0
	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Page 1"},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Page 1",
				BodyADF: rawJSON(t, map[string]any{
					"version": 1, "type": "doc",
					"content": []any{
						map[string]any{
							"type": "media",
							"attrs": map[string]any{
								"id": "att-1", "fileName": "file.png",
							},
						},
					},
				}),
			},
		},
		downloadHook: func() error {
			downloadCount++
			return fmt.Errorf("connection reset")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately — the retry should not block
	cancel()

	start := time.Now()
	_, err := Pull(ctx, fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}

	// With time.Sleep, this would take 1+2=3 seconds for retries.
	// With context-aware wait, it should return almost immediately.
	if elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s, expected < 2s (retry sleep not context-aware)", elapsed)
	}
}
```

Add `downloadHook func() error` to `fakePullRemote` and wire it into `DownloadAttachment`.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPull_AttachmentRetryCancellation" -v -timeout 30s
```

Expected: FAIL (times out or takes 3+ seconds).

**Step 3: Extract `contextSleep` helper and refactor retry loop**

Add helper to `internal/sync/pull.go`:

```go
// contextSleep sleeps for the given duration or returns early if ctx is cancelled.
func contextSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

Replace the retry loop at lines 353-375:

```go
err := func() error {
	var lastErr error
	for retry := 0; retry < 3; retry++ {
		if retry > 0 {
			if err := contextSleep(ctx, time.Duration(retry)*time.Second); err != nil {
				return err
			}
		}
		f, err := os.Create(assetPath)
		if err != nil {
			return fmt.Errorf("create attachment file %s: %w", assetPath, err)
		}

		err = remote.DownloadAttachment(ctx, attachmentID, pageID, f)
		_ = f.Close()
		if err == nil {
			return nil
		}
		lastErr = err
		if errors.Is(err, confluence.ErrNotFound) {
			break
		}
	}
	return lastErr
}()
```

**Step 4: Add `downloadHook` to fakePullRemote**

```go
downloadHook func() error
```

In `DownloadAttachment`:
```go
func (f *fakePullRemote) DownloadAttachment(_ context.Context, attachmentID string, pageID string, out io.Writer) error {
	if f.downloadHook != nil {
		return f.downloadHook()
	}
	// ... existing code
}
```

**Step 5: Run tests**

```bash
go test ./internal/sync/ -run "TestPull_AttachmentRetryCancellation" -v -timeout 30s
go test ./internal/sync/ -v
```

Expected: All PASS.

**Step 6: Commit**

```bash
git add internal/sync/pull.go internal/sync/pull_test.go
git commit -m "fix: use context-aware sleep in attachment download retry

Replace time.Sleep with select on ctx.Done() so Ctrl+C cancels
retry waits immediately instead of blocking for up to 3 seconds."
```

---

## Task 3: Use atomic file writes for attachment downloads

**Priority:** Critical
**Analysis Issue:** #3

**Why:** `os.Create(assetPath)` truncates the existing file before download starts. If the download fails, the previously-good asset data is destroyed. Writing to a temp file and renaming on success prevents data loss.

**Files:**
- Modify: `internal/sync/pull.go:353-375` (the retry loop from Task 2)
- Add test: `internal/sync/pull_test.go`

**Step 1: Write failing test**

Add to `internal/sync/pull_test.go`:

```go
func TestPull_AtomicAssetDownload_PreservesExistingOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	_ = os.MkdirAll(spaceDir, 0o755)

	// Pre-existing asset from a previous pull
	assetDir := filepath.Join(spaceDir, "assets", "1")
	_ = os.MkdirAll(assetDir, 0o755)
	existingAsset := filepath.Join(assetDir, "att-1-diagram.png")
	os.WriteFile(existingAsset, []byte("good-data-from-previous-pull"), 0o644)

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Page 1"},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Page 1",
				BodyADF: rawJSON(t, map[string]any{
					"version": 1, "type": "doc",
					"content": []any{
						map[string]any{
							"type": "media",
							"attrs": map[string]any{
								"id": "att-1", "pageId": "1", "fileName": "diagram.png",
							},
						},
					},
				}),
			},
		},
		downloadHook: func() error {
			return fmt.Errorf("network error")
		},
	}

	_, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:          "ENG",
		SpaceDir:          spaceDir,
		SkipMissingAssets: true,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	// The existing file should NOT be destroyed
	data, err := os.ReadFile(existingAsset)
	if err != nil {
		t.Fatalf("existing asset should still exist: %v", err)
	}
	if string(data) != "good-data-from-previous-pull" {
		t.Fatalf("existing asset was corrupted: got %q", string(data))
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPull_AtomicAssetDownload" -v
```

Expected: FAIL — existing file is truncated by `os.Create`.

**Step 3: Refactor download to use temp file + rename**

Replace the retry loop body in `pull.go`:

```go
err := func() error {
	var lastErr error
	for retry := 0; retry < 3; retry++ {
		if retry > 0 {
			if err := contextSleep(ctx, time.Duration(retry)*time.Second); err != nil {
				return err
			}
		}

		// Write to temp file, then rename on success (atomic)
		tmpFile, err := os.CreateTemp(filepath.Dir(assetPath), ".download-*")
		if err != nil {
			return fmt.Errorf("create temp file for %s: %w", assetPath, err)
		}
		tmpPath := tmpFile.Name()

		err = remote.DownloadAttachment(ctx, attachmentID, pageID, tmpFile)
		_ = tmpFile.Close()
		if err == nil {
			if renameErr := os.Rename(tmpPath, assetPath); renameErr != nil {
				_ = os.Remove(tmpPath)
				return fmt.Errorf("rename downloaded file %s: %w", assetPath, renameErr)
			}
			return nil
		}

		_ = os.Remove(tmpPath) // Clean up failed temp file
		lastErr = err
		if errors.Is(err, confluence.ErrNotFound) {
			break
		}
	}
	return lastErr
}()
```

Also remove the `os.Remove(assetPath)` on line 379 since we no longer touch the original file on failure. Instead, remove the temp file (already done inside the loop).

**Step 4: Run tests**

```bash
go test ./internal/sync/ -run "TestPull_AtomicAssetDownload" -v
go test ./internal/sync/ -v
```

Expected: All PASS.

**Step 5: Commit**

```bash
git add internal/sync/pull.go internal/sync/pull_test.go
git commit -m "fix: use atomic file writes for attachment downloads

Download to a temp file and rename on success. Prevents corruption
of existing assets when a re-download fails partway through."
```

---

## Task 4: Eliminate duplicate page listing between estimate and pull

**Priority:** Warning
**Analysis Issue:** #4

**Why:** `estimatePullImpactWithSpace` (cmd/pull.go:131) fetches ALL pages, then `Pull()` (pull.go:150) fetches ALL pages again. For a 5000-page space, this doubles the initial scan time.

**Files:**
- Modify: `internal/sync/pull.go:88-158` (accept pre-fetched pages)
- Modify: `cmd/pull.go:131-208` (pass pages from estimate to Pull)
- Modify: `internal/sync/pull.go` (add `PrefetchedPages` to `PullOptions`)

**Step 1: Add `PrefetchedPages` field to `PullOptions`**

In `internal/sync/pull.go`, add to `PullOptions`:

```go
type PullOptions struct {
	// ... existing fields ...
	PrefetchedPages []confluence.Page // Optional: pre-fetched page list to avoid duplicate listing
}
```

**Step 2: Use `PrefetchedPages` if provided**

In `Pull()`, replace the `listAllPages` call (line 150-158):

```go
var pages []confluence.Page
if len(opts.PrefetchedPages) > 0 {
	pages = opts.PrefetchedPages
} else {
	if opts.Progress != nil {
		opts.Progress.SetDescription("Scanning space for pages")
	}
	var err error
	pages, err = listAllPages(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: opts.SpaceKey,
		Status:   "current",
		Limit:    pullPageBatchSize,
	}, opts.Progress)
	if err != nil {
		return PullResult{}, fmt.Errorf("list pages: %w", err)
	}
}
```

**Step 3: Return pages from `estimatePullImpactWithSpace`**

In `cmd/pull.go`, change the return type to include pages:

```go
type pullImpact struct {
	changedMarkdown int
	deletedMarkdown int
	pages           []confluence.Page
}
```

Return the pages from `estimatePullImpactWithSpace`:

```go
return pullImpact{
	changedMarkdown: len(changedIDs),
	deletedMarkdown: len(deletedIDs),
	pages:           pages,
}, nil
```

**Step 4: Pass pages to `Pull()`**

In `runPull`, pass them through:

```go
result, err := syncflow.Pull(ctx, remote, syncflow.PullOptions{
	// ... existing fields ...
	PrefetchedPages: impact.pages,
})
```

**Step 5: Run tests**

```bash
go test ./internal/sync/ -v
go test ./cmd/ -v -count=1
go test ./...
```

Expected: All pass.

**Step 6: Commit**

```bash
git add internal/sync/pull.go cmd/pull.go
git commit -m "perf: pass pre-fetched page list from estimate to Pull

Eliminates duplicate ListPages API calls. The impact estimate already
fetches all pages; now Pull reuses them via PrefetchedPages option."
```

---

## Task 5: Scope `handlePullConflict` checkout to space directory

**Priority:** Warning
**Analysis Issue:** #5

**Why:** Options 2 and 3 in conflict resolution use `git checkout ... -- .` which operates on the ENTIRE repository, not just the space being pulled. This can silently discard unrelated work.

**Files:**
- Modify: `cmd/pull.go:552-595` (pass `scopePath` and `out` to handler)

**Step 1: Refactor `handlePullConflict` signature**

Change:
```go
func handlePullConflict(repoRoot, stashRef string) error {
```
To:
```go
func handlePullConflict(repoRoot, stashRef, scopePath string, in io.Reader, out io.Writer) error {
```

**Step 2: Scope the checkout commands**

Replace `"."` with `scopePath`:

```go
case "2":
	fmt.Fprintln(out, "Discarding local changes...")
	_, err := runGit(repoRoot, "checkout", "HEAD", "--", scopePath)
	// ...
case "3":
	fmt.Fprintln(out, "Keeping local version...")
	_, err := runGit(repoRoot, "checkout", stashRef, "--", scopePath)
```

**Step 3: Replace all `fmt.Println/Printf/Print/Scanln` with `out`/`in`**

```go
fmt.Fprintln(out, "\nCONFLICT DETECTED")
fmt.Fprintln(out, "Local changes could not be automatically merged with remote updates.")
fmt.Fprintln(out, "How would you like to proceed?")
fmt.Fprintln(out, " [1] Keep both (add conflict markers to files) - RECOMMENDED")
fmt.Fprintln(out, " [2] Use Remote version (discard my local changes for these files)")
fmt.Fprintln(out, " [3] Use Local version (overwrite remote updates with my local changes)")
fmt.Fprint(out, "\nChoice [1/2/3]: ")

scanner := bufio.NewScanner(in)
var choice string
if scanner.Scan() {
	choice = scanner.Text()
}
```

**Step 4: Update the call site in `applyAndDropStash`**

Pass `scopePath`, `in`, and `out` through. The call site is in `applyAndDropStash` (line 541). Either pass these as parameters, or refactor `applyAndDropStash` to accept them:

```go
func applyAndDropStash(repoRoot, stashRef, scopePath string, in io.Reader, out io.Writer) error {
```

Update the caller in the `defer` at line 179:
```go
restoreErr := applyAndDropStash(repoRoot, stashRef, scopePath, cmd.InOrStdin(), out)
```

**Step 5: Run tests**

```bash
go test ./cmd/ -v -count=1
go test ./...
```

Expected: All pass.

**Step 6: Commit**

```bash
git add cmd/pull.go
git commit -m "fix: scope conflict checkout to space directory, not entire repo

handlePullConflict now operates on the scopePath instead of '.',
preventing accidental reset of unrelated files. Also routes all
output through the cmd.OutOrStdout() writer for testability."
```

---

## Task 6: Add max-iterations guard to pagination loops

**Priority:** Warning
**Analysis Issue:** #6

**Why:** If Confluence returns non-empty but never-terminating cursors (API bug or data corruption), `listAllPages` loops forever. A safety limit prevents resource exhaustion.

**Files:**
- Modify: `internal/sync/pull.go:621-639` (`listAllPages`)
- Modify: `internal/sync/pull.go:708-733` (`listAllChanges`)
- Modify: `cmd/pull.go:718-742` (`listAllPullPagesForEstimate`)
- Modify: `cmd/pull.go:744-775` (`listAllPullChangesForEstimate`)

**Step 1: Add pagination safety constant**

In `internal/sync/pull.go`:
```go
const (
	maxPaginationIterations = 500 // 500 * 100 = 50,000 pages max
)
```

**Step 2: Add iteration guard to `listAllPages`**

```go
func listAllPages(ctx context.Context, remote PullRemote, opts confluence.PageListOptions, progress Progress) ([]confluence.Page, error) {
	result := []confluence.Page{}
	cursor := opts.Cursor
	for iteration := 0; iteration < maxPaginationIterations; iteration++ {
		opts.Cursor = cursor
		pageResult, err := remote.ListPages(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, pageResult.Pages...)
		if progress != nil {
			progress.Add(len(pageResult.Pages))
		}
		if strings.TrimSpace(pageResult.NextCursor) == "" || pageResult.NextCursor == cursor {
			break
		}
		cursor = pageResult.NextCursor
	}
	return result, nil
}
```

**Step 3: Add same guard to `listAllChanges`**

Apply the same `for iteration := 0; iteration < maxPaginationIterations; iteration++` pattern.

**Step 4: Apply to `cmd/pull.go` estimate functions**

Apply the same guard to `listAllPullPagesForEstimate` and `listAllPullChangesForEstimate`.

**Step 5: Run tests**

```bash
go test ./internal/sync/ -v
go test ./cmd/ -v -count=1
```

Expected: All pass (no behavior change for normal flows).

**Step 6: Commit**

```bash
git add internal/sync/pull.go cmd/pull.go
git commit -m "fix: add max-iterations guard to pagination loops

Prevents infinite loops if Confluence returns non-terminating
cursors. Safety limit of 500 iterations (50,000 pages)."
```

---

## Task 7: Preserve existing status/labels when fetch fails

**Priority:** Warning
**Analysis Issue:** #8

**Why:** When `GetContentStatus` or `GetLabels` fails, the page is written with empty status/labels. On a subsequent push, this could accidentally clear the status/labels in Confluence.

**Files:**
- Modify: `internal/sync/pull.go` (in the page detail fetch section, after Task 1's refactoring)
- Add test: `internal/sync/pull_test.go`

**Step 1: Write failing test**

```go
func TestPull_PreservesExistingStatusOnFetchFailure(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	_ = os.MkdirAll(spaceDir, 0o755)

	// Write existing file with status and labels
	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Page 1",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
			Status:  "Ready to review",
			Labels:  []string{"important", "arch"},
		},
		Body: "old body\n",
	}
	fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Page-1.md"), doc)

	state := fs.SpaceState{
		PagePathIndex: map[string]string{
			"Page-1.md": "1",
		},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Page 1", Version: 2},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Page 1",
				Version: 2,
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
		contentStatusErr: fmt.Errorf("API error"),
		labelsErr:        fmt.Errorf("API error"),
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State:    state,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	updatedDoc, _ := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "Page-1.md"))
	if updatedDoc.Frontmatter.Status != "Ready to review" {
		t.Fatalf("status should be preserved, got %q", updatedDoc.Frontmatter.Status)
	}
	if len(updatedDoc.Frontmatter.Labels) != 2 {
		t.Fatalf("labels should be preserved, got %v", updatedDoc.Frontmatter.Labels)
	}

	// Should have diagnostics about the failures
	foundStatusDiag := false
	foundLabelsDiag := false
	for _, d := range result.Diagnostics {
		if d.Code == "CONTENT_STATUS_FETCH_FAILED" {
			foundStatusDiag = true
		}
		if d.Code == "LABELS_FETCH_FAILED" {
			foundLabelsDiag = true
		}
	}
	if !foundStatusDiag || !foundLabelsDiag {
		t.Fatalf("expected diagnostics for fetch failures")
	}
}
```

Add `contentStatusErr` and `labelsErr` fields to `fakePullRemote`, and return them from `GetContentStatus`/`GetLabels` when set.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPull_PreservesExistingStatusOnFetchFailure" -v
```

Expected: FAIL — status and labels are empty.

**Step 3: Read existing frontmatter before overwriting**

In the Pull function, when status/labels fetch fails, read the existing file's frontmatter to preserve values:

```go
if result.statusErr != nil {
	// Try to preserve existing status from local file
	if existingPath, ok := pagePathByIDAbs[page.ID]; ok {
		if existingDoc, readErr := fs.ReadMarkdownDocument(existingPath); readErr == nil {
			page.ContentStatus = existingDoc.Frontmatter.Status
		}
	}
	diagnostics = append(diagnostics, PullDiagnostic{...})
} else {
	page.ContentStatus = result.status
}

// Same pattern for labels
```

**Step 4: Run tests**

```bash
go test ./internal/sync/ -run "TestPull_PreservesExistingStatusOnFetchFailure" -v
go test ./internal/sync/ -v
```

Expected: All PASS.

**Step 5: Commit**

```bash
git add internal/sync/pull.go internal/sync/pull_test.go
git commit -m "fix: preserve existing status and labels when API fetch fails

When GetContentStatus or GetLabels returns an error, read the
existing local file's frontmatter to avoid overwriting good data
with empty values."
```

---

## Task 8: Store space key in `.confluence-state.json`

**Priority:** Warning
**Analysis Issue:** #9

**Why:** When no target is specified, `resolveInitialPullContext` iterates markdown files to find the space key. If all files are deleted or corrupted, it falls back to the directory name — which may be wrong (e.g., `"Engineering (ENG)"` → key is `"Engineering (ENG)"` instead of `"ENG"`).

**Files:**
- Modify: `internal/fs/state.go` (add `SpaceKey` field)
- Modify: `internal/sync/pull.go:530-537` (save space key in state)
- Modify: `cmd/pull.go:313-332` (read space key from state)

**Step 1: Add `SpaceKey` to `SpaceState`**

In `internal/fs/state.go`:
```go
type SpaceState struct {
	SpaceKey             string            `json:"space_key,omitempty"`
	LastPullHighWatermark string            `json:"last_pull_high_watermark,omitempty"`
	PagePathIndex         map[string]string `json:"page_path_index,omitempty"`
	AttachmentIndex       map[string]string `json:"attachment_index,omitempty"`
}
```

**Step 2: Save space key in Pull result**

In `internal/sync/pull.go`, before the return at line 539:
```go
state.SpaceKey = opts.SpaceKey
```

**Step 3: Read space key from state in `resolveInitialPullContext`**

In `cmd/pull.go`, update the `target.Value == ""` branch (line 315):

```go
if _, err := os.Stat(filepath.Join(cwd, fs.StateFileName)); err == nil {
	state, err := fs.LoadState(cwd)
	if err == nil {
		if state.SpaceKey != "" {
			return initialPullContext{
				spaceKey: state.SpaceKey,
				spaceDir: cwd,
				fixedDir: true,
			}, nil
		}
		// Fallback: iterate files (existing behavior)
		for relPath := range state.PagePathIndex {
			// ... existing code
		}
	}
}
```

Apply the same pattern to the directory target branch (line 352-366).

**Step 4: Run tests**

```bash
go test ./internal/sync/ -v
go test ./cmd/ -v -count=1
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add internal/fs/state.go internal/sync/pull.go cmd/pull.go
git commit -m "feat: store space key in .confluence-state.json

Direct lookup instead of iterating markdown files to find the
space key. Gracefully falls back to file iteration for existing
state files that don't yet have the field."
```

---

## Task 9: Route `handlePullConflict` output through writer

**Priority:** Warning
**Analysis Issue:** #10

**Note:** This is already addressed by Task 5 which refactors `handlePullConflict` to accept `io.Writer`. If Task 5 is implemented, this task is a no-op. Keeping it here for traceability.

---

## Task 10: Add sub-step progress for page detail fetches

**Priority:** Suggestion
**Analysis Issue:** #11

**Why:** Each page requires 3 API calls but progress only increments by 1. Users see sluggish progress. Showing sub-steps (`[page 5/100] Fetching Page Title...`) improves UX.

**Files:**
- Modify: `internal/sync/pull.go` (in the concurrent fetch from Task 1)

**Step 1: Add `SetCurrentItem` call before each page fetch**

In the concurrent fetch goroutine (from Task 1):

```go
g.Go(func() error {
	if opts.Progress != nil {
		opts.Progress.SetCurrentItem(pageID)
	}
	// ... existing fetch logic
})
```

Since fetches are concurrent, the `SetCurrentItem` will show the most recently started page — this is acceptable for a progress indicator.

**Step 2: Run tests**

```bash
go test ./internal/sync/ -v
```

Expected: All pass.

**Step 3: Commit**

```bash
git add internal/sync/pull.go
git commit -m "feat: show current page in progress during detail fetches

Progress indicator now shows which page is being fetched, giving
users better feedback during the slowest phase of pull."
```

---

## Task 11: Clean up empty markdown parent directories after deletion

**Priority:** Suggestion
**Analysis Issue:** #14

**Why:** When markdown files are deleted during pull and their parent directories become empty, those directories are left behind. Only asset directories are cleaned up currently.

**Files:**
- Modify: `internal/sync/pull.go:509-515` (add cleanup after markdown deletion)
- Add test: `internal/sync/pull_test.go`

**Step 1: Write failing test**

```go
func TestPull_CleansEmptyMarkdownParentDirs(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")

	// Create nested structure: ENG/Parent/child.md
	childDir := filepath.Join(spaceDir, "Parent")
	_ = os.MkdirAll(childDir, 0o755)
	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Child", ID: "2", Space: "ENG", Version: 1},
		Body:        "child\n",
	}
	_ = fs.WriteMarkdownDocument(filepath.Join(childDir, "child.md"), doc)

	state := fs.SpaceState{
		PagePathIndex: map[string]string{
			"Parent/child.md": "2",
		},
	}

	// Remote has no pages — child is deleted
	fake := &fakePullRemote{
		space:       confluence.Space{ID: "space-1", Key: "ENG"},
		pages:       []confluence.Page{},
		pagesByID:   map[string]confluence.Page{},
		attachments: map[string][]byte{},
	}

	_, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State:    state,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	// The "Parent" directory should be cleaned up
	if _, err := os.Stat(childDir); !os.IsNotExist(err) {
		t.Fatalf("empty Parent directory should be removed, stat error=%v", err)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/sync/ -run "TestPull_CleansEmptyMarkdownParentDirs" -v
```

Expected: FAIL — empty directory still exists.

**Step 3: Add cleanup after markdown deletion**

In `pull.go`, after the markdown deletion loop (line 515), add:

```go
for _, relPath := range deletedMarkdown {
	absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
	_ = removeEmptyParentDirs(filepath.Dir(absPath), spaceDir)
}
```

**Step 4: Run tests**

```bash
go test ./internal/sync/ -run "TestPull_CleansEmptyMarkdownParentDirs" -v
go test ./internal/sync/ -v
```

Expected: All PASS.

**Step 5: Commit**

```bash
git add internal/sync/pull.go internal/sync/pull_test.go
git commit -m "fix: clean up empty parent directories after markdown deletion

When pull deletes markdown files, empty parent directories are
now removed up to the space root, matching the existing behavior
for asset directories."
```

---

## Task 12: Add diagnostic for malformed ADF content

**Priority:** Suggestion
**Analysis Issue:** #16

**Why:** `collectAttachmentRefs` silently returns an empty map when ADF is malformed. Pages are written without images and users don't know why. Adding a diagnostic makes the failure visible.

**Files:**
- Modify: `internal/sync/pull.go:1014-1022` (`collectAttachmentRefs`)

**Step 1: Change `collectAttachmentRefs` to return diagnostics**

Change signature:
```go
func collectAttachmentRefs(adfJSON []byte, defaultPageID string) (map[string]attachmentRef, *PullDiagnostic)
```

```go
func collectAttachmentRefs(adfJSON []byte, defaultPageID string) (map[string]attachmentRef, *PullDiagnostic) {
	if len(adfJSON) == 0 {
		return map[string]attachmentRef{}, nil
	}

	var raw any
	if err := json.Unmarshal(adfJSON, &raw); err != nil {
		return map[string]attachmentRef{}, &PullDiagnostic{
			Path:    defaultPageID,
			Code:    "ADF_PARSE_ERROR",
			Message: fmt.Sprintf("page %s has malformed ADF body, attachments may be missing: %v", defaultPageID, err),
		}
	}

	// ... rest unchanged
	return out, nil
}
```

**Step 2: Update the call site**

At line 294:
```go
refs, adfDiag := collectAttachmentRefs(page.BodyADF, page.ID)
if adfDiag != nil {
	diagnostics = append(diagnostics, *adfDiag)
}
```

**Step 3: Run tests**

```bash
go test ./internal/sync/ -v
```

Expected: All pass.

**Step 4: Commit**

```bash
git add internal/sync/pull.go
git commit -m "feat: emit diagnostic when ADF content is malformed

Users now see a warning when a page has unparseable ADF body,
explaining why attachments may be missing from the pulled output."
```

---

## Execution Order and Dependencies

```
Task 1 (bounded concurrency)      ── Independent, modifies fetch loop
Task 2 (context-aware retry)      ── Independent, modifies download loop
Task 3 (atomic file writes)       ── After Task 2 (modifies same download loop)
Task 4 (eliminate duplicate list)  ── Independent
Task 5 (scope conflict checkout)  ── Independent (also covers Task 9)
Task 6 (pagination guard)         ── Independent
Task 7 (preserve status/labels)   ── After Task 1 (uses refactored fetch results)
Task 8 (space key in state)       ── Independent
Task 10 (progress sub-steps)      ── After Task 1 (adds to concurrent fetch)
Task 11 (clean empty dirs)        ── Independent
Task 12 (ADF parse diagnostic)    ── Independent
```

| # | Task | Priority | Depends On |
|---|------|----------|------------|
| 1 | Bounded concurrency for page fetches | Critical | — |
| 2 | Context-aware retry sleep | Critical | — |
| 3 | Atomic file writes for downloads | Critical | 2 |
| 4 | Eliminate duplicate page listing | Warning | — |
| 5 | Scope conflict checkout + writer | Warning | — |
| 6 | Pagination guard | Warning | — |
| 7 | Preserve status/labels on fetch failure | Warning | 1 |
| 8 | Space key in state file | Warning | — |
| 9 | (Covered by Task 5) | Warning | 5 |
| 10 | Progress sub-steps | Suggestion | 1 |
| 11 | Clean empty markdown dirs | Suggestion | — |
| 12 | ADF parse diagnostic | Suggestion | — |

Tasks 1, 2, 4, 5, 6, 8, 11, 12 can start in parallel.
Task 3 requires Task 2 (same code block).
Tasks 7 and 10 require Task 1 (builds on refactored fetch loop).
