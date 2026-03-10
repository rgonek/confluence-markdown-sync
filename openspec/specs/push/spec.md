# Push Specification

## Purpose

Define the write-path sync contract for publishing local Markdown changes back to Confluence safely.

## Requirements

### Requirement: Push is always gated by validation

The system MUST complete validation successfully before any remote write during a real push.

#### Scenario: Validation failure aborts push

- GIVEN one or more changed files fail strict validation
- WHEN the user runs `conf push`
- THEN the system SHALL stop before any remote write occurs

### Requirement: Baseline-based change detection

The system SHALL compare local changes against the latest successful sync baseline for the space.

#### Scenario: Latest sync tag defines the baseline

- GIVEN the repository contains pull or push sync tags for a space
- WHEN push computes in-scope changes
- THEN the system SHALL use the latest timestamped sync tag for that space as the baseline

#### Scenario: No sync tags fall back to root commit

- GIVEN the repository has no prior sync tag for the space
- WHEN push computes its baseline
- THEN the system SHALL fall back to the repository root commit

### Requirement: Isolated push execution

The system SHALL isolate real push execution from the active user workspace.

#### Scenario: Real push creates snapshot ref, sync branch, and worktree

- GIVEN `conf push` detects in-scope changes
- WHEN a real push begins
- THEN the system SHALL create a snapshot ref at `refs/confluence-sync/snapshots/<space>/<timestamp>`
- AND the system SHALL create a sync branch `sync/<space>/<timestamp>`
- AND the system SHALL create a temporary worktree for the sync run

#### Scenario: Snapshot materialization tolerates long Windows workspace paths

- GIVEN `conf push` captured in-scope tracked and untracked workspace state in a snapshot ref
- AND the target workspace contains long nested Markdown or attachment paths on Windows
- WHEN push materializes the snapshot into the isolated worktree
- THEN the system SHALL restore the snapshot without relying on a Git path that replays untracked files through a failing long-path stash apply
- AND the system SHALL fail only if the snapshot cannot be restored through the long-path-safe restore path

#### Scenario: No-op push creates no recovery artifacts

- GIVEN push detects no in-scope Markdown changes
- WHEN the command exits
- THEN the system SHALL not create a snapshot ref, sync branch, merge commit, or sync tag

### Requirement: Conflict policy control

The system SHALL make remote-ahead conflict handling explicit.

#### Scenario: Space push defaults to pull-merge

- GIVEN the user runs a space-scoped push without `--on-conflict`
- WHEN push resolves the conflict policy
- THEN the system SHALL default to `pull-merge`

#### Scenario: File push requires an explicit policy or prompt

- GIVEN the user runs a single-file push
- WHEN `--on-conflict` is not supplied
- THEN the system SHALL require an interactive choice or fail in non-interactive mode

#### Scenario: Pull-merge conflict policy stops for review after pull

- GIVEN push detects a remote-ahead conflict
- AND the policy is `pull-merge`
- WHEN push handles the conflict
- THEN the system SHALL run pull for the target scope
- AND the system SHALL stop so the user can review and rerun push

#### Scenario: Pull-merge prints concrete non-interactive recovery guidance

- GIVEN push detects a remote-ahead conflict
- AND the policy is `pull-merge`
- WHEN the automatic pull stops with unresolved file conflicts or preserved local edits
- THEN the system SHALL state that the local edits were preserved
- AND the system SHALL print explicit next steps to resolve files, stage them, and rerun push

#### Scenario: Pull-merge never silently discards local edits

- GIVEN push detects a remote-ahead conflict
- AND the policy is `pull-merge`
- AND the target scope contains unpushed local edits
- WHEN push handles the conflict
- THEN the system SHALL preserve the local edits via a clean merge, conflict markers, or explicit recoverable state
- AND the system SHALL not silently discard local edits

### Requirement: Strict remote publishing

The system SHALL publish Markdown to Confluence using strict conversion and explicit attachment/link resolution.

#### Scenario: Push creates or updates remote content

- GIVEN a changed Markdown file validates successfully
- WHEN push processes the file
- THEN the system SHALL resolve page identity, links, and attachments
- AND the system SHALL create or update remote content as required

#### Scenario: Removing a tracked Markdown page archives the remote page

