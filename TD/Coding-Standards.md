---
title: Coding Standards
id: "7274584"
space: TD
version: 2
labels:
    - developer-guide
    - standards
    - go
    - typescript
    - sql
author: Robert Gonek
created_at: "2026-02-24T14:55:57Z"
last_modified_at: "2026-02-24T14:55:58Z"
last_modified_by: Robert Gonek
---
# Coding Standards

This document captures Luminary's engineering coding standards. It is not a style guide for its own sake — every rule here exists because we have run into the problem it prevents. New engineers should read this before their first PR.

These standards apply to production code. Throwaway scripts, one-off migrations, and prototypes have more latitude, but should be cleaned up before they outlive their purpose.

## Table of Contents

- [Go Standards](#go-standards)
- [TypeScript and React Standards](#typescript-and-react-standards)
- [SQL Standards](#sql-standards)
- [Linting Tools](#linting-tools)
- [Things We Have Stopped Arguing About](#things-we-have-stopped-arguing-about)

---

## Go Standards

### Error Handling

**Always wrap errors with context using** `%w`**.**

```go
// Bad
if err != nil {
    return err
}

// Good
if err != nil {
    return fmt.Errorf("fetching workspace %s: %w", workspaceID, err)
}
```

The `%w` verb (not `%v`) allows callers to use `errors.Is` and `errors.As` for type-checking and sentinel matching. If you use `%v`, you destroy the error chain and make debugging significantly harder.

Error messages should describe *what was being attempted*, not *what went wrong* (the original error provides the "what went wrong" part). The resulting error chain reads as a stack from outermost to innermost:

```
serving HTTP request: processing query: fetching workspace ws_889f: context deadline exceeded
```

**Do not panic in library code.**

Panics are not recoverable in a predictable way from a caller's perspective. Code that panics in a helper function can crash the entire HTTP server's goroutine pool. If a condition truly cannot be handled (e.g., nil pointer from a required dependency that was not injected), return a clear error at initialization time rather than panicking at call time.

The only acceptable use of `panic` in Luminary Go code is in `main()` or `init()` for unrecoverable startup failures, and even there, prefer `log.Fatal` so the logger gets a chance to flush.

**Do not use** `errors.New` **for sentinel errors in packages with external callers.** Use unexported struct types implementing the `error` interface so callers can use `errors.As` with type information:

```go
// In the luminary-go SDK (public API)
type QueueFullError struct {
    QueueSize int
    Dropped   int
}

func (e *QueueFullError) Error() string {
    return fmt.Sprintf("event queue full (size=%d): dropped %d events", e.QueueSize, e.Dropped)
}

// Caller
var queueErr *luminary.QueueFullError
if errors.As(err, &queueErr) {
    log.Warn("luminary queue full", zap.Int("dropped", queueErr.Dropped))
}
```

### Context Propagation

Every function that does I/O or that calls a function that does I/O must accept `context.Context` as its first argument. No exceptions.

```go
// Bad — no way to cancel or set a deadline
func (r *WorkspaceRepo) FindByID(id string) (*Workspace, error)

// Good
func (r *WorkspaceRepo) FindByID(ctx context.Context, id string) (*Workspace, error)
```

Do not store contexts in structs. Contexts are request-scoped; structs are longer-lived. Storing a context in a struct is almost always a sign that the struct's design needs revisiting.

Do not create your own background contexts mid-call-chain (`context.Background()` inside a handler). Thread the incoming context through. The only place to create a fresh background context is in `main()` and in test setup.

### Interface Design

Define interfaces at the point of consumption, not the point of implementation.

```go
// Bad: defined in the package that implements it
// pkg/cache/redis.go
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}
type RedisCache struct { ... }
func (c *RedisCache) Get(...) { ... }

// Good: defined in the package that uses it
// pkg/query/cache.go
type queryCache interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}
```

This keeps interfaces small (only what the consumer needs), makes mocking trivial, and avoids circular imports.

Keep interfaces small. An interface with more than 3-4 methods is a sign that a dependency is doing too much. Prefer multiple small interfaces over one large one (`io.Reader`, `io.Writer`, `io.Closer` rather than `io.ReadWriteCloser` in most cases).

### Goroutines

Never start a goroutine without knowing how it will end. Every `go func()` must have a clear termination condition — a channel close, a context cancellation, or a lifecycle that outlives it.

```go
// Bad — goroutine leaks if the caller returns
go func() {
    for event := range eventCh {
        process(event)
    }
}()

// Good — goroutine is tied to a WaitGroup and the caller waits for it
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    for event := range eventCh {
        process(event)
    }
}()
// ... and wg.Wait() is called somewhere appropriate
```

---

## TypeScript and React Standards

### Components

Use functional components exclusively. Class components have been banned since React 18. If you encounter a class component in a PR, the reviewer should request a rewrite.

```tsx
// Bad
class Dashboard extends React.Component<DashboardProps, DashboardState> { ... }

// Good
function Dashboard({ workspaceId, title }: DashboardProps) {
  // ...
}
```

Export components as named exports, not default exports. Default exports make refactoring harder (the component name is not enforced by the import) and confuse tree-shaking in some bundler configurations.

```tsx
// Bad
export default function Dashboard() { ... }

// Good
export function Dashboard() { ... }
```

### Custom Hooks

Custom hooks must begin with `use`. This is a React rule, not just a convention — the linter enforces it and React's reconciler uses it for hook call order detection.

Name hooks after what they return, not how they work:

```tsx
// Bad: describes implementation
function useFetchWorkspaceFromAPI(id: string) { ... }

// Good: describes the value
function useWorkspace(id: string): { workspace: Workspace | null; loading: boolean; error: Error | null } { ... }
```

Hooks that encapsulate complex async state should return a standard shape: `{ data, loading, error }`. Use `null` for `data` when not yet loaded (not `undefined`), and `null` for `error` when there is no error.

### Props and Types

Use `interface` for component props, not `type`:

```tsx
// Preferred (interfaces are extensible and produce better error messages)
interface ButtonProps {
  label: string;
  onClick: () => void;
  variant?: 'primary' | 'secondary' | 'ghost';
  disabled?: boolean;
}
```

Do not use `React.FC` or `React.FunctionComponent`. It adds implicit `children` prop typing that is incorrect since React 18 removed implicit children from all components.

```tsx
// Bad
const Button: React.FC<ButtonProps> = ({ label, onClick }) => { ... }

// Good
function Button({ label, onClick, variant = 'primary', disabled = false }: ButtonProps) { ... }
```

### Barrel Exports

Avoid barrel exports (`index.ts` files that re-export from many modules). They cause:

- Slower TypeScript language server performance.
- Unnecessary module evaluation in test environments.
- Circular dependency risks that are hard to debug.

If you are creating a published package (e.g., an internal component library), a single barrel export at the package root is acceptable. Within an application, import directly from source files.

---

## SQL Standards

### Parameterized Queries

Always use parameterized queries. Never interpolate user input into SQL strings.

```python
# Bad
cursor.execute(f"SELECT * FROM events WHERE workspace_id = '{workspace_id}'")

# Good
cursor.execute("SELECT * FROM events WHERE workspace_id = %s", (workspace_id,))
```

```go
// Bad
db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM events WHERE workspace_id = '%s'", workspaceID))

// Good
db.QueryContext(ctx, "SELECT * FROM events WHERE workspace_id = $1", workspaceID)
```

This applies to ClickHouse queries, Postgres queries, Redis Lua scripts (for parameterizable parts), and any other data store that accepts queries.

### No SELECT *

Never use `SELECT *` in application code. Explicit column lists:

- Prevent bugs when columns are added/removed/reordered.
- Make query intent clear for reviewers.
- Allow the database query planner to choose more efficient execution plans.

```sql
-- Bad
SELECT * FROM workspaces WHERE id = $1

-- Good
SELECT id, name, plan, created_at, owner_user_id
FROM workspaces
WHERE id = $1
```

`SELECT *` is acceptable in `psql` or ClickHouse exploratory sessions. Not in code that ships.

### Migration Naming

Database migrations use a timestamp prefix and a descriptive name:

```
YYYYMMDDHHMMSS_<description_in_snake_case>.sql
```

Examples:

```
20250114120000_add_workspace_sso_config.sql
20250115083000_backfill_user_plan_column.sql
20250118160000_create_audit_log_table.sql
```

The timestamp is in UTC. Generate it with `date -u +%Y%m%d%H%M%S`.

Migration files must be immutable once merged — never edit an existing migration file. If a migration has a mistake, write a new migration to correct it.

Destructive migrations (dropping columns, tables, or indexes) require a two-step process: (1) a migration to mark the column/table as deprecated and stop writing to it, (2) a separate migration deployed at least one release later to perform the actual drop. This ensures rollback is possible.

---

## Linting Tools

| Language | Linter | Config File | Notes |
| --- | --- | --- | --- |
| Go | `golangci-lint` | `.golangci.yml` in each repo root | Runs `staticcheck`, `gosec`, `errcheck`, `revive`. All enabled rules are blocking in CI. |
| Go | `go vet` | N/A | Runs as part of `golangci-lint`. |
| TypeScript | ESLint | `.eslintrc.json` | Extends `@luminary/eslint-config`. Rules for React hooks, no `any`, no default exports from non-entry files. |
| TypeScript | TypeScript compiler | `tsconfig.json` | `"strict": true` is mandatory. No `@ts-ignore` without a comment explaining why. |
| CSS/SCSS | Stylelint | `.stylelintrc.json` | BEM naming enforced for new components. |
| Python | Ruff | `pyproject.toml` | Replaces `flake8` + `isort`. `ruff check` and `ruff format`. |
| Python | mypy | `mypy.ini` | Strict mode for SDK code; standard mode for service code. |
| SQL | `sqlfluff` | `.sqlfluff` | Dialect set to `postgres` for migrations, `clickhouse` for analytics queries. |

All linters run in CI on every pull request. A linting failure blocks merge. Do not add `// nolint` or `# noqa` comments without a comment explaining the specific reason.

---

## Things We Have Stopped Arguing About

These are settled decisions. The time for debate has passed. If you disagree with one of these, open a proposal in the `#engineering-standards` Slack channel — do not relitigate it in PR comments.

### Formatting

- **Go:** `gofmt` (via `goimports`). No configuration. The formatter is the standard.
- **TypeScript/JavaScript:** Prettier with our shared config (`@luminary/prettier-config`). Run on save in your editor; enforced by `lint-staged` on commit.
- **Python:** `ruff format`. Replaces Black. Not configurable.

Formatting PRs are not reviewed for anything other than "does it pass CI." We do not debate brace placement, indentation width, or line length. These are solved problems.

### `var` in JavaScript

Do not use `var`. Use `const` for everything; use `let` when reassignment is necessary. `var` is banned by the ESLint config.

### Error Handling in Go: `log.Fatal` vs `panic` vs `os.Exit`

- `log.Fatal` in `main()` for startup failures. Flushes the logger.
- `os.Exit` only if the logger itself is broken.
- `panic` only in truly unreachable code paths (`default` branches of exhaustive switches where the compiler cannot enforce exhaustiveness). Always add a comment explaining why.

### Commit Message Format

[Conventional Commits](https://www.conventionalcommits.org/) is required for commits that will be cherry-picked or that auto-generate changelogs. For day-to-day feature work on long-lived branches, it is recommended but not enforced. Squash merge titles on PRs must follow Conventional Commits since they become the effective commit message on `main`.

### Single vs Double Quotes in TypeScript

Prettier enforces single quotes. This is not a debate.

### Named vs Anonymous Functions for Event Handlers in React

Always use named functions for non-trivial handlers. Anonymous arrow functions in JSX create a new function reference on every render, which can cause unnecessary re-renders in child components:

```tsx
// Bad for performance and stack traces
<button onClick={() => handleSubmit(form.values)}>Submit</button>

// Good
const handleSubmitClick = useCallback(() => handleSubmit(form.values), [form.values]);
<button onClick={handleSubmitClick}>Submit</button>
```

For truly trivial handlers (e.g., `onClick={() => setOpen(false)}`), the performance impact is negligible and inline is fine. Use judgment.
