---
title: Disaster Recovery RTO Analysis
id: "5177559"
space: TD
version: 2
labels:
    - rto
    - rpo
    - reliability
    - architecture
    - disaster-recovery
author: Robert Gonek
created_at: "2026-02-24T14:55:18Z"
last_modified_at: "2026-02-24T14:55:20Z"
last_modified_by: Robert Gonek
---
# Disaster Recovery RTO/RPO Analysis

This document records the targets, observed results, and post-drill analysis from Luminary's disaster recovery testing program. It is updated after each DR drill.

For the step-by-step recovery procedures, see the [Operations DR Runbook](https://placeholder.invalid/page/operations%2Fdisaster-recovery.md).

---

## Targets

| Component | RTO Target | RPO Target | Tier |
| --- | --- | --- | --- |
| API Service (customer-facing) | 15 minutes | 0 (stateless; no data loss possible) | Critical |
| Ingest API | 15 minutes | 5 minutes (events buffered in Kafka) | Critical |
| Postgres (primary) | 30 minutes | 5 minutes | Critical |
| ClickHouse cluster | 60 minutes | 15 minutes (S3-backed; re-attach cold data) | High |
| Redis (ElastiCache) | 20 minutes | 0 (cache; rebuilt on demand) | High |
| Kafka (MSK) | 45 minutes | 0 (replicated; no data loss on broker failure) | Critical |
| Stream Processor | 30 minutes | 10 minutes (replay from Kafka) | High |
| Search Service | 4 hours | 24 hours (index rebuilt from Postgres/ClickHouse) | Medium |
| Billing Service | 2 hours | 1 hour | High |
| Dashboard frontend | 10 minutes | 0 (stateless) | Critical |

---

## Q3 2025 DR Drill Results

**Drill date**: 2024-10-04\
**Scenario**: Simulated loss of `us-east-1a` AZ (cordon all `us-east-1a` nodes, fail over RDS, drain ALB target group in that AZ)\
**Duration**: 9:00 AM – 2:30 PM UTC\
**Participants**: Infrastructure team, Data Engineering on-call, Platform on-call

### Results vs Targets

| Component | RTO Target | Actual Recovery Time | Gap | Notes |
| --- | --- | --- | --- | --- |
| API Service | 15 min | 8 min | **On target** | EKS rescheduled pods to remaining AZs; ALB health check removed degraded nodes automatically |
| Ingest API | 15 min | 11 min | **On target** | Kafka producer re-connected to surviving brokers within 3 min; pod reschedule took 8 min |
| Postgres (primary) | 30 min | 22 min | **On target** | RDS Multi-AZ failover triggered automatically; DNS propagation was the main delay |
| ClickHouse cluster | 60 min | 94 min | **Missed by 34 min** | `ch-prod-01` was in `us-east-1a`; ZooKeeper session timeout was 90s but replica re-election + part reattachment from S3 took additional time |
| Redis | 20 min | 14 min | **On target** | ElastiCache cluster mode re-elected primary shards quickly; warm-up cache miss spike lasted ~4 min |
| Kafka (MSK) | 45 min | 7 min | **On target** | MSK auto-recovery of the `us-east-1a` broker replica; no manual intervention needed |
| Stream Processor | 30 min | 26 min | **On target** | State store restore from changelog on new AZ took 22 min; within target |
| Search Service | 4 hours | 4 hours 38 min | **Missed by 38 min** | Elasticsearch cluster split-brain briefly; shard allocation took longer than expected after coordinator re-election |
| Billing Service | 2 hours | 1 hour 47 min | **On target** |  |
| Dashboard frontend | 10 min | 6 min | **On target** |  |

**RPO analysis**: No data loss was observed for Postgres, Kafka, or ClickHouse. The S3-backed ClickHouse parts were intact. A 3-minute window of ingest events was buffered in Kafka during the Ingest API pod reschedule; these were fully processed after recovery. Effective RPO was approximately 0 for all components.

---

## Gap Analysis and Remediation

### ClickHouse Recovery Miss (34 minutes over target)

**Root cause**: The ZooKeeper session timeout (`zookeeper_session_timeout_ms = 30000`) caused a 30-second delay before the replica election triggered. After election, re-attaching cold S3 parts required synchronous metadata fetches for 847 parts across 24 monthly partitions. Each metadata fetch takes ~80ms; the math puts us at 68 seconds just for part reattachment — which is not fast enough at scale.

**Remediation actions taken**:

1. Reduced `zookeeper_session_timeout_ms` from 30,000ms to 10,000ms ✅ (deployed 2024-10-04)
2. Implemented parallel part metadata prefetching in the ClickHouse startup sequence using a custom `config.d` hook ✅ (deployed 2024-10-04)
3. Added a pre-warmed standby replica in `us-east-1b` that stays fully synced ✅ (completed 2024-10-04) — this reduced reattachment time dramatically since `ch-prod-02` was already current

**Post-remediation estimate**: With the standby in `us-east-1b` now pre-warmed, the expected ClickHouse recovery time is 25–35 minutes in an AZ failure scenario — within target.

### Search Service Split-Brain (38 minutes over target)

**Root cause**: The Elasticsearch cluster used a `discovery.seed_hosts` list that included the `us-east-1a` coordinator node. After that node became unreachable, the election round took 4 minutes due to `discovery.zen.minimum_master_nodes` misconfiguration (still set to `2` on a 3-node cluster, allowing split-brain).

**Remediation actions taken**:

1. Updated `minimum_master_nodes` to `2` AND enabled `cluster.election.back_off_time` to reduce election storm ✅
2. Removed `us-east-1a` coordinator node from the static seed list; replaced with Route53 internal DNS records that point only to healthy nodes ✅
3. Documented Elasticsearch AZ failure procedure in the Operations runbook ✅

---

## Improvement Actions Status

| Action | Owner | Status | Target Date |
| --- | --- | --- | --- |
| ClickHouse ZooKeeper timeout reduction | Data Engineering | Complete | 2024-10-04 |
| ClickHouse parallel part prefetching | Data Engineering | Complete | 2024-10-04 |
| ClickHouse pre-warmed `us-east-1b` standby | Infra | Complete | 2024-10-04 |
| Elasticsearch seed hosts fix | Platform | Complete | 2024-10-04 |
| Elasticsearch `minimum_master_nodes` fix | Platform | Complete | 2024-10-04 |
| Automate DR drill via runbook scripts | Infra | In progress | 2024-10-04 |
| Add RDS replica in `eu-west-1` for geo-failover | Infra | Planned | Q2 2026 |

---

## Next DR Drill

**Scheduled**: Q1 2026 — tentatively 2024-10-04\
**Scenario**: Full region failover simulation (`us-east-1` → `eu-west-1` read-only mode with partial write capability via async Postgres replica promotion)\
**Scope**: This drill will be more invasive than Q3 2025 — it tests cross-region failover, which has not been exercised in production before\
**Pre-drill requirements**:

- Verify `eu-west-1` ClickHouse read replica is current (Data Engineering)
- Verify `eu-west-1` RDS replica promotion procedure (Infra)
- Rehearse customer communication template (Customer Success)
- Run drill on a Thursday (lower traffic day, based on traffic analysis)

All engineers on the DR call list must confirm availability by 2024-10-04.
