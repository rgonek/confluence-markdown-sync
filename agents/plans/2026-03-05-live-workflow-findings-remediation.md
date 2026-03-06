# Live Workflow Findings Remediation Plan

## Objective

Close the reliability, workflow, and round-trip correctness gaps discovered during the live TD2 and SD2 verification run on March 5, 2026, so `conf` can graduate from limited beta use to a production-ready sync tool for real Confluence spaces.

## Source Inputs

- Live execution log: `C:\Users\rgone\AppData\Local\Temp\conf-live-20260305-211238\TEST_LOG.md`
- Spaces exercised with real API traffic: `TD2`, `SD2`
- Workflow exercised: `pull -> edit -> validate -> diff -> push -> pull`, plus direct Confluence API create/update/delete calls to simulate a second user

## Production Readiness Bar

The following items are blockers for a production-ready release:

1. Cross-space link handling parity between `validate`, `push`, and `pull`
2. Safe handling of frontmatter `status` on new-page pushes
3. Correct attachment/media ADF generation and round-trip pull behavior
4. Stable hierarchy round-trip for parent pages with children

The following items are not blockers for limited beta usage, but should be resolved before a `1.0.0` claim:

1. Mermaid support contract
2. Cleanup lifecycle for failed sync branches
3. Workflow polish and diagnostics consistency

## Assumptions

- Command model remains unchanged: `init`, `pull`, `push`, `validate`, `diff`, `search`, `relink`, `status`, `doctor`, `clean`
- Existing workspace layouts and frontmatter formats must remain backward compatible
- Fixes should preserve current safety guarantees around isolated worktrees, snapshot refs, and no-write validation
- Direct Confluence API behavior can vary by tenant, so new logic must fail safe and emit actionable diagnostics

## Workstream A: Cross-Space Link Resolution Parity

### Problems Observed

- `conf validate` succeeded for relative cross-space Markdown links, but `conf push` failed in its isolated worktree with unresolved link errors.
- The failure reproduced in both directions: `SD2 -> TD2` and `TD2 -> SD2`.
- Pull emitted unresolved warnings for valid absolute Confluence cross-space links that should have been preserved without diagnostics.

### Plan

- Trace and unify global page index construction across:
  - `cmd/validate.go`
  - `cmd/push_worktree.go`
  - `internal/sync/hooks.go`
  - `internal/sync/index.go`
- Make standalone `validate` and in-worktree `push` use the same repo root discovery, path normalization, and global index scope.
- Normalize cross-space path lookups so worktree-local paths resolve identically to active-workspace paths.
- Preserve fragment handling (`#anchor`) consistently for cross-space links in both directions.
- Adjust pull-side forward link handling so valid absolute Confluence cross-space links do not emit unresolved-reference warnings.
- Add diagnostics that distinguish:
  - truly unresolved cross-space links
  - intentionally preserved absolute cross-space links

### Validation

- Add integration coverage for:
  - new `SD2` page linking relatively to existing `TD2` page
  - new `TD2` page linking relatively to existing `SD2` page
  - `validate` success implies `push` success for the same content
- Add pull coverage proving absolute cross-space page URLs survive conversion without unresolved warnings.

## Workstream B: Frontmatter `status` Semantics and Rollback Safety

### Problems Observed

- New-page push with frontmatter `status: In progress` created the page body successfully, then failed during metadata sync with `Invalid status 'null'`.
- Rollback attempted `purge=true` deletion against a current page and failed, leaving orphaned content behind until manual API cleanup.

### Plan

- Separate page lifecycle state from content-status lozenge handling:
  - `state` remains mapped to page lifecycle (`current` / `draft`)
  - `status` remains mapped to content-status lozenge metadata only
- Audit new-page and existing-page metadata sync so content-status updates:
  - are only attempted when `status` is present
  - use the correct v1 content-status API payloads
  - can be deleted cleanly when `status` is removed
- Rework rollback for newly created pages:
  - if page creation succeeded but later metadata sync failed, use the correct delete path for current pages
  - never rely on `purge=true` where Confluence rejects it
- Extend rollback diagnostics to explicitly report:
  - created-page cleanup success
  - created-page cleanup failure
  - content-status rollback success/failure

### Validation

- Add push integration tests covering:
  - new page with `status`
  - update existing page with `status`
  - remove existing `status`
  - failure after page creation with successful cleanup
