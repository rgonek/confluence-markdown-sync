---
title: Data Quality Framework
id: "5374082"
space: TD
version: 2
labels:
    - data-quality
    - analytics
    - data-engineering
author: Robert Gonek
created_at: "2026-02-24T14:55:39Z"
last_modified_at: "2026-02-24T14:55:40Z"
last_modified_by: Robert Gonek
---
# Data Quality Framework

Luminary's data quality framework defines how we measure, monitor, and respond to quality issues in the analytics event pipeline. This document covers the quality dimensions we track, validation rules applied at ingestion, metrics and alerting, the quarantine queue, and the process for handling data quality incidents.

The framework was introduced in Q1 2026 as part of the Data Quality Initiative (ENG-3800).

## Data Quality Dimensions

We evaluate data quality across four dimensions:

### Completeness

Events contain all required fields. No nullable columns that are expected to be populated are null. Workspace-level completeness tracks the percentage of events with all instrumentation properties present.

### Accuracy

Field values are semantically valid — event timestamps are within a plausible range, numeric properties don't contain impossible values (negative session durations, impossibly high revenue amounts), and enum fields contain valid values.

### Timeliness

Events arrive within an acceptable lag of their recorded `event_timestamp`. Stale events (timestamp older than 48 hours at arrival) indicate SDK buffering issues or replay scenarios and are flagged separately.

### Consistency

Events from the same user session are internally consistent. For example, a `session_ended` event should not arrive before the corresponding `session_started`. Entity IDs (user IDs, workspace IDs) referenced in events should exist in the primary database at processing time.

## Validation Rules at Ingestion

Validation is applied in the ingestion service before events are written to Kafka. Validation failures increment counters and route events to the quarantine queue — they do not return an error to the SDK client (to avoid losing events from bugs in validation logic itself).

### Schema Validation

