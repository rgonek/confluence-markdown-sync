# Automation and CI

This document explains how to run `conf` safely in scripts and CI pipelines.

> **Beta** — `conf` is under active development. Test automation workflows in a sandbox space before targeting production.

## Automation Flags

Supported on `pull` and `push`:

- `--yes`
  - auto-approves safety confirmations.
- `--non-interactive`
  - disables prompts,
  - fails fast when a decision is required and not provided.

`pull` and `push` also take a repository-scoped workspace lock. If another sync is already mutating the same repo, the second command fails fast with a lock message instead of continuing into incidental Git/index failures.

Additional pull flag:

- `--skip-missing-assets` (`-s`)
  - skips missing attachment downloads (`404`/not found) and continues pull.
- `--force` (`-f`)
  - forces a full-space pull refresh even when incremental change detection reports no updated pages.

Additional push flag:

- `--on-conflict=pull-merge|force|cancel`
  - required with `push --non-interactive` for file targets.
  - optional for space targets (defaults to `pull-merge`).

## Safety Confirmation Rules

`conf` requires confirmation when an operation:

- affects more than 10 Markdown files, or
- includes delete operations.

Behavior:

- interactive mode: prompt user,
- `--yes`: auto-approve,
- `--non-interactive` without `--yes`: command fails.

## Conflict Policy in Push

When remote versions are ahead:

- `pull-merge`: when a remote-ahead conflict is detected, `push` triggers `pull`, preserves local edits via merge/conflict state/recoverable artifacts, then stops so you can review/resolve and retry push.
- `force`: overwrite based on remote head.
- `cancel`: stop without remote writes.

In non-interactive usage, set one explicitly.

If `pull-merge` stops after preserving local edits, the CLI now prints the expected recovery sequence explicitly:

1. Resolve the affected files or review preserved backup files.
2. `git add` each resolved file.
3. Rerun `conf push ... --on-conflict=cancel`.

If a real `push` fails after recovery artifacts are created, the CLI prints the next commands to run for:

- listing retained runs with `conf recover`,
- inspecting the retained sync branch with `git switch sync/<SPACE_KEY>/<UTC timestamp>`,
- diffing the retained snapshot against that branch, and
- cleaning up a single run with `conf recover --discard <SPACE_KEY>/<UTC timestamp> --yes`.

## Pull Conflict Handling Runbook

When `conf pull` restores stashed local changes and Git reports conflicts, interactive mode offers:

- `Keep both` (default): keeps conflict markers so you can resolve manually.
- `Use Remote`: discards local conflicting hunks and keeps pulled remote content.
- `Use Local`: reapplies local stashed content over pulled remote updates.

Recommended operator flow:

1. Prefer `Keep both` for high-signal docs where intent matters.
2. Resolve markers and run `conf validate <SPACE_KEY>`.
3. Commit the merge-resolution result before the next `conf push`.

For automation (`--non-interactive`), conflicts fail fast and require manual follow-up.

## Push Rollback Expectations

`conf push` performs strict preflight validation before remote writes. If a mutation fails mid-page, push attempts rollback for:

- pages created during the failed operation,
- attachments uploaded during the failed operation,
- page metadata changes (content status and labels).

Rollback outcomes are surfaced as diagnostics in command output:

- `ROLLBACK_METADATA_RESTORED` / `ROLLBACK_METADATA_FAILED`
- `ROLLBACK_PAGE_CONTENT_RESTORED` / `ROLLBACK_PAGE_CONTENT_FAILED`
- `ROLLBACK_ATTACHMENT_DELETED` / `ROLLBACK_ATTACHMENT_FAILED`
- `ROLLBACK_PAGE_DELETED` / `ROLLBACK_PAGE_DELETE_FAILED`

Archive/page-removal safety diagnostics:

- `ARCHIVE_TASK_TIMEOUT`
- `ARCHIVE_TASK_STILL_RUNNING`
- `ARCHIVE_TASK_FAILED`
- `ARCHIVE_CONFIRMED_AFTER_WAIT_FAILURE`

If any `*_FAILED` code appears, treat the run as partial and inspect the referenced page before retrying.
If `ARCHIVE_TASK_STILL_RUNNING` appears, Confluence did not finish within the current timeout and the verification read still saw the page as current; inspect the page remotely and consider increasing `--archive-task-timeout`.

Asset drift note:

- `conf status` remains page-only.
- Use `git status` for local asset changes and `conf diff` when automation needs attachment-aware remote inspection.
- The first push for locally sourced assets may emit `ATTACHMENT_PATH_NORMALIZED` because `conf` relocates files into the managed `assets/<page-id>/...` hierarchy. That rename is expected and stable after the next pull.

