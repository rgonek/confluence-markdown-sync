# Project Improvements and Fixes

Based on the project analysis, the `confluence-markdown-sync` CLI is structurally robust and highly production-ready, featuring defensive Git workflows, a resilient API client, cross-platform pathing, and rigorous testing. 

The following improvements and fixes have been identified and should be addressed to further elevate the project's quality.

## Progress
- [x] 1. Add Version Injection & Command
- [x] 2. Dynamic Media Type Detection
- [x] 3. Windows CI Testing Matrix
- [ ] 4. Cross-Platform Makefile
- [ ] 5. Test Performance Optimization (cmd package)

## 1. Add Version Injection & Command
**Problem:** The CLI currently lacks a `conf version` command or a `--version` flag, which makes it difficult to verify the deployed or installed version.
**Solution:** 
- Add a `Version` variable to `cmd/root.go` (or a dedicated `cmd/version.go`).
- Expose it via a `conf version` command and/or `--version` flag.
- Inject the version at build time in `.github/workflows/release.yml` using Go linker flags: `-ldflags "-X github.com/rgonek/confluence-markdown-sync/cmd.Version=${{ github.ref_name }}"`.

## 2. Dynamic Media Type Detection
**Problem:** In `internal/sync/hooks.go` (line 247), attachments are currently hardcoded as `"image"` with an explicit `TODO: Detect type based on extension if needed`.
**Solution:** 
- Confluence ADF expects a `"file"` macro (instead of `"image"`) for non-image assets like `.pdf` or `.zip`.
- Implement a helper function utilizing `strings.ToLower(filepath.Ext(destination))` to return `"image"` for standard image extensions (`.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, etc.) and `"file"` for all other file types.

## 3. Windows CI Testing Matrix
**Problem:** `.github/workflows/ci.yml` only runs tests on `ubuntu-latest`. Given the CLI heavily handles file system synchronization, path resolution, and `\` vs `/` differences across operating systems, a lack of automated Windows testing introduces a significant risk vector.
**Solution:**
- Introduce an OS matrix in the `test` job of `.github/workflows/ci.yml` to run tests on both `ubuntu-latest` and `windows-latest`.

## 4. Cross-Platform Makefile
**Problem:** The `Makefile` uses Windows-specific syntax for its `clean` target:
```makefile
@if exist $(BINARY) del /f $(BINARY)
@if exist $(BINARY).exe del /f $(BINARY).exe
```
This prevents `make clean` from successfully executing on Linux/macOS environments.
**Solution:** 
- Replace this with standard POSIX commands (e.g., `rm -f $(BINARY) $(BINARY).exe`) since `make` on Windows usually runs within Git Bash, MSYS2, or similar environments providing POSIX utils. Alternatively, define an OS-aware conditional clean command in the Makefile.

## 5. Test Performance Optimization (cmd package)
**Problem:** Running `go test ./cmd` takes over 70 seconds locally due to heavy, sequential disk I/O (spawning multiple Git worktrees, state files, and generating stashes).
**Solution:**
- Introduce `t.Parallel()` inside the top-level tests of `cmd/push_test.go`, `cmd/pull_test.go`, and others.
- Ensure that each parallelized test provisions its own strictly isolated temporary directory for Git operations to prevent race conditions during execution.
