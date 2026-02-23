Issue 1
- Title: push fails when adding Markdown image attachment (ADF -> storage conversion 400)
- Repro: in Technical documentation (TD)/A-CMS-E2E-Page-A.md add ![a-page-attachment.png](assets/3637249/a-page-attachment.png), create Technical documentation (TD)/assets/3637249/a-page-attachment.png, run go run . validate "Technical documentation (TD)/A-CMS-E2E-Page-A.md" (passes), then go run . push TD --yes --non-interactive --on-conflict=force
- Actual: push fails on page update with PUT .../wiki/api/v2/pages/3637249 -> 400 BAD_REQUEST / Error converting ADF to storage format
- Expected: attachment should upload and page should update successfully
- Evidence: exact failure text: update page 3637249 ... status 400 ... "Error converting ADF to storage format"
Issue 2
- Title: push fails when deleting attachment referenced in Markdown (wrong attachment ID type for delete API)
- Repro: in Technical documentation (TD)/Simple-with-attachment.md remove image line for assets/2359297/ffd70a27-0a48-48db-9662-24252c884152-image-20260220-184049.png, run go run . validate "Technical documentation (TD)/Simple-with-attachment.md" (passes), then go run . push TD --yes --non-interactive --on-conflict=pull-merge
- Actual: push fails trying to delete stale attachment via DELETE .../wiki/api/v2/attachments/ffd70a27-... with 400 INVALID_REQUEST_PARAMETER, server says expected ContentId
- Expected: attachment should be deleted (or mapped to deletable ID first), then page update should complete
- Evidence: .confluence-state.json stores UUID-style IDs (fileId-like) in attachment_index, but delete endpoint expects numeric/content attachment ID
Issue 3
- Title: push --dry-run mutates local frontmatter (confluence_version) even though dry-run should be non-mutating
- Repro: add temporary body line in Technical documentation (TD)/A-CMS-E2E-Page-A.md, stage file, run go run . push "Technical documentation (TD)/A-CMS-E2E-Page-A.md" --dry-run --yes --non-interactive --on-conflict=force, then inspect staged diff
- Actual: confluence_version in local file is incremented (observed 4 -> 5) during dry-run path
- Expected: dry-run should not modify local files, index, refs, or remote state
- Evidence: after dry-run, git diff --cached -- "Technical documentation (TD)/A-CMS-E2E-Page-A.md" showed only frontmatter version bump