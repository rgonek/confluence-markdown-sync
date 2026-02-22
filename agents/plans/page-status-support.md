# Confluence Page Status Support Plan

## 1. Overview
This document outlines the plan for supporting the `status` field in the Markdown frontmatter within the `cms` CLI, enabling users to create and manage unpublished drafts before making them live on Confluence.

## 2. Rationale & Constraints
The Confluence Cloud REST API v2 behaves in a specific and sometimes counter-intuitive way regarding page statuses:
- **Available Statuses:** The API uses statuses like `current`, `draft`, `archived`, `deleted`, `trashed`.
- **Publishing (`draft` -> `current`):** Creating a page with `"status": "draft"` creates an unpublished page. Changing it to `"status": "current"` publishes it.
- **"Unpublishing" (`current` -> `draft`):** The API **does not support unpublishing**. If you send `"status": "draft"` for a page that is already `current`, Confluence leaves the published page live and creates a hidden "draft edit" in the background.
- **Archiving:** Archiving is not supported via the v2 `PUT /pages/{id}` endpoint.

To prevent human confusion ("I set it to draft, why is it still live on the internet?") and AI hallucinations, `cms` will enforce a strict one-way transition constraint.

## 3. Data Model: Frontmatter
- Add `status` as a recognized optional key in the YAML frontmatter.
- Valid values: `current`, `draft`.
- If the `status` key is missing, it is implicitly treated as `current`.
- **Pull Behavior:** When running `cms pull`:
  - If the remote page is published (`current`), the `status` key will be omitted from the generated frontmatter to keep files clean.
  - If the remote page is a draft (or pulled via a specific draft fetch), it will explicitly include `status: draft`.

## 4. Mutability & Validation Rules
- `status` is **Mutable by user**, but with severe restrictions.
- **Allowed Transitions:** 
  - `missing` -> `current` (No-op)
  - `draft` -> `current` (Publishes the draft)
  - `draft` -> `draft` (Updates the draft)
- **Blocked Transitions:**
  - `current` -> `draft`: If a page is already published remotely (has an `id`), `cms validate` must fail with an error stating that Confluence does not support unpublishing pages.
- `cms validate` must verify that the value is either `current` or `draft`.

## 5. Sync Loop Updates

### `cms pull`
- Ensure the API client fetches both `current` and `draft` statuses when listing pages in a space. (Currently, the API defaults to `current` and `archived` if not specified).
- Ensure the `Pull` orchestration saves the correct status in the frontmatter (omitting if `current`).

### `cms push`
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
   - Modify `ListPages` to ensure it requests drafts if appropriate, or ensure the sync loop correctly identifies draft pages.
5. **Add Tests:**
   - Add unit tests for the new frontmatter validation rules.
   - Add integration tests for creating a draft and then publishing it.