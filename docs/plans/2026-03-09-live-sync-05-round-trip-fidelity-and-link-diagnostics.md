# Live Sync Follow-Up 05: Round-Trip Fidelity and Link Diagnostics

**Goal:** Preserve plain ISO-like date text, improve cross-space link diagnostics, and lock supported rich-content round-trips with automated coverage.

**Covers:** `F-009`, P1 item `1`, E2E items `9`, `10`, `11`, `12`, `13`

**Specs to update first if behavior changes:**
- `openspec/specs/pull-and-validate/spec.md`
- `openspec/specs/compatibility/spec.md`

**Likely files:**
- `internal/converter/reverse.go`
- `internal/converter/forward.go`
- `internal/converter/roundtrip_test.go`
- `internal/converter/reverse_test.go`
- `internal/converter/forward_test.go`
- `internal/sync/hooks.go`
- `internal/sync/diagnostics.go`
- `internal/sync/push_links_test.go`
- `cmd/validate.go`
- `cmd/e2e_test.go`
- `docs/compatibility.md`
- `docs/usage.md`

## Required outcomes

1. Ordinary body text like `2026-03-09` remains the same visible text after push/pull.
2. Cross-space links are preserved as readable links and reported with a preserved cross-space diagnostic instead of a generic unresolved warning.
3. Task lists, PlantUML, and Mermaid remain covered by explicit round-trip tests.

## Suggested implementation order

### Task 1: Fix date coercion

1. Audit the Markdown-to-ADF path for implicit date-node conversion.
2. Restrict date-node generation to explicit source markup only.
3. Add unit tests covering plain text date strings.

### Task 2: Improve cross-space diagnostics

1. Inspect link-resolution diagnostics for out-of-scope page links.
2. Emit a distinct preserved cross-space diagnostic category.
3. Keep the Markdown output readable and stable on pull.

### Task 3: Expand round-trip tests

1. E2E for cross-space link preservation.
2. E2E for task list round-trip.
3. E2E for PlantUML round-trip.
4. E2E for Mermaid warning and round-trip.
5. E2E for plain date text stability.

## Verification

1. `go test ./internal/converter/...`
2. `go test ./internal/sync/... -run Link`
3. `make test`

## Commit

Use one section commit, for example:

`fix(converter): preserve plain text iso dates on round-trip`
