# Automation and CI

This document explains how to run `conf` safely in scripts and CI pipelines.

## Automation Flags

Supported on `pull` and `push`:

- `--yes`
  - auto-approves safety confirmations.
- `--non-interactive`
  - disables prompts,
  - fails fast when a decision is required and not provided.

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

- `pull-merge`: when a remote-ahead conflict is detected, `push` triggers `pull`, then stops so you can review/resolve and retry push.
- `force`: overwrite based on remote head.
- `cancel`: stop without remote writes.

In non-interactive usage, set one explicitly.

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
- `ROLLBACK_ATTACHMENT_DELETED` / `ROLLBACK_ATTACHMENT_FAILED`
- `ROLLBACK_PAGE_DELETED` / `ROLLBACK_PAGE_DELETE_FAILED`

If any `*_FAILED` code appears, treat the run as partial and inspect the referenced page before retrying.

## Dry-Run Behavior (`push --dry-run`)

`--dry-run` simulates remote actions and conversion without mutating Confluence or local Git state.

Use it to verify:

- changed markdown scope,
- planned page operations,
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