- GIVEN a tracked Markdown page is removed locally and is in push scope
- WHEN push applies the deletion
- THEN the system SHALL archive the corresponding remote page
- AND the archived page SHALL be treated as removed from tracked local state after reconciliation

#### Scenario: Archive timeout is verified before classifying the delete as failed

- GIVEN a push archives a tracked remote page
- AND Confluence long-task polling times out or returns an inconclusive in-progress result
- WHEN push evaluates the delete outcome
- THEN the system SHALL perform a follow-up verification read before classifying the operation as failed
- AND the operator diagnostics SHALL distinguish "still running remotely" from a definite failure

#### Scenario: Removing tracked attachments deletes remote attachments

- GIVEN a push would remove tracked remote attachments
- WHEN push reconciles attachments
- THEN the system SHALL delete those remote attachments unless `--keep-orphan-assets` suppresses the deletion

#### Scenario: Orphan attachment deletion can be suppressed

- GIVEN a push would otherwise delete unreferenced remote attachments
- WHEN the user passes `--keep-orphan-assets`
- THEN the system SHALL keep those orphaned attachments

### Requirement: Preflight and dry-run inspection

The system SHALL provide safe non-write inspection modes for push.

#### Scenario: Preflight returns a push plan without writes

- GIVEN the user runs `conf push --preflight`
- WHEN the command evaluates the target scope
- THEN the system SHALL show the planned changes and validation outcome
- AND the system SHALL not modify remote content or local Git state

#### Scenario: Preflight uses the same validation scope as real push

- GIVEN the target scope contains a validation failure, including one introduced by planned deletions outside the directly changed file set
- WHEN the user runs `conf push --preflight`
- THEN the system SHALL surface the same validation failure a real push would surface before any remote write

#### Scenario: Space-scoped push validates the full space target

- GIVEN a space-scoped push has one or more in-scope Markdown changes
- WHEN the system performs preflight, dry-run, or real push validation
- THEN the system SHALL validate the full target space with the same strict profile before any remote write

#### Scenario: Content-status metadata is preflighted before write-path mutation

- GIVEN a push target contains frontmatter `status`
- WHEN push completes preflight for remote metadata writes
- THEN the system SHALL resolve or reject the target content-status value before creating or mutating remote page content

#### Scenario: Dry-run simulates remote work without mutation

- GIVEN the user runs `conf push --dry-run`
- WHEN the command simulates the push
- THEN the system SHALL evaluate conversion and planned remote actions
- AND the system SHALL not modify remote content or local Git state

#### Scenario: Preflight and dry-run cannot be combined

- GIVEN the user passes both `--preflight` and `--dry-run`
- WHEN the command validates flags
- THEN the system SHALL fail

### Requirement: Per-page Git audit trail

The system SHALL preserve per-page audit detail within each successful push run.

#### Scenario: Successful push creates per-page commits with trailers

- GIVEN push successfully syncs one or more pages
- WHEN the worktree finalizes commits
- THEN the system SHALL create one commit per pushed page
- AND each commit SHALL include `Confluence-Page-ID`, `Confluence-Version`, `Confluence-Space-Key`, and `Confluence-URL` trailers

#### Scenario: Successful non-no-op push creates sync tag

- GIVEN push successfully merges the sync branch
- WHEN the run finalizes
- THEN the system SHALL create an annotated tag named `confluence-sync/push/<space>/<timestamp>`

### Requirement: Failure retention and recovery metadata

The system SHALL retain enough information to inspect and clean up failed push runs later.

#### Scenario: Failed push retains recovery artifacts

- GIVEN a real push fails after snapshot creation
- WHEN the command exits
- THEN the system SHALL retain the snapshot ref and sync branch
- AND the system SHALL record recovery metadata under `.git/confluence-recovery/`

#### Scenario: Failed push prints concrete recovery commands

- GIVEN a real push fails after snapshot creation
- WHEN the command exits with retained recovery artifacts
- THEN the system SHALL print the retained snapshot ref and sync branch
- AND the system SHALL print concrete next-step commands for `conf recover`
- AND the system SHALL print a concrete branch-inspection command
- AND the system SHALL print a concrete cleanup command for the retained recovery run

#### Scenario: Successful push cleans recovery artifacts

- GIVEN a real push completes successfully
- WHEN the command exits
- THEN the system SHALL delete temporary recovery metadata and cleanup the worktree
