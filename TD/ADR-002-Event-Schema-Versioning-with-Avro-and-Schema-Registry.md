---
title: 'ADR-002: Event Schema Versioning with Avro and Schema Registry'
id: "7078015"
space: TD
version: 2
labels:
    - adr
    - architecture-decision
    - schema
    - kafka
    - avro
author: Robert Gonek
created_at: "2026-02-24T14:55:07Z"
last_modified_at: "2026-02-24T14:55:08Z"
last_modified_by: Robert Gonek
---
# ADR-002: Event Schema Versioning with Avro and Schema Registry

| Field | Value |
| --- | --- |
| **Status** | Accepted |
| **Date** | 2022-01-08 |
| **Deciders** | Platform Engineering, Data Engineering |
| **Supersedes** | — |
| **Superseded by** | — |

---

## Context

Throughout 2023, we suffered three separate production incidents where a customer-side SDK update changed the shape of an event payload in a way that broke downstream processing:

- **Incident INC-0482 (March 2023):** A customer renamed the `user_id` property to `userId` (camelCase) in their server-side instrumentation. The Flink stream processor expected `user_id`, failed to enrich events with user properties, and silently wrote null enrichment data to ClickHouse for 11 hours before the data quality alert fired.
- **Incident INC-0601 (July 2023):** A customer added a new required field to their custom `checkout_completed` event without registering the schema change. Because we validated only the envelope structure (not property types), integer values sent as strings passed validation, causing ClickHouse insert failures for that event type for 6 hours.
- **Incident INC-0714 (October 2023):** A customer removed a field that a saved dashboard funnel query was filtering on. The query began returning zero results, which was interpreted as a genuine drop in conversions by their product team. The incident was not detected for 3 days.

The root cause of all three incidents was the same: **event schemas were implicitly defined by whatever the SDK sent, with no formal contract enforced at the boundary.**

Our existing approach was to accept JSON event payloads and validate only the envelope structure (required top-level fields like `event_name`, `timestamp`, `anonymous_id`). Property-level structure was "documented" in a Notion wiki but never enforced programmatically. This created a wide gap between documented intent and runtime reality.

The team needed a solution that:

1. Enforces schema contracts at the ingestion boundary, not just as documentation.
2. Prevents breaking changes from reaching consumers without an explicit migration.
3. Supports gradual schema evolution (adding optional fields) without requiring coordinated deploys.
4. Works at scale — schema validation must not add more than 1 ms of overhead on the ingestion hot path.
5. Is self-service for customers — engineers should be able to register and evolve schemas via an API or UI without Platform Engineering involvement.

---

## Decision

We will adopt **Avro** as the event serialisation format, enforced via the **Confluent Schema Registry** deployed on our EKS cluster.

**Schema registration:** Customers register their event schemas via the Luminary developer API (`POST /v1/schemas/{workspace_id}/{event_name}`). The API validates the schema for Avro correctness and registers it with the Schema Registry under the subject `{workspace_id}.{event_name}`.

**Compatibility mode:** All subjects are registered with `BACKWARD_COMPATIBLE` enforcement by default. This means:

- New optional fields (with a default value) are allowed.
- Renaming or removing fields is rejected.
- Changing a field's type is rejected.

Customers who need breaking schema changes must create a new event name (e.g., `checkout_completed_v2`) and migrate their instrumentation to the new event name. The old event name and its accumulated data continue to function.

**Wire format:** We use the Confluent wire format — a 5-byte header (magic byte `0x00` + 4-byte schema ID) prepended to the Avro binary payload. This allows consumers to look up the schema by ID without embedding schema metadata in every message.

**Validation enforcement point:** The Schema Validator sidecar on the Ingestion Service validates each event against the registered schema before the event is published to Kafka. Invalid events are rejected with a `422` response. Events that pass validation are serialised to Avro binary before being written to Kafka; the original JSON is not stored.

**Flink schema handling:** The Stream Processor carries a schema projection map. When consuming messages, it reads the schema ID from the Confluent wire format header, fetches the latest schema version for that subject, and uses the Avro `GenericDatumReader` with schema evolution to project old messages to the latest schema (filling new fields with their defaults).

---

## Alternatives Considered

### JSON Schema with AJV Validation

