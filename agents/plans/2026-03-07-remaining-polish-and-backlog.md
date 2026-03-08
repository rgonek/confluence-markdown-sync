# Remaining Polish & Backlog Plan

## Objective

Consolidate all unfinished work from the March 5 live-workflow polish follow-ups into a single actionable plan. Everything listed here is either a remaining P1 polish item or a P2 backlog item that was deferred during the remediation and polish passes.

## Relationship To Completed Work

- `2026-03-05-live-workflow-findings-remediation.md` — production blockers, fully landed on `codex/live-workflow-findings-remediation`.
- `2026-03-05-live-workflow-polish-followups.md` — 16 of 18 main items completed; items 16 and 17 remain. P2 backlog (items 19–28) was untouched.

## Remaining P1 Items

### 1. Strengthen Push Preflight And Release Messaging (was item 16)

#### Problem

Some failures are still surprising because capability mismatches or degraded behavior are only discovered during execution. Also, the current maturity level should be communicated more explicitly.

#### Plan

- Expand preflight so it can optionally report:
  - remote capability concerns
  - exact planned page and attachment mutations
  - known degraded modes before write execution
- Review release docs, README language, and versioning guidance so the product is clearly labeled beta until blocker workstreams are done.
- Keep maturity messaging aligned with actual tested behavior.

#### Validation

- Add preflight coverage for degraded-mode reporting.
- Update release-facing docs to match the current maturity contract.

### 2. Align Generated `AGENTS.md` With Actual Workflow And Documentation Strategy (was item 17)

#### Problem

The generated `AGENTS.md` scaffolding is no longer fully aligned with the current codebase, the live-tested behavior, or the desired documentation process:

- it still splits usage into human-in-the-loop and autonomous modes, even though one general workflow is sufficient
- it still refers to `space` as a normal frontmatter key users must not edit, which is stale relative to the current frontmatter model
- technical templates overstate support for Mermaid and relative cross-space links
- it does not explain the intended direction that generated Specs/PRDs should become the working source of truth for feature behavior and product intent

#### Plan

- Update generated workspace and space-level `AGENTS.md` templates in:
  - `cmd/init.go`
  - `cmd/agents.go`
- Replace the split workflow sections with one general recommended workflow:
  - `pull -> edit -> validate -> diff -> push`
  - mention that humans may still review or approve specific steps, but do not model that as a separate mode
- Remove stale frontmatter guidance and align template language with the real model:
  - `id` remains immutable
  - `version` remains sync-managed
  - `state`, `status`, and `labels` remain user-editable
  - do not present `space` as a normal active frontmatter field
- Add a concise support-contract note or link to docs covering:
  - same-space links
  - cross-space links
  - attachments
  - PlantUML
  - Mermaid
  - hierarchy behavior
- Add explicit guidance that new Specs/PRDs should be generated and maintained as the intended source of truth.
- Ensure generated `AGENTS.md` points readers to the primary plan and any future Specs/PRDs when behavior or requirements are unclear.

#### Validation

- Add golden-style tests for generated `AGENTS.md` output.
- Verify generated templates do not mention the old split workflow model.
- Verify generated templates align with current frontmatter behavior and current documented support boundaries.

---

## P2 Backlog

### 3. Add Release Gating With Live Sandbox Smoke Tests (was item 19)

#### Problem

Current quality signals are still too synthetic to fully protect releases from workflow regressions that only appear against live Confluence tenants.

#### Plan

- Require an explicit sandbox live smoke-test check before promoting release candidates.
- Keep it gated to sandbox-configured spaces only.
- Separate release-blocking live checks from ordinary developer CI to avoid accidental production-space execution.

### 4. Add Upgrade And Migration Coverage For Older Workspaces (was item 20)

#### Problem

Fixes to state files, hierarchy layout, and metadata handling may unintentionally break existing user workspaces created by older versions.

#### Plan

- Add migration fixtures for older `.confluence-state.json` and markdown layouts.
- Verify pull/push/status/doctor behavior stays safe after upgrade.
- Document any migration semantics if automatic normalization changes persisted files.

