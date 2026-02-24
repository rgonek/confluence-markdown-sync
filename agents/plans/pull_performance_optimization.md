# Confluence Space Pull Performance Optimization Plan

## Problem Statement
Currently, pulling a large Confluence space can take up to an hour. The operation is extremely slow because the `conf pull` orchestration operates entirely sequentially in a single thread. For a space with 1000 pages and 500 attachments, this results in at least 1500 individual HTTP requests, where each request must wait for the previous one to complete, suffering the full penalty of network latency every time.

## Objective
Reduce the execution time of `conf pull` significantly by parallelizing independent operations while ensuring we remain within Atlassian's rate limits and maintain existing stability.

## Planned Improvements

### 1. Introduce Concurrency (Worker Pools)
We will introduce `golang.org/x/sync/errgroup` to execute independent tasks concurrently, significantly reducing wait times. We will implement bounded concurrency (e.g., 5-10 workers) to avoid exhausting local resources (sockets/files) and immediately triggering remote rate limits.

The following sequential loops in `internal/sync/pull.go` will be refactored to run concurrently:
*   **Folder Metadata Resolution:** Querying `remote.GetFolder` for missing parent folders.
*   **Page Fetching:** Fetching page bodies (`remote.GetPage`) for identified changed pages.
*   **Asset Downloading:** Downloading attachments (`remote.DownloadAttachment`) for referenced media.
*   **Markdown Conversion & Saving:** Converting ADF to Markdown and writing to disk.

### 2. Add API Rate-Limit Resiliency (HTTP 429)
Parallelizing requests increases the likelihood of hitting Atlassian Confluence API rate limits (`429 Too Many Requests`).
*   Modify `internal/confluence/client.go` to intercept `429` responses.
*   Respect the `Retry-After` header provided by Atlassian.
*   Implement automatic backoff and retry for requests instead of failing the pull process outright.

### 3. Concurrency-Safe State Management
Because the current loops build and modify maps sequentially (e.g., `changedPages`, `diagnostics`), these need to be protected or structured safely to avoid race conditions:
*   Use `sync.Mutex` where shared state is updated by concurrent workers (e.g., aggregating diagnostics).
*   Pre-allocate slices/maps and have workers write to isolated indices to avoid mutex contention where possible.

### 4. Progress Reporting Updates
Ensure `schollz/progressbar` calls are thread-safe and updates accurately reflect the progress of concurrent operations.

## Implementation Steps

1.  **Add Dependencies**: Run `go get golang.org/x/sync/errgroup`.
2.  **Update Confluence Client (`internal/confluence/client.go`)**:
    *   Modify `do(req *http.Request, out any)` or create a wrapper to handle `http.StatusTooManyRequests` (429).
    *   Read `Retry-After` header. If present, sleep for the requested duration. If absent, use an exponential backoff.
    *   Add a maximum retry limit (e.g., 3-5 times).
3.  **Refactor `internal/sync/pull.go` for Concurrency**:
    *   Define a default concurrency limit constant (e.g., `const pullConcurrency = 10`).
    *   Update `resolveFolderHierarchyFromPages` to use `errgroup`.
    *   Update the page fetching loop (filling `changedPages`).
    *   Update the asset downloading loop.
    *   Update the markdown conversion and writing loop.
    *   Safeguard `diagnostics` appending with a mutex.
4.  **Testing**:
    *   Run `make test` to ensure existing invariants hold.
    *   Execute an E2E test against a large dummy space to verify performance gains and rate-limit handling.
