# Production Readiness Comprehensive Remediation Plan

## Objective

Deliver a production-grade reliability and operations baseline for conf by closing data-safety gaps in pull and push execution, hardening cancellation and retry behavior, improving release and observability standards, and raising quality gates so unattended operation is safe in real team environments.

## Assumptions

- The current command model remains stable and no command deprecations are required in this cycle.
- Backward compatibility for existing workspace layouts and frontmatter conventions is required.
- A temporary increase in test and CI runtime is acceptable to gain stronger correctness guarantees.
- Improvements are prioritized for safety and recoverability before throughput optimization.

## Implementation Plan

- [x] Remove placeholder fallback in strict reverse link handling so unresolved internal links fail consistently in validate and push (internal/sync/hooks.go:179, cmd/validate.go:326, internal/sync/push.go:468).
- [x] Split push into preflight planning and mutation phases so intent is resolved before remote writes begin (internal/sync/push.go:430, internal/sync/push.go:491, internal/sync/push.go:554).
- [x] Add rollback handlers for partial push failures, including created pages, uploaded assets, and metadata drift, and report rollback outcomes in diagnostics (internal/sync/push.go:317, internal/sync/push.go:1034).
- [x] Change pull discard-local semantics to drop stash only after successful pull completion, preventing local loss on unrelated failures (cmd/pull.go:163).
- [x] Replace broad pull cleanup with scoped restoration that minimizes destructive resets while preserving user-authored content (cmd/pull.go:423, cmd/pull.go:424).
- [x] Unify incremental pagination to use continuation semantics across estimate and execution paths and prevent offset drift (internal/sync/pull.go:793, internal/sync/pull.go:814, cmd/pull.go:776).
- [x] Persist SpaceKey as authoritative sync state and remove markdown-scan fallback inference during target resolution (internal/fs/state.go:18, internal/sync/pull.go:619, cmd/pull.go:319).
- [x] Propagate command context through diff and validate conversion paths so cancellation immediately stops expensive operations (cmd/conf/main.go:13, cmd/diff.go:58, cmd/validate.go:326).
- [x] Extend retry behavior to include transient network and timeout failures while preserving idempotent remote interactions (internal/confluence/client.go:763, internal/confluence/retry.go:18).
- [x] Make retry and rate-limit policies operator-configurable through flags and environment to handle tenant-specific quotas (internal/confluence/ratelimit.go:8, cmd/root.go:59).
- [x] Add explicit client close lifecycle for limiter resources so long-running automation does not leak background goroutines (internal/confluence/ratelimit.go:60, internal/confluence/client.go:120).
- [x] Raise quality gates and add tests for uncovered high-risk paths in push, relink, progress, and root command wiring (internal/sync/push.go:260, internal/sync/push.go:1034, cmd/relink.go:139, cmd/progress.go:17, cmd/root.go:40, tools/coveragecheck/main.go:22, .golangci.yml:7).
- [x] Rework e2e suites to require sandbox configuration and remove hardcoded live identifiers so tests align with safety policy (cmd/e2e_test.go:25, cmd/e2e_test.go:171, cmd/e2e_test.go:258, AGENTS.md:55).
- [ ] Expand release workflow with checksums, signing, SBOM generation, vulnerability scanning, and publish steps for verifiable artifacts (.github/workflows/release.yml:34).
- [ ] Replace shell-specific clean behavior and validate developer targets on Windows and Linux to improve portability (Makefile:44, .github/workflows/ci.yml:21).
- [ ] Add licensing and support-governance documents so distribution posture matches installation and release expectations (README.md:13).
- [ ] Expand operator runbooks for conflict handling, rollback expectations, and dry-run behavior with test-backed guidance (docs/automation.md:41, cmd/push.go:350, cmd/pull.go:574).
- [x] Add structured pull and push telemetry for timing, retries, conflict choices, and rollback events to improve incident diagnosis (cmd/pull.go:71, cmd/push.go:79, cmd/automation.go:52, internal/confluence/client.go:787, internal/sync/push.go:399, cmd/progress.go:36).

## Verification Criteria

- Strict validation rejects unresolved internal links and assets without placeholder substitutions in both validate and push pathways.
- Forced-failure pull scenarios preserve local changes and state metadata when discard-local is enabled and pull exits with error.
- Push failure simulations prove no orphaned remote artifacts remain after rollback handling completes.
- Incremental pull pagination tests demonstrate no skipped or duplicated pages across large change sets.
- Cancellation tests confirm diff and validate stop promptly when interrupt or terminate signals are received.
- Retry and rate-limit configuration is externally tunable and covered by unit and integration tests.
- Coverage and lint gates fail when critical orchestration modules regress below newly defined thresholds.
- Release workflow outputs include verifiable checksums, provenance metadata, and SBOM artifacts for each built binary.

## Potential Risks and Mitigations

1. **Risk: Transactional and rollback logic increases push orchestration complexity and regression potential.**
   Mitigation: Deliver in small slices behind integration tests that simulate failures at each remote mutation boundary and require green reliability suites before merge.

2. **Risk: Confluence API behavior differences across tenants can invalidate retry and rollback assumptions.**
   Mitigation: Add contract-style tests with mocked API variants and keep fallback-safe defaults that stop writes when response semantics are ambiguous.

3. **Risk: Higher quality gates can slow development throughput and increase CI cycle time.**
   Mitigation: Phase gate increases over multiple iterations, parallelize test jobs, and prioritize high-risk package coverage before broad threshold expansion.

4. **Risk: Cross-platform shell and tooling changes can introduce local workflow friction.**
   Mitigation: Validate on both Windows and Linux CI runners and document migration notes in developer docs before enabling stricter checks.

5. **Risk: E2E sandbox hardening may reduce convenience for ad-hoc testing.**
   Mitigation: Provide a repeatable sandbox bootstrap script and explicit environment contract so safe testing remains easy while production spaces stay protected.

## Alternative Approaches

1. **Minimal Safety Patch Track:** Address only immediate data-loss and strict-validation blockers first, then defer CI, release, and observability work. This shortens time to partial hardening but leaves operational maturity gaps.
2. **Reliability-First Track:** Complete pull and push correctness, rollback, and cancellation hardening first, then raise quality and release controls in a second wave. This balances risk reduction and delivery speed.
3. **Platform Modernization Track:** Perform a larger architecture refactor that separates orchestration, remote mutation planning, and execution engines before fixes. This may yield cleaner long-term design but carries the highest short-term regression risk.
