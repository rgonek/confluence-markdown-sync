---
title: Analytics Query Patterns
id: "7438402"
space: TD
version: 2
labels:
    - data-engineering
    - clickhouse
    - performance
    - query-patterns
author: Robert Gonek
created_at: "2026-02-24T14:55:34Z"
last_modified_at: "2026-02-24T14:55:35Z"
last_modified_by: Robert Gonek
---
# Analytics Query Patterns

This guide covers performance patterns and anti-patterns for writing ClickHouse queries within the Luminary Query Service. Most of the guidance here was learned from production incidents or performance investigations. The Query Service team enforces several of these rules via static analysis (`make lint-queries`) on query templates, but understanding the reasoning will help you write better queries from scratch.

---

## Golden Rules

1. **Always filter** `workspace_id` **and a date range first.** These are the first two columns in the `events` ordering key. Everything else is secondary.
2. **Never use** `SELECT *`**.** The `events` table has 40+ columns with mixed compression codecs. Reading unused columns wastes I/O and decompression CPU.
3. **Avoid** `FINAL` **on large tables.** Use it only on `sessions` and `workspace_settings`, which are intentionally small. On `events`, `FINAL` is never needed (the table uses MergeTree, not ReplacingMergeTree).
4. **Use** `LowCardinality` **comparisons.** Columns declared `LowCardinality(String)` use dictionary encoding on disk. Filtering them is significantly faster than filtering plain `String` columns.
5. **Prefer dictionaries over JOINs for dimension data.** `dictGet(...)` is a point-lookup into an in-memory hash table. A JOIN against `user_profiles` would require scanning millions of rows.

---

## Partition Pruning

ClickHouse partitions the `events` table by `toYYYYMM(ingested_date)`. A query without a date filter must scan every partition — currently 24 partitions representing 2 years of data. A query with a tight date range scans 1–2 partitions.

**How to verify partition pruning is working:**

```sql
-- Add EXPLAIN to see how many parts will be read
EXPLAIN PLAN
SELECT count()
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND is_deleted = 0;
```

In the EXPLAIN output, look for `Selected N/M parts` — N should be close to the number of monthly partitions covered by the date range. If N equals M (all parts selected), the partition key is not being pruned.

**Common mistake**: Using `toDate(received_at)` as the date filter instead of `ingested_date`. Since `ingested_date` is the partition key column and `received_at` is not, filtering on `received_at` forces a full scan even if the time range is narrow.

```sql
-- BAD: filters on a non-partition column; scans all partitions
SELECT count()
FROM luminary.events
WHERE workspace_id = '...'
  AND toDate(received_at) BETWEEN '2025-09-01' AND '2025-09-30';

-- GOOD: filters on the partition key; prunes to 1 partition
SELECT count()
FROM luminary.events
WHERE workspace_id = '...'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30';
```

---

## Skipping Index Usage

Three bloom filter indexes are defined on `events`:

- `idx_user_id` on `user_id`
- `idx_session_id` on `session_id`
- `idx_is_bot` on `is_bot` (minmax)

These are skipping indexes — they don't change the row scan order but allow ClickHouse to skip granules (8,192-row blocks) where the filter condition cannot match.

Bloom filters are most effective when:

- The value being searched for is rare (appears in < 20% of granules)
- You're doing equality filters (`user_id = 'x'`), not range filters

**Checking if a skipping index is being used:**

```sql
EXPLAIN indexes = 1
SELECT event_id, event_name, received_at
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND user_id = 'user_abc123';
```

The output will show `Indexes: ..., Skip: idx_user_id (used)` if the skipping index is activated. If you're looking up a user who sent millions of events (common for bot traffic), the bloom filter may not help much — the granule skip rate will be low.

---

## Avoiding SELECT *

The impact of `SELECT *` on the `events` table is severe. The table has columns with very different compression profiles:

- `properties Map(String, String)` with ZSTD(3) can compress 10–20x but decompression is expensive.
- `raw_payload`-equivalent columns, if present, are large.
- `event_id UUID` with ZSTD(3) decompresses quickly but is often not needed.

**Bad vs Good Query Patterns:**

| Pattern | Problem | Fix |
| --- | --- | --- |
| `SELECT *` | Reads all columns; decompresses everything | List only needed columns explicitly |
| `WHERE toDate(received_at) = today()` | Disables partition pruning | Use `ingested_date = today()` |
| `GROUP BY properties['plan']` | Map access inside GROUP BY forces full map decompression | Extract to a pre-computed column if used frequently, or accept the cost |
| `ORDER BY received_at DESC LIMIT 10` | Top-N with ORDER BY scans the full partition range | Add `LIMIT ... BY` or use a windowed pre-aggregate |
| `SELECT uniq(user_id)` | `uniq()` uses HyperLogLog with low precision | Use `uniqCombined64()` for better precision at similar cost |
| `JOIN users ON events.user_id = users.id` | Distributed JOIN may shuffle data across nodes | Replace with `dictGet('luminary.user_profiles', ...)` |
| `SELECT ... WHERE event_name LIKE '%checkout%'` | String pattern match cannot use bloom filter | Prefer exact match `event_name = 'checkout_completed'` or `IN (...)` |