Every event is validated against the registered Avro schema for its `event_type`. See [Event Schema Registry](https://placeholder.invalid/page/data-engineering%2Fevent-schema-registry.md) for schema management.

Validation checks:

- Required fields are present and non-null
- Field types match the schema definition
- String fields do not exceed maximum length limits
- Nested objects conform to their sub-schema

### Event Timestamp Sanity Check

```
Valid range: [now - 48h, now + 5m]
```

Events with `event_timestamp` older than 48 hours are flagged as `STALE`. Events with timestamps more than 5 minutes in the future are flagged as `CLOCK_SKEW`. Both are quarantined for review but not silently dropped.

### Workspace Existence Check

The `workspace_id` in every event is checked against a cached bloom filter of valid workspace IDs (refreshed every 60 seconds from Postgres). Events referencing unknown workspaces are quarantined with code `UNKNOWN_WORKSPACE`. This catches misconfigured SDK initialization (wrong API key → wrong workspace ID mapping).

### Property Value Bounds

Numeric properties are checked against workspace-specific or global bounds:

| Property | Rule |
| --- | --- |
| `revenue` | Must be ≥ 0 and ≤ 10,000,000 per event |
| `session_duration_ms` | Must be ≥ 0 and ≤ 86,400,000 (24h) |
| `item_count` | Must be ≥ 0 and ≤ 100,000 |
| Any string property | Must be ≤ 1024 characters |

Events failing bounds checks are quarantined with code `OUT_OF_BOUNDS_PROPERTY`.

## Data Quality Metrics

Quality metrics are emitted as DogStatsD gauges and counters from the ingestion service and the ClickHouse data quality job (runs hourly).

| Metric | Measurement Point | Threshold | Alert |
| --- | --- | --- | --- |
| `dq.events.schema_validation_failure_rate` | Ingestion service | > 0.5% of events | `#alerts-data-platform` |
| `dq.events.stale_arrival_rate` | Ingestion service | > 2% of events | `#alerts-data-platform` |
| `dq.events.unknown_workspace_rate` | Ingestion service | > 0.1% of events | `#alerts-data-platform` (may indicate bad SDK deploy) |
| `dq.events.quarantine_queue_depth` | Quarantine consumer | > 10,000 events | PagerDuty medium |
| `dq.clickhouse.completeness_pct` | Hourly ClickHouse job | < 99% for any workspace > 1000 events/day | `#alerts-data-platform` |
| `dq.clickhouse.consistency_violations` | Hourly ClickHouse job | > 0 for critical event pairs | PagerDuty high |
| `dq.pipeline.end_to_end_lag_seconds` | ClickHouse consumer | p95 > 60s | PagerDuty high |

Metrics are visible in the Data Quality Grafana dashboard (placeholder link — to be migrated to Datadog in ENG-4280).

## Quarantine Queue

Events that fail validation are not silently dropped. They are written to a dedicated Kafka topic: `analytics.events.quarantine`.

### Quarantine Event Schema

Quarantined events are wrapped with additional metadata:

```json
{
  "quarantine_id": "qar_01HX2KZABCDEF",
  "quarantine_reason": "SCHEMA_VALIDATION_FAILURE",
  "quarantine_detail": "Missing required field: user_id",
  "original_event": { ...original event payload... },
  "received_at": "2026-02-20T14:32:00Z",
  "workspace_id": "ws_abc123",
  "source_ip": "203.0.113.45"
}
```

### Quarantine Reason Codes

| Code | Meaning |
| --- | --- |
| `SCHEMA_VALIDATION_FAILURE` | Event fails Avro schema validation |
| `STALE` | Event timestamp older than 48 hours at arrival |
| `CLOCK_SKEW` | Event timestamp more than 5 minutes in the future |
| `UNKNOWN_WORKSPACE` | `workspace_id` not found in workspace registry |
| `OUT_OF_BOUNDS_PROPERTY` | Numeric property outside allowed range |
| `MALFORMED_JSON` | Event could not be parsed as valid JSON |

### Quarantine Retention

Quarantined events are retained for **30 days** in the `analytics.events.quarantine` topic. After 30 days, they are deleted unless a reprocessing decision has been made.

The `dq-review` service provides a UI (internal, accessible at `dq-review.internal.luminary.io`) for browsing quarantined events by workspace and reason code. Access requires the `data-engineering` role in the internal SSO.

### Reprocessing

If quarantined events are determined to be valid after a fix (e.g., a schema validation bug in the ingestion service), they can be replayed from the quarantine topic to the main `analytics.events` topic using the `dq-replay` tool:

```shell
# Replay quarantined events for a workspace between two timestamps
dq-replay \
  --workspace-id ws_abc123 \
  --from "2026-02-19T00:00:00Z" \
  --to "2026-02-20T00:00:00Z" \
  --reason SCHEMA_VALIDATION_FAILURE \
  --dry-run    # Remove --dry-run to actually replay
```

Always run with `--dry-run` first and review the output before replaying.

## Data Quality Incidents

### Ownership

Data quality incidents are owned by the **Data Engineering** team. For ingestion-side issues (schema failures, quarantine queue depth), the platform on-call is initially engaged if there's a PagerDuty page. For ClickHouse-side issues (completeness, consistency), the Data Engineering team is directly notified via Slack.

### Escalation Path

1. `#alerts-data-platform` — automated alerts land here; Data Engineering team monitors
2. If no response in 30 minutes during business hours: `@data-eng-oncall` Slack handle
3. If customer data is visibly impacted (missing events in dashboards): treat as P1, engage the on-call engineering manager

### Investigation Checklist

When a data quality alert fires:

1. Identify the affected workspace(s) or global scope
2. Determine which validation rule is failing and at what rate
3. Check recent SDK version deployments (bad SDK release is a common cause)
4. Check schema registry for recent schema changes
5. Sample quarantined events to understand the actual event structure
6. Determine if the issue is in the SDK, the validation rule, or the schema definition
7. Decide: fix the rule, fix the schema, or notify the workspace owner to fix their SDK

### Customer Communication

If a workspace owner has lost data (events quarantined and not replayable), Customer Success must be notified within 4 hours so they can proactively reach out. Create a Jira ticket tagged `data-incident` in the ENG project with the affected workspace IDs, estimated event count, and time range.

## Related

- [Event Schema Registry](https://placeholder.invalid/page/data-engineering%2Fevent-schema-registry.md)
- [Materialized Views](https://placeholder.invalid/page/data-engineering%2Fmaterialized-views.md)
- [Ingestion pipeline service docs](https://placeholder.invalid/page/services)
- [Query Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-query-service.md)