## Dry-Run Behavior (`push --dry-run`)

`--dry-run` simulates remote actions and conversion without mutating Confluence or local Git state.

Use it to verify:

- changed markdown scope,
- planned page operations,
- full-space strict validation for space-scoped pushes,
- conversion and link/media resolution readiness.

Recommended sequence before unattended push:

```powershell
conf validate ENG
conf push ENG --dry-run --on-conflict=cancel
conf push ENG --yes --non-interactive --on-conflict=cancel
```

## Recommended Non-Interactive Commands

```powershell
conf pull ENG --yes --non-interactive --skip-missing-assets --force
conf validate ENG
conf push ENG --yes --non-interactive --on-conflict=cancel
```

## Live E2E Environment Contract

The `go test -tags=e2e ./cmd -run TestWorkflow` suite is intended for explicit live sandbox spaces only.

Required environment for `make test-e2e`:

- `CONF_E2E_DOMAIN`
- `CONF_E2E_EMAIL`
- `CONF_E2E_API_TOKEN`
- `CONF_E2E_PRIMARY_SPACE_KEY`
- `CONF_E2E_SECONDARY_SPACE_KEY`

Compatibility notes:

- No `ATLASSIAN_*`, `CONFLUENCE_*`, `CONF_LIVE_*`, legacy alias, or page-ID variables are required by the E2E harness.
- The E2E test process maps `CONF_E2E_DOMAIN`, `CONF_E2E_EMAIL`, and `CONF_E2E_API_TOKEN` into the runtime config expected by `conf` and the direct API client.
- Core conflict-path tests create and clean up their own temporary pages rather than depending on shared seeded page IDs.
- Capability-specific live suites, such as folder-fallback coverage, should be opt-in and skip unless the required tenant behavior or capability flag is available.

Example:

```powershell
$env:CONF_E2E_DOMAIN = 'https://your-domain.atlassian.net'
$env:CONF_E2E_EMAIL = 'you@example.com'
$env:CONF_E2E_API_TOKEN = 'your-token'
$env:CONF_E2E_PRIMARY_SPACE_KEY = 'SANDBOX'
$env:CONF_E2E_SECONDARY_SPACE_KEY = 'SANDBOX2'

go test -v -tags=e2e ./cmd -run TestWorkflow
```

`make test-e2e` wraps the same live suite after building `conf`, and `make release-check` is the repository release gate:

```powershell
make release-check
```

`make release-check` runs:

- `make fmt-check`
- `make lint`
- `make test`
- `make test-e2e`

Use it only with the explicit sandbox environment above. It is intended for release candidates, not for production spaces or casual local iteration.

## Live Sandbox Baseline Policy

Release verification should start from a stable, documented sandbox baseline. The live E2E suite now enforces that by running a force-pull baseline check before the broader workflow assertions.

Policy:

- Prefer fixing or recreating noisy sandbox seed content.
- If a known sandbox warning cannot be removed immediately, document it here and keep the automated allowlist aligned with `cmd/e2e_test.go`.
- Treat any new unexpected baseline diagnostic as a release blocker until it is explained and either removed or explicitly allowlisted.

Current documented baseline allowlist for the maintained release sandbox:

| Space | Expected diagnostic match | Reason |
|------|----------------------------|--------|
| `TD2` | `path=17727489`, `code=UNKNOWN_MEDIA_ID_UNRESOLVED` | Existing seed page still contains unresolved media identities; pull skips stale-attachment pruning for safety. |
| `TD2` | `path=Technical-Documentation/Live-Workflow-Test-2026-03-05/Endpoint-Notes.md`, `code=unresolved_reference`, message contains `pageId=17727489#Task-list` | Seed content now includes another unresolved same-space task-list anchor reference. |
| `TD2` | `path=Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md`, `code=unresolved_reference`, message contains `pageId=17727489` | Seed content now includes another unresolved same-space page reference. |
| `TD2` | `path=Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md`, `code=unresolved_reference`, message contains `pageId=17530900#Task-list` | Seed content still links to an unresolved remote target. |
| `TD2` | `path=Technical-Documentation/Live-Workflow-Test-2026-03-05/Checklist-and-Diagrams.md`, message contains `UNKNOWN_MEDIA_ID` | Seed content still contains unresolved media fallback output. |
| `SD2` | `path=Software-Development/Release-Sandbox-2026-03-05.md`, `code=unresolved_reference`, message contains `pageId=17334539` | Seed content still links to an unresolved remote target. |

If these spaces are cleaned up later, remove the allowlist entries in the same change that removes the warnings.