---

## Aggregation Pushdown

ClickHouse can push aggregations down into the reading layer (before data is transferred between nodes in a distributed query). Use aggregate functions from the `-State`/`-Merge` family when building pipelines, and prefer `GROUP BY` with `LIMIT` over sorting large result sets.

**Using combinators for multi-step aggregation:**

```sql
-- Step 1: compute partial state (can be done per-shard in parallel)
SELECT
    workspace_id,
    ingested_date,
    event_name,
    uniqCombined64State(user_id) AS user_state,
    sumState(1)                  AS event_state
FROM luminary.events
WHERE ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND is_deleted = 0
GROUP BY workspace_id, ingested_date, event_name;

-- Step 2: merge partial states from all shards
SELECT
    workspace_id,
    ingested_date,
    event_name,
    uniqCombined64Merge(user_state)  AS unique_users,
    sumMerge(event_state)            AS event_count
FROM aggregated_intermediate
GROUP BY workspace_id, ingested_date, event_name;
```

This pattern is used internally in the materialized view pipeline and in the Query Service's parallel query executor.

---

## Dictionary Lookups vs JOINs

`dictGet` lookups are dramatically faster than JOINs for enriching events with user or workspace dimension data:

```sql
-- BAD: JOIN forces ClickHouse to hash-join against up to 8M user rows
SELECT
    e.event_name,
    u.plan_tier,
    count() AS cnt
FROM luminary.events e
INNER JOIN luminary.user_dim u ON e.user_id = u.user_id
WHERE e.workspace_id = '...'
  AND e.ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
GROUP BY e.event_name, u.plan_tier;

-- GOOD: dictionary lookup is O(1) per row, fully in-memory
SELECT
    e.event_name,
    dictGet('luminary.user_profiles', 'plan_name', e.user_id) AS plan_tier,
    count() AS cnt
FROM luminary.events e
WHERE e.workspace_id = '...'
  AND e.ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
GROUP BY e.event_name, plan_tier;
```

