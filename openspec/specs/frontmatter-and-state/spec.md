# Frontmatter And State Specification

## Purpose

Define the Markdown metadata contract and per-space state model used by sync, validation, search, and recovery.

## Requirements

### Requirement: Reserved frontmatter schema

The system SHALL recognize and normalize the reserved frontmatter keys used by `conf`.

#### Scenario: Existing page frontmatter includes sync metadata

- GIVEN a Markdown file represents an existing remote page
- WHEN `conf` parses its frontmatter
- THEN the system SHALL recognize `title`, `id`, `version`, `state`, `status`, `labels`, `created_by`, `created_at`, `updated_by`, and `updated_at`

#### Scenario: New page frontmatter omits remote identity

- GIVEN a Markdown file represents a new page that has not been pushed
- WHEN `conf` parses its frontmatter
- THEN the system SHALL allow `id` to be empty
- AND the system SHALL not require `version` until a remote page identity exists

### Requirement: Space identity is not frontmatter

The system MUST derive space identity from workspace context and local state rather than from Markdown frontmatter.

#### Scenario: Markdown file has no `space` key

- GIVEN a managed Markdown file in a tracked space directory
- WHEN `conf` validates or pushes the file
- THEN the system SHALL resolve the space from directory and state context
- AND the system SHALL not require a `space` frontmatter field

#### Scenario: Legacy `confluence_space_key` metadata is present

- GIVEN a Markdown file contains legacy `confluence_space_key` metadata
- WHEN `conf` rewrites normalized frontmatter
- THEN the system SHALL not emit `space` or `confluence_space_key` as part of the canonical schema

### Requirement: Immutable and sync-managed metadata

The system SHALL enforce ownership rules for frontmatter fields.

#### Scenario: Immutable page ID edit is rejected

- GIVEN a file already tracks a page ID
- WHEN the local `id` differs from the tracked or baseline ID
- THEN `validate` SHALL fail

#### Scenario: Published page cannot be set back to draft

- GIVEN a tracked page previously synced as `current`
- WHEN a user changes `state` to `draft`
- THEN `validate` SHALL fail

#### Scenario: Sync-managed timestamps are preserved by sync

- GIVEN pull or push updates a tracked page
- WHEN frontmatter is rewritten
- THEN the system SHALL control `version`, `created_by`, `created_at`, `updated_by`, and `updated_at`

### Requirement: Label normalization

The system SHALL normalize labels deterministically.

#### Scenario: Labels are trimmed and normalized

- GIVEN frontmatter labels contain mixed case, duplicates, and surrounding whitespace
- WHEN the document is normalized for sync
- THEN the system SHALL lowercase, trim, deduplicate, and sort the labels

#### Scenario: Labels containing whitespace are invalid

- GIVEN a frontmatter label contains internal whitespace
- WHEN `validate` checks the schema
- THEN the system SHALL report a validation error

### Requirement: Unknown frontmatter preservation

The system SHALL preserve non-reserved frontmatter keys.

#### Scenario: Custom metadata survives normalization

- GIVEN a Markdown document contains custom frontmatter keys that do not collide with reserved sync keys
- WHEN `conf` reads and rewrites the document
- THEN the system SHALL preserve those custom keys

### Requirement: Per-space state file

The system SHALL keep local sync state in `.confluence-state.json` inside each managed space directory.

#### Scenario: State file tracks core indexes

- GIVEN a managed space directory
- WHEN `conf` loads or saves state
- THEN the state file SHALL support `space_key`, `last_pull_high_watermark`, `page_path_index`, `attachment_index`, and `folder_path_index`

#### Scenario: Missing state initializes cleanly

- GIVEN a managed space directory without `.confluence-state.json`
- WHEN a command loads state
- THEN the system SHALL treat the state as empty and initialized

#### Scenario: State file remains local-only

- GIVEN `conf init` or later workspace maintenance runs
- WHEN ignore rules are ensured
- THEN the system SHALL keep `.confluence-state.json` gitignored
