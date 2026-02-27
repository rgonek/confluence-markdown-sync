# Test Coverage Extension Plan

## Goal
Increase test coverage for `internal/git` (currently 55.5%) and `cmd` (currently 65.2%) to exceed CI gates with a safer margin and ensure functional reliability.

## 1. `internal/git` Coverage Extension (Target: >70%)
Focus on unimplemented or low-coverage foundational Git operations.

- **`branch.go`**:
  - `CurrentBranch`: Test in a new repo (should return `main` or `master`) and after switching branches.
  - `Merge`: Test merging a feature branch into main with and without conflicts.
  - `DeleteBranch`: Test deleting an unmerged branch (should fail unless forced) and a merged branch.
- **`commit.go`**:
  - `AddForce`: Test adding a file that is explicitly ignored by `.gitignore`.
  - `Tag`: Test creating a lightweight tag and an annotated tag, then verifying they exist.
- **`git.go`**:
  - `NewClient`: Test initialization and validation of `RootDir`.
  - `RunGit`: Test execution of invalid git commands to exercise error reporting.
- **`stash.go`**:
  - `StashApply`: Test applying a specific stash ref.
  - `StashDrop`: Test dropping a specific stash ref.
  - `StashPop`: Test popping with and without conflicts.
- **`worktree.go`**:
  - `PruneWorktrees`: Test pruning stale worktree metadata.
  - `RemoveWorktree`: Test removing a worktree that is dirty (should require force or fail).

## 2. `cmd` Coverage Extension (Target: >75%)
Focus on end-to-end command execution and complex logic blocks.

- **`status` command (`status.go`, `status_run_test.go`)**:
  - Implement `TestRunStatus_Integration`: Setup a real Git repo, create local changes (staged, unstaged, untracked), mock `confluence.Client` to return matching/differing pages, and verify the output of `runStatus`.
- **`clean` command (`clean.go`, `clean_test.go`)**:
  - Implement `TestRunClean_Integration`: Create stale sync branches, worktrees, and snapshot refs. Run `clean` and verify they are removed.
- **`prune` command (`prune.go`)**:
  - Implement `TestRunPrune_Integration`: Create local markdown files that do not exist in a mocked Confluence space. Run `prune` and verify they are deleted.
- **`agents` command (`agents.go`, `agents_test.go`)**:
  - Implement `TestRunAgentsInit`: Run the command and verify that the expected `.md` files (Tech, HR, PM, etc.) are created with the correct templates.
- **Dry Run Simulator (`dry_run_remote.go`)**:
  - Exercise the simulator by running `push --dry-run` and `pull --dry-run` in integration tests, ensuring all simulator methods (like `ArchivePages`, `MovePage`) are touched.
- **Automation Helpers (`automation.go`)**:
  - Test `resolvePushConflictPolicy` with all policy variants.
  - Test `requireSafetyConfirmation` with different impact thresholds.

## 3. Implementation Strategy
1. **Infrastructure**: Enhance `cmd/helpers_test.go` and `internal/git/helpers_test.go` to provide a reusable `TestContext` that includes a real Git repo and a configurable mock Confluence client.
2. **Phase 1: Git Foundation**: Implement the `internal/git` tests first, as they are used by most `cmd` tests.
3. **Phase 2: CLI Commands**: Implement integration-style tests for `status`, `clean`, `prune`, and `agents`.
4. **Verification**: Run `go run ./tools/coveragecheck` after each phase to track progress.