The dictionary is refreshed every 5–10 minutes, so there is a brief staleness window. This is acceptable for all analytics use cases. See [ClickHouse Schema Reference — user_profiles](https://placeholder.invalid/page/data-engineering%2Fclickhouse-schema.md) for the dictionary DDL.

---

## Materialized View Usage

Use the `funnels_aggregated` materialized view for funnel queries wherever possible. The MV provides pre-aggregated step completion counts; reading from it is 10–100x faster than computing funnel steps from raw events for large workspaces.

The Query Service routes to the MV when all of these conditions hold:

- The funnel definition matches a registered funnel in `funnel_step_definitions`
- The query does not use per-user path analysis (cohort or user-level breakdown requires raw events)
- The date range is older than 5 minutes (very recent data may not yet be aggregated)

When any condition fails, the engine falls back to raw event scanning. You can see which path the engine chose by checking the `X-Query-Path` response header in the internal query API.

---

## Complex Query Examples

### Example 1: Retention Cohort (N-day retention)

Calculate what percentage of users who performed their first event in September returned on day 7 and day 30:

```sql
WITH
  -- Identify users whose first event in any partition was in September
  cohort AS (
    SELECT
      user_id,
      min(ingested_date) AS cohort_date
    FROM luminary.events
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
      AND is_deleted = 0
      AND is_bot = 0
    GROUP BY user_id
    HAVING cohort_date BETWEEN '2025-09-01' AND '2025-09-30'
  ),
  day7 AS (
    SELECT DISTINCT e.user_id
    FROM luminary.events e
    INNER JOIN cohort c ON e.user_id = c.user_id
    WHERE e.workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND e.ingested_date = c.cohort_date + INTERVAL 7 DAY
      AND e.is_deleted = 0
  ),
  day30 AS (
    SELECT DISTINCT e.user_id
    FROM luminary.events e
    INNER JOIN cohort c ON e.user_id = c.user_id
    WHERE e.workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND e.ingested_date = c.cohort_date + INTERVAL 30 DAY
      AND e.is_deleted = 0
  )
SELECT
  count(DISTINCT c.user_id)                               AS cohort_size,
  count(DISTINCT d7.user_id)                              AS retained_day7,
  count(DISTINCT d30.user_id)                             AS retained_day30,
  round(100.0 * count(DISTINCT d7.user_id)  / count(DISTINCT c.user_id), 2) AS day7_pct,
  round(100.0 * count(DISTINCT d30.user_id) / count(DISTINCT c.user_id), 2) AS day30_pct
FROM cohort c
LEFT JOIN day7  d7  ON c.user_id = d7.user_id
LEFT JOIN day30 d30 ON c.user_id = d30.user_id;
```

**Notes**: The CTEs with JOINs are evaluated by ClickHouse's query planner as hash joins. The cohort CTE is built first (small-ish result set), then used as the build side of subsequent joins. This is acceptable for workspaces with < 5M monthly users; above that, move to the retention MV (under development).

---

### Example 2: Property Breakdown with Percentages

Break down a conversion event by a string property (`plan_type`) showing count, share, and cumulative share:

```sql
SELECT
    properties['plan_type']          AS plan_type,
    count()                          AS conversions,
    round(100.0 * count() /
          sum(count()) OVER (), 2)   AS pct_of_total,
    round(100.0 * sum(count()) OVER
          (ORDER BY count() DESC
           ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) /
          sum(count()) OVER (), 2)   AS cumulative_pct
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
  AND event_name = 'subscription_upgraded'
  AND is_deleted = 0
GROUP BY plan_type
ORDER BY conversions DESC
LIMIT 20;
```

---

### Example 3: Rolling 7-Day Active Users

Compute daily rolling 7-day unique active users (R7AU) for the past 30 days:

```sql
WITH daily_users AS (
    SELECT
        ingested_date AS dt,
        groupArray(user_id) AS users_that_day
    FROM luminary.events
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND ingested_date BETWEEN '2025-09-01' AND '2025-10-01'
      AND is_deleted = 0
      AND is_bot = 0
    GROUP BY ingested_date
)
SELECT
    d.dt,
    uniqCombined64(u) AS r7au
FROM daily_users d
ARRAY JOIN users_that_day AS u
-- Rolling window: include all rows from the prior 6 days through today
INNER JOIN daily_users d2 ON d2.dt BETWEEN d.dt - 6 AND d.dt
WHERE d2.dt BETWEEN '2025-09-01' AND '2025-10-01'
GROUP BY d.dt
ORDER BY d.dt;
```

> This is one of the more expensive rolling-window queries. For workspaces with > 10M events/day in the window, consider pre-computing this in the daily aggregation batch job.

---

### Example 4: First-Touch Attribution

Determine the first UTM source for each converting user:

```sql
SELECT
    first_touch_source,
    count(DISTINCT user_id)  AS converting_users
FROM (
    SELECT
        user_id,
        argMin(utm_source, received_at) AS first_touch_source
    FROM luminary.events
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND ingested_date BETWEEN '2025-07-01' AND '2025-09-30'
      AND utm_source != ''
      AND is_deleted = 0
    GROUP BY user_id
) first_touches
INNER JOIN (
    SELECT DISTINCT user_id
    FROM luminary.events
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
      AND event_name = 'subscription_upgraded'
      AND is_deleted = 0
) converters USING (user_id)
GROUP BY first_touch_source
ORDER BY converting_users DESC;
```

`argMin(value, timestamp)` is ClickHouse's efficient way to get the value at the minimum timestamp without sorting the full dataset.

---

### Example 5: Path Analysis (Top N Event Sequences)

Find the 10 most common 3-event sequences leading up to a target event:

```sql
WITH
  target_events AS (
    SELECT user_id, received_at AS conversion_ts
    FROM luminary.events
    WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
      AND event_name = 'subscription_upgraded'
      AND is_deleted = 0
  ),
  prior_events AS (
    SELECT
        e.user_id,
        e.event_name,
        e.received_at,
        t.conversion_ts,
        row_number() OVER (
            PARTITION BY e.user_id, t.conversion_ts
            ORDER BY e.received_at DESC
        ) AS step_back
    FROM luminary.events e
    INNER JOIN target_events t ON e.user_id = t.user_id
    WHERE e.workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
      AND e.ingested_date BETWEEN '2025-08-01' AND '2025-09-30'
      AND e.received_at < t.conversion_ts
      AND e.event_name != 'subscription_upgraded'
      AND e.is_deleted = 0
  )
SELECT
    groupArray(event_name)[1] AS step_minus_1,
    groupArray(event_name)[2] AS step_minus_2,
    groupArray(event_name)[3] AS step_minus_3,
    count()                   AS occurrences
FROM (
    SELECT user_id, conversion_ts, event_name
    FROM prior_events
    WHERE step_back <= 3
    ORDER BY user_id, conversion_ts, step_back
)
GROUP BY step_minus_1, step_minus_2, step_minus_3
ORDER BY occurrences DESC
LIMIT 10;
```

Path analysis queries are expensive. The Query Service applies a workspace event-count heuristic and automatically samples large datasets (down to 10%) when the estimated scan size exceeds 500M rows. The sample rate is surfaced to the customer in the UI as a "results based on X% sample" notice.
