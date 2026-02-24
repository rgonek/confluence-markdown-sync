---
title: Data Engineering
id: "4948083"
space: TD
version: 2
labels:
    - data-engineering
    - overview
author: Robert Gonek
created_at: "2026-02-24T14:55:45Z"
last_modified_at: "2026-02-24T14:55:47Z"
last_modified_by: Robert Gonek
---
# Data Engineering

The Data Engineering section covers Luminary's data infrastructure: the pipelines that ingest, validate, enrich, and store event data from tens of thousands of workspaces, and the query layer that serves that data back to customers in sub-second response times.

Most of the complexity in Luminary's backend lives here. Events arrive via the Ingest API at millions per day, flow through a Kafka-based streaming pipeline, land in ClickHouse for analytics queries, and are mirrored to S3 for long-term retention and compliance exports.

## Documentation

| Document | Description |
| --- | --- |
| [ClickHouse Schema Reference](ClickHouse-Schema-Reference.md) | Full DDL and query patterns for every table in the analytics database |
| [Kafka Topics Reference](https://placeholder.invalid/page/data-engineering%2Fkafka-topics-reference.md) | Topic inventory, schemas, consumer groups, and retention policies |
| [Stream Processing](https://placeholder.invalid/page/data-engineering%2Fstream-processing.md) | Kafka Streams topology, state stores, lag monitoring, and late-event handling |
| [Data Retention & Purging](Data-Retention-and-Purging.md) | GDPR deletion flows, per-type retention table, TTL configuration, S3 lifecycle rules |
| [Analytics Query Patterns](Analytics-Query-Patterns.md) | Performance guide for writing ClickHouse queries in the query engine |

## Data Store Inventory

| Store | Technology | Primary Role | Managed By |
| --- | --- | --- | --- |
| Analytics DB | ClickHouse 23.8 (3-node EC2 cluster) | Immutable event storage, aggregations, funnel computation | Data Engineering |
| Operational DB | PostgreSQL 15 (RDS Multi-AZ) | Workspace config, user records, billing, sessions metadata | Platform / Service teams |
| Cache | Redis 7 (ElastiCache cluster mode, 6 shards) | Hot query results, rate-limit counters, session tokens | Platform |
| Message Bus | Apache Kafka 3.5 (MSK, 3 brokers) | Event ingestion pipeline, service integration events | Data Engineering |
| Object Storage | Amazon S3 | Raw event archive, export files, model artifacts, backups | Data Engineering / Infra |

## Event Volume (current)

- **Ingest rate**: ~4,200 events/sec sustained, peak~18,000 events/sec
- **Daily volume**: ~350M events/day across all workspaces
- **ClickHouse data size**: ~14 TB compressed (raw events table)
- **Kafka lag (P99)**: < 2 seconds end-to-end under normal load

## Ownership

The Data Engineering team owns the pipeline from Kafka ingestion through ClickHouse landing. The Query Service team owns the ClickHouse read path and query execution layer. Infra owns provisioning and patching of all underlying resources.

For on-call escalation paths see the [Operations runbook index](https://placeholder.invalid/page/operations%2Findex.md).
