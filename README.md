# confluence-markdown-sync

Write docs like code. Publish to Confluence with confidence. ✍️

> **Beta** — `conf` is under active development. Core sync workflows (pull, push, validate, diff, status) are tested against live Confluence tenants, but edge cases remain. Pin a specific version for production use and test changes in a sandbox space before relying on new releases.

`conf` is a Go CLI that keeps Confluence pages and local Markdown in sync, so teams can use editor + Git + CI workflows without giving up Confluence as the publishing platform.

## Why teams use `conf` ✨
- 📝 Markdown-first authoring with Confluence as the destination.
- 🛡️ Safe sync model with validation before remote writes.
- 👀 Clear preview step via `conf diff` for tracked pages and `conf push --preflight` for brand-new files.
- 🔎 Local full-text search across synced Markdown with SQLite or Bleve backends.
- 🤖 Works in local repos and automation pipelines.

## Install 🛠️

### Build from source 🧱
```powershell
git clone https://github.com/rgonek/confluence-markdown-sync.git
cd confluence-markdown-sync
go build -o conf ./cmd/conf
```

### Install with Go ⚡
```powershell
go install github.com/rgonek/confluence-markdown-sync/cmd/conf@latest
```

## Init a workspace 🚀

Inside the folder/repo where you want synced docs:

```powershell
conf init
```

`conf init` prepares Git metadata, `.gitignore`, and `.env` scaffolding, and creates an initial commit when it initializes a new Git repository.
If `ATLASSIAN_*` or legacy `CONFLUENCE_*` credentials are already set in the environment, `conf init` writes `.env` from them without prompting.

`conf pull` mirrors Confluence hierarchy locally by placing folders and child pages in nested directories. Pages with children use `<Page>/<Page>.md` so they are distinct from pure folders. Leaf-page title renames can keep the existing Markdown path when the effective parent directory is unchanged, but pages that own subtree directories move when their self-owned directory segment changes. Hierarchy moves and ancestor/path-segment sanitization changes are surfaced as `PAGE_PATH_MOVED` notes in `conf pull`/`conf diff`, and `conf status` previews tracked moves before the next pull.

## Quick flow 🔄
> ⚠️ **IMPORTANT**: If you are developing `conf` itself, NEVER run sync commands against real Confluence spaces in the repository root. This prevents accidental commits of synced documentation. Use a separate sandbox folder.

```powershell
# 1) Pull a Confluence space
conf pull ENG

# Force a full-space refresh (ignore incremental change detection)
conf pull ENG --force

# 2) Validate local markdown
conf validate ENG

# 3) Preview local vs remote
conf diff ENG

# Preview a brand-new file before its first push
conf push .\ENG\New-Page.md --preflight

# 4) Push local changes
conf push ENG --on-conflict=cancel
```

## At a glance 👀
- Commands: `init`, `init agents [TARGET]`, `pull [TARGET]`, `push [TARGET]`, `recover`, `status [TARGET]`, `clean`, `validate [TARGET]`, `diff [TARGET]`, `relink [TARGET]`, `search QUERY`
- Version: `conf version` or `conf --version`
- Target rule: `.md` suffix means file mode; otherwise space mode (`SPACE_KEY`)
- Required auth: `ATLASSIAN_DOMAIN`, `ATLASSIAN_EMAIL`, `ATLASSIAN_API_TOKEN`
- Extension support: PlantUML is the only first-class rendered extension handler; Mermaid is preserved as code, and raw `adf:extension` / unknown macro handling is best-effort and should be sandbox-validated before relying on it
- Cross-space links are preserved as readable remote links rather than rewritten to local Markdown paths
- Removing tracked Markdown pages archives the corresponding remote page; follow-up pull removes the archived page from tracked local state
- Status scope: `conf status` reports Markdown page drift only; use `git status` or `conf diff` for attachment-only changes
- Label rules: labels are trimmed, lowercased, deduplicated, and sorted; empty labels and labels containing whitespace are rejected
- Search filters: `--space`, repeatable `--label`, `--heading`, `--created-by`, `--updated-by`, date bounds, and `--result-detail`
- Git remote is optional (local Git is enough)

## Docs 📚
- Usage and command reference: `docs/usage.md`
- Feature and tenant compatibility matrix: `docs/compatibility.md`
- Automation, CI behavior, and live sandbox smoke-test runbook: `docs/automation.md`
- Changelog: `CHANGELOG.md`
- Security policy: `SECURITY.md`
- Support policy: `SUPPORT.md`
- License: `LICENSE`

## Extension and macro support 🧩

| Item | Support level | What `conf` does | Notes |
|------|---------------|------------------|-------|
| PlantUML (`plantumlcloud`) | Rendered round-trip support | Pull converts the Confluence extension into Markdown with a managed `adf-extension` wrapper and `puml` body; push reconstructs the Confluence macro. | This is the only first-class custom extension handler in the repo today. |
| Mermaid | Preserved but not rendered | Keeps Mermaid as fenced code in Markdown and pushes it back as an ADF `codeBlock` with `language: mermaid`. | `conf validate`/`conf push` warn with `MERMAID_PRESERVED_AS_CODEBLOCK` so the render downgrade is explicit. |
| Raw ADF extension preservation | Best-effort preservation only | Unhandled extension nodes can fall back to raw ```` ```adf:extension ```` JSON blocks so the original ADF payload can be carried through Markdown with minimal interpretation. | This is a low-level escape hatch, not a rendered feature contract or a verified end-to-end round-trip guarantee. Validate any workflow that depends on it in a sandbox before relying on it. |
| Unknown Confluence macros/extensions | Unsupported as a first-class feature | `conf` does not ship custom handlers for unknown macros, beyond whatever best-effort raw ADF preservation may be possible for some remote extension payloads. | Do not assume unknown macros will round-trip or render correctly. Push can still fail if Confluence rejects the macro or if the instance does not have the required app installed; sandbox validation is recommended before depending on this path. |

## Development 🧑‍💻
- `make build`
- `make test`
- `make fmt`
- `make lint`
