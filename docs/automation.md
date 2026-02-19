# Automation and CI

This document explains how to run `cms` safely in scripts and CI pipelines.

## Automation Flags

Supported on `pull` and `push`:

- `--yes`
  - auto-approves safety confirmations.
- `--non-interactive`
  - disables prompts,
  - fails fast when a decision is required and not provided.

Additional push flag:

- `--on-conflict=pull-merge|force|cancel`
  - required with `push --non-interactive`.

## Safety Confirmation Rules

`cms` requires confirmation when an operation:

- affects more than 10 Markdown files, or
- includes delete operations.

Behavior:

- interactive mode: prompt user,
- `--yes`: auto-approve,
- `--non-interactive` without `--yes`: command fails.

## Conflict Policy in Push

When remote versions are ahead:

- `pull-merge`: stop and reconcile via pull + merge workflow.
- `force`: overwrite based on remote head.
- `cancel`: stop without remote writes.

In non-interactive usage, set one explicitly.

## Recommended Non-Interactive Commands

```powershell
cms pull ENG --yes --non-interactive
cms validate ENG
cms push ENG --yes --non-interactive --on-conflict=cancel
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

      - name: Build cms
        run: go build -o cms .

      - name: Pull docs
        env:
          ATLASSIAN_DOMAIN: ${{ secrets.ATLASSIAN_DOMAIN }}
          ATLASSIAN_EMAIL: ${{ secrets.ATLASSIAN_EMAIL }}
          ATLASSIAN_API_TOKEN: ${{ secrets.ATLASSIAN_API_TOKEN }}
        run: ./cms pull ENG --yes --non-interactive

      - name: Validate docs
        env:
          ATLASSIAN_DOMAIN: ${{ secrets.ATLASSIAN_DOMAIN }}
          ATLASSIAN_EMAIL: ${{ secrets.ATLASSIAN_EMAIL }}
          ATLASSIAN_API_TOKEN: ${{ secrets.ATLASSIAN_API_TOKEN }}
        run: ./cms validate ENG
```

## Operational Notes

- `push` always runs `validate` before remote writes.
- A Git remote is not required for `cms` operations.
- Sync state is local (`.confluence-state.json`) and should remain gitignored.