- Add direct client tests for content-status API calls and rollback error handling.

## Workstream C: Attachment and Media Fidelity

### Problems Observed

- Push uploaded both attachments, but resulting ADF was wrong:
  - the image attachment did not appear as a valid image/media node
  - the file link degraded into `[Embedded content]` with `UNKNOWN_MEDIA_ID`
- Pull round-tripped the broken ADF into placeholder Markdown:
  - `\[Embedded content\] [Media: UNKNOWN_MEDIA_ID]`
- Because the page body retained `UNKNOWN_MEDIA_ID`, pull skipped stale attachment pruning for safety.
- Later local attachment deletion did not remove the remote attachments.

### Plan

- Trace the full attachment pipeline across:
  - markdown asset discovery
  - asset path normalization/migration
  - upload result mapping
  - reverse conversion hook output
  - final ADF patching before page update
- Fix image handling so pushed Markdown images become valid Confluence media/image nodes with real attachment identity.
- Fix file-link handling so pushed Markdown file links become valid file/media nodes with real attachment IDs.
- Ensure the final outgoing ADF references uploaded attachments, not placeholder or pending IDs.
- Fix forward conversion so pull can resolve those attachment nodes back to stable local asset paths.
- Ensure stale attachment pruning works after a full push/pull cycle, including remote attachment deletion.
- Improve push summary output to report attachment create/delete operations when they occur.

### Validation

- Add live-style integration coverage for:
  - Markdown image attachment
  - Markdown file attachment
  - mixed image + file on the same page
  - delete attachment locally -> push -> confirm remote deletion
  - delete attachment remotely -> pull -> confirm local deletion
- Add direct ADF assertions in tests to verify:
  - image attachment resolves to real media node
  - file attachment resolves to real file/media node
  - no `UNKNOWN_MEDIA_ID` remains in pushed payloads

## Workstream D: Hierarchy Round-Trip Invariants

### Problems Observed

- A parent page created locally as `<Parent>/<Parent>.md` pushed successfully, but after pull it came back as:
  - top-level `Live-Workflow-Test-2026-03-05.md`
  - sibling directory `Live-Workflow-Test-2026-03-05/`
- That violates the documented invariant for pages with children.
- The resulting `folder_path_index` also showed inconsistent Windows-style separator normalization in state.

### Plan

- Revisit pull path planning for pages with children in:
  - `internal/sync/pull_paths.go`
  - `internal/sync/pull.go`
  - `internal/sync/pull_pages.go`
- Ensure that pages with children always hydrate back to `DirectoryName/DirectoryName.md`.
- Normalize path separators consistently across:
  - `page_path_index`
  - `folder_path_index`
  - in-memory planned path maps
- Verify that folder-backed children and page-backed children both preserve the same local hierarchy rules after round-trip.
- Add explicit regression guards for page-parent plus folder-child combinations, since the live run exercised both on the same tree.

### Validation

- Add E2E round-trip tests for:
  - parent page with direct page children
  - parent page with folder child
  - nested folder child created remotely and then pulled locally
- Assert exact post-pull paths, not just page presence.

## Workstream E: Mermaid Support Contract

### Problems Observed

- PlantUML survived push as a Confluence extension.
- Mermaid did not. It was stored as a plain `codeBlock` with language `mermaid`, which likely does not render as a diagram in the Confluence UI.

### Plan

- Decide explicitly whether Mermaid is:
  - supported as a first-class rendered Confluence extension, or
  - preserved only as fenced code
- If Mermaid support is intended:
  - implement forward and reverse extension handlers similar to `plantumlcloud`
  - ensure push emits the correct Confluence extension/macro payload
  - ensure pull round-trips back to authored Markdown form
- If Mermaid support is not intended:
  - document it clearly in `README.md`, `AGENTS.md`, and usage docs
  - add validation or diagnostics so users understand the downgrade before push

### Validation

- Add round-trip tests for Mermaid matching the chosen support model.
- Add one live integration test that checks actual rendered ADF node type for Mermaid content.

## Workstream F: Cleanup and Recovery Artifact Lifecycle

### Problems Observed

- Failed pushes correctly retained snapshot refs and `sync/*` branches for recovery.
- `conf clean --yes --non-interactive` removed snapshot refs but did not prune the leftover `sync/*` branches.