### 5. Make `--dry-run` Closer To Real Execution (was item 21)

#### Problem

`--dry-run` is useful, but it should validate more of the real execution path so operators can trust it as a genuine preflight.

#### Plan

- Validate final payload shape, attachment mutation plan, and cleanup plan in dry-run mode.
- Show the exact remote operations that would occur, including page/archive/attachment changes.
- Preserve the guarantee that no local or remote state is mutated.

### 6. Add Read-Only Inspection For Recovery Artifacts (was item 22)

#### Problem

Even before a full `recover` workflow exists, operators need an easy way to inspect failed-run artifacts without dropping into Git internals.

#### Plan

- Add a read-only inspection command or submode to list:
  - retained `sync/*` branches
  - snapshot refs
  - failed run timestamps
  - associated failure reasons when available

### 7. Improve No-Op Explainability (was item 23)

#### Problem

No-op runs succeed quietly, but they often do not explain why nothing changed, which makes troubleshooting harder.

#### Plan

- Improve no-op output for `pull`, `push`, and `clean` so it states why the command was a no-op.
- Distinguish cases such as:
  - no local changes
  - no remote changes
  - changes existed but were intentionally skipped

### 8. Add Performance And Scale Tests (was item 24)

#### Problem

Live validation covered correctness, but not scale. Large spaces, pagination stress, and attachment-heavy pages may still expose bottlenecks or edge-case failures.

#### Plan

- Add scale-oriented tests for:
  - larger page counts
  - attachment-heavy pages
  - long pagination chains
  - rate-limit and retry pressure

### 9. Strengthen Destructive Operation Previews (was item 25)

#### Problem

Archive/delete pushes should make the exact destructive target set obvious before execution.

#### Plan

- Expand preflight and confirmation flows to show exact pages and attachments that will be archived or deleted.
- Keep summaries concise, but make destructive targets explicit.

### 10. Add A Feature/Tenant Compatibility Matrix (was item 26)

#### Problem

Operators need a clearer understanding of what behavior is guaranteed, what is best-effort, and what depends on tenant capability.

#### Plan

- Publish a compatibility matrix covering:
  - core sync features
  - macro/extension support
  - tenant capability dependencies
  - degraded fallback modes

### 11. Add Changelog Discipline For Sync Semantics (was item 27)

#### Problem

Behavior changes in sync semantics are especially important to operators, but they are easy to lose in generic release notes.

#### Plan

- Track user-visible sync behavior changes explicitly in changelog or release-note guidance.
- Highlight changes to:
  - hierarchy rules
  - attachment handling
  - validation strictness
  - cleanup/recovery semantics

### 12. Add Sanitized Golden Live Fixtures (was item 28)

#### Problem

Synthetic fixtures are not catching enough real-world edge cases from Confluence content.

#### Plan

- Build a sanitized fixture corpus from real pulled pages.
- Use it for round-trip, pull, push, and diff regression tests.
- Keep private or tenant-specific details removed while preserving structure that triggered bugs in the live run.

---

## Suggested Order

### Priority 1 — Complete remaining polish
1. Push preflight and release messaging (item 1)
2. Generated `AGENTS.md` alignment (item 2)

### Priority 2 — Backlog, grouped by theme

**Operator experience:**
3. No-op explainability (item 7)
4. Destructive operation previews (item 9)
5. Recovery artifact inspection (item 6)

**Testing and quality gates:**
6. Dry-run fidelity (item 5)
7. Performance and scale tests (item 8)
8. Sanitized golden live fixtures (item 12)
9. Release gating with sandbox smoke tests (item 3)

**Documentation and contracts:**
10. Feature/tenant compatibility matrix (item 10)
11. Changelog discipline (item 11)

**Migration safety:**
12. Upgrade and migration coverage (item 4)

## Success Criteria

- Preflight makes degraded modes and risky capabilities visible before remote writes start.
- Release docs accurately reflect the current beta maturity contract.
- Generated `AGENTS.md` scaffolding reflects one general workflow, current product constraints, and the intended Specs/PRDs documentation direction.
- Backlog items are tracked and prioritized for post-beta execution.
