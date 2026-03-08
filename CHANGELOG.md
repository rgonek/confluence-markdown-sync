# Changelog

All notable changes to `conf` are documented here. This changelog focuses on
user-visible sync semantics — hierarchy rules, attachment handling, validation
strictness, and cleanup/recovery behavior.

## [Unreleased]

### Added
- Push preflight (`--preflight`) now probes remote capabilities and reports
  degraded modes (content-status, folder API) before execution.
- Push preflight shows exact planned page and attachment mutations.
- Generated AGENTS.md uses a single unified workflow instead of split
  human-in-the-loop / autonomous modes.
- Recovery artifact inspection via `conf recover` command.
- No-op commands now explain why nothing changed.
- Destructive operation previews show exact pages/attachments targeted.
- Feature/tenant compatibility matrix in documentation (`docs/compatibility.md`).

### Changed
- Generated AGENTS.md frontmatter guidance no longer lists `space` as an
  immutable key (it was removed from frontmatter).
- README includes beta maturity notice.

### Fixed
- (none yet)

### Removed
- (none yet)

## Sync Semantics Change Tracking

Changes in the following categories are always noted explicitly:

- **Hierarchy rules**: How pages are organized in directories, folder vs page
  parent handling, path move detection.
- **Attachment handling**: Upload/delete/preserve logic, orphan asset cleanup,
  asset path conventions.
- **Validation strictness**: What `validate` and `push` reject, Mermaid
  warnings, frontmatter schema enforcement.
- **Cleanup/recovery semantics**: Sync branch lifecycle, snapshot ref
  retention, recovery metadata, `clean` behavior.
