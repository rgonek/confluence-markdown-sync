# Compatibility Specification

## Purpose

Define the supported capability matrix and degraded fallback behavior when Confluence tenant features or content types are only partially supported.

## Requirements

### Requirement: Folder API fallback

The system SHALL degrade safely when the Confluence Folder API is unavailable.

#### Scenario: Pull falls back to page-based hierarchy

- GIVEN a tenant does not support the folder lookup API required for hierarchy resolution
- WHEN `pull` or `diff` resolves page paths
- THEN the system SHALL continue with page-based hierarchy fallback
- AND the system SHALL emit compatibility diagnostics

#### Scenario: Push skips folder-specific behavior when unsupported

- GIVEN a tenant does not support folder operations needed for hierarchy writes
- WHEN `push` resolves remote hierarchy
- THEN the system SHALL fall back to page-based hierarchy behavior
- AND the system SHALL emit compatibility diagnostics

### Requirement: Content status API fallback

The system SHALL keep syncing page content even when the tenant does not support content-status operations.

#### Scenario: Content-status sync is disabled for unsupported tenants

- GIVEN a tenant returns compatibility probe errors for content-status operations
- WHEN `pull` or `push` handles content-status metadata
- THEN the system SHALL disable content-status syncing for that run
- AND the system SHALL continue syncing page content and labels

### Requirement: PlantUML first-class support

The system SHALL treat PlantUML as the only first-class rendered extension handler currently implemented.

#### Scenario: PlantUML round-trips as a managed extension

- GIVEN page content contains a supported `plantumlcloud` extension
- WHEN `pull`, `diff`, `validate`, or `push` process that content
- THEN the system SHALL round-trip it through the managed PlantUML handler

### Requirement: Mermaid preserved as code

The system SHALL preserve Mermaid content without claiming rendered Confluence macro support.

#### Scenario: Mermaid fence warns before push

- GIVEN a Markdown document contains a Mermaid fenced code block
- WHEN `validate` or `push` process the document
- THEN the system SHALL warn that the content will be preserved as a Confluence code block rather than a rendered Mermaid macro

### Requirement: Raw ADF preservation is best-effort only

The system SHALL treat raw `adf:extension` preservation as a low-level escape hatch rather than a guaranteed authoring contract.

#### Scenario: Unhandled extension node is preserved best-effort

- GIVEN pulled content contains an extension node without a repo-specific handler
- WHEN forward conversion preserves it as raw `adf:extension` content
- THEN the system SHALL treat that path as best-effort preservation only

### Requirement: Unknown macros are not first-class authoring targets

The system SHALL not promise round-trip support for unknown Confluence macros or app-specific extensions.

#### Scenario: Unknown macro may still fail on push

- GIVEN content depends on an unknown or app-specific Confluence macro
- WHEN the user pushes the content
- THEN the system SHALL not guarantee success even if the content survived a prior pull in raw ADF form
