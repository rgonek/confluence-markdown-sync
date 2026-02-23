# Plan: Confluence Content Status and Labels Integration

## Overview
Currently, the `confluence-markdown-sync` tool syncs the Confluence document lifecycle state (`current` vs `draft`) via the `status` field in the Markdown frontmatter.

This plan outlines the migration of this field to `state`, and the introduction of two new features:
1.  **Content Status (`status`):** Syncing the visual lozenge in the Confluence UI (e.g., "Rough", "In Progress", "Ready to review").
2.  **Labels (`labels`):** Syncing Confluence page labels.

## 1. Frontmatter Schema Changes

The new Markdown frontmatter schema will be:

```yaml
---
id: "12345"
space: "ENG"
version: 2
state: current                  # Was previously `status`. Valid values: 'current', 'draft'. Omitted defaults to 'current'.
status: "Ready to review"       # NEW: Maps to Confluence "Content Status" (visual lozenge)
labels:                         # NEW: Maps to Confluence Page Labels
  - architecture
  - backend
---
```

### Migration Path for `state`
No backward compatibility is required. We will rename `status` to `state` directly in the code, updating all structs, validation logic, and test fixtures. Existing markdown files will need to be manually updated by the user, or will simply fail validation until corrected.

## 2. API Client Additions (`internal/confluence/client.go`)

Based on our exploration of the Atlassian APIs, these features require using the Confluence REST API v1, as the v2 API does not support mutating labels or content statuses.

### Content Status (v1 API)
We will add new methods to `confluence.Client`:
*   `GetContentStatus(ctx context.Context, pageID string) (ContentStatus, error)`
    *   Endpoint: `GET /wiki/rest/api/content/{id}/state`
*   `SetContentStatus(ctx context.Context, pageID string, statusName string) error`
    *   Endpoint: `PUT /wiki/rest/api/content/{id}/state`
    *   Payload: `{"name": statusName}` (Confluence will automatically assign the default color for space-defined statuses).
*   `DeleteContentStatus(ctx context.Context, pageID string) error`
    *   Endpoint: `DELETE /wiki/rest/api/content/{id}/state`

### Labels (v1 API)
We will add new methods to `confluence.Client`:
*   `GetLabels(ctx context.Context, pageID string) ([]string, error)`
    *   Endpoint: `GET /wiki/rest/api/content/{id}/label`
*   `AddLabels(ctx context.Context, pageID string, labels []string) error`
    *   Endpoint: `POST /wiki/rest/api/content/{id}/label`
    *   Payload: `[{"prefix": "global", "name": "label1"}, ...]`
*   `RemoveLabel(ctx context.Context, pageID string, labelName string) error`
    *   Endpoint: `DELETE /wiki/rest/api/content/{id}/label?name={name}`

*Note: We should update the `Page` struct returned by `GetPage` to include these fields if possible by requesting `expand=metadata.labels` (v1) or fetching them concurrently alongside the page body (v2).*

## 3. Sync Logic Updates

### `conf pull` (`internal/sync/pull.go`)
1.  When fetching changed pages, concurrently fetch their Content Status and Labels using the new API methods.
2.  Populate these fields in the `fs.MarkdownDocument` before writing to disk.

### `conf push` (`internal/sync/push.go`)
1.  **Labels Sync:**
    *   Fetch existing labels from Confluence.
    *   Calculate the diff (labels to add, labels to remove) based on the frontmatter `labels` array.
    *   Call `AddLabels` and `RemoveLabel` as needed.
2.  **Content Status Sync:**
    *   If the frontmatter `status` is empty, call `DeleteContentStatus`.
    *   If the frontmatter `status` has a value, call `SetContentStatus`.

## 4. Validation (`conf validate`)
*   Update `internal/fs/frontmatter.go` validation logic:
    *   `state` must be `current`, `draft`, or empty.
    *   `status` is any string.
    *   `labels` must be an array of strings. Labels cannot contain spaces (enforce Confluence label rules locally to fail fast).

## Implementation Order
- [x] 1.  Update `internal/fs/frontmatter.go` (Schema, Parsing Migration, Validation).
- [x] 2.  Add v1 API methods to `internal/confluence/client.go` with tests.
- [x] 3.  Integrate into `pull.go` (Reading from remote, writing to local).
- [x] 4.  Integrate into `push.go` (Diffing local vs remote, writing to remote).