**Approach:** Define schemas as JSON Schema (Draft 7) documents. Validate at the ingestion boundary using AJV in the Schema Validator sidecar. Publish raw JSON to Kafka.

**Pros:**

- JSON Schema is widely understood; low ramp-up cost for customers.
- Excellent tooling support (editors, validators, code generators).
- No binary serialisation step; messages are human-readable in Kafka.

**Cons:**

- No binary encoding: JSON is 3–5× larger than Avro binary on the wire. At our event volume (peak 150,000 events/second projected), Kafka storage costs increase substantially.
- JSON Schema has no built-in compatibility enforcement — a registry would need to be built or bought separately.
- Schema evolution semantics are not as well-defined as Avro; compatibility checking requires custom logic.
- Consumers still need to handle schema evolution manually; no auto-projection for new fields.

**Verdict:** Rejected. Storage cost impact was prohibitive, and the lack of native compatibility enforcement meant we would still need to build or integrate a compatibility layer.

### Protocol Buffers (Protobuf) with Schema Registry

**Approach:** Define schemas as `.proto` files. Compile to language-specific generated code. Use a Protobuf-aware Schema Registry (Confluent supports Protobuf subjects).

**Pros:**

- Protobuf is extremely compact and fast to serialise/deserialise.
- Strong tooling across all major languages.
- Good compatibility story: field numbers ensure backward/forward compatibility.
- Confluent Schema Registry supports Protobuf subjects natively.

**Cons:**

- Customer-facing SDK integration is significantly harder. Customers cannot dynamically register schemas from a web UI — they must generate `.proto` files and share compiled stubs, which is a poor experience for a self-service product.
- Schema evolution requires understanding of field number semantics, which is non-obvious for customers without Protobuf experience.
- Protobuf's `oneof` and nested message types make the schema model harder to represent as a flat event property bag.
- Flink's Protobuf deserialization requires generated classes at job compile time, making dynamic schema loading (for customer-specific schemas) significantly more complex.

**Verdict:** Rejected. The customer-facing self-service requirement was incompatible with Protobuf's code-generation model. The operational complexity for dynamic schema registration was not justified given Avro's similar performance characteristics.

---

## Consequences

### Positive

- Zero schema-related production incidents since adoption (November 2023 to present).
- Kafka storage costs reduced by ~62% due to Avro binary encoding vs. prior JSON approach (average event: 840 bytes JSON → 320 bytes Avro).
- Customers receive immediate, specific error responses when their event payload does not match the registered schema, dramatically reducing debugging time.
- The Flink stream processor code is simpler: instead of defensive null-checking for every possible property key, it reads typed Avro fields with defined defaults.

### Negative / Risks

- **Operational overhead of Schema Registry.** We self-host Confluent Schema Registry on EKS. It requires its own Kafka cluster (3-broker dedicated schema registry cluster), monitoring, and upgrade management. Two minor Schema Registry outages have occurred; in both cases, the Schema Validator sidecar's 60-second local schema cache provided full transparency to the ingestion path.
- **Breaking schema changes require a new event name.** Several enterprise customers have pushed back on this constraint; they want to rename fields as their own product evolves. We have documented a migration guide and the developer API guides customers through the `v2` event naming pattern, but this remains a point of friction.
- **ClickHouse stores properties as a JSON blob, not as Avro-typed columns.** Despite validating and serialising events as Avro, we deserialise back to a JSON string before writing to ClickHouse's `properties` column. This was a pragmatic decision (see [ADR-001](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=5341327#consequences): schema migrations on ClickHouse are expensive), but it means ClickHouse does not benefit from Avro's type information at query time.
- **Schema Registry is a new operational dependency** that must be included in incident runbooks and oncall training.

---

## References

- [Data Flow — SDK Batching and Kafka Topics](https://placeholder.invalid/page/architecture%2Fdata-flow.md)
- [Ingestion Service — Schema Validator](https://placeholder.invalid/page/architecture%2Fsystem-overview.md)
- [RFC-002: Event Pipeline Rewrite](https://placeholder.invalid/page/..%2FSD%2Fdecisions%2Frfc-002-event-pipeline-rewrite.md)
- [Customer-facing Schema API Docs (external)](https://developers.luminaryapp.com/schemas)
