# Unified Implementation Order

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan group-by-group.

**Goal:** Implement all 32 issues from three production-readiness reviews in a safe, dependency-respecting order. Each group becomes a feature branch merged via PR.

**Source Plans:**
- `production-readiness-full.md` — 14 tasks (prefixed `PROD-`)
- `pull-production-readiness.md` — 11 actionable tasks (prefixed `PULL-`)
- `push-new-page-improvements.md` — 7 tasks (prefixed `PUSH-`)

**Key Decisions:**
- `PULL-8` (SpaceKey in state) and `PUSH-2` (FolderPathIndex in state) are **merged** into a single task in Group 4 to avoid merge conflicts on `internal/fs/state.go`.
- `PULL-9` is a no-op (covered by `PULL-5`).

---

## Dependency Graph

```
Group 1 (Foundation)
  │
  ├──→ Group 2 (HTTP Client)  ──┐
  ├──→ Group 3 (Pull Hardening) │
  └──→ Group 4 (State + Iface) ─┤
         │                       │
         ├──→ Group 5 (Push Folders) ←─ Group 2
         │
         └──→ Group 6 (Command Safety) ←─ Group 1
                  │
                  ▼
              Group 7 (Code Quality) ←─ Groups 2-6
                  │
                  ▼
              Group 8 (CI/Tooling)
```

**Parallel lanes after Group 1:** Groups 2, 3, and 4 can be developed simultaneously.

---

## Group 1: Foundation Fixes

**Branch:** `fix/foundation`
**Depends on:** nothing
**Goal:** Get the codebase to a clean, buildable, testable state.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 1.1 | Fix failing unit tests in `PlanPagePaths` | PROD-1 | Critical | `internal/sync/pull.go` |
| 1.2 | Remove `scripts/` directory | PROD-2 | Critical | `scripts/*` (delete) |
| 1.3 | Fix `go.mod` Go version (1.25.5 → actual) | PROD-4 | Warning | `go.mod`, `go.sum` |

**Internal order:** 1.1 and 1.2 are independent; 1.3 after both (run `go mod tidy` on clean state).

**Verification:** `go vet ./...` clean, `go test ./...` all pass.

**Details:**
- **1.1:** Remove the `hasChildren` self-nesting block and `pageHasChildren` map from `plannedPageRelPath` in `pull.go:832-850`. Root cause: pages with children were placed inside their own title subdirectory (`Root/Root.md` instead of `Root.md`).
- **1.2:** Delete `scripts/generate_test_data.go`, `scripts/clean_test_data.go`, `scripts/wipe_spaces.go`. All declare `func main()` breaking `go vet`/`go test`.
- **1.3:** Change `go 1.25.5` to the installed version (e.g., `go 1.24`), run `go mod tidy`.

---

## Group 2: HTTP Client Resilience

**Branch:** `feat/http-resilience`
**Depends on:** Group 1
**Goal:** Make the Confluence HTTP client production-ready with retry, rate limiting, timeouts, and new folder/move endpoints.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 2.1 | Add HTTP retry with exponential backoff | PROD-3 | Critical | `internal/confluence/retry.go` (new), `retry_test.go` (new), `client.go` |
| 2.2 | Reduce HTTP timeout from 300s to 60s | PROD-9 | Warning | `internal/confluence/client.go` |
| 2.3 | Add client-side rate limiting (5 req/s) | PROD-10 | Warning | `internal/confluence/ratelimit.go` (new), `ratelimit_test.go` (new), `client.go` |
| 2.4 | Share transport between API and download clients | PROD-14 | Suggestion | `internal/confluence/client.go` |
| 2.5 | Add `CreateFolder` and `MovePage` methods | PUSH-1 | High | `internal/confluence/client.go`, `types.go`, `client_test.go` |

