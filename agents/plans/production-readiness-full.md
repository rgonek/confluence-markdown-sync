# Production Readiness — Full Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Address all 17 issues from the production-readiness review to make `conf` production-grade.

**Architecture:** 14 actionable tasks (3 script-related suggestions are resolved by Task 2). Ordered by priority: Critical first, then Warning, then Suggestion. Each task is independently committable and testable.

**Tech Stack:** Go 1.24, Cobra CLI, `net/http`, `os/signal`, `context`, `time`, `log/slog`, GitHub Actions, golangci-lint

**Issue Coverage Map:**

| Review # | Issue | Task |
|----------|-------|------|
| 1 | Failing unit tests | Task 1 |
| 2 | Scripts package won't compile | Task 2 |
| 3 | No retry/backoff on API calls | Task 3 |
| 4 | go.mod declares Go 1.25.5 | Task 4 |
| 5 | Enormous function sizes | Task 5 |
| 6 | Stash management is fragile | Task 6 |
| 7 | No context cancellation / signal handling | Task 7 |
| 8 | API token in memory / logs | Task 8 |
| 9 | Hardcoded 300-second HTTP timeout | Task 9 |
| 10 | No rate limiting for bulk operations | Task 10 |
| 11 | Add golangci-lint | Task 11 |
| 12 | Add CI/CD pipeline | Task 12 |
| 13 | Add structured logging | Task 13 |
| 14 | wipe_spaces.go hardcoded space keys | Task 2 (removed) |
| 15 | generate_test_data.go hardcoded path | Task 2 (removed) |
| 16 | Download client should share TLS config | Task 14 |
| 17 | go generate / build tags for scripts | Task 2 (removed) |

---

## Task 1: Fix failing unit tests in `internal/sync/pull_test.go`

**Priority:** Critical
**Review Issue:** #1

**Files:**
- Fix: `internal/sync/pull.go:832-850` (function `plannedPageRelPath`)
- Verify: `internal/sync/pull_test.go:207-227` (TestPlanPagePaths_MaintainsConfluenceHierarchy)
- Verify: `internal/sync/pull_test.go:56-205` (TestPull_IncrementalRewriteDeleteAndWatermark)

**Root cause:**

In `plannedPageRelPath` (line 845), when `hasChildren == true`, the function appends the page's own title as an ancestor segment:
```go
if hasChildren {
    ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
}
```

For a root page `{ID: "1", Title: "Root"}` with children, this produces `Root/Root.md` because:
1. `ancestorPathSegments` returns `nil` (no parent → root page)
2. `hasChildren` is `true` (page "2" has ParentPageID "1")
3. `ancestorSegments = ["Root"]`, `filename = "Root.md"` → `Root/Root.md`

The test expects `Root.md` (flat). The child pages already inherit parent paths via `ancestorPathSegments` — the `hasChildren` self-nesting is redundant and wrong.

Test expectations confirm this:
- Root (no parent, has children) → `Root.md` (flat)
- Child (parent "1", has children) → `Child.md` (flat)
- Grand Child (parent "2", no children) → `Child/Grand-Child.md` (nested under parent by `ancestorPathSegments`)

The `hasChildren` block needs to be removed entirely, along with the `pageHasChildren` map.

**Step 1: Run failing tests to confirm**

```bash
go test ./internal/sync/ -run "TestPlanPagePaths_MaintainsConfluenceHierarchy|TestPull_IncrementalRewriteDeleteAndWatermark" -v
```

Expected: Both FAIL.

**Step 2: Remove `hasChildren` self-nesting from `plannedPageRelPath`**

In `internal/sync/pull.go`, delete lines 845-847:
```go
if hasChildren {
    ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
}
```

Update the function signature to remove `hasChildren` parameter:
```go
func plannedPageRelPath(page confluence.Page, pageByID map[string]confluence.Page, folderByID map[string]confluence.Folder) string {
```

Update the call site in `PlanPagePaths` (line 804):
```go
baseRelPath := plannedPageRelPath(page, pageByID, folderByID)
```

Remove the `pageHasChildren` map (lines 753-777) from `PlanPagePaths` since it's no longer referenced.

**Step 3: Run the fixed tests**

```bash
go test ./internal/sync/ -run "TestPlanPagePaths_MaintainsConfluenceHierarchy|TestPull_IncrementalRewriteDeleteAndWatermark" -v
```

