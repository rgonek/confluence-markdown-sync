# Pull And Validate Specification

## Purpose

Define the read-path sync contract: pulling remote content into Markdown and validating local content before any remote write.

## Requirements

### Requirement: Incremental pull planning

The system SHALL use the per-space watermark to plan incremental pulls, with a bounded overlap window for safety.

#### Scenario: Pull uses the stored watermark

- GIVEN `.confluence-state.json` contains `last_pull_high_watermark`
- WHEN the user runs `conf pull`
- THEN the system SHALL use that timestamp with an overlap window to identify potentially changed remote content

#### Scenario: Force pull bypasses incremental optimization

- GIVEN the user runs `conf pull <SPACE> --force`
- WHEN pull planning begins
- THEN the system SHALL refresh the full tracked space rather than relying on incremental change detection

### Requirement: Best-effort forward conversion

The system SHALL convert Confluence ADF to Markdown in best-effort mode for `pull` and `diff`.

#### Scenario: Unresolved same-space reference degrades instead of failing pull

- GIVEN a pulled page contains an unresolved reference during forward conversion
- WHEN `pull` or `diff` converts the page
- THEN the system SHALL preserve fallback output
- AND the system SHALL emit diagnostics instead of failing the whole run

### Requirement: Hierarchy-preserving page layout

The system SHALL map Confluence hierarchy into deterministic Markdown paths.

#### Scenario: Parent pages with children own a directory

- GIVEN a page has child pages
- WHEN pull plans local Markdown paths
- THEN the page SHALL be written as `<Page>/<Page>.md`

#### Scenario: Missing or cyclic ancestry falls back safely

- GIVEN a page's parent or folder ancestry cannot be resolved cleanly
- WHEN pull plans local paths
- THEN the system SHALL continue with a safe fallback path
- AND the system SHALL emit diagnostics describing the degraded hierarchy resolution

### Requirement: Link and attachment rewrite on pull

The system SHALL rewrite same-space references to local Markdown and asset paths whenever the local targets are known.

#### Scenario: Same-space page link becomes relative Markdown link

- GIVEN a Confluence page link points to another managed page in the same workspace
- WHEN pull converts the source page
- THEN the system SHALL rewrite the link to a relative Markdown path

#### Scenario: Attachment becomes local asset reference

- GIVEN a Confluence attachment is referenced from pulled content
- WHEN pull downloads the attachment
- THEN the system SHALL store it under `assets/<page-id>/<attachment-id>-<filename>`
- AND the converted Markdown SHALL point to the local relative asset path

### Requirement: Delete reconciliation

The system SHALL hard-delete tracked local files and assets removed remotely.

#### Scenario: Removed remote page is deleted locally

- GIVEN `page_path_index` tracks a page that no longer exists remotely
- WHEN pull reconciles tracked content
- THEN the system SHALL delete the corresponding local Markdown file

#### Scenario: Removed remote attachment is deleted locally

- GIVEN `attachment_index` tracks an attachment that is no longer referenced or no longer exists remotely
- WHEN pull reconciles tracked content
- THEN the system SHALL delete the corresponding local asset file

### Requirement: Pull workspace protection

The system SHALL protect dirty local workspace state while applying pull results.

#### Scenario: Dirty scope is stashed before pull

- GIVEN the target space scope has local changes
- WHEN `conf pull` begins
- THEN the system SHALL stash in-scope changes before mutating pulled files unless `--discard-local` is set

#### Scenario: Successful pull restores stashed workspace state

- GIVEN pull previously stashed local changes
- WHEN the pull completes successfully
- THEN the system SHALL reapply the stashed state
- AND the system SHALL repair pulled `version` metadata if the stash reintroduced an older value

### Requirement: Pull commit and tagging

The system SHALL create audit artifacts only for non-no-op pull runs.

#### Scenario: Pull with changes creates commit and tag

- GIVEN pull changes scoped Markdown or tracked assets
- WHEN the run finalizes
- THEN the system SHALL create a scoped commit
- AND the system SHALL create an annotated tag named `confluence-sync/pull/<space>/<timestamp>`

#### Scenario: No-op pull creates no audit tag

- GIVEN pull produces no scoped changes
- WHEN the run finalizes
- THEN the system SHALL not create a pull commit or sync tag

### Requirement: Strict validation before push

The system SHALL validate local Markdown with the same strict reverse-conversion profile used by push.

#### Scenario: Strict conversion failure blocks validation

- GIVEN a Markdown file contains an unresolved strict link or media reference
- WHEN `conf validate` runs
- THEN the system SHALL fail validation

#### Scenario: Duplicate page IDs block validation

- GIVEN two Markdown files in the same validation scope declare the same `id`
- WHEN `conf validate` builds the page index
- THEN the system SHALL fail validation

#### Scenario: Mermaid content produces warning, not failure

- GIVEN a Markdown file contains a Mermaid fenced code block
- WHEN `conf validate` runs
- THEN the system SHALL emit a warning indicating the content will be preserved as a code block on push
