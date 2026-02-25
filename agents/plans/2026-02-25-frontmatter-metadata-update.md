# Frontmatter Metadata Standardization Plan

## Objective
Standardize the naming convention of frontmatter properties in `confluence-markdown-sync` to strictly reflect system API metadata. This ensures perfect symmetry and intuitively separates system-generated metadata from user-editable content.

We are adopting the `_by` and `_at` matrix:
* **Creation:** `created_by` / `created_at`
* **Modification:** `updated_by` / `updated_at`

*Note: Since the tool is not yet broadly used, backward compatibility (migrating old properties) is not required.*

## Target Changes

### 1. Update Core Structs (`internal/fs/frontmatter.go`)
Update the `Frontmatter` and `frontmatterYAML` structs:
- `Author` -> `CreatedBy` (`yaml:"created_by,omitempty"`)
- `LastModifiedBy` -> `UpdatedBy` (`yaml:"updated_by,omitempty"`)
- `LastModifiedAt` -> `UpdatedAt` (`yaml:"updated_at,omitempty"`)
- `CreatedAt` -> Remains unchanged (`yaml:"created_at,omitempty"`)

### 2. Update YAML Marshaling/Unmarshaling (`internal/fs/frontmatter.go`)
- Update `MarshalYAML`'s switch statement to filter out the new keys (`created_by`, `updated_by`, `updated_at`) from the `Extra` map.
- Update `UnmarshalYAML` to correctly map the new YAML fields into the `Frontmatter` struct.

### 3. Update Sync Logic (`internal/sync/` packages)
- Find where Confluence page history is mapped to frontmatter properties (likely in `pull.go` or a `mapper.go`).
- Change assignments:
  - Map `page.History.CreatedBy.DisplayName` to `CreatedBy`.
  - Map `page.History.CreatedDate` to `CreatedAt`.
  - Map `page.History.LastUpdated.By.DisplayName` to `UpdatedBy`.
  - Map `page.History.LastUpdated.When` to `UpdatedAt`.

### 4. Update Tests & Test Data
- `internal/fs/frontmatter_test.go`: Update mock YAML frontmatter strings to use the new field names.
- `internal/sync/*_test.go`: Update any tests validating the frontmatter metadata.
- Any golden files in `testdata/` folders that include `author`, `last_modified_by`, or `last_modified_at`.

### 5. Update Documentation
- Check `README.md` for references to `author` or `last_modified_*` and update to `created_by`, `updated_by`, etc.
- Check `agents/plans/confluence_sync_cli.md` for any mentions of these frontmatter properties.

## Execution Steps
1. Run a global `grep` across the repository for `Author`, `author:`, `LastModifiedBy`, `last_modified_by`, `LastModifiedAt`, and `last_modified_at`.
2. Apply the renames in Go files using structural search and replace or direct edits.
3. Update `.md` files in `testdata` if applicable.
4. Run `make test` locally to ensure no compilation errors or test failures.
5. Commit the changes.

## Implementation Progress

- [x] Searched the repository for legacy metadata names and YAML keys.
- [x] Renamed frontmatter fields and YAML tags in `internal/fs/frontmatter.go` to `created_by` / `created_at` / `updated_by` / `updated_at`.
- [x] Updated pull mapping to populate `CreatedBy`, `CreatedAt`, `UpdatedBy`, and `UpdatedAt`.
- [x] Updated tests in `internal/fs/frontmatter_test.go` and `cmd/pull_test.go`.
- [x] Updated docs metadata key reference in `docs/usage.md`.
- [x] Verified no remaining references in `README.md` or `agents/plans/confluence_sync_cli.md` required edits for legacy metadata keys.
