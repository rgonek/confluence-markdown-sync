---
title: ClickHouse Materialized Views Reference
id: "5537974"
space: TD
version: 2
labels:
    - materialized-views
    - data-engineering
    - clickhouse
    - analytics
author: Robert Gonek
created_at: "2026-02-24T14:55:50Z"
last_modified_at: "2026-02-24T14:55:51Z"
last_modified_by: Robert Gonek
---
# ClickHouse Materialized Views Reference

This page documents all materialized views in the `analytics` ClickHouse database. Each materialized view is a ClickHouse MV that reads from a source table on insert and writes pre-aggregated rows into a destination table. They are not recalculated on demand — they update incrementally as new data arrives.

**Important**: Materialized views in ClickHouse execute at insert time, not query time. If a MV has a bug, the destination table accumulates incorrect data from the time the bug was introduced. Fixing the view definition does not fix historical data — you must backfill by replaying inserts or running a manual `INSERT INTO ... SELECT` from the source.

## `mv_sessions_from_events`

**Purpose**: Reconstructs user sessions from the raw event stream. Groups consecutive events from the same user within a 30-minute inactivity window into sessions.

**Added**: 2022-01-08 | **Owner**: Analytics team

**Why it exists**: Querying raw events to compute sessions on the fly was taking 8–15 seconds for large workspaces. Pre-aggregating sessions reduced this to <200ms.

**Source table**: `analytics.events` (raw event rows)

**Destination table**: `analytics.sessions`

```sql
CREATE MATERIALIZED VIEW analytics.mv_sessions_from_events
TO analytics.sessions
AS
SELECT
    workspace_id,
    user_id,
    session_id,
    min(event_timestamp)                        AS session_start,
    max(event_timestamp)                        AS session_end,
    dateDiff('second', min(event_timestamp),
             max(event_timestamp))              AS duration_seconds,
    countIf(event_type = 'page_viewed')         AS pageview_count,
    countIf(event_type != 'page_viewed')        AS track_event_count,
    count()                                     AS total_event_count,
    any(initial_referrer)                       AS referrer,
    any(utm_source)                             AS utm_source,
    any(utm_campaign)                           AS utm_campaign,
    any(device_type)                            AS device_type,
    any(country)                                AS country
FROM analytics.events
GROUP BY workspace_id, user_id, session_id;
```

**Refresh behavior**: Incremental — new events trigger a partial aggregation merge. ClickHouse merges partial aggregation states using the `AggregatingMergeTree` engine on the destination table.

**Query patterns that use it**:

- Session count metrics (dashboard overview)
- Session duration distributions
- Bounce rate calculation
- Traffic source attribution

---

## `mv_funnels_precomputed`

**Purpose**: Pre-aggregates funnel step completion counts for saved funnels. Instead of re-scanning all events for every funnel query, this MV maintains step-by-step completion counts that update as new events arrive.

**Added**: 2022-01-08 | **Owner**: Analytics team

**Why it exists**: Funnel queries on large workspaces (>50M events) were timing out at 30 seconds. Pre-computation reduced query time to <500ms for most funnels.

**Source table**: `analytics.funnel_memberships` (populated by the funnel evaluation job in the Worker Service)

**Destination table**: `analytics.funnel_step_counts`

```sql
CREATE MATERIALIZED VIEW analytics.mv_funnels_precomputed
TO analytics.funnel_step_counts
AS
SELECT
    workspace_id,
    funnel_id,
    step_index,
    toDate(event_timestamp)         AS date,
    count(DISTINCT user_id)         AS users_reached,
    countIf(completed = 1)          AS users_completed_step,
    avg(time_to_step_seconds)       AS avg_time_to_step
FROM analytics.funnel_memberships
GROUP BY workspace_id, funnel_id, step_index, date;
```

**Refresh behavior**: Incremental on insert to `analytics.funnel_memberships`.

**Query patterns that use it**:

- Funnel visualization in the dashboard
- Funnel conversion rate reports
- Step-by-step drop-off analysis

**Known limitation**: This MV does not support funnel filters applied after save (e.g. "show funnel for users in segment X"). Filtered funnel queries fall back to the raw scan path. Tracked in ENG-4015.

---

