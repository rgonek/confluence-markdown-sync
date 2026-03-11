# Workspace Specification

## Purpose

Define the local workspace contract for `conf`, including initialization, target resolution, directory layout, and safety prompts.

## Requirements

### Requirement: Workspace bootstrap

The system SHALL initialize a usable local `conf` workspace with local Git, ignore rules, credential scaffolding, and helper docs.

#### Scenario: `conf init` bootstraps a new repository

- GIVEN the current directory is not already a Git repository
- WHEN the user runs `conf init`
- THEN the system SHALL initialize Git on branch `main`
- AND the system SHALL create or update `.gitignore`
- AND the system SHALL create `.env` scaffolding when credentials are not already persisted
- AND the system SHALL create helper docs such as `README.md` and `AGENTS.md` when missing

#### Scenario: `conf init` reuses an existing repository

- GIVEN the current directory is already a Git repository
- WHEN the user runs `conf init`
- THEN the system SHALL preserve the current Git repository
- AND the system SHALL update workspace support files in place without reinitializing history

### Requirement: Target parsing

The system SHALL resolve command targets consistently across commands that accept `[TARGET]`.

#### Scenario: Markdown path selects file mode

- GIVEN a command argument that ends with `.md`
- WHEN `conf` parses `[TARGET]`
- THEN the system SHALL treat the target as a single Markdown file

#### Scenario: Non-Markdown target selects space mode

- GIVEN a command argument that does not end with `.md`
- WHEN `conf` parses `[TARGET]`
- THEN the system SHALL treat the target as a space target

#### Scenario: Omitted target uses current directory context

- GIVEN the user omits `[TARGET]`
- WHEN a target-aware command runs inside a managed space directory
- THEN the system SHALL infer the space from the current directory and state context

### Requirement: Space directory layout

The system SHALL maintain one directory per managed Confluence space.

#### Scenario: New space directory is created from remote metadata

- GIVEN `pull` resolves a space that is not already tracked locally
- WHEN the command materializes the local space directory
- THEN the system SHALL use a sanitized `Name (KEY)` directory name

#### Scenario: Existing tracked directory is reused

- GIVEN a space is already tracked locally
- WHEN `pull`, `push`, `validate`, or `status` run for that space
- THEN the system SHALL reuse the tracked directory rather than renaming it opportunistically

### Requirement: Safety confirmation

The system SHALL require explicit confirmation before large or destructive operations proceed.

#### Scenario: Large operation requires confirmation

- GIVEN a `pull` or `push` run affects more than 10 Markdown files
- WHEN the run reaches the execution gate
- THEN the system SHALL ask for confirmation unless `--yes` is set

#### Scenario: Delete operation requires confirmation

- GIVEN a `pull`, `push`, `prune`, or destructive recovery flow includes deletes
- WHEN the run reaches the execution gate
- THEN the system SHALL ask for confirmation unless `--yes` is set

#### Scenario: Non-interactive mode fails fast

- GIVEN confirmation is required
- AND the user passes `--non-interactive` without `--yes`
- WHEN the command reaches the confirmation gate
- THEN the system SHALL fail instead of prompting

### Requirement: Local-only Git operation

The system SHALL work without requiring a Git remote.

#### Scenario: Local repository without remote still supports sync

- GIVEN the workspace is a local Git repository without any configured remote
- WHEN the user runs `pull`, `validate`, `diff`, `push`, `status`, `doctor`, or `recover`
- THEN the system SHALL rely on local Git state only
- AND the system SHALL not require `git fetch`, `git pull`, or `git push`

### Requirement: Mutating workspace commands are serialized per repository

The system SHALL prevent concurrent mutating sync commands from operating on the same local repository at the same time.

#### Scenario: Second mutating command fails fast while a sync lock is held

- GIVEN another `pull` or `push` command is already mutating the same repository
- WHEN a second `pull` or `push` command starts
- THEN the system SHALL fail fast with a clear lock/conflict error
- AND the system SHALL not proceed far enough to trigger incidental Git index or filesystem corruption errors
