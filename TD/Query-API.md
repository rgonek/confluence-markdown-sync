---
title: Query API
id: "4948052"
space: TD
version: 2
labels:
    - api
    - query
    - analytics
    - dsl
author: Robert Gonek
created_at: "2026-02-24T14:54:57Z"
last_modified_at: "2026-02-24T14:54:59Z"
last_modified_by: Robert Gonek
---
# Query API

The Luminary Query API gives you programmatic access to the analytics data your workspace has ingested via the [Events Ingestion API](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6258833). Queries are expressed as JSON documents using the Luminary Query DSL—a structured, composable query language designed for time-series event data.

**Base URL:** `https://api.luminary.io/v2/query`

**API Version:** v2 (Stable)

**Owner:** Analytics Engine Team

---

## Overview

The Query API supports two execution modes:

- **Synchronous mode** — the server executes the query and returns results in a single HTTP response. Best for lightweight queries (expected result set < 10,000 rows) that complete in under 30 seconds.
- **Asynchronous mode** — the server queues the query and returns a `queryId` immediately. Poll `GET /v2/query/{queryId}` for status and results. Required for complex or large-result queries. Maximum result retention is **72 hours** after query completion.

All queries are scoped to the authenticated workspace. Cross-workspace queries are not supported.

See [Rate Limits and Quotas](https://placeholder.invalid/page/api-reference%2Frate-limits-and-quotas.md) for query-tier limits by plan.

---

## Query Rate Limits

Query workloads are billed separately from ingestion. Each workspace plan has a concurrent query limit and a daily compute unit quota:

| Limit | Starter | Growth | Enterprise |
| --- | --- | --- | --- |
| Concurrent queries | 2 | 10 | Custom |
| Query compute units / day | 500 | 5,000 | Custom |
| Max query result rows | 10,000 | 100,000 | 1,000,000 |
| Max async result retention | 24 hours | 72 hours | 72 hours |
| `POST /v2/query` requests/minute | 30 | 120 | Custom |
| `GET /v2/query/{queryId}` requests/minute | 120 | 600 | Custom |

A "query compute unit" is consumed proportionally to the number of events scanned and the complexity of the aggregation. Simple count queries over 7-day windows consume far fewer units than multi-group aggregations over 90-day windows.

---

## The Query DSL

A query document is a JSON object with the following top-level structure:

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `eventType` | string or array | Yes | One or more event types to query. E.g., `"purchase_completed"` or `["checkout_started", "purchase_completed"]`. |
| `timeRange` | object | Yes | Defines the time window for the query. See [Time Range](#time-range). |
| `filters` | array | No | Array of filter conditions applied before aggregation. See [Filters](#filters). |
| `groupBy` | array | No | Properties to group results by. See [Group By](#group-by). |
| `aggregations` | array | Yes | One or more aggregation definitions. See [Aggregations](#aggregations). |
| `orderBy` | array | No | Sort order for results. |
| `limit` | integer | No | Max rows to return. Default 1,000, max determined by plan tier. |
| `timezone` | string | No | IANA timezone for time-bucketing (e.g., `"America/New_York"`). Defaults to `"UTC"`. |
| `async` | boolean | No | Force asynchronous execution. Default `false` (auto-selects based on query complexity). |

### Time Range

| Field | Type | Description |
| --- | --- | --- |
| `timeRange.type` | string | `"relative"` or `"absolute"` |
| `timeRange.last` | string | For relative ranges: e.g., `"7d"`, `"30d"`, `"90d"`, `"24h"`, `"1h"`. |
| `timeRange.from` | string | For absolute ranges: RFC 3339 start timestamp (inclusive). |
| `timeRange.to` | string | For absolute ranges: RFC 3339 end timestamp (exclusive). |
| `timeRange.granularity` | string | Time bucket size for time-series breakdowns: `"hour"`, `"day"`, `"week"`, `"month"`. Optional; omit for non-time-series aggregations. |

### Filters

Each filter is an object with `field`, `operator`, and `value`:

| Operator | Applicable Types | Description |
| --- | --- | --- |
| `eq` | string, number, boolean | Exact match |
| `neq` | string, number, boolean | Not equal |
| `in` | string, number | Value is in the provided array |
| `not_in` | string, number | Value is not in the provided array |
| `gt` | number | Greater than |
| `gte` | number | Greater than or equal |
| `lt` | number | Less than |
| `lte` | number | Less than or equal |
| `contains` | string | Substring match (case-insensitive) |
| `starts_with` | string | Prefix match (case-insensitive) |
| `is_null` | any | Field is absent or null |
| `is_not_null` | any | Field is present and not null |

Filter `field` references use dot notation to reach nested properties: `properties.currency`, `context.os.name`, `userId`.

Filters are combined with `AND` by default. To use `OR` logic, wrap multiple filter objects in a `{"or": [...]}` operator.

### Group By

Each entry in `groupBy` is an object:

| Field | Type | Description |
| --- | --- | --- |
| `field` | string | Property path to group by (e.g., `"properties.plan"`, `"context.os.name"`) |
| `alias` | string | Optional display name for this dimension in results |

Special group-by values:

- `"$timestamp"` — group by time bucket (requires `timeRange.granularity`)
- `"$userId"` — group by user
- `"$anonymousId"` — group by anonymous ID

### Aggregations

| Function | Input Type | Description |
| --- | --- | --- |
| `count` | — | Count of matching events |
| `count_distinct` | string, number | Count of unique values for a field |
| `sum` | number | Sum of a numeric property |
| `avg` | number | Average of a numeric property |
| `min` | number | Minimum value |
| `max` | number | Maximum value |
| `p50` | number | 50th percentile |
| `p75` | number | 75th percentile |
| `p90` | number | 90th percentile |
| `p95` | number | 95th percentile |
| `p99` | number | 99th percentile |

Each aggregation entry:

```json
{
  "function": "sum",
  "field": "properties.revenue",
  "alias": "totalRevenue"
}
```

---

## Endpoints

### POST /v2/query — Run a Query

Submits a query for execution.

#### Request Headers

| Header | Required | Value |
| --- | --- | --- |
| `Content-Type` | Yes | `application/json` |
| `Authorization` | Yes | `Bearer <access_token_or_api_key>` |
| `X-Luminary-Workspace` | Yes | Workspace slug |

#### Request Body

A query document conforming to the [Query DSL](#the-query-dsl).

#### Response — Synchronous (200 OK)

Returned when the query completes within the timeout and the result set is within the row limit.

| Field | Type | Description |
| --- | --- | --- |
| `queryId` | string | Stable ID for this query execution |
| `status` | string | Always `"complete"` for synchronous responses |
| `executionMs` | integer | Query execution time in milliseconds |
| `computeUnitsConsumed` | number | Compute units deducted from daily quota |
| `rowCount` | integer | Number of rows in the result |
| `columns` | array | Column metadata (name, type) |
| `rows` | array | Result data as an array of arrays (column-ordered) |
| `nextCursor` | string | Cursor for fetching the next page. Null if all results are returned. |

#### Response — Asynchronous (202 Accepted)

Returned when the query is queued for async execution.

| Field | Type | Description |
| --- | --- | --- |
| `queryId` | string | Use this to poll `GET /v2/query/{queryId}` |
| `status` | string | `"queued"` or `"running"` |
| `statusUrl` | string | Full URL to poll |
| `estimatedWaitSeconds` | integer | Server-estimated wait time (informational, not a guarantee) |

#### Error Codes

| HTTP Status | Error Code | Description |
| --- | --- | --- |
| 400 | `INVALID_QUERY` | Query DSL validation failed. See `details`. |
| 400 | `INVALID_EVENT_TYPE` | A referenced event type does not exist |
| 400 | `INVALID_FIELD_REFERENCE` | A `groupBy` or `filter` field does not exist in the event schema |
| 400 | `TIME_RANGE_TOO_LARGE` | Requested time range exceeds plan maximum (see [Rate Limits](https://placeholder.invalid/page/api-reference%2Frate-limits-and-quotas.md)) |
| 402 | `COMPUTE_QUOTA_EXCEEDED` | Daily compute unit quota has been exhausted |
| 429 | `RATE_LIMIT_EXCEEDED` | Per-minute query rate limit reached |
| 503 | `QUERY_ENGINE_UNAVAILABLE` | Temporary infrastructure issue; retry with backoff |

#### curl Example

```shell
curl -X POST https://api.luminary.io/v2/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "X-Luminary-Workspace: acme-corp" \
  -d '{
    "eventType": "purchase_completed",
    "timeRange": {
      "type": "relative",
      "last": "30d",
      "granularity": "day"
    },
    "aggregations": [
      {"function": "count", "alias": "purchases"},
      {"function": "sum", "field": "properties.revenue", "alias": "totalRevenue"}
    ],
    "groupBy": [
      {"field": "$timestamp", "alias": "date"}
    ],
    "orderBy": [
      {"field": "date", "direction": "asc"}
    ]
  }'
```

---

### GET /v2/query/{queryId} — Poll Async Result

Returns the current status and results (when complete) of a previously submitted query.

#### Path Parameters

| Parameter | Type | Description |
| --- | --- | --- |
| `queryId` | string | The query ID from `POST /v2/query` |

#### Query Parameters

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `cursor` | string | — | Pagination cursor from a previous response's `nextCursor` field |
| `pageSize` | integer | 1000 | Rows per page. Max 10,000. |

#### Response Body (200 OK)

| Field | Type | Description |
| --- | --- | --- |
| `queryId` | string | The query ID |
| `status` | string | `"queued"`, `"running"`, `"complete"`, `"failed"`, or `"expired"` |
| `submittedAt` | string | RFC 3339 timestamp of submission |
| `completedAt` | string | RFC 3339 timestamp of completion. Null if not yet complete. |
| `expiresAt` | string | RFC 3339 timestamp after which results will be deleted |
| `executionMs` | integer | Null until complete |
| `computeUnitsConsumed` | number | Null until complete |
| `error` | object | Present only when `status` is `"failed"` |
| `rowCount` | integer | Total rows (across all pages). Null until complete. |
| `columns` | array | Column metadata. Null until complete. |
| `rows` | array | Current page of result rows. Empty if status is not `"complete"`. |
| `nextCursor` | string | Pagination cursor. Null if no more pages. |

```json
{
  "queryId": "qry_01HZ9KQVXPN4M3T7BWCJ8DREF",
  "status": "complete",
  "submittedAt": "2025-06-03T14:40:00.000Z",
  "completedAt": "2025-06-03T14:40:04.217Z",
  "expiresAt": "2025-06-06T14:40:04.217Z",
  "executionMs": 4217,
  "computeUnitsConsumed": 12.4,
  "rowCount": 30,
  "columns": [
    {"name": "date", "type": "datetime"},
    {"name": "purchases", "type": "integer"},
    {"name": "totalRevenue", "type": "float"}
  ],
  "rows": [
    ["2025-05-04T00:00:00Z", 142, 18934.50],
    ["2025-05-05T00:00:00Z", 189, 24112.00],
    ["2025-05-06T00:00:00Z", 97, 12540.75]
  ],
  "nextCursor": null
}
```

#### curl Example

```shell
curl "https://api.luminary.io/v2/query/qry_01HZ9KQVXPN4M3T7BWCJ8DREF?pageSize=100" \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "X-Luminary-Workspace: acme-corp"
```

---

### GET /v2/query/{queryId}/download — Download Results as CSV

Returns the complete result set of a completed query as a CSV file. Useful for bulk exports to spreadsheet tools or downstream data pipelines.

This endpoint streams the response, so it is safe to use on very large result sets (up to the plan's row limit). The `Content-Disposition` header will be set to `attachment; filename="query-{queryId}.csv"`.

#### Path Parameters

| Parameter | Type | Description |
| --- | --- | --- |
| `queryId` | string | The query ID (must be in `"complete"` status) |

#### Request Headers

| Header | Required | Value |
| --- | --- | --- |
| `Authorization` | Yes | `Bearer <access_token_or_api_key>` — must have `query:download` scope |

#### Response (200 OK)

`Content-Type: text/csv; charset=utf-8`

The first row is the header row with column names. Subsequent rows contain the data. Timestamps are formatted as RFC 3339 strings.

```csv
date,purchases,totalRevenue
2025-05-04T00:00:00Z,142,18934.50
2025-05-05T00:00:00Z,189,24112.00
2025-05-06T00:00:00Z,97,12540.75
```

#### Error Codes

| HTTP Status | Error Code | Description |
| --- | --- | --- |
| 403 | `INSUFFICIENT_SCOPE` | Token is missing the `query:download` scope |
| 404 | `QUERY_NOT_FOUND` | No query with this ID exists for the workspace |
| 409 | `QUERY_NOT_COMPLETE` | Query is still running or failed; no results to download |
| 410 | `QUERY_EXPIRED` | Results have been deleted (past retention window) |

#### curl Example

```shell
curl "https://api.luminary.io/v2/query/qry_01HZ9KQVXPN4M3T7BWCJ8DREF/download" \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "X-Luminary-Workspace: acme-corp" \
  -o "revenue-report.csv"
```

---

## Complex Query Example

The following query answers the question: **"For each pricing plan, what was the weekly purchase revenue and conversion rate from checkout start in the last 90 days, for users on macOS or Windows only?"**

```json
{
  "async": true,
  "eventType": "purchase_completed",
  "timeRange": {
    "type": "relative",
    "last": "90d",
    "granularity": "week"
  },
  "filters": [
    {
      "field": "context.os.name",
      "operator": "in",
      "value": ["macOS", "Windows"]
    },
    {
      "field": "properties.currency",
      "operator": "eq",
      "value": "USD"
    },
    {
      "field": "properties.revenue",
      "operator": "gt",
      "value": 0
    }
  ],
  "groupBy": [
    {"field": "$timestamp", "alias": "week"},
    {"field": "properties.plan", "alias": "plan"}
  ],
  "aggregations": [
    {"function": "count", "alias": "completedPurchases"},
    {"function": "sum", "field": "properties.revenue", "alias": "totalRevenue"},
    {"function": "avg", "field": "properties.revenue", "alias": "avgOrderValue"},
    {"function": "p90", "field": "properties.revenue", "alias": "p90OrderValue"},
    {"function": "count_distinct", "field": "userId", "alias": "uniqueBuyers"}
  ],
  "orderBy": [
    {"field": "week", "direction": "asc"},
    {"field": "totalRevenue", "direction": "desc"}
  ],
  "limit": 500,
  "timezone": "America/New_York"
}
```

**Query breakdown:**

| Component | What it does |
| --- | --- |
| `async: true` | Forces async execution since a 90-day multi-group aggregation may exceed 30s |
| `eventType` | Only `purchase_completed` events are scanned |
| `timeRange.last: "90d"` | Scans the last 90 calendar days |
| `timeRange.granularity: "week"` | Results are bucketed into ISO weeks |
| `filters[0]` | Keeps only events from macOS or Windows clients |
| `filters[1]` | Removes non-USD transactions from the revenue calculation |
| `filters[2]` | Excludes zero-value or refunded purchases |
| `groupBy[0]` | Creates one row per week |
| `groupBy[1]` | Further subdivides each week by pricing plan |
| `aggregations` | Computes 5 metrics per group: purchase count, total revenue, average order value, 90th percentile order value, and unique buyer count |
| `timezone` | Week boundaries are calculated in US Eastern time |

**Example partial response:**

```json
{
  "queryId": "qry_01HZ9KQVXPN4M3T7BWCJ8D99",
  "status": "complete",
  "executionMs": 8843,
  "computeUnitsConsumed": 38.7,
  "rowCount": 78,
  "columns": [
    {"name": "week", "type": "datetime"},
    {"name": "plan", "type": "string"},
    {"name": "completedPurchases", "type": "integer"},
    {"name": "totalRevenue", "type": "float"},
    {"name": "avgOrderValue", "type": "float"},
    {"name": "p90OrderValue", "type": "float"},
    {"name": "uniqueBuyers", "type": "integer"}
  ],
  "rows": [
    ["2025-03-10T05:00:00Z", "growth", 312, 62184.00, 199.31, 399.00, 298],
    ["2025-03-10T05:00:00Z", "starter", 84, 5964.00, 70.99, 99.00, 81],
    ["2025-03-10T05:00:00Z", "enterprise", 28, 42000.00, 1500.00, 2400.00, 28],
    ["2025-03-17T04:00:00Z", "growth", 341, 67927.00, 199.20, 399.00, 326]
  ],
  "nextCursor": "eyJvZmZzZXQiOjR9"
}
```

---

## Result Pagination

Queries that return more rows than `pageSize` (default 1,000) will include a `nextCursor` in the response. Pass this cursor as the `cursor` query parameter on the next `GET /v2/query/{queryId}` request to fetch the next page.

Cursors are **opaque strings** — do not attempt to decode or construct them. They are valid only for the query ID they were returned with and expire when the query result expires.

```
Page 1: GET /v2/query/qry_abc123
        → rows [0..999], nextCursor: "eyJvZmZzZXQiOjEwMDB9"

Page 2: GET /v2/query/qry_abc123?cursor=eyJvZmZzZXQiOjEwMDB9
        → rows [1000..1999], nextCursor: "eyJvZmZzZXQiOjIwMDB9"

Page 3: GET /v2/query/qry_abc123?cursor=eyJvZmZzZXQiOjIwMDB9
        → rows [2000..2241], nextCursor: null  ← last page
```

For bulk export use cases, prefer the `/download` endpoint, which streams the full result in a single request.
