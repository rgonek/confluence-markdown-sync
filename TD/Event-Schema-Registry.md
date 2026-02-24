---
title: Event Schema Registry
id: "5996822"
space: TD
version: 2
labels:
    - data-engineering
    - schema-registry
    - kafka
    - avro
author: Robert Gonek
created_at: "2026-02-24T14:55:43Z"
last_modified_at: "2026-02-24T14:55:45Z"
last_modified_by: Robert Gonek
---
# Event Schema Registry

Luminary uses [Confluent Schema Registry](https://docs.confluent.io/platform/current/schema-registry/index.html) to manage Avro schemas for all Kafka topics. This ensures that producers and consumers share a common schema contract and that schema evolution is controlled.

Schema Registry is deployed in the `data` namespace in the Kubernetes cluster and is accessible internally at `schema-registry.data.svc.cluster.local:8081`.

## What the Schema Registry Does

The Schema Registry stores versioned schemas identified by a **subject name**. For Luminary's event topics, the subject naming convention is `<topic>-value` (e.g. `analytics.events-value`). When a producer serializes an Avro message, it includes a schema ID in the message header. Consumers use this ID to fetch the correct schema from the registry and deserialize the message.

This decouples producers and consumers: a consumer can handle messages from multiple schema versions as long as they are compatible.

## Compatibility Mode

All production topics use **FULL_TRANSITIVE** compatibility. This means:

- New schemas must be **backward compatible** (consumers using the old schema can read new messages)
- New schemas must be **forward compatible** (consumers using the new schema can read old messages)
- This compatibility must hold transitively across all historical versions, not just between adjacent versions

In practice, FULL_TRANSITIVE means:

- You can **add** optional fields with defaults
- You can **remove** fields that had defaults
- You cannot **rename** fields
- You cannot **change** field types
- You cannot **add** required fields (no default value)

If you need to make a breaking change, you must create a new topic and migrate producers/consumers.

## Registering a New Event Schema

### Step 1: Write the Avro schema

Create an `.avsc` file in `data-engineering/schemas/`:

```json
// data-engineering/schemas/workspace_created.avsc
{
  "type": "record",
  "name": "WorkspaceCreated",
  "namespace": "io.luminary.events",
  "doc": "Emitted when a new workspace is created",
  "fields": [
    {
      "name": "event_id",
      "type": "string",
      "doc": "UUID for this event instance"
    },
    {
      "name": "event_timestamp",
      "type": {
        "type": "long",
        "logicalType": "timestamp-millis"
      },
      "doc": "Unix timestamp in milliseconds when the event occurred"
    },
    {
      "name": "workspace_id",
      "type": "string",
      "doc": "Luminary workspace ID (ws_*)"
    },
    {
      "name": "workspace_name",
      "type": "string"
    },
    {
      "name": "plan",
      "type": {
        "type": "enum",
        "name": "Plan",
        "symbols": ["FREE", "STARTER", "GROWTH", "ENTERPRISE"]
      }
    },
    {
      "name": "created_by_user_id",
      "type": "string"
    },
    {
      "name": "trial_ends_at",
      "type": ["null", {"type": "long", "logicalType": "timestamp-millis"}],
      "default": null,
      "doc": "Null if not on trial"
    }
  ]
}
```

### Step 2: Validate compatibility before registering

```shell
# Check if the new schema is compatible with the existing subject
curl -X POST \
  -H "Content-Type: application/vnd.schemaregistry.v1+json" \
  -d "{\"schema\": $(cat workspace_created.avsc | jq -Rs .)}" \
  http://schema-registry.data.svc.cluster.local:8081/compatibility/subjects/analytics.workspace-created-value/versions/latest
```

Expected response for a compatible schema:

```json
{"is_compatible": true}
```

### Step 3: Register the schema

```shell
curl -X POST \
  -H "Content-Type: application/vnd.schemaregistry.v1+json" \
  -d "{\"schema\": $(cat workspace_created.avsc | jq -Rs .)}" \
  http://schema-registry.data.svc.cluster.local:8081/subjects/analytics.workspace-created-value/versions
```

Response:

```json
{"id": 47}
```

The returned `id` is the schema ID embedded in every Kafka message using this schema.

### Step 4: Create the Kafka topic (if new event type)

```shell
kafka-topics.sh \
  --bootstrap-server prod-msk:9092 \
  --create \
  --topic analytics.workspace-created \
  --partitions 16 \
  --replication-factor 3 \
  --config retention.ms=604800000 \
  --config min.insync.replicas=2
```

### Step 5: Add to the schema inventory

Update the [Schema Inventory](#schema-inventory) table at the bottom of this document and open a PR.

### Step 6: Update consumer SDKs

Consumers that need to handle the new event type must regenerate their Avro bindings. For Go:

```shell
avrogen -pkg events -o pkg/events/generated/ data-engineering/schemas/workspace_created.avsc
```

## Versioning Procedure

When updating an existing schema:

1. Create a new `.avsc` file (e.g. `workspace_created_v2.avsc`) for review purposes — don't edit the original in place until the PR is merged.
2. Run the compatibility check (Step 2 above) before opening the PR.
3. PR description must include: what changed, why, and which producers/consumers are affected.
4. After merge, register the new version (Step 3). Do not delete old versions — consumers may still be deserializing old messages from retention.

## How Client SDKs Use the Registry

The Luminary server-side SDK clients (used by internal Go and Node.js services) use the Confluent Avro serializer which automatically:

1. Fetches the latest schema ID for the subject on first write
2. Embeds the 4-byte schema ID as a magic byte prefix in the message value
3. Caches the schema ID locally to avoid a registry round-trip on every message

On the consumer side, the deserializer reads the magic byte prefix, fetches the schema from the registry (cached), and deserializes the message.

Local development uses a mock schema registry. See the [Local Development with Mock Registry](#local-development-with-mock-registry) section below.

## Local Development with Mock Registry

For local development and unit tests, use `testcontainers` to spin up a real Schema Registry instance:

```go
// In test setup
import "github.com/testcontainers/testcontainers-go/modules/registry"

schemaRegistry, err := registry.Run(ctx, "confluentinc/cp-schema-registry:7.6.0",
    registry.WithKafka(kafkaContainer),
)
```

Alternatively, for simpler unit tests, use the mock registry client provided in `pkg/schemaregistry/mock/`:

```go
mockRegistry := mock.NewRegistry()
mockRegistry.RegisterSchema("analytics.events-value", eventSchema)
```

The mock registry is not compatible with real Confluent Schema Registry behavior for compatibility checks — use `testcontainers` when testing compatibility logic.

## Schema Inventory

Current registered schemas as of February 2026. Schema IDs match the Confluent Schema Registry IDs in production.

| Event Type | Topic | Subject | Schema ID | Added | Owner |
| --- | --- | --- | --- | --- | --- |
| `page_viewed` | `analytics.events` | `analytics.events-value` | 1 | 2023-04 | Platform |
| `track` (generic) | `analytics.events` | `analytics.events-value` | 1 | 2023-04 | Platform |
| `identify` | `analytics.identify` | `analytics.identify-value` | 3 | 2023-04 | Platform |
| `group` | `analytics.group` | `analytics.group-value` | 5 | 2023-05 | Platform |
| `workspace_created` | `analytics.workspace-created` | `analytics.workspace-created-value` | 12 | 2023-06 | Growth |
| `workspace_deleted` | `analytics.workspace-deleted` | `analytics.workspace-deleted-value` | 14 | 2023-06 | Growth |
| `user_invited` | `analytics.user-invited` | `analytics.user-invited-value` | 17 | 2023-07 | Growth |
| `user_joined` | `analytics.user-joined` | `analytics.user-joined-value` | 18 | 2023-07 | Growth |
| `subscription_created` | `billing.subscription-created` | `billing.subscription-created-value` | 21 | 2023-09 | Billing |
| `subscription_updated` | `billing.subscription-updated` | `billing.subscription-updated-value` | 22 | 2023-09 | Billing |
| `subscription_cancelled` | `billing.subscription-cancelled` | `billing.subscription-cancelled-value` | 23 | 2023-09 | Billing |
| `invoice_paid` | `billing.invoice-paid` | `billing.invoice-paid-value` | 24 | 2023-09 | Billing |
| `invoice_payment_failed` | `billing.invoice-payment-failed` | `billing.invoice-payment-failed-value` | 25 | 2023-09 | Billing |
| `feature_flag_evaluated` | `analytics.feature-flags` | `analytics.feature-flags-value` | 31 | 2023-11 | Platform |
| `export_requested` | `jobs.export-requested` | `jobs.export-requested-value` | 34 | 2024-01 | Analytics |
| `export_completed` | `jobs.export-completed` | `jobs.export-completed-value` | 35 | 2024-01 | Analytics |
| `webhook_delivery_attempted` | `jobs.webhook-delivery` | `jobs.webhook-delivery-value` | 38 | 2024-03 | Platform |
| `gdpr_deletion_requested` | `compliance.gdpr-deletion` | `compliance.gdpr-deletion-value` | 41 | 2024-06 | Platform |
| `report_created` | `analytics.report-created` | `analytics.report-created-value` | 44 | 2024-08 | Analytics |
| `funnel_saved` | `analytics.funnel-saved` | `analytics.funnel-saved-value` | 46 | 2024-10 | Analytics |
| `workspace_created` (v2) | `analytics.workspace-created` | `analytics.workspace-created-value` | 47 | 2025-02 | Growth |
| `api_key_created` | `security.api-key-created` | `security.api-key-created-value` | 49 | 2025-04 | Platform |
| `api_key_revoked` | `security.api-key-revoked` | `security.api-key-revoked-value` | 50 | 2025-04 | Platform |
| `session_started` | `analytics.sessions` | `analytics.sessions-value` | 52 | 2025-06 | Mobile |
| `session_ended` | `analytics.sessions` | `analytics.sessions-value` | 52 | 2025-06 | Mobile |

## Related

- [Data Quality Framework](Data-Quality-Framework.md)
- [Materialized Views](https://placeholder.invalid/page/data-engineering%2Fmaterialized-views.md)
- [Worker Service](https://placeholder.invalid/page/services%2Fworker-service.md) — consumes several of these topics
