# confluence-markdown-sync

Write docs like code. Publish to Confluence with confidence. ✍️

`conf` is a Go CLI that keeps Confluence pages and local Markdown in sync, so teams can use editor + Git + CI workflows without giving up Confluence as the publishing platform.

## Why teams use `conf` ✨
- 📝 Markdown-first authoring with Confluence as the destination.
- 🛡️ Safe sync model with validation before remote writes.
- 👀 Clear preview step via `conf diff` before push.
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

`conf pull` mirrors Confluence hierarchy locally by placing folders and child pages in nested directories. Pages with children use `<Page>/<Page>.md` so they are distinct from pure folders.

## Quick flow 🔄

```powershell
# 1) Pull a Confluence space
conf pull ENG

# Force a full-space refresh (ignore incremental change detection)
conf pull ENG --force

# 2) Validate local markdown
conf validate ENG

# 3) Preview local vs remote
conf diff ENG

# 4) Push local changes
conf push ENG --on-conflict=cancel
```

## At a glance 👀
- Commands: `init`, `pull [TARGET]`, `push [TARGET]`, `validate [TARGET]`, `diff [TARGET]`
- Target rule: `.md` suffix means file mode; otherwise space mode (`SPACE_KEY`)
- Required auth: `ATLASSIAN_DOMAIN`, `ATLASSIAN_EMAIL`, `ATLASSIAN_API_TOKEN`
- Git remote is optional (local Git is enough)

## Docs 📚
- Usage and command reference: `docs/usage.md`
- Automation and CI behavior: `docs/automation.md`

## Development 🧑‍💻
- `make build`
- `make test`
- `make fmt`
- `make lint`