## `mv_user_daily_activity`

**Purpose**: Per-user daily active status roll-up. Records whether a given user was active (any event) on a given calendar day, per workspace. Used for DAU/WAU/MAU calculations.

**Added**: 2022-01-08 | **Owner**: Analytics team

**Why it exists**: DAU calculations require counting distinct active users per day. Maintaining a daily activity bitmap is much more efficient than counting distinct users over raw events.

**Source table**: `analytics.events`

**Destination table**: `analytics.user_daily_activity`

```sql
CREATE MATERIALIZED VIEW analytics.mv_user_daily_activity
TO analytics.user_daily_activity
AS
SELECT
    workspace_id,
    toDate(event_timestamp)         AS activity_date,
    user_id,
    1                               AS is_active,
    min(event_timestamp)            AS first_event_at,
    max(event_timestamp)            AS last_event_at,
    count()                         AS event_count
FROM analytics.events
WHERE user_id != ''
  AND isNotNull(user_id)
GROUP BY workspace_id, activity_date, user_id;
```

**Refresh behavior**: Incremental. Destination table uses `ReplacingMergeTree` with `is_active` as the version column, so duplicate rows for the same `(workspace_id, activity_date, user_id)` are collapsed on merge.

**Query patterns that use it**:

- DAU/WAU/MAU metrics (dashboard home)
- User engagement trends
- Cohort active rate calculations

---

## `mv_workspace_event_counts_hourly`

**Purpose**: Hourly event count roll-ups per workspace and event type. Used for usage metering, billing calculations, and the ingestion volume monitoring dashboard.

**Added**: 2022-01-08 | **Owner**: Platform team

**Why it exists**: The Worker Service's billing metering job originally scanned all events for the billing period each time it ran. For high-volume workspaces, this became too slow. Pre-aggregating hourly counts reduced billing job runtime from ~8 minutes to <30 seconds.

**Source table**: `analytics.events`

**Destination table**: `analytics.workspace_event_counts_hourly`

```sql
CREATE MATERIALIZED VIEW analytics.mv_workspace_event_counts_hourly
TO analytics.workspace_event_counts_hourly
AS
SELECT
    workspace_id,
    toStartOfHour(event_timestamp)  AS hour,
    event_type,
    count()                         AS event_count,
    countIf(is_anonymous = 1)       AS anonymous_event_count,
    countIf(is_anonymous = 0)       AS identified_event_count,
    uniq(user_id)                   AS unique_users,
    uniq(session_id)                AS unique_sessions
FROM analytics.events
GROUP BY workspace_id, hour, event_type;
```

**Refresh behavior**: Incremental on insert. Destination uses `SummingMergeTree` — rows with the same `(workspace_id, hour, event_type)` key are summed on merge.

**Query patterns that use it**:

- Monthly event count for billing
- Hourly ingestion volume monitoring
- Per-event-type breakdown in the workspace settings page
- Ingestion rate alerting (Datadog metric source)

---

## Backfilling a Materialized View

If you need to populate the destination table for historical data (e.g. after fixing a bug in a MV definition or after adding a new MV to an existing table):

```sql
-- First, truncate the destination table if you're doing a full backfill
TRUNCATE TABLE analytics.user_daily_activity;

-- Then insert from the source using the same SELECT as the MV definition
INSERT INTO analytics.user_daily_activity
SELECT
    workspace_id,
    toDate(event_timestamp)         AS activity_date,
    user_id,
    1                               AS is_active,
    min(event_timestamp)            AS first_event_at,
    max(event_timestamp)            AS last_event_at,
    count()                         AS event_count
FROM analytics.events
WHERE user_id != ''
  AND isNotNull(user_id)
GROUP BY workspace_id, activity_date, user_id
SETTINGS max_execution_time = 3600;  -- Large backfills can take a while
```

Coordinate with the Data Engineering team before running large backfills on production — they consume significant ClickHouse resources and can impact query performance for customers.

## Related

- [Data Quality Framework](Data-Quality-Framework.md)
- [Event Schema Registry](Event-Schema-Registry.md)
- [Query Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-query-service.md)
- [Worker Service](https://placeholder.invalid/page/services%2Fworker-service.md) — billing metering aggregation job