### Plan

- Extend `clean` so it can safely remove stale `sync/*` branches when:
  - current branch is not a `sync/*` branch
  - no linked worktree remains
  - corresponding recovery refs are already gone or explicitly targeted for cleanup
- Emit a cleanup summary that reports:
  - removed worktrees
  - removed snapshot refs
  - removed sync branches
  - skipped branches and why
- Add branch-retention rules for genuinely active recovery scenarios so cleanup does not become destructive.

### Validation

- Add integration coverage for:
  - failed push leaves branch + snapshot ref
  - `conf clean` removes both when safe
  - `conf clean` preserves active branch when current HEAD is on a sync branch

## Workstream G: Workflow Polish and Diagnostics Consistency

### Problems Observed

- `conf init` still prompted whenever `.env` was missing, even though credentials were already available through environment variables.
- `conf status` reported clean content state while sandbox Git still showed deleted attachment files.
- Push repeatedly warned on Confluence folder-list HTTP 500 responses, even though fallback behavior worked.
- Diff output was useful, but not always clear about metadata parity versus content parity.

### Plan

- Improve `conf init` so non-interactive environments can scaffold `.env` directly from already-set auth environment variables.
- Decide whether `status` should remain Markdown-only or grow an optional asset-drift view. If it stays Markdown-only, document that explicitly.
- Improve diagnostics around unresolved references and fallback behavior so users can distinguish:
  - harmless preserved absolute links
  - degraded but pullable content
  - truly broken references
- Refine folder-list fallback logging so repeated tenant-side 500s remain visible but less noisy.
- Review `diff` metadata rendering for labels and other synced frontmatter so remote parity is easier to interpret.

### Validation

- Add command tests for non-interactive `init` with env-backed auth.
- Add status/diff documentation and tests for chosen asset-drift semantics.
- Add logging tests for folder-list fallback de-duplication or clearer warning messages.

## Delivery Order

1. Workstream B: `status` frontmatter safety and rollback correctness
2. Workstream A: cross-space link parity between `validate`, `push`, and `pull`
3. Workstream C: attachment/media correctness
4. Workstream D: hierarchy round-trip invariants
5. Workstream E: Mermaid support contract
6. Workstream F: cleanup branch lifecycle
7. Workstream G: workflow polish and diagnostics

This order prioritizes data correctness and remote-write safety before workflow polish.

## Verification Matrix

The remediation is complete only when the live scenarios below pass without workarounds:

- New page with `status` pushes successfully and cleans up safely on forced failure.
- Relative cross-space links succeed in both `validate` and `push`.
- Absolute cross-space page URLs pull without unresolved warnings.
- Image and file attachments round-trip through push and pull with valid ADF and stable Markdown.
- Local attachment deletion removes remote attachments.
- Parent page with children round-trips back to `<Parent>/<Parent>.md`.
- Mermaid behavior matches the documented support model.
- `conf clean` removes stale sync branches as well as snapshot refs.
- A final real `pull -> edit -> validate -> diff -> push -> pull` run over TD2 and SD2 completes with:
  - no unresolved warnings
  - clean `conf status`
  - clean `conf doctor`
  - no manual API cleanup required

## Risks and Mitigations

1. **Risk: Attachment fixes span several layers and can regress existing media behavior.**
   Mitigation: add end-to-end assertions on final ADF plus round-trip Markdown, not just unit-level hook tests.

2. **Risk: Hierarchy fixes can destabilize existing pulled workspaces.**
   Mitigation: normalize paths carefully, add migration-safe handling for existing state files, and test both old and new layouts.

3. **Risk: Cross-space link fixes can overreach into intentionally external URLs.**
   Mitigation: keep strict separation between same-tenant Confluence URLs, known cross-space local targets, and true external links.

4. **Risk: Cleanup improvements could accidentally remove recovery state users still need.**
   Mitigation: preserve explicit safety checks and require clear eligibility before deleting sync branches.

5. **Risk: Mermaid support may depend on tenant-specific Confluence macro behavior.**
   Mitigation: decide and document the support contract first, then implement only the tested, portable behavior.

## Release Recommendation

Until Workstreams A through D are complete and verified in a fresh live run, `conf` should be treated as beta software for carefully supervised use, not a production-ready general release.
