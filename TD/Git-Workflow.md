---
title: Git Workflow
id: "5046429"
space: TD
version: 2
labels:
    - developer-guide
    - git
    - workflow
author: Robert Gonek
created_at: "2026-02-24T14:56:11Z"
last_modified_at: "2026-02-24T14:56:12Z"
last_modified_by: Robert Gonek
---
# Git Workflow

Luminary uses **trunk-based development**. There is one long-lived branch — `main` — and engineers work on short-lived feature branches that merge back to main frequently. This keeps integration costs low and makes CI feedback fast.

## Core Principles

- **Short-lived branches**: Feature branches should not live longer than **3 days**. If a branch is taking longer, it's a sign the change should be broken into smaller pieces. Branches older than 5 days will show up in the weekly `#eng-housekeeping` Slack digest.
- **Small, focused PRs**: PRs should do one thing. Mixing a refactor with a feature makes review harder and rollback harder.
- **Main is always deployable**: Every commit that lands on main must pass CI and be safe to deploy. Never merge a PR that breaks the build.
- **No force-push to main**: Branch protection rules enforce this. If you think you need to force-push to main, you don't — ask in `#eng-platform`.

## Branch Naming

```
<type>/<short-description>
```

Examples:

- `feat/workspace-export-api`
- `fix/session-token-refresh-race`
- `refactor/query-service-connection-pool`
- `chore/update-go-deps`
- `docs/api-versioning-guide`

No ticket numbers required in the branch name (they go in the PR body), but they're fine to include: `feat/ENG-4201-workspace-export`.

## Commit Message Conventions

We follow [Conventional Commits](https://www.conventionalcommits.org/). Every commit message must start with a type prefix:

| Type | When to Use |
| --- | --- |
| `feat` | A new feature or behavior visible to users or API consumers |
| `fix` | A bug fix |
| `refactor` | Code restructuring with no behavior change |
| `chore` | Dependency updates, tooling, CI changes — nothing that affects runtime behavior |
| `docs` | Documentation only changes |
| `test` | Adding or updating tests with no production code change |
| `perf` | Performance improvement |

Format:

```
<type>(<scope>): <imperative short description>

<optional body>

<optional footer>
```

Examples:

```
feat(query-api): add field selection via ?fields= parameter

Implements sparse fieldset selection for GET /workspaces, GET /events,
and GET /reports. Closes ENG-4102.

feat(auth): extend token expiry to 30 minutes for enterprise workspaces

fix(worker): prevent duplicate export jobs on connection retry

chore(deps): update go.mod to Go 1.22.3
```

The short description is imperative ("add" not "added", "prevent" not "preventing"). Keep it under 72 characters.

## Squash Merge Policy

All PRs are squash-merged into main. This means:

- Your branch can have any number of WIP commits with any commit messages
- What lands on main is a single commit
- **The PR title becomes the commit message on main** — write your PR title following the conventional commit format above
- The PR body becomes the commit description and is included in the squash commit

This means messy intermediate commits on feature branches are fine. What matters is the PR title.

## Pull Request Process

1. Push your branch and open a PR. Fill in the PR template (it auto-populates from `.github/PULL_REQUEST_TEMPLATE.md`).
2. CI must be green before requesting review. Don't request review on a failing PR.
3. At least one approval from someone who didn't write the code.
4. Resolve all review comments before merging (or explicitly mark as "won't fix" with a reason).
5. Squash and merge using the GitHub UI. Delete the branch after merge (GitHub will auto-delete if configured).

## Tagging Releases

Services are tagged at deploy time by CI, not manually. The pattern is:

```
<service>/<semver>
```

Example: `query-service/v2.4.1`

If you need to tag a release manually (e.g. for the SDK which follows its own release cycle):

```shell
git tag -a sdk/v3.2.1 -m "SDK v3.2.1: Add React Native SDK, fix iOS background flush"
git push origin sdk/v3.2.1
```

Annotated tags (`-a`) are required for release tags so the message is preserved.

## Dealing with Merge Conflicts

If your branch is behind main and you have conflicts:

```shell
git fetch origin
git rebase origin/main
# Fix conflicts, then:
git add <conflicted files>
git rebase --continue
```

Prefer rebase over merge for updating feature branches. A merge commit on a short-lived feature branch adds noise with no benefit.

If the conflict is in a generated file (e.g. `go.sum`, `pnpm-lock.yaml`), regenerate it rather than manually resolving:

```shell
# go.sum
go mod tidy

# pnpm-lock.yaml
pnpm install
```

## Useful Git Aliases

Add these to your `~/.gitconfig` under `[alias]`:

```ini
[alias]
    # Short status
    st = status -sb

    # Pretty log with graph
    lg = log --oneline --graph --decorate --all

    # Log with author and relative date (great for PRs)
    who = log --oneline --format="%C(yellow)%h%Creset %C(blue)%an%Creset %ar — %s"

    # Undo last commit but keep changes staged
    undo = reset --soft HEAD~1

    # List branches sorted by last commit date
    recent = branch --sort=-committerdate --format='%(committerdate:relative)%09%(refname:short)'

    # Push current branch to origin (sets upstream automatically)
    pushup = push -u origin HEAD

    # Squash all commits on current branch into one (interactive)
    squash-branch = "!git rebase -i $(git merge-base HEAD origin/main)"

    # Show what files changed in the last commit
    last = diff HEAD~1..HEAD --name-only
```

Popular team dotfiles are shared in the `#eng-dotfiles` Slack channel if you want more.

## Related

- [GitHub Actions Reference](https://placeholder.invalid/page/infrastructure%2Fgithub-actions-reference.md) — CI workflows
- [Writing Runbooks](https://placeholder.invalid/page/developer-guide%2Fwriting-runbooks.md)
- [Onboarding to On-Call](https://placeholder.invalid/page/developer-guide%2Fonboarding-to-on-call.md)