## Live Sandbox Release Checklist

Use this checklist for a release candidate. It turns the 2026-03-09 one-off live verification into a repeatable gate.

1. Confirm the target spaces are explicit non-production sandboxes and that the `CONF_E2E_*` environment variables point to them.
2. Run `make release-check`.
3. Treat any failure in `TestWorkflow_SandboxBaselineDiagnosticsAllowlist` as baseline noise that must be cleaned up or documented before release.
4. Treat any failure in `TestWorkflow_EndToEndCleanupParity` as a release blocker because it means the workflow did not return the sandbox to a clean state.
5. Run the manual smoke workflow below if you need human review of operator prompts, diffs, or recovery messaging in addition to the automated live suite.
6. Capture the release artifacts:
   - `make release-check` output
   - any retained recovery commands or branch names from failed push scenarios
   - final `git status --short` and `conf status <SPACE>` output from manual smoke workspaces, if the manual runbook was used
7. Approve the release only if:
   - `fmt-check`, `lint`, unit tests, and live E2E all pass
   - baseline diagnostics are limited to the documented allowlist
   - cleanup parity leaves the sandbox with clean `git status` and clean `conf status`
   - temporary live-test workspaces and scratch content are removed or restored

Failure triage:

- Unexpected baseline diagnostics: update the sandbox seed content first; only add to the allowlist when the warning is understood and intentionally accepted.
- Live E2E write-path failures: inspect the scratch pages directly in Confluence, then rerun only after the sandbox is back in a known state.
- Cleanup-parity failures: verify archived/deleted scratch pages, force-pull the affected space, and confirm both `git status` and `conf status` are clean before rerunning the gate.

## Live Sandbox Smoke-Test Runbook

Use this runbook for manual live verification against an explicit non-production Confluence space. It is intentionally operator-driven and repeatable; do **not** run it in the repository root and do **not** point it at production content.

### Preconditions And Guardrails

Before you start:

- use a dedicated sandbox space key that is approved for destructive testing,
- run in a temporary workspace directory **outside** the `confluence-markdown-sync` repository,
- use a dedicated scratch page in that sandbox space (do not edit shared team pages),
- keep `id` and `space` frontmatter unchanged, and do not hand-edit `version`,
- expect to restore the scratch page to its original content before deleting the workspace,
- stop immediately if the target space is not clearly non-production.

Recommended environment contract:

```powershell
$RepoRoot      = 'C:\Dev\confluence-markdown-sync'
$Conf          = Join-Path $RepoRoot 'conf.exe'
$env:CONF_LIVE_PRIMARY_SPACE_KEY = 'SANDBOX'
$env:CONF_LIVE_SECONDARY_SPACE_KEY = 'SANDBOX2' # optional, for cross-space smoke tests
$SandboxSpace  = $env:CONF_LIVE_PRIMARY_SPACE_KEY
$SmokeRoot     = Join-Path $env:TEMP ("conf-live-smoke-" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
$WorkspaceA    = Join-Path $SmokeRoot 'workspace-a'
$WorkspaceB    = Join-Path $SmokeRoot 'workspace-b'
```

Credentials must already be available through `ATLASSIAN_DOMAIN`, `ATLASSIAN_EMAIL`, and `ATLASSIAN_API_TOKEN` (or the legacy `CONFLUENCE_*` names). Build `conf` from the repo root first if needed:

```powershell
Set-Location $RepoRoot
make build
```

### 1. Bootstrap Two Isolated Sandbox Workspaces

Use two workspaces so you can exercise both the happy path and a real remote-ahead conflict.

```powershell
New-Item -ItemType Directory -Force -Path $WorkspaceA, $WorkspaceB | Out-Null

Set-Location $WorkspaceA
& $Conf init
& $Conf pull $SandboxSpace --yes --non-interactive --skip-missing-assets --force

Set-Location $WorkspaceB
& $Conf init
& $Conf pull $SandboxSpace --yes --non-interactive --skip-missing-assets --force
```

After the first pull, pick one existing scratch page in the sandbox and set its relative path explicitly in both workspaces. Example:

```powershell
$ScratchRelative = 'SANDBOX\Smoke Tests\CLI Smoke Test Scratch.md'
$ScratchFileA    = Join-Path $WorkspaceA $ScratchRelative
$ScratchFileB    = Join-Path $WorkspaceB $ScratchRelative
Copy-Item $ScratchFileA "$ScratchFileA.pre-smoke.bak" -Force
```

If the scratch page does not already exist, create it manually in the sandbox first and rerun `conf pull`; do not improvise with a production page.

