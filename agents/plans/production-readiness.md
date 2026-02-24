# Production Readiness Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix all issues identified in the production-readiness review to make `conf` production-grade.

**Architecture:** Seven self-contained tasks ordered by severity — critical fixes first (failing tests, dead scripts, retry logic), then robustness (Go version, signal handling, rate limiting), then maintainability (function decomposition, CI, linting). Each task is independently committable.

**Tech Stack:** Go 1.24, Cobra CLI, `net/http`, `os/signal`, `context`, `time`, GitHub Actions

---

## Task 1: Fix failing unit tests in `internal/sync/pull_test.go`

**Priority:** Critical
**Why:** Two tests fail — `TestPull_IncrementalRewriteDeleteAndWatermark` and `TestPlanPagePaths_MaintainsConfluenceHierarchy`. The hierarchy preservation fix (commit `696a33f`) changed `PlanPagePaths` behavior so that root-level pages with children now get nested into `Title/Title.md` instead of staying at `Title.md`. The tests expect the old flat layout for root pages.

**Files:**
- Fix: `internal/sync/pull.go:832-850` (function `plannedPageRelPath`)
- Verify: `internal/sync/pull_test.go:207-227` (TestPlanPagePaths_MaintainsConfluenceHierarchy)
- Verify: `internal/sync/pull_test.go:56-205` (TestPull_IncrementalRewriteDeleteAndWatermark)

**Root cause analysis:**

In `plannedPageRelPath` (line 832), when a page `hasChildren == true`, the function appends the page's own title as an ancestor segment (line 846):
```go
if hasChildren {
    ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
}
```

For a root page like `{ID: "1", Title: "Root"}` with children, this produces `Root/Root.md` because:
1. `ancestorPathSegments` returns `nil` (no parent ID → root page)
2. `hasChildren` is `true` (page "2" has ParentPageID "1")
3. So `ancestorSegments = ["Root"]`, `filename = "Root.md"` → `Root/Root.md`

The test expects `Root.md` (flat). The page with children should only create a subdirectory for its **children**, not place itself inside its own subdirectory. A root page with children should remain at the top level.

**Step 1: Run the failing tests to confirm the exact failures**

```bash
go test ./internal/sync/ -run "TestPlanPagePaths_MaintainsConfluenceHierarchy|TestPull_IncrementalRewriteDeleteAndWatermark" -v 2>&1
```

Expected: Both FAIL.

**Step 2: Fix `plannedPageRelPath` in `internal/sync/pull.go`**

