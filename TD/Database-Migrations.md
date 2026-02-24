---
title: Database Migrations
id: "5701832"
space: TD
version: 2
labels:
    - developer-guide
    - database
    - migrations
    - postgresql
author: Robert Gonek
created_at: "2026-02-24T14:56:01Z"
last_modified_at: "2026-02-24T14:56:03Z"
last_modified_by: Robert Gonek
---
# Database Migrations

Luminary uses [golang-migrate](https://github.com/golang-migrate/migrate) to manage PostgreSQL schema changes. All DDL changes must go through the migration system — no manual `ALTER TABLE` or `CREATE INDEX` statements against any environment.

---

## Table of Contents

1. [Naming Conventions](#naming-conventions)
2. [Writing Up and Down Migrations](#writing-up-and-down-migrations)
3. [Running Migrations](#running-migrations)
4. [Zero-Downtime Migrations: Expand/Contract Pattern](#zero-downtime-migrations-expandcontract-pattern)
5. [Common Pitfalls](#common-pitfalls)

---

## Naming Conventions

Migration files live in `migrations/` at the repository root. Each migration is a pair of files:

```
migrations/
  0001_create_workspaces.up.sql
  0001_create_workspaces.down.sql
  0002_add_workspaces_plan_column.up.sql
  0002_add_workspaces_plan_column.down.sql
  0003_create_reports_table.up.sql
  0003_create_reports_table.down.sql
```

Rules:

- Prefix with a **zero-padded 4-digit sequence number**: `0042_`, `0043_`.
- Sequence numbers must be **monotonically increasing** and have **no gaps**. CI will fail if gaps exist.
- Use **snake_case** for the descriptive suffix. Be specific: `0027_add_reports_archived_at_index` is better than `0027_update_reports`.
- The `.up.sql` file applies the change. The `.down.sql` file reverts it exactly.
- Down migrations are required. A PR with an `.up.sql` and no corresponding `.down.sql` will not be merged.

Generate a new migration pair:

```shell
make migration NAME=add_reports_archived_at_index
# Creates:
#   migrations/0043_add_reports_archived_at_index.up.sql
#   migrations/0043_add_reports_archived_at_index.down.sql
```

---

## Writing Up and Down Migrations

### Example: Adding a Column

```sql
-- migrations/0027_add_reports_archived_at.up.sql
ALTER TABLE reports
  ADD COLUMN archived_at TIMESTAMPTZ;

COMMENT ON COLUMN reports.archived_at IS
  'Timestamp when the report was archived. NULL means the report is active.';
```

```sql
-- migrations/0027_add_reports_archived_at.down.sql
ALTER TABLE reports
  DROP COLUMN archived_at;
```

### Example: Creating a Table

```sql
-- migrations/0031_create_data_sources.up.sql
CREATE TABLE data_sources (
  id            UUID         NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  workspace_id  UUID         NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name          TEXT         NOT NULL,
  kind          TEXT         NOT NULL CHECK (kind IN ('postgresql', 'bigquery', 'snowflake', 'redshift', 's3')),
  config        JSONB        NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  deleted_at    TIMESTAMPTZ
);

CREATE INDEX data_sources_workspace_id_idx ON data_sources(workspace_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX data_sources_workspace_name_idx ON data_sources(workspace_id, lower(name)) WHERE deleted_at IS NULL;

COMMENT ON TABLE data_sources IS 'External data sources connected to a workspace.';
```

```sql
-- migrations/0031_create_data_sources.down.sql
DROP TABLE data_sources;
```

### Example: Adding an Index Concurrently

Long-running index builds on large tables must use `CREATE INDEX CONCURRENTLY` to avoid locking reads. However, `CREATE INDEX CONCURRENTLY` cannot run inside a transaction, so the migration must use the `-- migrate: no-transaction` directive:

```sql
-- migrations/0038_add_events_workspace_timestamp_idx.up.sql
-- migrate: no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS events_workspace_timestamp_idx
  ON events(workspace_id, occurred_at DESC)
  WHERE deleted_at IS NULL;
```

```sql
-- migrations/0038_add_events_workspace_timestamp_idx.down.sql
-- migrate: no-transaction
DROP INDEX CONCURRENTLY IF EXISTS events_workspace_timestamp_idx;
```

---

## Running Migrations

### Local Development

```shell
# Apply all pending migrations
make migrate-up

# Roll back the last migration
make migrate-down

# Roll back N migrations
make migrate-down N=3

# Check current migration version
make migrate-version

# Force a specific version (use only to recover from a failed migration)
make migrate-force VERSION=0037
```

These targets use the `DATABASE_URL` from your `.env` file. See [Local Development Setup](https://placeholder.invalid/page/developer-guide%2Flocal-development-setup.md) for setup.

### CI Pipeline

Migrations run automatically in the `integration-test` CI stage before integration tests execute. The CI database is a fresh PostgreSQL instance; all migrations run from version 0. A failed migration fails the CI job immediately.

```yaml
# .github/workflows/ci.yml (excerpt)
- name: Run migrations
  run: |
    migrate \
      -path ./migrations \
      -database "$DATABASE_URL" \
      up
  env:
    DATABASE_URL: postgres://luminary:luminary@localhost:5432/luminary_test?sslmode=disable
```

### Production Deployments

Production migrations run as a Kubernetes `Job` in the `pre-deploy` Helm hook, before the new service pods start. This ensures the schema is updated before any new code that depends on it is running.

```yaml
# infra/helm/luminary/templates/migration-job.yaml (excerpt)
annotations:
  "helm.sh/hook": pre-upgrade,pre-install
  "helm.sh/hook-weight": "-10"
  "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
```

The migration job uses a dedicated `luminary-migrator` database user with `DDL` privileges. Application pods use `luminary-app`, which has only `DML` privileges (`SELECT`, `INSERT`, `UPDATE`, `DELETE`).

---

## Zero-Downtime Migrations: Expand/Contract Pattern

Luminary runs rolling deployments with no maintenance windows. Any migration that would lock tables or break compatibility with the currently running code version must use the **expand/contract pattern**, splitting the change across multiple deployments.

```mermaid
flowchart TD
    A([Start: Need to rename column]) --> B

    B["Phase 1 — Expand\nDeploy N: Add new column\nWrite to BOTH old and new columns\nRead from old column"]
    B --> C

    C["Phase 2 — Migrate Data\nDeploy N (same): Backfill new column\nfrom old column for existing rows\n(run as a background job or migration)"]
    C --> D

    D["Phase 3 — Cutover\nDeploy N+1: Read from new column\nWrite to BOTH columns\n(backward-compat for N still running)"]
    D --> E

    E["Phase 4 — Contract\nDeploy N+2: Stop writing to old column\nDrop old column in migration"]
    E --> F([Done])

    style A fill:#27ae60,color:#fff
    style F fill:#27ae60,color:#fff
    style B fill:#3498db,color:#fff
    style C fill:#3498db,color:#fff
    style D fill:#f39c12,color:#fff
    style E fill:#e74c3c,color:#fff
```

### Concrete Example: Renaming `reports.owner_id` to `reports.created_by`

**Migration 0041 (Expand) — Add new column, no data change:**

```sql
-- migrations/0041_reports_add_created_by.up.sql
ALTER TABLE reports ADD COLUMN created_by UUID REFERENCES users(id);
-- Backfill existing rows
UPDATE reports SET created_by = owner_id WHERE created_by IS NULL;
ALTER TABLE reports ALTER COLUMN created_by SET NOT NULL;
```

```sql
-- migrations/0041_reports_add_created_by.down.sql
ALTER TABLE reports DROP COLUMN created_by;
```

**Deploy N:** Application writes to both `owner_id` and `created_by`. Reads use `owner_id`.

**Deploy N+1:** Application reads from `created_by`. Still writes to both.

**Migration 0044 (Contract) — Drop old column:**

```sql
-- migrations/0044_reports_drop_owner_id.up.sql
ALTER TABLE reports DROP COLUMN owner_id;
```

```sql
-- migrations/0044_reports_drop_owner_id.down.sql
ALTER TABLE reports ADD COLUMN owner_id UUID REFERENCES users(id);
UPDATE reports SET owner_id = created_by;
ALTER TABLE reports ALTER COLUMN owner_id SET NOT NULL;
```

**Deploy N+2:** Old column is gone. Code no longer references `owner_id`.

### Operations That Require Expand/Contract

| Operation | Risk | Pattern |
| --- | --- | --- |
| Renaming a column | Old code breaks on deploy N+1 | Add new column → dual-write → switch reads → drop old |
| Dropping a column | Running code still references it | Stop all references in code first → deploy → then drop |
| Adding a NOT NULL column | Breaks inserts from running old code | Add as nullable → backfill → add constraint → drop nullable |
| Renaming a table | Immediate breakage | Create view with old name → migrate code → drop view |
| Changing a column type | Implicit casts may fail | Add new column with new type → dual-write → cutover → drop old |

---

## Common Pitfalls

**Never use** `ALTER TABLE ... RENAME COLUMN` **directly in a single deployment.** It will break the currently running pods until the rollout completes.

**Never add a column with a volatile default in a single statement on a large table.** `ALTER TABLE events ADD COLUMN processed BOOLEAN NOT NULL DEFAULT false` rewrites the entire table. Use `ADD COLUMN processed BOOLEAN` (nullable, no default) first, then a separate `UPDATE` in batches, then `ALTER COLUMN processed SET DEFAULT false` and `ALTER COLUMN processed SET NOT NULL`.

**Do not run** `VACUUM FULL` **or** `CLUSTER` **as part of a migration.** These acquire `ACCESS EXCLUSIVE` locks and will block all reads and writes. Schedule them as maintenance operations during low-traffic windows via the `#eng-platform` team.

**Sequence gaps are automatically detected.** If you cherry-pick or revert a migration PR and leave a gap in the sequence, CI will fail with a `migration sequence gap detected` error. Fill the gap by renaming the offending files.
