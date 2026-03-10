# Recovery And Maintenance Specification

## Purpose

Define the operational commands used to inspect, repair, and clean the local sync workspace after failures or drift.

## Requirements

### Requirement: Recover lists retained failed-push artifacts

The system SHALL let operators inspect retained push recovery artifacts without mutating them by default.

#### Scenario: Recover lists snapshot refs and sync branches

- GIVEN failed push artifacts exist in the repository
- WHEN the user runs `conf recover`
- THEN the system SHALL list retained sync branches, snapshot refs, and any recorded failure metadata

#### Scenario: Recover inspection includes suggested next commands

- GIVEN failed push artifacts exist in the repository
- WHEN the user runs `conf recover`
- THEN the system SHALL show a concrete inspect command for each retained run
- AND the system SHALL show a concrete discard command for each retained run
- AND the system SHALL show the general `conf recover --discard-all --yes` cleanup command

### Requirement: Doctor surfaces active workspace sync locks

The system SHALL let operators inspect leftover repository sync locks that can block new mutating commands.

#### Scenario: Doctor reports a stale workspace sync lock

- GIVEN a managed repository contains an abandoned sync lock from a prior `pull` or `push`
- WHEN the user runs `conf doctor`
- THEN the system SHALL report the stale lock as an operational issue

### Requirement: Recover only discards safe artifacts

The system SHALL prevent accidental deletion of active recovery state.

#### Scenario: Current recovery branch is protected

- GIVEN the current `HEAD` is on a retained sync branch
- WHEN the user tries to discard that recovery run
- THEN the system SHALL refuse to discard it

#### Scenario: Linked worktree blocks discard

- GIVEN a retained recovery run still has an active linked worktree
- WHEN the user tries to discard that run
- THEN the system SHALL retain it and explain why it is blocked

### Requirement: Clean removes stale managed artifacts

The system SHALL remove abandoned managed sync artifacts that are safe to delete.

#### Scenario: Clean removes stale snapshot refs and worktrees

- GIVEN the repository contains stale managed worktrees or stale snapshot refs
- WHEN the user runs `conf clean`
- THEN the system SHALL remove the stale artifacts that are safe to clean

### Requirement: Doctor detects local consistency issues

The system SHALL inspect the relationship between `.confluence-state.json`, Markdown files, and Git workspace state.

#### Scenario: Doctor reports missing tracked file

- GIVEN `page_path_index` tracks a page whose Markdown file no longer exists
- WHEN the user runs `conf doctor`
- THEN the system SHALL report a missing-file issue

#### Scenario: Doctor reports unresolved conflict markers

- GIVEN a tracked Markdown file contains Git conflict markers
- WHEN the user runs `conf doctor`
- THEN the system SHALL report a conflict-marker issue

#### Scenario: Doctor reports hierarchy layout problem

- GIVEN a parent page has nested child Markdown under a directory layout that violates the page-with-children rule
- WHEN the user runs `conf doctor`
- THEN the system SHALL report a hierarchy layout issue

### Requirement: Doctor repairs repairable issues only

The system SHALL support conservative automatic repair for issues that can be fixed safely.

#### Scenario: Doctor repairs stale state entry

- GIVEN `doctor` finds a missing-file state entry that is safe to remove
- WHEN the user runs `conf doctor --repair`
- THEN the system SHALL remove the stale state entry

#### Scenario: Doctor does not auto-repair ambiguous content issues

- GIVEN `doctor` finds an ID mismatch or unresolved conflict markers
- WHEN the user runs `conf doctor --repair`
- THEN the system SHALL leave those issues for manual resolution

### Requirement: Prune deletes orphaned local assets safely

The system SHALL remove only local assets that are no longer referenced by any Markdown file in the space.

#### Scenario: Prune lists orphaned assets before deletion

- GIVEN orphaned local assets exist under `assets/`
- WHEN the user runs `conf prune`
- THEN the system SHALL list the orphaned assets before deletion

#### Scenario: Prune requires approval

- GIVEN `conf prune` would delete orphaned assets
- WHEN the user has not passed `--yes`
- THEN the system SHALL require confirmation or fail in non-interactive mode