Expected: Both PASS.

**Step 4: Run full sync package tests**

```bash
go test ./internal/sync/ -v
```

Expected: All pass.

**Step 5: Run cmd tests for regressions**

```bash
go test ./cmd/ -v -count=1
```

Expected: All pass.

**Step 6: Commit**

```bash
git add internal/sync/pull.go
git commit -m "fix: remove self-nesting for pages with children in PlanPagePaths

Root-level and intermediate pages were incorrectly placed inside their
own title subdirectory (Root/Root.md instead of Root.md). Children
already inherit parent paths via ancestorPathSegments."
```

---

## Task 2: Remove `scripts/` directory

**Priority:** Critical
**Review Issues:** #2, #14, #15, #17

**Why:** All three files declare `func main()` in `package main`, causing `go test ./...` and `go vet ./...` to fail. They contain hardcoded developer-specific paths (`D:/confluence-test-final`, `D:/confluence-test-data`) and hardcoded space keys (`TD`, `SD`). No production code depends on them.

**Files:**
- Delete: `scripts/generate_test_data.go`
- Delete: `scripts/clean_test_data.go`
- Delete: `scripts/wipe_spaces.go`

**Step 1: Verify no production code imports scripts/**

```bash
grep -r "confluence-markdown-sync/scripts" --include="*.go" .
```

Expected: No matches.

**Step 2: Delete the scripts directory**

```bash
rm -rf scripts/
```

**Step 3: Verify build error is resolved**

```bash
go vet ./...
```

Expected: Clean — no `main redeclared` errors.

**Step 4: Run full test suite**

```bash
go test ./...
```

Expected: No `scripts [build failed]` line.

**Step 5: Commit**

```bash
git add -u scripts/
git commit -m "chore: remove scripts/ directory

Developer-only utilities with hardcoded paths and space keys. All
three files declared func main() in the same package, breaking
go vet and go test."
```

---

## Task 3: Add HTTP retry with exponential backoff

**Priority:** Critical
**Review Issue:** #3

**Why:** Confluence Cloud returns `429` and intermittent `5xx` under load. Without retry, space-wide operations fail partway through.

**Files:**
- Create: `internal/confluence/retry.go`
- Create: `internal/confluence/retry_test.go`
- Modify: `internal/confluence/client.go:676-707` (the `do` method)

**Step 1: Write failing tests**

Create `internal/confluence/retry_test.go` with four tests:
- `TestClient_RetriesOn429`: Server returns 429 twice then 200. Assert 3 total attempts.
- `TestClient_RetriesOn500`: Server returns 500 once then 200. Assert 2 total attempts.
- `TestClient_DoesNotRetryOn400`: Server returns 400. Assert 1 attempt only.
- `TestClient_GivesUpAfterMaxRetries`: Server always returns 429. Assert 4 total attempts (1 original + 3 retries), final error.

Use `httptest.NewServer` with `atomic.Int32` counter. Use `GetSpace` as the operation under test since it's a simple GET with JSON response.

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/confluence/ -run "TestClient_Retries|TestClient_DoesNotRetry|TestClient_GivesUp" -v
```

Expected: FAIL.

**Step 3: Create `internal/confluence/retry.go`**

```go
package confluence

import (
    "math"
    "math/rand"
    "net/http"
    "strconv"
    "time"
)

const (
    defaultMaxRetries = 3
    defaultBaseDelay  = 500 * time.Millisecond
    defaultMaxDelay   = 30 * time.Second
)

func isRetryableStatus(code int) bool {
    return code == http.StatusTooManyRequests ||
        code == http.StatusInternalServerError ||
        code == http.StatusBadGateway ||
        code == http.StatusServiceUnavailable ||
        code == http.StatusGatewayTimeout
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
    if resp != nil {
        if ra := resp.Header.Get("Retry-After"); ra != "" {
            if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
                return time.Duration(secs) * time.Second
            }
        }
    }
    backoff := float64(defaultBaseDelay) * math.Pow(2, float64(attempt))
    if backoff > float64(defaultMaxDelay) {
        backoff = float64(defaultMaxDelay)
    }
    jitter := backoff * 0.25 * (rand.Float64()*2 - 1)
    return time.Duration(backoff + jitter)
}
```

**Step 4: Modify the `do` method in `client.go`**

Replace `func (c *Client) do(req *http.Request, out any) error` with retry loop:

1. Loop `attempt := 0` to `defaultMaxRetries`
2. On success (2xx) — decode and return
3. On non-retryable status — return `APIError` immediately
4. On retryable status — read body, build `APIError`, sleep with jitter, retry
5. Respect `req.Context()` cancellation between retries
6. For retries with body, use `req.GetBody()` to reset (works because `newRequest` uses `bytes.NewReader`)
7. Keep verbose logging, adding `retry #N` prefix for retries

**Step 5: Run retry tests**

```bash
go test ./internal/confluence/ -run "TestClient_Retries|TestClient_DoesNotRetry|TestClient_GivesUp" -v
```

Expected: All PASS.

**Step 6: Run full test suite**

```bash
go test ./...
```

Expected: All pass (existing client tests should be unaffected since mock servers return 200).

**Step 7: Commit**

```bash
git add internal/confluence/retry.go internal/confluence/retry_test.go internal/confluence/client.go
git commit -m "feat: add HTTP retry with exponential backoff for 429/5xx

Retries up to 3 times with exponential backoff and jitter. Respects
Retry-After header. Does not retry 4xx client errors."
```

---

## Task 4: Fix `go.mod` Go version

**Priority:** Warning
**Review Issue:** #4

**Files:**
- Modify: `go.mod` line 3

**Step 1: Check current Go toolchain version**

```bash
go version
```

**Step 2: Update `go.mod`**

Change `go 1.25.5` to the actual installed version (e.g., `go 1.24`).

**Step 3: Run `go mod tidy`**

```bash
go mod tidy
```

**Step 4: Run tests**

```bash
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "fix: set go.mod to valid Go release version"
```

---

## Task 5: Decompose large functions

**Priority:** Warning
**Review Issue:** #5

**Why:** `runPush` is ~506 lines (line 83-589 in `cmd/push.go`), `runPull` is ~199 lines (line 71-270 in `cmd/pull.go`), and `Pull` is ~460 lines (line 88-548 in `internal/sync/pull.go`). These are hard to test in isolation and review.

**Files:**
- Modify: `cmd/push.go`
- Modify: `cmd/pull.go` (optional, already better structured)
- Modify: `internal/sync/pull.go` (optional, can be deferred)

**Approach:** Focus on `cmd/push.go` since it's the worst offender. Extract logical phases into named helpers.

**Step 1: Identify extraction targets in `runPush`**

The function has clear phases marked by comments:
1. Lines 83-184: **Setup + Preflight** → extract `runPushPreflight`
2. Lines 186-275: **Dry-run mode** → extract `runPushDryRun`
3. Lines 277-428: **Stash + Worktree + Validate + Diff** → extract `preparePushWorktree`
4. Lines 438-497: **Push to Confluence** → extract `executePush`
5. Lines 507-589: **Commit + Merge + Tag + Restore** → extract `finalizePush`

**Step 2: Extract `runPushPreflight` helper**

Move lines 142-184 into a standalone function:
```go
func runPushPreflight(out io.Writer, gitClient *git.Client, spaceKey, spaceDir, spaceScopePath, changeScopePath string, target config.Target) error {
```

**Step 3: Extract `runPushDryRun` helper**

Move lines 189-274 into a standalone function:
```go
func runPushDryRun(ctx context.Context, out io.Writer, gitClient *git.Client, target config.Target, spaceKey, spaceDir, spaceScopePath, changeScopePath string, onConflict string) error {
```

**Step 4: Extract stash-restore helper**

The most impactful extraction: the repeated `if stashRef != "" { _ = gitClient.StashPop(stashRef) }` pattern appears 15+ times. Extract:
```go
func popStashOnError(gitClient *git.Client, stashRef string) {
    if stashRef != "" {
        _ = gitClient.StashPop(stashRef)
    }
}
```

Then replace all instances. This also reduces the line count and makes the stash management pattern visible.

**Step 5: Run full test suite**

```bash
go test ./cmd/ -v -count=1
go test ./...
```

Expected: All pass (pure refactoring, no behavior change).

**Step 6: Commit**

```bash
git add cmd/push.go
git commit -m "refactor: decompose runPush into smaller named functions

Extract preflight, dry-run, and stash helpers to reduce the main
function from ~500 to ~150 lines. No behavior change."
```

---

## Task 6: Improve stash management safety in `cmd/push.go`

**Priority:** Warning
**Review Issue:** #6

**Why:** If the process is killed between stash and pop in push.go, user changes are orphaned. The code has 15+ manual `if stashRef != "" { StashPop }` calls. Pull.go already uses `defer` for stash — push.go should follow the same pattern.

**Files:**
- Modify: `cmd/push.go` (refactor stash handling into defer)

**Step 1: Analyze the current stash lifecycle in push.go**

Currently in `runPush` (lines 277-589):
- Stash is created at line 278
- On every error, stash is manually popped (15+ locations)
- On success, stash is popped at line 582
- The comment at line 282 says "intentionally DO NOT defer here" because restoration depends on success/failure

But `runPull` at line 159 shows the correct pattern: a `defer` closure that reads `runErr` to decide cleanup behavior. Push should adopt the same approach.

**Step 2: Refactor to use `defer` for stash restoration**

After stash creation (line 278), add:
```go
if stashRef != "" {
    defer func() {
        if popErr := gitClient.StashPop(stashRef); popErr != nil {
            fmt.Fprintf(out, "warning: stash restore had conflicts: %v\n", popErr)
        }
    }()
}
```

Then remove all 15+ manual `if stashRef != "" { StashPop }` calls throughout the function.

**Important:** The conflict-handling path (line 479-497) pops the stash *before* calling `runPull`. This case needs special handling — set `stashRef = ""` before calling runPull to prevent the defer from double-popping:
```go
if stashRef != "" {
    _ = gitClient.StashPop(stashRef)
    stashRef = "" // Prevent defer from double-popping
}
```

**Step 3: Run push tests**

```bash
go test ./cmd/ -run "TestPush" -v -count=1
```

Expected: All pass.

**Step 4: Run full test suite**

```bash
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add cmd/push.go
git commit -m "refactor: use defer for stash management in push

Replace 15+ manual stash-pop calls with a single defer closure.
Prevents orphaned stashes if the process is killed mid-operation."
```

---

## Task 7: Add graceful signal handling

**Priority:** Warning
**Review Issue:** #7

**Why:** Ctrl+C during push can leave dangling worktrees, incomplete stash, or half-pushed pages.

**Files:**
- Modify: `cmd/conf/main.go`
- Modify: `cmd/root.go`
- Modify: `cmd/push.go` (replace `context.Background()` with `cmd.Context()`)
- Modify: `cmd/pull.go` (replace `context.Background()` with `cmd.Context()`)

**Step 1: Update `cmd/conf/main.go`**

```go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/rgonek/confluence-markdown-sync/cmd"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    if err := cmd.ExecuteContext(ctx); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

**Step 2: Add `ExecuteContext` to `cmd/root.go`**

```go
func ExecuteContext(ctx context.Context) error {
    return rootCmd.ExecuteContext(ctx)
}
```

Keep the existing `Execute()` for backward compatibility (tests may use it).

**Step 3: Replace `context.Background()` in push.go and pull.go**

In `cmd/push.go` line 84, change:
```go
ctx := context.Background()
```
to:
```go
ctx := cmd.Context()
```

In `cmd/pull.go` line 72, change:
```go
ctx := context.Background()
```
to:
```go
ctx := cmd.Context()
```

The Confluence client methods already accept `ctx`, so cancellation propagates to HTTP calls automatically.

**Step 4: Run tests**

```bash
go test ./cmd/ -v -count=1
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add cmd/conf/main.go cmd/root.go cmd/push.go cmd/pull.go
git commit -m "feat: add graceful signal handling for SIGINT/SIGTERM

Ctrl+C now cancels the context, allowing in-flight HTTP calls and
git operations to terminate with cleanup via existing defer blocks."
```

---

## Task 8: Audit verbose logging for API token leakage

**Priority:** Warning
**Review Issue:** #8

**Why:** Verbose mode (`--verbose`) logs HTTP request URLs. While current code only logs `Method` + `URL` (safe), we should add a test to prevent regression.

**Files:**
- Verify: `internal/confluence/client.go:677-678` and `339-340` (verbose output)
- Create: test in `internal/confluence/client_test.go` to assert no auth header leakage

**Step 1: Verify current verbose output is safe**

Current verbose logging (already verified):
```go
// Line 677-678
if c.verbose {
    fmt.Printf("%s %s\n", req.Method, req.URL.String())
}
```

This only logs Method and URL — no auth headers, no request body. The `SetBasicAuth` call (line 667) sets the `Authorization` header on the request object, but it's not logged. **This is safe.**

**Step 2: Add a regression test**

Add to `internal/confluence/client_test.go`:

```go
func TestClient_VerboseDoesNotLeakToken(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"results":[]}`))
    }))
    defer srv.Close()

    // Capture stdout
    old := os.Stdout
    r, w, _ := os.Pipe()
    os.Stdout = w

    client, _ := NewClient(ClientConfig{
        BaseURL:    srv.URL,
        Email:      "user@example.com",
        APIToken:   "super-secret-token-12345",
        HTTPClient: srv.Client(),
        Verbose:    true,
    })

    client.ListSpaces(context.Background(), SpaceListOptions{Limit: 1})

    w.Close()
    os.Stdout = old

    var buf bytes.Buffer
    io.Copy(&buf, r)
    output := buf.String()

    if strings.Contains(output, "super-secret-token-12345") {
        t.Fatal("verbose output leaks API token")
    }
    if strings.Contains(output, "Authorization") {
        t.Fatal("verbose output leaks Authorization header")
    }
}
```

**Step 3: Run the test**

```bash
go test ./internal/confluence/ -run "TestClient_VerboseDoesNotLeakToken" -v
```

Expected: PASS.

**Step 4: Commit**

```bash
git add internal/confluence/client_test.go
git commit -m "test: add regression test for verbose token leakage

Verifies that --verbose output only logs method and URL, never
API tokens or Authorization headers."
```

---

## Task 9: Replace hardcoded HTTP timeout with per-request context timeouts

**Priority:** Warning
**Review Issue:** #9

**Why:** The global `defaultHTTPTimeout = 300 * time.Second` is overly generous for page operations. The download client uses `Timeout: 0` (no timeout), which can hang indefinitely on stalled connections.

**Files:**
- Modify: `internal/confluence/client.go`

**Step 1: Reduce the default timeout**

Change `defaultHTTPTimeout` from 300s to a more reasonable 60s:
```go
const defaultHTTPTimeout = 60 * time.Second
```

The download client already has its own per-request timeout (30 min, line 314-317) via context, so the global `Timeout: 0` is acceptable there. However, add a transport-level timeout as a safety net.

**Step 2: Add a read timeout for the download client**

In `NewClient`, change the download client:
```go
downloadClient: &http.Client{
    Timeout: 30 * time.Minute, // Safety net for stalled downloads
},
```

This is a fallback — the per-request context timeout at line 316 handles normal cases. The 30-minute global timeout prevents truly stalled connections.

**Step 3: Run tests**

```bash
go test ./internal/confluence/ -v
go test ./...
```

Expected: All pass.

**Step 4: Commit**

```bash
git add internal/confluence/client.go
git commit -m "fix: reduce default HTTP timeout from 300s to 60s

Add 30-minute safety timeout for download client to prevent
indefinitely stalled connections."
```

---

## Task 10: Add client-side rate limiting

**Priority:** Warning
**Review Issue:** #10

**Why:** Confluence Cloud rate limits are ~5-10 req/s. Proactive throttling avoids 429 storms during bulk operations.

**Files:**
- Create: `internal/confluence/ratelimit.go`
- Create: `internal/confluence/ratelimit_test.go`
- Modify: `internal/confluence/client.go` (add limiter field, call in `do`)

**Step 1: Write failing test**

Create `internal/confluence/ratelimit_test.go`:
```go
func TestRateLimiter_ThrottlesRequests(t *testing.T) {
    rl := newRateLimiter(10) // 10 req/s
    defer rl.stop()

    start := time.Now()
    for i := 0; i < 5; i++ {
        if err := rl.wait(context.Background()); err != nil {
            t.Fatal(err)
        }
    }
    elapsed := time.Since(start)

    // 5 requests at 10/s should take ~400ms (first is immediate, then 4 waits)
    if elapsed < 350*time.Millisecond {
        t.Fatalf("expected >= 350ms for 5 requests at 10/s, got %s", elapsed)
    }
}

func TestRateLimiter_RespectsContextCancellation(t *testing.T) {
    rl := newRateLimiter(1) // 1 req/s = slow
    defer rl.stop()

    // Drain the first tick
    _ = rl.wait(context.Background())

    ctx, cancel := context.WithCancel(context.Background())
    cancel() // Cancel immediately

    err := rl.wait(ctx)
    if err == nil {
        t.Fatal("expected context error")
    }
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/confluence/ -run "TestRateLimiter" -v
```

Expected: FAIL (type doesn't exist yet).

**Step 3: Implement rate limiter**

Create `internal/confluence/ratelimit.go`:
```go
package confluence

import (
    "context"
    "time"
)

const defaultRequestsPerSecond = 5

type rateLimiter struct {
    ticker *time.Ticker
}

func newRateLimiter(rps int) *rateLimiter {
    if rps <= 0 {
        rps = defaultRequestsPerSecond
    }
    return &rateLimiter{
        ticker: time.NewTicker(time.Second / time.Duration(rps)),
    }
}

func (rl *rateLimiter) wait(ctx context.Context) error {
    select {
    case <-rl.ticker.C:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

func (rl *rateLimiter) stop() {
    rl.ticker.Stop()
}
```

**Step 4: Integrate into Client**

Add `limiter *rateLimiter` field to `Client` struct. Initialize in `NewClient`:
```go
limiter: newRateLimiter(defaultRequestsPerSecond),
```

Add rate limiting at the top of the `do` method:
```go
if err := c.limiter.wait(req.Context()); err != nil {
    return err
}
```

**Step 5: Run tests**

```bash
go test ./internal/confluence/ -v
go test ./...
```

Expected: All pass.

**Step 6: Commit**

```bash
git add internal/confluence/ratelimit.go internal/confluence/ratelimit_test.go internal/confluence/client.go
git commit -m "feat: add client-side rate limiting (5 req/s default)

Proactively throttles API calls to stay within Confluence Cloud
rate limits during bulk operations."
```

---

## Task 11: Add golangci-lint to lint target

**Priority:** Suggestion
**Review Issue:** #11

**Files:**
- Modify: `Makefile`
- Create: `.golangci.yml`

**Step 1: Create `.golangci.yml` config**

```yaml
run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - staticcheck
    - govet
    - gosec
    - ineffassign
    - unused

issues:
  exclude-use-default: true
```

**Step 2: Update Makefile lint target**

Add a conditional: use golangci-lint if available, fall back to go vet:

```makefile
## lint: run golangci-lint (falls back to go vet)
lint:
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run ./... || $(GO) vet ./...
```

**Step 3: Test locally**

```bash
make lint
```

Expected: Either golangci-lint runs, or falls back to go vet cleanly.

**Step 4: Commit**

```bash
git add .golangci.yml Makefile
git commit -m "chore: add golangci-lint config and update lint target

Enables errcheck, staticcheck, govet, gosec, ineffassign, unused.
Falls back to go vet if golangci-lint is not installed."
```

---

## Task 12: Add GitHub Actions CI pipeline

**Priority:** Suggestion
**Review Issue:** #12

**Files:**
- Create: `.github/workflows/ci.yml`

**Step 1: Create the workflow**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Vet
        run: go vet ./...

      - name: Test
        run: go test -race -coverprofile=coverage.out ./...

      - name: Check formatting
        run: test -z "$(gofmt -l .)"

      - name: Lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest
        continue-on-error: true
```

**Step 2: Commit**

```bash
mkdir -p .github/workflows
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions pipeline for test, vet, fmt, lint"
```

---

## Task 13: Add structured logging with `log/slog`

**Priority:** Suggestion
**Review Issue:** #13

**Why:** The project uses `fmt.Printf` everywhere. `log/slog` (stdlib since Go 1.21) enables log levels, JSON output for automation, and cleaner separation of user-facing output from debug output.

**Files:**
- Modify: `internal/confluence/client.go` (replace verbose `fmt.Printf` with slog)
- Modify: `cmd/root.go` (configure slog handler based on `--verbose`)

**Approach:** Minimal change — only replace the verbose/debug `fmt.Printf` calls in the confluence client with `slog.Debug`. User-facing output (`fmt.Fprintf(out, ...)`) stays as-is since it's intentional CLI output, not logging.

**Step 1: Configure slog in root.go**

In `init()` or a `PersistentPreRunE`, set the default slog level based on `--verbose`:

```go
rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
    level := slog.LevelWarn
    if flagVerbose {
        level = slog.LevelDebug
    }
    slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
    return nil
}
```

**Step 2: Replace verbose `fmt.Printf` in client.go**

In the `do` method, replace:
```go
if c.verbose {
    fmt.Printf("%s %s\n", req.Method, req.URL.String())
}
```
with:
```go
slog.Debug("http request", "method", req.Method, "url", req.URL.String())
```

Do the same for the download verbose log (line 339-340) and retry verbose logs.

Remove the `verbose bool` field from `Client` and `ClientConfig` since slog handles level filtering globally.

**Step 3: Run tests**

```bash
go test ./...
```

Expected: All pass. Tests that check verbose output may need updating if they were asserting on stdout.

**Step 4: Commit**

```bash
git add cmd/root.go internal/confluence/client.go
git commit -m "refactor: replace verbose fmt.Printf with log/slog

Debug output now uses structured logging via slog. User-facing
CLI output remains as fmt.Fprintf. Verbose flag controls slog level."
```

---

## Task 14: Share transport config between HTTP clients

**Priority:** Suggestion
**Review Issue:** #16

**Why:** The download client is a completely separate `http.Client` with default transport settings. It should share TLS configuration and connection pooling with the main client.

**Files:**
- Modify: `internal/confluence/client.go` (NewClient function, lines 94-111)

**Step 1: Share the transport**

In `NewClient`, create one shared transport and use it for both clients:

```go
transport := http.DefaultTransport.(*http.Transport).Clone()

httpClient := cfg.HTTPClient
if httpClient == nil {
    httpClient = &http.Client{
        Timeout:   defaultHTTPTimeout,
        Transport: transport,
    }
}

downloadClient := &http.Client{
    Timeout:   30 * time.Minute,
    Transport: transport,
}
```

If `cfg.HTTPClient` is provided (tests), use its transport for the download client too:
```go
if cfg.HTTPClient != nil {
    downloadClient.Transport = cfg.HTTPClient.Transport
}
```

**Step 2: Run tests**

```bash
go test ./internal/confluence/ -v
go test ./...
```

Expected: All pass.

**Step 3: Commit**

```bash
git add internal/confluence/client.go
git commit -m "refactor: share HTTP transport between API and download clients

Both clients now share TLS config and connection pooling."
```

---

## Execution Order and Dependencies

```
Task 1 (fix tests)          ─┐
Task 2 (remove scripts)      ├── Independent, can run in parallel
Task 4 (fix go.mod)         ─┘
         │
Task 3 (retry logic)        ── After Task 2 (so go test ./... works clean)
         │
Task 9 (HTTP timeouts)      ── After Task 3 (retry changes the `do` method)
Task 10 (rate limiting)     ── After Task 3 (adds to `do` method)
Task 14 (shared transport)  ── After Task 9 (changes client construction)
         │
Task 5 (decompose functions) ── After Tasks 1, 6
Task 6 (stash management)   ── After Task 1
Task 7 (signal handling)    ── Independent
Task 8 (token leakage test) ── Independent
         │
Task 11 (golangci-lint)     ── After all code changes
Task 12 (CI pipeline)       ── After Task 11
Task 13 (structured logging)── After Task 3 (replaces verbose in client.go)
```

| # | Task | Priority | Depends On |
|---|------|----------|------------|
| 1 | Fix failing unit tests | Critical | — |
| 2 | Remove scripts/ directory | Critical | — |
| 3 | Add HTTP retry with backoff | Critical | 2 |
| 4 | Fix go.mod Go version | Warning | — |
| 5 | Decompose large functions | Warning | 1, 6 |
| 6 | Improve stash management | Warning | 1 |
| 7 | Add signal handling | Warning | — |
| 8 | Audit verbose for token leakage | Warning | — |
| 9 | Per-request context timeouts | Warning | 3 |
| 10 | Add rate limiting | Warning | 3 |
| 11 | Add golangci-lint | Suggestion | all code tasks |
| 12 | Add CI pipeline | Suggestion | 11 |
| 13 | Add structured logging | Suggestion | 3 |
| 14 | Share transport config | Suggestion | 9 |
