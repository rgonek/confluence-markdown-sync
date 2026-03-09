# Live Sync Follow-Up 01: Attachment Publication

**Goal:** Fix the pushed-attachment `UNKNOWN_MEDIA_ID` bug, preserve local asset links across push/pull, and cover attachment add/delete behavior with automated tests.

**Covers:** `F-004`, E2E items `4`, `5`, `6`

**Specs to update first if behavior changes:**
- `openspec/specs/push/spec.md`
- `openspec/specs/pull-and-validate/spec.md`

**Likely files:**
- `internal/sync/push_assets.go`
- `internal/sync/push_adf.go`
- `internal/sync/push.go`
- `internal/sync/pull_assets.go`
- `internal/sync/assets.go`
- `internal/sync/push_assets_test.go`
- `internal/sync/pull_assets_test.go`
- `internal/sync/push_adf_test.go`
- `cmd/e2e_test.go`
- `README.md`
- `docs/usage.md`
- `docs/compatibility.md`

## Required outcomes

1. Pushed ADF references real uploaded attachment/media IDs instead of `UNKNOWN_MEDIA_ID`.
2. A follow-up pull keeps Markdown asset links as local `assets/<page-id>/...` paths.
3. Deleting an attachment locally removes the corresponding remote attachment unless suppressed.
4. Tests cover upload, round-trip, and deletion at unit/integration/E2E levels.

## Suggested implementation order

### Task 1: Trace attachment identity through push

1. Inspect the attachment upload and ADF assembly path.
2. Identify where the uploaded attachment ID or media identity is lost before final publish.
3. Update the push pipeline so ADF is rendered after attachment resolution or receives the resolved identity map.

### Task 2: Preserve local asset references on pull

1. Verify the pull-side asset rewrite still prefers local files when the attachment index is known.
2. Fix any degraded fallback path that emits `UNKNOWN_MEDIA_ID` text after a successful upload.

### Task 3: Add focused regression tests

1. Unit/integration tests around attachment identity propagation in `internal/sync`.
2. E2E test for upload correctness:
   - create page with file and image attachments
   - push
   - verify remote attachment list
   - fetch remote ADF directly
   - assert no `UNKNOWN_MEDIA_ID`
3. E2E test for pull round-trip:
   - force-pull after upload
   - assert Markdown still uses local `assets/<page-id>/...` paths
4. E2E test for attachment deletion:
   - remove local attachment reference and asset file
   - push
   - verify remote deletion
   - pull
   - assert local asset and state entry are gone

### Task 4: Docs alignment

1. Update user-facing docs only if visible behavior or wording changes.

## Verification

1. `go test ./internal/sync/...`
2. `go test ./cmd/... -run Attachment`
3. `make test`

## Commit

Use one section commit, for example:

`fix(sync): publish attachments with resolved media ids`
