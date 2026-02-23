# Confluence Page Status Support Plan

## 1. Overview
This document outlines the plan for supporting the `status` field in the Markdown frontmatter within the `conf` CLI, enabling users to create and manage unpublished drafts before making them live on Confluence.

## 2. Rationale & Constraints
The Confluence Cloud REST API v2 behaves in a specific and sometimes counter-intuitive way regarding page statuses:
- **Available Statuses:** The API uses statuses like `current`, `draft`, `archived`, `deleted`, `trashed`.
- **Publishing (`draft` -> `current`):** Creating a page with `"status": "draft"` creates an unpublished page. Changing it to `"status": "current"` publishes it.
- **"Unpublishing" (`current` -> `draft`):** The API **does not support unpublishing**. If you send `"status": "draft"` for a page that is already `current`, Confluence leaves the published page live and creates a hidden "draft edit" in the background.
- **Archiving:** Archiving is not supported via the v2 `PUT /pages/{id}` endpoint.

To prevent human confusion ("I set it to draft, why is it still live on the internet?") and AI hallucinations, `conf` will enforce a strict one-way transition constraint.

## 3. Data Model: Frontmatter
- Add `status` as a recognized optional key in the YAML frontmatter.
- Valid values: `current`, `draft`.
- If the `status` key is missing, it is implicitly treated as `current`.
- **Pull Behavior:** When running `conf pull`:
  - If the remote page is published (`current`), the `status` key will be omitted from the generated frontmatter to keep files clean.
  - If the remote page is a draft (or pulled via a specific draft fetch), it will explicitly include `status: draft`.

## 4. Mutability & Validation Rules
- `status` is **Mutable by user**, but with severe restrictions.
- **Allowed Transitions:** 
  - `missing` -> `current` (No-op)
  - `draft` -> `current` (Publishes the draft)
  - `draft` -> `draft` (Updates the draft)
- **Blocked Transitions:**
  - `current` -> `draft`: If a page is already published remotely (has an `id`), `conf validate` must fail with an error stating that Confluence does not support unpublishing pages.
- `conf validate` must verify that the value is either `current` or `draft`.

## 5. Sync Loop Updates

### `conf pull`
- Fetch only `current` status when listing pages in a space. (Confluence v2 API does not support `draft` in the space list filter).
- After listing, identify any locally tracked pages (from state) that are missing from the remote `current` list.
- For each missing page, explicitly fetch it by ID. 
  - If the page exists and is a `draft` (and belongs to the target space), include it in the pull result.
  - If the page exists and is `current` (e.g. was moved/published but missed in the list), include it.
  - If the page is not found (404), treat it as deleted.
- Ensure the `Pull` orchestration saves the correct status in the frontmatter (omitting if `current`).

### `conf push`
- When calling `remote.CreatePage` or `remote.UpdatePage`, read the `status` from the frontmatter (defaulting to `"current"` if missing).
- Ensure that creating a new placeholder page (for attachment IDs) uses the frontmatter's target status instead of hardcoding `"current"`.

## 6. Implementation Steps
1. **Update `internal/fs/frontmatter.go`:**
   - Add `Status string` to `Frontmatter` struct.
   - Update YAML unmarshaling/marshaling logic.
2. **Update `internal/fs/frontmatter.go` Validation:**
   - Add `ValidateFrontmatterSchema` checks to ensure `status` is only `current` or `draft`.
   - Update `ValidateImmutableFrontmatter` to block `current` -> `draft` transitions if `id` is present.
3. **Update `internal/sync/push.go`:**
   - Remove hardcoded `"status": "current"` strings.
   - Inject `doc.Frontmatter.Status` into `PageUpsertInput`.
4. **Update `internal/confluence/client.go`:**
   - Update `ListPages` to use `current` by default if no status is specified, and ensure it correctly passes the status parameter.
5. **Update `internal/sync/pull.go`:**
   - Modify `listAllPages` to only fetch `current`.
   - Add logic to "recover" draft pages that are known locally by fetching them individually.
6. **Add Tests:**
   - Add unit tests for the new frontmatter validation rules.
   - Add integration tests for creating a draft and then publishing it.
   - Add integration tests for pulling a space that contains both published pages and drafts.
