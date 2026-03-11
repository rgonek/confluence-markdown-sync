# Feature and Tenant Compatibility Matrix

This document describes which `conf` features are fully supported, which depend
on optional Confluence tenant APIs, and what degraded fallback behavior applies
when a dependency is unavailable.

## Matrix

| Feature | Support Level | Tenant Dependency | Degraded Fallback |
|---------|--------------|-------------------|-------------------|
| Page sync (pull/push) | Full | None | — |
| Page hierarchy (folders) | Full | Folder API | Page-based hierarchy when folder API returns any API error (`FOLDER_COMPATIBILITY_MODE` / `FOLDER_LOOKUP_UNAVAILABLE`), with diagnostics, summaries, and JSON reports distinguishing unsupported capability from upstream endpoint failure |
| Content status (lozenges) | Full | Content Status API | Status sync disabled when API returns 404/405/501 (`CONTENT_STATUS_COMPATIBILITY_MODE`) |
| Labels | Full | None | — |
| Attachments (images/files) | Full | None | — |
| Markdown task lists | Full | None | Native Confluence task nodes on push, Markdown checkbox lists on pull |
| PlantUML diagrams | Rendered round-trip | `plantumlcloud` macro | — |
| Mermaid diagrams | Preserved as code | None | Pushed as ADF `codeBlock`; `MERMAID_PRESERVED_AS_CODEBLOCK` warning emitted by `validate` and `push` |
| Same-space links | Full | None | — |
| Cross-space links | Full | Sibling space directories | Preserved as readable remote links with preserved-cross-space diagnostics instead of generic unresolved-reference failures |
| Plain ISO-like date text | Full | None | Ordinary text remains ordinary text; no implicit date-macro coercion |
| Raw ADF extension | Best-effort | None | Low-level preservation only; not a verified round-trip guarantee |
| Unknown macros | Unsupported | App-specific | May fail on push if Confluence rejects the macro; sandbox validation recommended |
| Page archiving | Full | Archive API | — |
| Dry-run simulation | Full | Read-only API access | — |
| Preflight capability check | Full | Content Status API | Reports degraded modes before execution and uses the same validation scope as real push |

## Compatibility Mode Details

### Folder API (`FOLDER_COMPATIBILITY_MODE` / `FOLDER_LOOKUP_UNAVAILABLE`)

`conf` uses the Confluence Folder API to resolve page hierarchy during `pull`
and `push`. If the tenant does not expose this API (any API-level error is
returned), `conf` automatically falls back to page-based hierarchy:

- **Pull**: hierarchy is derived from page parent relationships only; folder
  nodes are treated as regular parent pages. Emits `FOLDER_LOOKUP_UNAVAILABLE`.
- **Push**: folder creation is skipped; pages are nested under page parents
  instead. Emits `FOLDER_COMPATIBILITY_MODE`.

No configuration change is needed. The mode is detected automatically on the
first folder lookup attempt each run. Diagnostics should make it clear whether
the fallback was triggered by an unsupported tenant capability ("tenant does
not support the folder API") or by an upstream endpoint failure ("folder API
endpoint failed upstream").

### Content Status API (`CONTENT_STATUS_COMPATIBILITY_MODE`)

`conf` syncs the Confluence "Content Status" visual lozenge (frontmatter key
`status`) through the Content Status API. If the tenant returns 404, 405, or
501 for the probe request, `conf` disables content-status sync for the current
run and emits `CONTENT_STATUS_COMPATIBILITY_MODE`.

The page body and all other metadata continue to sync normally. Only the
`status` lozenge value is skipped. On supported tenants, push now resolves the
requested status against the tenant’s available content states before any page
create/update mutation so invalid status writes fail early.

### Mermaid (`MERMAID_PRESERVED_AS_CODEBLOCK`)

Mermaid diagrams are not rendered as Confluence diagram macros. `conf` keeps
Mermaid fenced code blocks in Markdown and writes them back as ADF `codeBlock`
nodes with `language: mermaid` on push. `conf validate` and `conf push` emit
`MERMAID_PRESERVED_AS_CODEBLOCK` so the downgrade is explicit before the write
happens.

Use PlantUML (`plantumlcloud`) when a page must keep rendering as a first-class
Confluence diagram macro.

### Markdown Task Lists

Markdown checkbox lists are treated as native task content. Push writes
Confluence task nodes, and pull restores the same checked/unchecked list state
back into Markdown.

### Cross-Space Links

Cross-space links are preserved as readable remote URLs or references. They are
not rewritten to local relative Markdown paths, and they should not degrade into
generic unresolved-reference errors when preservation succeeds.

### Plain ISO-like Date Text

Ordinary body text such as `2026-03-09` must remain ordinary text across
push/pull round-trips unless the source explicitly requested a date macro or
equivalent structured markup.

### PlantUML (`plantumlcloud`)

PlantUML is the only first-class rendered extension handler in `conf`. Pull and
diff convert the `plantumlcloud` Confluence macro into a managed
`adf-extension` wrapper with a `puml` code body. Validate and push reconstruct
the Confluence macro from the same wrapper.

### Raw ADF Extension and Unknown Macros

Extension nodes without a repo-specific handler can be preserved as raw
```` ```adf:extension ```` JSON fences. This is a low-level escape hatch and
not a verified end-to-end round-trip contract. Unknown macros may survive a
pull in raw ADF form but can still be rejected by Confluence on push if the
app is not installed or if the tenant rejects the payload. Always sandbox-
validate any workflow that relies on raw ADF preservation.

## Preflight Capability Check

Running `conf push --preflight` probes the remote tenant before any write and
reports which compatibility modes are active. When the pending push needs
folder hierarchy writes, preflight surfaces whether fallback is caused by an
unsupported tenant capability or by an upstream folder endpoint failure. It
also probes content-status compatibility ahead of time so operators can decide
whether to proceed. The final push summary and JSON report surface the active
fallback mode as well, so degraded folder behavior is still visible after the
run completes.
