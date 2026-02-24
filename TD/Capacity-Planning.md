---
title: Capacity Planning
id: "6652054"
space: TD
version: 2
labels:
    - scaling
    - capacity-planning
    - operations
    - infrastructure
author: Robert Gonek
created_at: "2026-02-24T14:57:09Z"
last_modified_at: "2026-02-24T14:57:15Z"
last_modified_by: Robert Gonek
---
# Capacity Planning

This page documents Luminary's capacity planning process: how we measure current utilization, when we scale, and how we forecast future requirements. Capacity reviews are conducted quarterly by Platform Engineering and Data Platform.

For current alert thresholds based on capacity, see [Monitoring & Alerting](https://placeholder.invalid/page/operations%2Fmonitoring-and-alerting.md). For SLO context, see [SLA & SLO Definitions](https://placeholder.invalid/page/operations%2Fsla-and-slo-definitions.md).

---

## Table of Contents

1. [Current Capacity Snapshot](#current-capacity-snapshot)
2. [Load Forecast — Next 3 Quarters](#load-forecast--next-3-quarters)
3. [Scaling Triggers and Procedures](#scaling-triggers-and-procedures)
4. [Quarterly Capacity Review Process](#quarterly-capacity-review-process)
5. [Cost Optimization Initiatives](#cost-optimization-initiatives)

---

## Current Capacity Snapshot

*Snapshot as of: 2026-02-01*

| Service / Resource | Current Allocation | Peak Utilization (30d) | Headroom | Next Scaling Threshold | Owner |
| --- | --- | --- | --- | --- | --- |
| API Gateway pods | 4 pods × 1 vCPU / 1 GB | 58% CPU, 42% RAM | ~72% growth headroom | CPU >70% avg 10 min → add 2 pods | Platform |
| Auth Service pods | 3 pods × 0.5 vCPU / 512 MB | 34% CPU, 38% RAM | Comfortable | CPU >70% → add 2 pods | Platform |
| Ingestion API pods | 4 pods × 1 vCPU / 1 GB | 61% CPU, 45% RAM | ~65% growth headroom | CPU >70% → HPA scales | Platform |
| Ingestion Workers (max) | 12 pods max × 2 vCPU / 2 GB | 8 pods peak, 4 idle | 4 pods spare capacity | KEDA scales on lag >50k | Data Platform |
| ClickHouse nodes | 12 nodes × 16 vCPU / 128 GB / 2 TB SSD | 52% CPU, 61% RAM, 67% disk | ~50% disk headroom | Disk >80% → expand PVCs | Data Platform |
| Kafka brokers | 3 × 4 vCPU / 16 GB / 1 TB SSD | 38% CPU, 44% disk | Comfortable | Disk >70% → add broker | Data Platform |
| Postgres (Aurora) | `db.r6g.2xlarge` (8 vCPU, 64 GB) | 41% CPU, 68% connections | ~47% connection headroom | CPU >60% → upgrade instance class | Platform |
| GKE Node Pool (general) | 6 nodes × `n2-standard-8` | 62% CPU requested, 58% RAM | ~38% headroom | Requested CPU >80% → add 2 nodes | Platform |
| GKE Node Pool (ClickHouse) | 12 nodes × `n2-highmem-16` | Steady, well-utilized | Low — watch disk | Disk >75% → PVC expand | Data Platform |
| GCS Storage (backups) | ~8 TB used | Grows ~200 GB/month | 5+ years at current rate | N/A (object storage) | Platform |

**Key observations (Q1 2026)**:

- ClickHouse disk growth is the most pressing capacity concern. At current growth rate (+180 GB/month compressed), nodes will hit the 80% warning threshold around **August 2026**.
- Postgres connection utilization is high due to connection pooling settings on auth-service. A PgBouncer tuning task is in progress (PLAT-4710).
- Ingestion worker KEDA scaling is working well — peak of 8 pods during the Monday morning traffic surge.

---

## Load Forecast — Next 3 Quarters

Projections based on current customer growth rate (+18% QoQ events volume) and committed ARR pipeline (Sales forecast from [Sprint Planning](https://placeholder.invalid/page/..%2FSD%2Fmeetings%2Fsprint-planning-sprint-47.md)).

| Resource | Current (Q1 2026) | Q2 2026 Forecast | Q3 2026 Forecast | Q4 2026 Forecast | Action Required |
| --- | --- | --- | --- | --- | --- |
| Ingestion throughput (events/sec, peak) | 28k | 33k | 39k | 46k | Workers: KEDA max replicas → 16 by Q3 |
| ClickHouse data volume | 14 TB | 17 TB | 20.5 TB | 24 TB | PVC expansion Q2; add 2 nodes by Q3 |
| API Gateway RPS (peak) | 1,100 | 1,300 | 1,550 | 1,830 | Increase pod max in HPA by Q3 |
| Postgres storage | 180 GB | 230 GB | 285 GB | 340 GB | No action needed; Aurora auto-scales |
| Postgres connections (peak) | 612 / 900 | 720 / 900 | 840 / 900 | ~1,050 → overflow | PgBouncer + upgrade to `r6g.4xlarge` by Q3 |
| Kafka storage (`events-raw`) | 420 GB | 500 GB | 590 GB | 700 GB | Add broker by Q4; increase retention review |
| Monthly GCS cost (backups) | $1,240 | $1,450 | $1,710 | $2,020 | Lifecycle rules review Q2 |

---

## Scaling Triggers and Procedures

### API Gateway / Auth Service / Ingestion API (HPA)

These services use Kubernetes HorizontalPodAutoscaler. No manual intervention normally required.

```shell
# Check current HPA state
kubectl get hpa -n production

# Manual override if HPA is misbehaving
kubectl scale deployment api-gateway -n production --replicas=6
```

Scaling thresholds are defined in `luminary-k8s/envs/production/api-gateway/hpa.yaml`. Changes to thresholds require a PR review.

### Ingestion Workers (KEDA)

KEDA scales workers based on Kafka consumer lag. The `ScaledObject` config is at `luminary-k8s/envs/production/ingestion-worker/scaledobject.yaml`.

Current config: scale up when lag >50k, target 1 pod per 30k lag messages, max 12 pods.

**To increase max replicas** (requires capacity review approval):

```shell
# Edit the ScaledObject
kubectl edit scaledobject ingestion-worker-scaler -n production
# Change: maxReplicaCount: 12 → 16
```

### ClickHouse — Disk Expansion

ClickHouse PVCs use GKE `pd-ssd` storage class which supports online expansion.

```shell
# Expand a PVC (no pod restart required for online resize)
kubectl patch pvc data-clickhouse-shard-0-replica-0 -n clickhouse \
  -p '{"spec":{"resources":{"requests":{"storage":"3Ti"}}}}'

# Verify resize was accepted by GKE
kubectl get pvc data-clickhouse-shard-0-replica-0 -n clickhouse

# Confirm ClickHouse sees the new disk space (may take 1-2 min)
kubectl exec -n clickhouse clickhouse-shard-0-replica-0 -- \
  clickhouse-client --query "SELECT name, formatReadableSize(total_space) FROM system.disks;"
```

### ClickHouse — Adding a Shard

Adding a new shard requires updating the cluster topology and redistribution of data. This is a planned operation, not an emergency measure. It requires a dedicated maintenance window and L2 (Priya Nair) to lead.

High-level process:

1. Provision 2 new GKE nodes (`n2-highmem-16`) in the ClickHouse node pool.
2. Update `chi` (ClickHouseInstallation) CR to add shard 6 with 2 replicas.
3. ZooKeeper automatically propagates topology change.
4. Run resharding script to redistribute data from existing shards (background operation, takes hours).
5. Update ingestion workers and query service with new cluster topology endpoint.

### Postgres — Vertical Scaling

Aurora Postgres supports near-zero-downtime vertical scaling (brief failover ~30 seconds during instance class change).

```shell
# Modify Aurora cluster instance class (AWS CLI)
aws rds modify-db-instance \
  --db-instance-identifier luminary-postgres-prod-writer \
  --db-instance-class db.r6g.4xlarge \
  --apply-immediately \
  --region us-central1
```

Schedule instance class changes during the low-traffic window (03:00–05:00 UTC). Announce in `#deployments` and update the status page as **Scheduled Maintenance**.

---

## Quarterly Capacity Review Process

The quarterly capacity review is a 1-hour meeting held in the first week of each quarter. Attendees: Platform Eng lead, Data Platform lead, Finance (for cost review).

**Agenda**:

1. Review previous quarter actuals vs forecast (were our forecasts accurate?).
2. Update the load forecast table for the next 3 quarters using latest growth metrics from Datadog and Sales pipeline data.
3. Identify any resources within 1 quarter of a hard scaling threshold.
4. Approve capacity expansion requests (PVC increases, node pool changes, instance upgrades).
5. Review cost optimization initiatives progress.

**Inputs for the meeting**:

- Datadog capacity report: 90-day trend for all metrics in the [Current Capacity Snapshot](#current-capacity-snapshot) table.
- Sales team: committed new ARR for next quarter + forecast event volume from top 5 prospects.
- GCP billing export: current monthly cost by service.

**Output**: Updated capacity planning doc (this page) + Jira capacity tickets for approved changes.

---

## Cost Optimization Initiatives

| Initiative | Expected Saving | Target Quarter | Status | Owner |
| --- | --- | --- | --- | --- |
| ClickHouse cold-tier: move partitions older than 90 days to `pd-standard` | ~$800/month | Q2 2026 | In planning (PLAT-4801) | Priya Nair |
| GCS backup lifecycle rules: move backups >30 days to Nearline, >90 days to Coldline | ~$300/month | Q2 2026 | In progress (PLAT-4803) | Platform |
| Committed Use Discounts: convert `n2-highmem-16` ClickHouse nodes to 1-year CUD | ~$1,200/month | Q1 2026 | Approved, pending GCP order | Platform |
| PgBouncer connection pooler: reduce Postgres instance class requirement | ~$400/month | Q2 2026 | In progress (PLAT-4710) | Platform |
| Spot instances for ingestion workers (stateless, KEDA-managed) | ~$200/month | Q3 2026 | Under evaluation | Platform |
| Grafana/Loki storage: reduce log retention staging from 14d to 3d | ~$60/month | Q1 2026 | Done ✅ | Platform |

**Total approved / in-progress savings**: ~$2,960/month

---

*Last reviewed: 2026-02-01 · Owner: Platform Engineering · Next capacity review: 2026-04-07*