**Internal order:** 2.1 → 2.2 → 2.3 → 2.4 (all modify `client.go` `do` method or constructor). 2.5 is independent (adds new methods, doesn't touch `do`).

**Verification:** `go test ./internal/confluence/ -v` all pass.

**Details:**
- **2.1:** Retry up to 3 times on 429/5xx with exponential backoff + jitter. Respect `Retry-After` header. Don't retry 4xx. Modify the `do` method with retry loop. Use `req.GetBody()` to reset body for retries.
- **2.2:** Change `defaultHTTPTimeout` to 60s. Add `Timeout: 30 * time.Minute` safety net on download client.
- **2.3:** Token-bucket rate limiter using `time.Ticker`. Call `limiter.wait(req.Context())` at top of `do`. Respect context cancellation.
- **2.4:** Clone `http.DefaultTransport`, share between main and download clients. If `cfg.HTTPClient` provided (tests), use its transport for download client too.
- **2.5:** `CreateFolder` → `POST /wiki/api/v2/folders`. `MovePage` → `PUT /wiki/rest/api/content/{id}/move/append/{targetId}`. Add `FolderCreateInput` type. Add both to `Service` interface.

---

## Group 3: Pull Command Hardening

**Branch:** `feat/pull-hardening`
**Depends on:** Group 1
**Goal:** Make `conf pull` safe under rate limits, cancellation-aware, and robust against partial failures.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 3.1 | Add bounded concurrency for page detail fetches | PULL-1 | Critical | `internal/sync/pull.go`, `pull_concurrent_test.go` (new) |
| 3.2 | Replace `time.Sleep` with context-aware wait in retry | PULL-2 | Critical | `internal/sync/pull.go`, `pull_test.go` |
| 3.3 | Use atomic file writes for attachment downloads | PULL-3 | Critical | `internal/sync/pull.go`, `pull_test.go` |
| 3.4 | Add max-iterations guard to pagination loops | PULL-6 | Warning | `internal/sync/pull.go`, `cmd/pull.go` |
| 3.5 | Preserve existing status/labels when API fetch fails | PULL-7 | Warning | `internal/sync/pull.go`, `pull_test.go` |
| 3.6 | Clean empty markdown parent dirs after deletion | PULL-11 | Suggestion | `internal/sync/pull.go`, `pull_test.go` |
| 3.7 | Emit diagnostic for malformed ADF content | PULL-12 | Suggestion | `internal/sync/pull.go` |
| 3.8 | Show current page in progress during detail fetches | PULL-10 | Suggestion | `internal/sync/pull.go` |

**Internal order:** 3.1 first (restructures fetch loop used by 3.5 and 3.8). 3.2 → 3.3 (same download retry block). Others independent.

**Verification:** `go test ./internal/sync/ -v` all pass.

**Details:**
- **3.1:** Replace serial page detail loop with `errgroup.WithContext` limited to 5 workers. Need `go get golang.org/x/sync/errgroup`. Add `getPageHook` to `fakePullRemote` for testing.
- **3.2:** Add `contextSleep` helper using `select` on `time.After` and `ctx.Done()`. Replace `time.Sleep` in attachment retry.
- **3.3:** Write to `os.CreateTemp` then `os.Rename` on success. Remove temp file on failure. Never truncate existing asset.
- **3.4:** Add `maxPaginationIterations = 500` constant. Apply to `listAllPages`, `listAllChanges`, and estimate functions in `cmd/pull.go`. Also detect cursor loops (`nextCursor == cursor`).
- **3.5:** When `GetContentStatus`/`GetLabels` fails, read existing file's frontmatter to preserve values. Emit diagnostics.
- **3.6:** After markdown deletion, call `removeEmptyParentDirs` up to space root.
- **3.7:** Change `collectAttachmentRefs` to return `*PullDiagnostic` on JSON unmarshal failure.
- **3.8:** Call `opts.Progress.SetCurrentItem(pageID)` in concurrent fetch goroutine.

---

## Group 4: State & Interface Extensions

**Branch:** `feat/state-extensions`
**Depends on:** Group 1
**Goal:** Extend SpaceState and PushRemote with fields/methods needed by Groups 5 and 6.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 4.1 | Add `SpaceKey` + `FolderPathIndex` to `SpaceState` | PULL-8 + PUSH-2 (merged) | Warning/High | `internal/fs/state.go` |
| 4.2 | Add `CreateFolder` + `MovePage` to `PushRemote` interface | PUSH-2 | High | `internal/sync/push.go`, `cmd/dry_run_remote.go`, test fakes |

**Internal order:** 4.1 then 4.2 (interface references the new state fields).

**Verification:** `go test ./...` all pass.

**Details:**
- **4.1:** Add two fields to `SpaceState`:
  ```go
  SpaceKey        string            `json:"space_key,omitempty"`
  FolderPathIndex map[string]string `json:"folder_path_index,omitempty"`
  ```
  Update `NewSpaceState()` to initialize `FolderPathIndex`. Save `SpaceKey` in `Pull()` before return. Add `FolderPathIndex` initialization.
- **4.2:** Add `CreateFolder(ctx, FolderCreateInput) (Folder, error)` and `MovePage(ctx, pageID, targetID string) error` to `PushRemote`. Add dry-run stubs. Update all test fakes.

---

## Group 5: Push New Page Improvements

**Branch:** `feat/push-folders`
**Depends on:** Groups 2 (push-1: CreateFolder/MovePage client methods) + 4 (FolderPathIndex, PushRemote interface)
**Goal:** Complete the push flow for new pages with Confluence folder support and safety guards.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 5.1 | Auto-create Confluence folders for orphaned directories | PUSH-3 | High | `internal/sync/push.go`, `push_test.go` |
| 5.2 | Guard against deleted ID field creating duplicates | PUSH-4 | High | `internal/sync/push.go`, `push_test.go` |
| 5.3 | Replace placeholder content with empty ADF document | PUSH-5 | Medium | `internal/sync/push.go` |
| 5.4 | Emit diagnostics when folders are auto-created | PUSH-6 | Medium | `internal/sync/push.go`, `cmd/push.go` |

**Internal order:** 5.1 first (5.4 depends on it). 5.2 and 5.3 are independent.

**Verification:** `go test ./internal/sync/ -v && go test ./cmd/ -v -count=1`.

**Details:**
- **5.1:** Implement `ensureFolderHierarchy` to create Confluence folders segment-by-segment. Enhance `resolveParentIDFromHierarchy` to check `folderIDByPath`. Create page at space root, then `MovePage` under folder (v2 API can't parent to folder). Persist `folderIDByPath` in state.
- **5.2:** Before creating a new page, check if `state.PagePathIndex[relPath]` has an existing ID. If so, error with guidance to restore the `id` field.
- **5.3:** Change initial page body from `"Initial sync..."` paragraph to `{"version":1,"type":"doc","content":[]}`.
- **5.4:** Add `PushDiagnostic` type and `Diagnostics` field to `PushResult`. Emit diagnostic when folder auto-creation occurs. Display in `cmd/push.go`.

---

## Group 6: Command-Level Safety & UX

**Branch:** `feat/command-safety`
**Depends on:** Groups 1 + 4 (SpaceKey for pull-8 cmd changes, FolderPathIndex for push-7)
**Goal:** Make CLI commands safe to interrupt, properly scoped, and well-structured.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 6.1 | Add graceful signal handling (SIGINT/SIGTERM) | PROD-7 | Warning | `cmd/conf/main.go`, `cmd/root.go`, `cmd/push.go`, `cmd/pull.go` |
| 6.2 | Improve stash management with `defer` in push | PROD-6 | Warning | `cmd/push.go` |
| 6.3 | Eliminate duplicate page listing between estimate and pull | PULL-4 | Warning | `internal/sync/pull.go`, `cmd/pull.go` |
| 6.4 | Scope conflict checkout to space directory + use writer | PULL-5 | Warning | `cmd/pull.go` |
| 6.5 | Use SpaceKey from state in `resolveInitialPullContext` | PULL-8 (cmd part) | Warning | `cmd/pull.go` |
| 6.6 | Persist folder IDs in state during pull (round-trip) | PUSH-7 | Medium | `internal/sync/pull.go` |

**Internal order:** 6.1 first (context propagation enables cancellation for all commands). 6.2 after 6.1 (stash defer works with signal context). 6.3-6.6 are independent of each other.

**Verification:** `go test ./cmd/ -v -count=1 && go test ./...`.

**Details:**
- **6.1:** Create `signal.NotifyContext` in `main.go`. Add `ExecuteContext(ctx)` to `root.go`. Replace `context.Background()` with `cmd.Context()` in push.go and pull.go.
- **6.2:** Replace 15+ manual `if stashRef != "" { StashPop }` calls with a single `defer` closure. Set `stashRef = ""` before pull-merge path to prevent double-pop.
- **6.3:** Add `PrefetchedPages []confluence.Page` to `PullOptions`. Return pages from `estimatePullImpactWithSpace`. Pass through to `Pull()`.
- **6.4:** Add `scopePath`, `in io.Reader`, `out io.Writer` params to `handlePullConflict`. Replace `"."` with `scopePath` in checkout commands. Replace `fmt.Println`/`Scanln` with `fmt.Fprintln(out)`/`bufio.Scanner(in)`.
- **6.5:** In `resolveInitialPullContext`, check `state.SpaceKey` before falling back to file iteration or directory name.
- **6.6:** Build `folderPathIndex` from `folderByID` map in `Pull()`. Implement `buildFolderLocalPath` to walk folder chain. Save in `result.State.FolderPathIndex`.

---

## Group 7: Code Quality & Observability

**Branch:** `refactor/code-quality`
**Depends on:** Groups 2-6 (all code changes done)
**Goal:** Improve code structure, add regression tests, and modernize logging.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 7.1 | Decompose `runPush` into smaller named functions | PROD-5 | Warning | `cmd/push.go` |
| 7.2 | Add regression test for verbose token leakage | PROD-8 | Warning | `internal/confluence/client_test.go` |
| 7.3 | Replace verbose `fmt.Printf` with `log/slog` | PROD-13 | Suggestion | `cmd/root.go`, `internal/confluence/client.go` |

**Internal order:** 7.2 before 7.3 (7.3 removes the `verbose` field that 7.2 tests — the test should be written first and then updated when slog is added). 7.1 is independent.

**Verification:** `go test ./... -count=1`.

**Details:**
- **7.1:** Extract `runPushPreflight`, `runPushDryRun`, `popStashOnError` helpers. Target: reduce `runPush` from ~500 to ~150 lines.
- **7.2:** Test that verbose mode only logs method + URL, never API tokens or Authorization headers. Uses `os.Pipe` to capture stdout.
- **7.3:** Configure `slog.SetDefault` in `PersistentPreRunE` based on `--verbose`. Replace `if c.verbose { fmt.Printf(...) }` with `slog.Debug("http request", ...)`. Remove `verbose` field from Client/ClientConfig.

---

## Group 8: Tooling & CI

**Branch:** `chore/ci-tooling`
**Depends on:** Group 7 (all code finalized)
**Goal:** Automated quality gates.

| # | Task | Source | Priority | Files |
|---|------|--------|----------|-------|
| 8.1 | Add golangci-lint config and Makefile lint target | PROD-11 | Suggestion | `.golangci.yml` (new), `Makefile` |
| 8.2 | Add GitHub Actions CI pipeline | PROD-12 | Suggestion | `.github/workflows/ci.yml` (new) |

**Internal order:** 8.1 then 8.2 (CI uses lint config).

**Verification:** `make lint` runs cleanly. Push to GitHub and verify CI runs.

**Details:**
- **8.1:** Enable errcheck, staticcheck, govet, gosec, ineffassign, unused. Makefile lint target falls back to `go vet` if golangci-lint not installed.
- **8.2:** GitHub Actions workflow on push/PR to main: checkout, setup-go (from go.mod), vet, test with race detector, gofmt check, golangci-lint-action (continue-on-error).

---

## Summary: 8 Groups, 32 Tasks

| Group | Branch | Tasks | Priority | Depends On | Key Files |
|-------|--------|-------|----------|------------|-----------|
| 1 | `fix/foundation` | 3 | Critical | — | `pull.go`, `scripts/`, `go.mod` |
| 2 | `feat/http-resilience` | 5 | Critical/Warning | G1 | `client.go`, `types.go` |
| 3 | `feat/pull-hardening` | 8 | Critical/Warning | G1 | `pull.go`, `pull_test.go` |
| 4 | `feat/state-extensions` | 2 | Warning/High | G1 | `state.go`, `push.go`, `dry_run_remote.go` |
| 5 | `feat/push-folders` | 4 | High/Medium | G2, G4 | `push.go`, `push_test.go` |
| 6 | `feat/command-safety` | 6 | Warning/Medium | G1, G4 | `cmd/push.go`, `cmd/pull.go` |
| 7 | `refactor/code-quality` | 3 | Warning/Suggestion | G2-G6 | `cmd/push.go`, `client.go`, `root.go` |
| 8 | `chore/ci-tooling` | 2 | Suggestion | G7 | `.golangci.yml`, `ci.yml`, `Makefile` |

**Parallel execution lanes:**
- After G1: G2, G3, G4 can run simultaneously
- After G2+G4: G5 can start
- After G1+G4: G6 can start
- G7 waits for all feature/safety groups
- G8 is the final step

**Estimated PR count:** 8 PRs, merged in order.

---

## Cross-Plan Traceability

| Source Task | Group | Group Task |
|-------------|-------|------------|
| PROD-1 | G1 | 1.1 |
| PROD-2 | G1 | 1.2 |
| PROD-3 | G2 | 2.1 |
| PROD-4 | G1 | 1.3 |
| PROD-5 | G7 | 7.1 |
| PROD-6 | G6 | 6.2 |
| PROD-7 | G6 | 6.1 |
| PROD-8 | G7 | 7.2 |
| PROD-9 | G2 | 2.2 |
| PROD-10 | G2 | 2.3 |
| PROD-11 | G8 | 8.1 |
| PROD-12 | G8 | 8.2 |
| PROD-13 | G7 | 7.3 |
| PROD-14 | G2 | 2.4 |
| PULL-1 | G3 | 3.1 |
| PULL-2 | G3 | 3.2 |
| PULL-3 | G3 | 3.3 |
| PULL-4 | G6 | 6.3 |
| PULL-5 | G6 | 6.4 |
| PULL-6 | G3 | 3.4 |
| PULL-7 | G3 | 3.5 |
| PULL-8 | G4 (state) + G6 (cmd) | 4.1 + 6.5 |
| PULL-9 | — | (covered by PULL-5 / G6 task 6.4) |
| PULL-10 | G3 | 3.8 |
| PULL-11 | G3 | 3.6 |
| PULL-12 | G3 | 3.7 |
| PUSH-1 | G2 | 2.5 |
| PUSH-2 | G4 (state + interface) | 4.1 + 4.2 |
| PUSH-3 | G5 | 5.1 |
| PUSH-4 | G5 | 5.2 |
| PUSH-5 | G5 | 5.3 |
| PUSH-6 | G5 | 5.4 |
| PUSH-7 | G6 | 6.6 |
