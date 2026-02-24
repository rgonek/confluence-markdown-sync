---
title: ClickHouse Schema Reference
id: "4882813"
space: TD
version: 2
labels:
    - data-engineering
    - clickhouse
    - schema
    - reference
author: Robert Gonek
created_at: "2026-02-24T14:55:36Z"
last_modified_at: "2026-02-24T14:55:38Z"
last_modified_by: Robert Gonek
---
# ClickHouse Schema Reference

This document is the authoritative schema reference for Luminary's ClickHouse analytics cluster (`analytics-cluster-prod`). It covers every table in the `luminary` database: DDL, storage configuration, retention policy, and common query patterns.

**Cluster**: `analytics-cluster-prod` (3 nodes: `ch-prod-01`, `ch-prod-02`, `ch-prod-03`) **ClickHouse version**: 23.8.4 LTS **Database**: `luminary` **Replication**: ReplicatedMergeTree on all tables, ZooKeeper-managed

> All tables use the `ON CLUSTER analytics-cluster-prod` clause in production DDL. The cluster uses a single shard with three replicas for operational simplicity. A horizontal sharding strategy is tracked in [RFC-002](https://placeholder.invalid/page/..%2FSD%2Fdecisions%2Frfc-002-event-pipeline-rewrite.md).

---

## Table of Contents

- [events](#events)
- [sessions](#sessions)
- [funnels\_materialized](#funnels_materialized)
- [user\_profiles (Dictionary)](#user_profiles)
- [workspace\_settings](#workspace_settings)

---

## events

The `events` table is the core of Luminary's analytics storage. Every event tracked by customer workspaces lands here after passing through the validation and enrichment pipeline. It is append-only; rows are never updated.

### DDL

```sql
CREATE TABLE luminary.events ON CLUSTER analytics-cluster-prod
(
    -- Identity
    event_id        UUID            CODEC(ZSTD(3)),
    workspace_id    UUID            CODEC(ZSTD(3)),
    environment     LowCardinality(String) CODEC(ZSTD(1)),

    -- Timestamps
    received_at     DateTime64(3, 'UTC')    CODEC(DoubleDelta, ZSTD(1)),
    client_ts       DateTime64(3, 'UTC')    CODEC(DoubleDelta, ZSTD(1)),
    ingested_date   Date                    CODEC(DoubleDelta),

    -- Event metadata
    event_name      LowCardinality(String)  CODEC(ZSTD(3)),
    event_type      LowCardinality(String)  CODEC(ZSTD(1)),  -- 'track', 'page', 'identify', 'group'
    library_name    LowCardinality(String)  CODEC(ZSTD(1)),
    library_version LowCardinality(String)  CODEC(ZSTD(1)),

    -- User / session
    user_id         String                  CODEC(ZSTD(3)),
    anonymous_id    String                  CODEC(ZSTD(3)),
    session_id      String                  CODEC(ZSTD(3)),

    -- Properties (schemaless)
    properties      Map(String, String)     CODEC(ZSTD(3)),
    context_traits  Map(String, String)     CODEC(ZSTD(3)),

    -- Device / platform context
    platform        LowCardinality(String)  CODEC(ZSTD(1)),
    os_name         LowCardinality(String)  CODEC(ZSTD(1)),
    os_version      String                  CODEC(ZSTD(1)),
    browser_name    LowCardinality(String)  CODEC(ZSTD(1)),
    browser_version String                  CODEC(ZSTD(1)),
    device_type     LowCardinality(String)  CODEC(ZSTD(1)),

    -- Network / geo
    ip_address      String                  CODEC(ZSTD(3)),  -- nulled out after geo enrichment for GDPR
    country_code    LowCardinality(String)  CODEC(ZSTD(1)),
    region          LowCardinality(String)  CODEC(ZSTD(1)),
    city            String                  CODEC(ZSTD(3)),
    timezone        LowCardinality(String)  CODEC(ZSTD(1)),

    -- UTM / attribution
    utm_source      LowCardinality(String)  CODEC(ZSTD(1)),
    utm_medium      LowCardinality(String)  CODEC(ZSTD(1)),
    utm_campaign    String                  CODEC(ZSTD(3)),
    utm_term        String                  CODEC(ZSTD(3)),
    utm_content     String                  CODEC(ZSTD(3)),
    referrer        String                  CODEC(ZSTD(3)),

    -- Pipeline metadata
    pipeline_version UInt8                 CODEC(ZSTD(1)),
    is_bot           UInt8  DEFAULT 0      CODEC(ZSTD(1)),
    is_deleted       UInt8  DEFAULT 0      CODEC(ZSTD(1)),  -- soft-delete flag for GDPR purge
    schema_version   UInt16 DEFAULT 1      CODEC(ZSTD(1))
)
ENGINE = ReplicatedMergeTree(
    '/clickhouse/tables/{shard}/luminary/events',
    '{replica}'
)
PARTITION BY toYYYYMM(ingested_date)
ORDER BY (workspace_id, ingested_date, event_name, received_at)
TTL ingested_date + INTERVAL 24 MONTH DELETE
    WHERE is_deleted = 1
SETTINGS
    index_granularity = 8192,
    min_bytes_for_wide_part = 10485760,
    storage_policy = 'tiered_s3';
```

### Skipping Indexes

```sql
-- Applied after initial table creation
ALTER TABLE luminary.events ON CLUSTER analytics-cluster-prod
    ADD INDEX idx_user_id    (user_id)     TYPE bloom_filter(0.01) GRANULES 3,
    ADD INDEX idx_session_id (session_id)  TYPE bloom_filter(0.01) GRANULES 3,
    ADD INDEX idx_is_bot     (is_bot)      TYPE minmax GRANULES 1;
```

### Partition & Ordering Key Explanation

**Partition key**: `toYYYYMM(ingested_date)` Partitions the table by calendar month. A given month's partition is typically 400–900 GB compressed across the three replicas. Monthly partitioning allows:

- Efficient TTL-based expiry (entire partitions are dropped atomically)
- Faster bulk GDPR deletes (partition detach + reattach pattern for mass purge)
- Clear operational boundaries for backfill jobs

**Ordering key**: `(workspace_id, ingested_date, event_name, received_at)` The ordering key was chosen to support Luminary's two dominant query shapes:

1. **Workspace-scoped time-range queries** – nearly all customer-facing queries filter on `workspace_id` and a date range first. Placing `workspace_id` first in the sort order means rows for the same workspace are co-located on disk, and date-range filters prune aggressively after workspace filtering.
2. **Event-name aggregations within a workspace** – grouping by `event_name` within a workspace date range is the bread-and-butter of funnel and trend queries. Having `event_name` third in the key means ClickHouse reads a contiguous range of rows for a given event type.

`received_at` is appended last to give a deterministic sort within granules, which helps the bloom filter indexes on `user_id` stay compact.

### Storage Policy: `tiered_s3`

```xml
<!-- /etc/clickhouse-server/config.d/storage.xml -->
<storage_configuration>
  <disks>
    <default>
      <keep_free_space_bytes>10737418240</keep_free_space_bytes>
    </default>
    <s3_disk>
      <type>s3</type>
      <endpoint>https://s3.us-east-1.amazonaws.com/luminary-clickhouse-prod/</endpoint>
      <use_environment_credentials>true</use_environment_credentials>
    </s3_disk>
  </disks>
  <policies>
    <tiered_s3>
      <volumes>
        <hot>
          <disk>default</disk>
          <max_data_part_size_bytes>5368709120</max_data_part_size_bytes>
        </hot>
        <cold>
          <disk>s3_disk</disk>
          <prefer_not_to_merge>true</prefer_not_to_merge>
        </cold>
      </volumes>
      <move_factor>0.2</move_factor>
    </tiered_s3>
  </policies>
</storage_configuration>
```

Parts younger than 30 days are on NVMe (hot tier). Parts older than 30 days are automatically moved to S3 (cold tier) by the background mover thread. Cold-tier reads are significantly slower (~3–8x) and should be avoided for interactive queries; the query engine enforces a 90-day default window on customer-facing charts.

### Data Retention Policy

| Condition | Action | Configured Via |
| --- | --- | --- |
| `ingested_date` older than 24 months | Hard delete partition | TTL clause |
| `is_deleted = 1` | Hard delete row immediately on next merge | TTL WHERE clause |
| Any row from a purged `workspace_id` | Marked `is_deleted = 1` by purge job, then TTL removes | Purge pipeline |

The 24-month TTL is a hard floor for all plans. Enterprise plan data may have a contracted retention extension; these workspaces have their TTL overridden at the partition level via a scheduled ALTER.

See [Data Retention & Purging](https://placeholder.invalid/page/data-engineering%2Fdata-retention-and-purging.md) for the full deletion flow.

### Common Query Patterns

**Unique users in a date range:**

```sql
SELECT uniqCombined64(user_id) AS unique_users
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND event_type = 'track'
  AND is_deleted = 0
  AND is_bot = 0;
```

**Daily event count by event name:**

```sql
SELECT
    ingested_date,
    event_name,
    count() AS event_count,
    uniqCombined64(user_id) AS unique_users
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND is_deleted = 0
  AND is_bot = 0
GROUP BY ingested_date, event_name
ORDER BY ingested_date, event_count DESC;
```

**Conversion funnel (manual step calculation):**

```sql
WITH
  step1 AS (
    SELECT DISTINCT user_id
    FROM luminary.events
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
      AND event_name = 'signup_started'
      AND is_deleted = 0
  ),
  step2 AS (
    SELECT DISTINCT e.user_id
    FROM luminary.events e
    INNER JOIN step1 s1 ON e.user_id = s1.user_id
    WHERE e.workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND e.ingested_date BETWEEN '2025-09-01' AND '2025-10-15'
      AND e.event_name = 'signup_completed'
      AND e.is_deleted = 0
  )
SELECT
  (SELECT count() FROM step1) AS entered_funnel,
  (SELECT count() FROM step2) AS completed_signup,
  round(100.0 * (SELECT count() FROM step2) / (SELECT count() FROM step1), 2) AS conversion_pct;
```

**Top properties for a given event:**

```sql
SELECT
    prop_key,
    prop_value,
    count() AS occurrences
FROM luminary.events
ARRAY JOIN
    mapKeys(properties)  AS prop_key,
    mapValues(properties) AS prop_value
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND event_name = 'plan_upgraded'
  AND is_deleted = 0
GROUP BY prop_key, prop_value
ORDER BY occurrences DESC
LIMIT 50;
```

---

## sessions

The `sessions` table stores derived session records, assembled by the session stitching job that runs over the raw events stream. A session represents a contiguous period of user activity, bounded by a 30-minute inactivity gap.

Sessions are written by the `session-worker` service (not via Kafka pipeline) using the ReplacingMergeTree engine, which allows session records to be updated as new events arrive within the session window.

### DDL

```sql
CREATE TABLE luminary.sessions ON CLUSTER analytics-cluster-prod
(
    session_id          String                  CODEC(ZSTD(3)),
    workspace_id        UUID                    CODEC(ZSTD(3)),
    user_id             String                  CODEC(ZSTD(3)),
    anonymous_id        String                  CODEC(ZSTD(3)),

    session_start       DateTime64(3, 'UTC')    CODEC(DoubleDelta, ZSTD(1)),
    session_end         DateTime64(3, 'UTC')    CODEC(DoubleDelta, ZSTD(1)),
    session_date        Date                    CODEC(DoubleDelta),
    duration_seconds    UInt32                  CODEC(ZSTD(1)),

    event_count         UInt32                  CODEC(ZSTD(1)),
    page_view_count     UInt32                  CODEC(ZSTD(1)),
    screen_view_count   UInt32                  CODEC(ZSTD(1)),

    entry_page          String                  CODEC(ZSTD(3)),
    exit_page           String                  CODEC(ZSTD(3)),

    country_code        LowCardinality(String)  CODEC(ZSTD(1)),
    region              LowCardinality(String)  CODEC(ZSTD(1)),
    platform            LowCardinality(String)  CODEC(ZSTD(1)),
    device_type         LowCardinality(String)  CODEC(ZSTD(1)),
    browser_name        LowCardinality(String)  CODEC(ZSTD(1)),

    utm_source          LowCardinality(String)  CODEC(ZSTD(1)),
    utm_medium          LowCardinality(String)  CODEC(ZSTD(1)),
    utm_campaign        String                  CODEC(ZSTD(3)),
    referrer            String                  CODEC(ZSTD(3)),

    is_bounce           UInt8   DEFAULT 0,
    is_deleted          UInt8   DEFAULT 0,
    updated_at          DateTime64(3, 'UTC'),
    _version            UInt64                  CODEC(ZSTD(1))
)
ENGINE = ReplicatedReplacingMergeTree(
    '/clickhouse/tables/{shard}/luminary/sessions',
    '{replica}',
    _version
)
PARTITION BY toYYYYMM(session_date)
ORDER BY (workspace_id, session_date, session_id)
TTL session_date + INTERVAL 24 MONTH DELETE
SETTINGS index_granularity = 8192;
```

### Deduplication Behaviour

Because ClickHouse's ReplacingMergeTree only deduplicates during background merges (not at query time), all queries against `sessions` must include a `FINAL` modifier or use the `argMax` pattern:

```sql
-- Preferred: FINAL (forces deduplication, higher memory cost)
SELECT
    session_date,
    count() AS session_count,
    avg(duration_seconds) AS avg_duration,
    countIf(is_bounce = 1) / count() AS bounce_rate
FROM luminary.sessions FINAL
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND session_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND is_deleted = 0
GROUP BY session_date
ORDER BY session_date;
```

The Query Service wraps all session queries in a thin materialized view that flushes deduplicated data on a 5-minute schedule, which avoids the `FINAL` overhead for the most common dashboard queries.

### Partition & Ordering Key

Same monthly partitioning rationale as `events`. Ordered by `(workspace_id, session_date, session_id)` — session queries never span workspaces and are almost always date-ranged.

### Data Retention Policy

Sessions inherit the same 24-month TTL as events. Session records for deleted users are marked `is_deleted = 1` by the purge pipeline and are removed on the next merge.

---

## funnels_materialized

`funnels_materialized` is a Materialized View (MV) that pre-aggregates funnel step completion counts per workspace per day per funnel definition. The view is driven by inserts into `events` and dramatically accelerates the funnel query path for workspaces with high event volumes.

### DDL

```sql
-- Target table (MV writes here)
CREATE TABLE luminary.funnels_aggregated ON CLUSTER analytics-cluster-prod
(
    funnel_id           UUID                    CODEC(ZSTD(3)),
    workspace_id        UUID                    CODEC(ZSTD(3)),
    step_index          UInt8,
    step_event_name     LowCardinality(String)  CODEC(ZSTD(3)),
    agg_date            Date                    CODEC(DoubleDelta),

    users_entered       AggregateFunction(uniqCombined64, String),
    users_completed     AggregateFunction(uniqCombined64, String),
    median_time_to_step AggregateFunction(quantile(0.5), Float64)
)
ENGINE = ReplicatedAggregatingMergeTree(
    '/clickhouse/tables/{shard}/luminary/funnels_aggregated',
    '{replica}'
)
PARTITION BY toYYYYMM(agg_date)
ORDER BY (workspace_id, funnel_id, agg_date, step_index)
TTL agg_date + INTERVAL 24 MONTH DELETE;

-- Materialized View definition
CREATE MATERIALIZED VIEW luminary.funnels_materialized
ON CLUSTER analytics-cluster-prod
TO luminary.funnels_aggregated
AS
SELECT
    f.funnel_id                             AS funnel_id,
    e.workspace_id                          AS workspace_id,
    f.step_index                            AS step_index,
    f.event_name                            AS step_event_name,
    toDate(e.ingested_date)                 AS agg_date,
    uniqCombined64State(e.user_id)          AS users_entered,
    uniqCombined64State(
        if(e.event_name = f.event_name, e.user_id, '')
    )                                        AS users_completed,
    quantileState(0.5)(
        toFloat64(dateDiff('second', e.session_start_ts, e.received_at))
    )                                        AS median_time_to_step
FROM luminary.events e
JOIN luminary.funnel_step_definitions f
    ON e.workspace_id = f.workspace_id
    AND e.event_name = f.event_name
WHERE e.is_deleted = 0
  AND e.is_bot = 0
GROUP BY funnel_id, workspace_id, step_index, step_event_name, agg_date;
```

### Querying the MV

Because the MV uses `AggregateFunction` state columns, you must use the `-Merge` combinator when querying:

```sql
SELECT
    step_index,
    step_event_name,
    uniqCombined64Merge(users_entered)  AS entered,
    uniqCombined64Merge(users_completed) AS completed,
    round(100.0 * uniqCombined64Merge(users_completed) /
          nullIf(uniqCombined64Merge(users_entered), 0), 2) AS conversion_pct
FROM luminary.funnels_aggregated
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND funnel_id   = '01923c5d-e6f7-8901-a2b3-c4d5e6f78901'
  AND agg_date BETWEEN '2025-09-01' AND '2025-09-30'
GROUP BY step_index, step_event_name
ORDER BY step_index;
```

### Limitations

The MV only captures funnel progression from the point of view of individual events — it does not enforce ordered step completion within a time window. Strict ordered funnel queries (e.g. "user must complete step 1 before step 2 within 7 days") still go to the raw `events` table. The query engine routes based on funnel definition flags.

---

## user_profiles

`user_profiles` is implemented as a ClickHouse **Dictionary** backed by Postgres. It provides fast key-value lookup of user dimension data (name, email, plan) inside ClickHouse queries without joining to Postgres at query time.

### Dictionary DDL

```sql
CREATE DICTIONARY luminary.user_profiles ON CLUSTER analytics-cluster-prod
(
    user_id         String,
    workspace_id    String,
    email           String DEFAULT '',
    display_name    String DEFAULT '',
    plan_name       LowCardinality(String) DEFAULT 'free',
    created_at      DateTime DEFAULT now(),
    country_code    LowCardinality(String) DEFAULT '',
    is_internal     UInt8 DEFAULT 0
)
PRIMARY KEY user_id
SOURCE(POSTGRESQL(
    host     'rds-prod-primary.luminary.internal'
    port     5432
    user     'clickhouse_reader'
    password ''   -- resolved from environment at startup
    db       'luminary_prod'
    table    'user_dim_view'
))
LAYOUT(COMPLEX_KEY_HASHED(SHARDS 4))
LIFETIME(MIN 300 MAX 600);
```

The dictionary is refreshed every 5–10 minutes (jittered). It holds approximately 8M user records and occupies ~2 GB of RAM per ClickHouse node.

### Usage in Queries

```sql
-- Enrich events with user dimension at query time
SELECT
    e.event_name,
    dictGet('luminary.user_profiles', 'plan_name', e.user_id)  AS plan_name,
    dictGet('luminary.user_profiles', 'country_code', e.user_id) AS country_code,
    count() AS cnt
FROM luminary.events e
WHERE e.workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND e.ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND e.is_deleted = 0
GROUP BY e.event_name, plan_name, country_code
ORDER BY cnt DESC
LIMIT 20;
```

### Refresh & Staleness

The dictionary uses `LIFETIME(MIN 300 MAX 600)` — a new background load starts between 5 and 10 minutes after the previous load completed. During the reload, ClickHouse continues serving the previous version. If the reload fails, the old version is kept and an error is logged; no query-time error is surfaced to customers.

To force an immediate reload (e.g. after a bulk user import):

```sql
SYSTEM RELOAD DICTIONARY luminary.user_profiles;
```

### Known Scaling Limits

At ~8M users the dictionary comfortably fits in 2 GB RAM. If user count exceeds~30M, we will need to shard the dictionary by workspace or migrate to a range-based layout. This is tracked as a capacity planning item — see [Capacity Planning](https://placeholder.invalid/page/operations%2Fcapacity-planning.md).

---

## workspace_settings

`workspace_settings` is a small ReplicatedMergeTree table that caches workspace-level configuration flags used during query execution — primarily feature flags and data sampling rates that affect query behavior. It is refreshed from Postgres every 2 minutes by the `config-sync` job.

This table is intentionally small (<100K rows) and is used to avoid per-query round-trips to Postgres during high-volume query bursts.

### DDL

```sql
CREATE TABLE luminary.workspace_settings ON CLUSTER analytics-cluster-prod
(
    workspace_id        UUID                    CODEC(ZSTD(3)),
    plan_tier           LowCardinality(String)  CODEC(ZSTD(1)),
    data_retention_days UInt16  DEFAULT 730,
    sampling_rate       Float32 DEFAULT 1.0,
    timezone            LowCardinality(String)  DEFAULT 'UTC',
    bot_filtering_enabled UInt8 DEFAULT 1,
    cross_domain_tracking UInt8 DEFAULT 0,
    custom_event_limit  UInt32  DEFAULT 500,
    updated_at          DateTime64(3, 'UTC'),
    _version            UInt64
)
ENGINE = ReplicatedReplacingMergeTree(
    '/clickhouse/tables/{shard}/luminary/workspace_settings',
    '{replica}',
    _version
)
ORDER BY workspace_id
TTL updated_at + INTERVAL 7 DAY  -- stale settings auto-expire; config-sync keeps them fresh
SETTINGS index_granularity = 256;
```

### Usage in Query Engine

```sql
-- Applied automatically by the query engine before executing customer queries
WITH ws AS (
    SELECT sampling_rate, timezone, bot_filtering_enabled
    FROM luminary.workspace_settings FINAL
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
    LIMIT 1
)
SELECT
    toDate(toTimeZone(received_at, (SELECT timezone FROM ws))) AS local_date,
    event_name,
    count() AS cnt
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND is_deleted = 0
  AND (bot_filtering_enabled = 0 OR is_bot = 0)
  AND cityHash64(user_id) % 1000 < (SELECT toUInt16(sampling_rate * 1000) FROM ws)
GROUP BY local_date, event_name
ORDER BY local_date, cnt DESC;
```

### Consistency Note

Because `workspace_settings` lags Postgres by up to 2 minutes, there is a brief window after plan changes where a workspace might query with stale settings (e.g., old retention days or sampling rate). This is acceptable for all current use cases. If strict consistency is needed, the query engine can be configured to bypass this table and hit Postgres directly via the `?force_settings_reload=true` internal query parameter.

---

## Schema Change Process

All DDL changes to production ClickHouse tables follow this process:

1. **RFC or ticket**: Changes to partition key, order key, or codec require a written proposal reviewed by the Data Engineering team.
2. **Staging validation**: Run the DDL on `analytics-cluster-staging` and execute the query regression suite (`make test-clickhouse`).
3. **Online ALTER**: Most column additions and codec changes can be applied online via `ALTER TABLE ... ADD COLUMN` / `MODIFY COLUMN`. ClickHouse applies these lazily (only when parts are rewritten).
4. **Partition key changes**: Require creating a new table, backfilling data, and cutting over via DNS alias update on the Query Service. Coordinate with the Query Service team.
5. **Production apply**: Run via the `ch-migrations` tool in the `data-platform` repo, which records the migration in `luminary.schema_migrations`.

```sql
-- Check current migration state
SELECT migration_id, applied_at, applied_by, description
FROM luminary.schema_migrations
ORDER BY applied_at DESC
LIMIT 10;
```