The fix: a page with children should NOT nest itself inside its own title directory. The `hasChildren` flag should only affect path planning for child pages (which already get the parent's title as an ancestor segment via `ancestorPathSegments`). Remove the self-nesting:

Change `internal/sync/pull.go` lines 845-847 from:
```go
if hasChildren {
    ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
}
```

To simply removing those three lines entirely. The `hasChildren` parameter can also be removed from the function signature and the `pageHasChildren` map computation at the call site can be removed.

However — before removing, verify whether any *other* test relies on the `hasChildren` nesting behaviour. Search for tests that expect subdirectory-based paths for parent pages:

```bash
grep -n "hasChildren\|pageHasChildren" internal/sync/pull.go internal/sync/pull_test.go
```

If the `hasChildren` nesting is needed for non-root pages (pages that are both children AND parents), then the fix is more nuanced: only apply the nesting when the page itself has a parent. In that case, change:

```go
if hasChildren {
    ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
}
```

to:

```go
if hasChildren && len(ancestorSegments) > 0 {
    ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
}
```

This preserves the nesting for intermediate pages (e.g., Child with grandchildren becomes `Child/Child.md`) while keeping root pages flat (e.g., Root stays at `Root.md`). But examine the test expectations first — `TestPlanPagePaths_MaintainsConfluenceHierarchy` expects:
- Root (ID "1", no parent) → `Root.md` (flat)
- Child (ID "2", parent "1") → `Child.md` (flat — no subdirectory)
- Grand Child (ID "3", parent "2") → `Child/Grand-Child.md` (nested under parent)

So `Child` is both a parent (has Grand Child) AND a child (parent is Root), yet the test expects `Child.md` not `Child/Child.md`. The nesting should only create a directory *named after the parent* for the children, not nest the parent inside itself. The correct logic is: **remove the `hasChildren` self-nesting entirely**.

The child pages already pick up their parent's title via `ancestorPathSegments`. That's the correct mechanism for hierarchy — it reads the parent chain upward. The `hasChildren` block is redundant and wrong.

```go
// DELETE these lines (845-847):
// if hasChildren {
//     ancestorSegments = append(ancestorSegments, fs.SanitizePathSegment(title))
// }
```

Also remove the `hasChildren` parameter from `plannedPageRelPath` and the `pageHasChildren` map from `PlanPagePaths` since they're no longer used.

**Step 3: Run the tests to verify they pass**

```bash
go test ./internal/sync/ -run "TestPlanPagePaths_MaintainsConfluenceHierarchy|TestPull_IncrementalRewriteDeleteAndWatermark" -v 2>&1
```

Expected: Both PASS.

**Step 4: Run ALL tests to check for regressions**

```bash
go test ./internal/sync/ -v 2>&1
```

Expected: All pass. If `TestPlanPagePaths_UsesFolderHierarchy` or other hierarchy tests fail, the folder-based hierarchy path uses `ancestorPathSegments` which follows folder chains, not the `hasChildren` path — so they should be unaffected.

**Step 5: Also run cmd tests (which test push/pull end-to-end)**

```bash
go test ./cmd/ -v -count=1 2>&1
```

Expected: All pass.

**Step 6: Commit**

```bash
git add internal/sync/pull.go
git commit -m "fix: remove self-nesting for pages with children in PlanPagePaths

Root-level and intermediate pages with children were incorrectly placed
inside their own title subdirectory (e.g., Root/Root.md instead of Root.md).
Children already inherit parent paths via ancestorPathSegments, making the
hasChildren self-nesting redundant."
```

---

## Task 2: Remove `scripts/` directory

**Priority:** Critical
**Why:** All three files (`generate_test_data.go`, `clean_test_data.go`, `wipe_spaces.go`) declare `func main()` in `package main`, causing `go test ./...` and `go vet ./...` to fail with `main redeclared in this block`. These are developer-only utilities with hardcoded paths (`D:/confluence-test-data`, `D:/confluence-test-final`) and hardcoded space keys (`TD`, `SD`). They are not part of the CLI and serve no production purpose.

**Files:**
- Delete: `scripts/generate_test_data.go`
- Delete: `scripts/clean_test_data.go`
- Delete: `scripts/wipe_spaces.go`

**Step 1: Verify no production code imports from scripts/**

```bash
grep -r "confluence-markdown-sync/scripts" --include="*.go" .
```

Expected: No matches.

**Step 2: Delete the scripts directory**

```bash
rm -rf scripts/
```

**Step 3: Run `go vet` to confirm the build error is resolved**

```bash
go vet ./...
```

Expected: Clean output, no errors.

**Step 4: Run full test suite**

```bash
go test ./...
```

Expected: No `scripts [build failed]` line. All other packages pass.

**Step 5: Commit**

```bash
git add -u scripts/
git commit -m "chore: remove scripts/ directory

Developer-only utilities with hardcoded paths and space keys.
All three files declared func main() in the same package, causing
go vet and go test to fail. These scripts are not part of the CLI."
```

---

## Task 3: Add HTTP retry with exponential backoff

**Priority:** Critical
**Why:** Confluence Cloud routinely returns `429 Too Many Requests` and intermittent `5xx` errors. Without retry logic, space-wide push/pull of 50+ pages will fail partway through. This is the single biggest production risk.

**Files:**
- Modify: `internal/confluence/client.go:676-707` (the `do` method)
- Create: `internal/confluence/retry.go` (retry logic, keeps client.go focused)
- Create: `internal/confluence/retry_test.go`

**Step 1: Write the failing test for retry behaviour**

Create `internal/confluence/retry_test.go`:

```go
package confluence

import (
    "context"
    "net/http"
    "net/http/httptest"
    "sync/atomic"
    "testing"
)

func TestClient_RetriesOn429(t *testing.T) {
    var attempts int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n := atomic.AddInt32(&attempts, 1)
        if n <= 2 {
            w.Header().Set("Retry-After", "0")
            w.WriteHeader(http.StatusTooManyRequests)
            w.Write([]byte(`{"message":"rate limited"}`))
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"id":"space-1","key":"TEST"}`))
    }))
    defer srv.Close()

    client, err := NewClient(ClientConfig{
        BaseURL:    srv.URL,
        Email:      "test@example.com",
        APIToken:   "token",
        HTTPClient: srv.Client(),
    })
    if err != nil {
        t.Fatal(err)
    }

    _, err = client.GetSpace(context.Background(), "TEST")
    if err != nil {
        t.Fatalf("expected success after retries, got: %v", err)
    }
    if got := atomic.LoadInt32(&attempts); got != 3 {
        t.Fatalf("expected 3 attempts, got %d", got)
    }
}

func TestClient_RetriesOn500(t *testing.T) {
    var attempts int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n := atomic.AddInt32(&attempts, 1)
        if n == 1 {
            w.WriteHeader(http.StatusInternalServerError)
            w.Write([]byte(`{"message":"internal error"}`))
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"id":"space-1","key":"TEST"}`))
    }))
    defer srv.Close()

    client, err := NewClient(ClientConfig{
        BaseURL:    srv.URL,
        Email:      "test@example.com",
        APIToken:   "token",
        HTTPClient: srv.Client(),
    })
    if err != nil {
        t.Fatal(err)
    }

    _, err = client.GetSpace(context.Background(), "TEST")
    if err != nil {
        t.Fatalf("expected success after retry, got: %v", err)
    }
    if got := atomic.LoadInt32(&attempts); got != 2 {
        t.Fatalf("expected 2 attempts, got %d", got)
    }
}

func TestClient_DoesNotRetryOn400(t *testing.T) {
    var attempts int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&attempts, 1)
        w.WriteHeader(http.StatusBadRequest)
        w.Write([]byte(`{"message":"bad request"}`))
    }))
    defer srv.Close()

    client, err := NewClient(ClientConfig{
        BaseURL:    srv.URL,
        Email:      "test@example.com",
        APIToken:   "token",
        HTTPClient: srv.Client(),
    })
    if err != nil {
        t.Fatal(err)
    }

    _, err = client.GetSpace(context.Background(), "TEST")
    if err == nil {
        t.Fatal("expected error for 400")
    }
    if got := atomic.LoadInt32(&attempts); got != 1 {
        t.Fatalf("expected 1 attempt (no retry for 400), got %d", got)
    }
}

func TestClient_GivesUpAfterMaxRetries(t *testing.T) {
    var attempts int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&attempts, 1)
        w.Header().Set("Retry-After", "0")
        w.WriteHeader(http.StatusTooManyRequests)
        w.Write([]byte(`{"message":"rate limited"}`))
    }))
    defer srv.Close()

    client, err := NewClient(ClientConfig{
        BaseURL:    srv.URL,
        Email:      "test@example.com",
        APIToken:   "token",
        HTTPClient: srv.Client(),
    })
    if err != nil {
        t.Fatal(err)
    }

    _, err = client.GetSpace(context.Background(), "TEST")
    if err == nil {
        t.Fatal("expected error after max retries")
    }
    // Default max retries = 3, so 1 original + 3 retries = 4 attempts
    if got := atomic.LoadInt32(&attempts); got != 4 {
        t.Fatalf("expected 4 attempts (1 + 3 retries), got %d", got)
    }
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/confluence/ -run "TestClient_Retries|TestClient_DoesNotRetry|TestClient_GivesUp" -v
```

Expected: FAIL (retry logic doesn't exist yet).

**Step 3: Implement retry logic in `internal/confluence/retry.go`**

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
    defaultMaxRetries  = 3
    defaultBaseDelay   = 500 * time.Millisecond
    defaultMaxDelay    = 30 * time.Second
)

// isRetryableStatus returns true for HTTP status codes that warrant a retry.
func isRetryableStatus(code int) bool {
    return code == http.StatusTooManyRequests ||
        code == http.StatusInternalServerError ||
        code == http.StatusBadGateway ||
        code == http.StatusServiceUnavailable ||
        code == http.StatusGatewayTimeout
}

// retryDelay calculates the delay before the next retry attempt using
// exponential backoff with jitter. It respects the Retry-After header if present.
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
    // Add jitter: ±25%
    jitter := backoff * 0.25 * (rand.Float64()*2 - 1)
    return time.Duration(backoff + jitter)
}
```

**Step 4: Modify the `do` method in `internal/confluence/client.go`**

Replace the `do` method (lines 676-707) to add retry logic:

```go
func (c *Client) do(req *http.Request, out any) error {
    var lastErr error
    for attempt := 0; attempt <= defaultMaxRetries; attempt++ {
        if attempt > 0 {
            // Re-check context before retrying
            if err := req.Context().Err(); err != nil {
                return err
            }
        }
        if c.verbose {
            if attempt > 0 {
                fmt.Printf("  retry #%d: %s %s\n", attempt, req.Method, req.URL.String())
            } else {
                fmt.Printf("%s %s\n", req.Method, req.URL.String())
            }
        }
        resp, err := c.httpClient.Do(req)
        if err != nil {
            return err // Network errors are not retried
        }

        if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
            defer resp.Body.Close()
            if out == nil {
                _, _ = io.Copy(io.Discard, resp.Body)
                return nil
            }
            if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
                return fmt.Errorf("decode response JSON: %w", err)
            }
            _, _ = io.Copy(io.Discard, resp.Body)
            return nil
        }

        bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
        resp.Body.Close()

        lastErr = &APIError{
            StatusCode: resp.StatusCode,
            Method:     req.Method,
            URL:        req.URL.String(),
            Message:    decodeAPIErrorMessage(bodyBytes),
            Body:       string(bodyBytes),
        }

        if !isRetryableStatus(resp.StatusCode) || attempt == defaultMaxRetries {
            return lastErr
        }

        delay := retryDelay(resp, attempt)
        if c.verbose {
            fmt.Printf("  retrying in %s (status %d)\n", delay, resp.StatusCode)
        }
        select {
        case <-time.After(delay):
        case <-req.Context().Done():
            return req.Context().Err()
        }

        // For retries, we need to reset the request body if it was consumed.
        // GET/DELETE requests have no body, so this is safe.
        // For POST/PUT with body, the body is already consumed.
        // We need to handle this by buffering the body in newRequest.
        if req.Body != nil && req.GetBody != nil {
            newBody, err := req.GetBody()
            if err != nil {
                return lastErr
            }
            req.Body = newBody
        }
    }
    return lastErr
}
```

**Important:** The `newRequest` method uses `json.Marshal` + `bytes.NewReader` for the body. `bytes.Reader` implements `io.Seeker`, so `http.NewRequest` will set `GetBody` automatically. This means POST/PUT retries will work correctly.

Verify this by checking that `newRequest` uses `bytes.NewReader` (not `bytes.NewBuffer`):

```bash
grep -n "NewReader\|NewBuffer" internal/confluence/client.go
```

If it uses `bytes.NewBuffer`, change to `bytes.NewReader` so `GetBody` is set by the stdlib.

**Step 5: Run the retry tests**

```bash
go test ./internal/confluence/ -run "TestClient_Retries|TestClient_DoesNotRetry|TestClient_GivesUp" -v
```

Expected: All PASS.

**Step 6: Run full test suite**

```bash
go test ./...
```

Expected: All pass.

**Step 7: Commit**

```bash
git add internal/confluence/retry.go internal/confluence/retry_test.go internal/confluence/client.go
git commit -m "feat: add HTTP retry with exponential backoff for 429 and 5xx

Confluence Cloud returns 429 and intermittent 5xx errors under load.
The client now retries up to 3 times with exponential backoff and jitter.
Respects Retry-After header. Does not retry 4xx client errors."
```

---

## Task 4: Fix `go.mod` Go version

**Priority:** Warning
**Why:** `go 1.25.5` does not exist (Go is at 1.24.x as of Feb 2026). This may cause build errors for contributors or CI tools that validate Go version declarations.

**Files:**
- Modify: `go.mod` line 3

**Step 1: Fix the version**

Change `go 1.25.5` to a valid released version. Check which version the current toolchain is:

```bash
go version
```

Then update `go.mod` line 3 to match (e.g., `go 1.24`).

**Step 2: Run `go mod tidy`**

```bash
go mod tidy
```

**Step 3: Run tests**

```bash
go test ./...
```

Expected: All pass.

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "fix: set go.mod to valid Go version"
```

---

## Task 5: Add graceful signal handling

**Priority:** Warning
**Why:** If a user hits Ctrl+C during a push, the process may leave dangling git worktrees, incomplete stash state, or half-pushed pages. Adding signal handling enables cleanup.

**Files:**
- Modify: `cmd/conf/main.go` (wrap context with signal.NotifyContext)
- Modify: `cmd/root.go` (pass context down)
- Modify: `cmd/push.go` (use cmd.Context() consistently)
- Modify: `cmd/pull.go` (use cmd.Context() consistently)

**Step 1: Update `cmd/conf/main.go` to install signal handler**

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/rgonek/confluence-markdown-sync/cmd"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    cmd.ExecuteContext(ctx)
}
```

**Step 2: Update `cmd/root.go` to accept external context**

Add an `ExecuteContext(ctx)` function that passes the context to cobra:

```go
func ExecuteContext(ctx context.Context) {
    if err := rootCmd.ExecuteContext(ctx); err != nil {
        os.Exit(1)
    }
}
```

**Step 3: Verify push.go and pull.go use `cmd.Context()`**

Search for `context.Background()` in cmd/push.go and cmd/pull.go and replace with `cmd.Context()` to propagate cancellation. The Confluence client already accepts `ctx` parameters, so cancellation will propagate to HTTP calls.

```bash
grep -n "context.Background()" cmd/push.go cmd/pull.go
```

Replace each occurrence with `cmd.Context()`.

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

Ctrl+C now cancels the context, allowing in-flight HTTP requests
and git operations to terminate cleanly."
```

---

## Task 6: Add client-side rate limiting

**Priority:** Warning
**Why:** Confluence Cloud rate limits are typically 5-10 req/s for standard tiers. Space-wide operations on 100+ pages can easily exceed this, even with retry logic. Proactive throttling avoids 429 storms.

**Files:**
- Modify: `internal/confluence/client.go` (add rate limiter field)
- Create: `internal/confluence/ratelimit.go` (simple token bucket)
- Create: `internal/confluence/ratelimit_test.go`

**Step 1: Write a simple rate limiter**

Use Go's `golang.org/x/time/rate` or implement a simple token-bucket limiter. Since the project minimizes dependencies, a simple `time.Ticker`-based approach is cleaner:

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

**Step 2: Integrate into Client**

Add a `limiter *rateLimiter` field to `Client` struct. Initialize in `NewClient`. Call `c.limiter.wait(req.Context())` at the top of the `do` method before making the HTTP call.

**Step 3: Write tests**

Create `internal/confluence/ratelimit_test.go` that verifies the limiter throttles requests to ~5/s and respects context cancellation.

**Step 4: Run tests**

```bash
go test ./internal/confluence/ -v
go test ./...
```

Expected: All pass.

**Step 5: Commit**

```bash
git add internal/confluence/ratelimit.go internal/confluence/ratelimit_test.go internal/confluence/client.go
git commit -m "feat: add client-side rate limiting (5 req/s default)

Proactively throttles API calls to stay within Confluence Cloud rate
limits, reducing 429 responses during bulk operations."
```

---

## Task 7: Add GitHub Actions CI pipeline

**Priority:** Suggestion
**Why:** No CI exists. Regressions can slip in undetected. A basic pipeline running tests, vet, and formatting on PRs prevents this.

**Files:**
- Create: `.github/workflows/ci.yml`

**Step 1: Create the CI workflow**

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
        run: |
          gofmt -l .
          test -z "$(gofmt -l .)"
```

**Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions pipeline for test, vet, and fmt"
```

---

## Execution Order

| # | Task | Priority | Est. Complexity |
|---|------|----------|-----------------|
| 1 | Fix failing unit tests | Critical | Low |
| 2 | Remove scripts/ directory | Critical | Trivial |
| 3 | Add HTTP retry with backoff | Critical | Medium |
| 4 | Fix go.mod Go version | Warning | Trivial |
| 5 | Add signal handling | Warning | Low |
| 6 | Add rate limiting | Warning | Low-Medium |
| 7 | Add CI pipeline | Suggestion | Low |

Tasks 1, 2, and 4 are independent and can be executed in parallel.
Task 3 should be done before Task 6 (retry before rate limiting).
Task 5 is independent.
Task 7 should be done last (CI validates everything else).