### 2. Run The Pull -> Edit -> Validate -> Diff -> Push -> Pull Cycle

Append a timestamped marker to the scratch page without touching frontmatter:

```powershell
$StampA = Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'
Add-Content -Path $ScratchFileA -Value "`nSmoke test marker A: $StampA`n"

Set-Location $WorkspaceA
& $Conf validate $ScratchFileA
& $Conf diff $ScratchFileA
& $Conf push $ScratchFileA --yes --non-interactive --on-conflict=cancel
& $Conf pull $SandboxSpace --yes --non-interactive
git --no-pager status --short
```

Expected outcome:

- `validate` succeeds (warnings may appear, but there should be no hard failures),
- `diff` shows only the intended scratch-page change,
- `push` succeeds without touching unrelated pages,
- the follow-up `pull` leaves the workspace clean except for the intentional scratch-page edit now reflected in Git history/state.

### 3. Simulate A Real Remote-Ahead Conflict

`WorkspaceB` is still based on the pre-push state, so it can be used to simulate a genuine conflict against the same page.

```powershell
$StampB = Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'
Add-Content -Path $ScratchFileB -Value "`nSmoke test marker B: $StampB`n"

Set-Location $WorkspaceB
& $Conf validate $ScratchFileB
& $Conf push $ScratchFileB --yes --non-interactive --on-conflict=pull-merge
```

Expected outcome:

- `push` detects that the remote page is ahead,
- `--on-conflict=pull-merge` triggers a pull of the newer remote state,
- the command stops for operator review instead of silently overwriting the remote page.

Inspect the result before resolving:

```powershell
git --no-pager status --short
git --no-pager diff -- $ScratchRelative
```

Then resolve the scratch page in `WorkspaceB` so it contains the final intended test content, validate again, preview it, and complete the push:

```powershell
& $Conf validate $ScratchFileB
& $Conf diff $ScratchFileB
& $Conf push $ScratchFileB --yes --non-interactive --on-conflict=cancel
& $Conf pull $SandboxSpace --yes --non-interactive
```

If you specifically want to exercise interactive pull conflict handling, keep an uncommitted edit in `WorkspaceB`, run `conf pull $ScratchFileB` **without** `--non-interactive`, and verify the `Keep both` / `Use Remote` / `Use Local` prompt flow described earlier in this document.

### 4. Cleanup And Restore Expectations

The sandbox should end the smoke test in the same remote state it started from. Restore the original scratch-page content from the backup captured in `WorkspaceA`, then push that restoration before deleting the temporary workspaces.

```powershell
Copy-Item "$ScratchFileA.pre-smoke.bak" $ScratchFileA -Force

Set-Location $WorkspaceA
& $Conf validate $ScratchFileA
& $Conf diff $ScratchFileA
& $Conf push $ScratchFileA --yes --non-interactive --on-conflict=cancel
& $Conf pull $SandboxSpace --yes --non-interactive

Remove-Item $SmokeRoot -Recurse -Force
```

Cleanup checklist:

- the scratch page content is restored (or intentionally left in a known baseline state for the next run),
- no temporary workspace under `$SmokeRoot` remains,
- no live sandbox content was ever pulled into the repository root,
- any unexpected diagnostics or partial rollback messages are captured before the next release candidate is approved.

## CI Pipeline Example

```yaml
name: docs-sync-check

on:
  workflow_dispatch:
  schedule:
    - cron: "0 */6 * * *"

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.x'

      - name: Build conf
        run: go build -o conf ./cmd/conf

      - name: Pull docs
        env:
          ATLASSIAN_DOMAIN: ${{ secrets.ATLASSIAN_DOMAIN }}
          ATLASSIAN_EMAIL: ${{ secrets.ATLASSIAN_EMAIL }}
          ATLASSIAN_API_TOKEN: ${{ secrets.ATLASSIAN_API_TOKEN }}
        run: ./conf pull ENG --yes --non-interactive --skip-missing-assets --force

      - name: Validate docs
        env:
          ATLASSIAN_DOMAIN: ${{ secrets.ATLASSIAN_DOMAIN }}
          ATLASSIAN_EMAIL: ${{ secrets.ATLASSIAN_EMAIL }}
          ATLASSIAN_API_TOKEN: ${{ secrets.ATLASSIAN_API_TOKEN }}
        run: ./conf validate ENG
```

## Operational Notes

- `push` always runs `validate` before remote writes.
- A Git remote is not required for `conf` operations.
- Sync state is local (`.confluence-state.json`) and should remain gitignored.
- After non-no-op syncs, use generated tags (`confluence-sync/pull/...`, `confluence-sync/push/...`) for audit and recovery checkpoints.
