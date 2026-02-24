---
title: Backup and Restore Procedures
id: "5341392"
space: TD
version: 2
labels:
    - infrastructure
    - backup
    - disaster-recovery
    - operations
author: Robert Gonek
created_at: "2026-02-24T14:56:32Z"
last_modified_at: "2026-02-24T14:56:34Z"
last_modified_by: Robert Gonek
---
# Backup and Restore Procedures

This document covers backup strategies, restore procedures, and verification steps for all persistent data stores in the Luminary platform. Each section includes RTO/RPO targets and step-by-step restore commands.

**In an active incident**, jump directly to the restore procedure for the affected store. Page the on-call DBA if restore takes longer than 30 minutes or if you encounter unexpected errors.

## Recovery Objectives Summary

| Store | RPO | RTO | Backup Method |
| --- | --- | --- | --- |
| Postgres (RDS) | 5 minutes | 30 minutes | RDS automated backups + PITR |
| ClickHouse | 1 hour | 2 hours | S3 incremental backups via `clickhouse-backup` |
| Redis (ElastiCache) | 24 hours (best effort) | 15 minutes | ElastiCache daily snapshots |
| Kafka | N/A (event log, not recoverable) | 30 minutes (topic recreation) | MirrorMaker 2 replication |

Redis data is considered ephemeral cache — data loss results in a cache cold start, not data loss in the business sense. If Redis data is business-critical in a specific cache (e.g., rate limit counters), document it here.

## Postgres (RDS)

### Backup Configuration

- **Automated backups**: Enabled. Retention: 35 days. Backup window: 03:00–04:00 UTC (inside Saturday maintenance window).
- **Backup storage**: S3 in `us-east-1` with cross-region replication to `eu-west-1` (enabled for compliance).
- **PITR**: Enabled. Transaction logs are continuously backed up — can restore to any point within the retention window.
- **Manual snapshots**: Taken before every major schema migration and before any infrastructure change affecting the RDS instance.

### Pre-Migration Snapshot

Always take a manual snapshot before schema migrations:

```shell
aws rds create-db-snapshot \
  --db-instance-identifier luminary-prod-postgres \
  --db-snapshot-identifier "pre-migration-$(date +%Y%m%d-%H%M%S)" \
  --tags Key=reason,Value=pre-migration Key=env,Value=production
```

Wait for the snapshot to be available before proceeding:

```shell
aws rds wait db-snapshot-available \
  --db-snapshot-identifier "pre-migration-<TIMESTAMP>"
```

### Point-in-Time Restore Procedure

Use PITR when you need to recover from data corruption or accidental deletion. You are restoring to a **new RDS instance** — never restore over the production instance directly.

**Step 1: Identify the restore point**

Choose the target timestamp. Use UTC. If you know the approximate time of the bad event, target 2–5 minutes before it.

```shell
# List available restore windows
aws rds describe-db-instances \
  --db-instance-identifier luminary-prod-postgres \
  --query 'DBInstances[0].LatestRestorableTime'
```

**Step 2: Initiate the PITR restore**

```shell
aws rds restore-db-instance-to-point-in-time \
  --source-db-instance-identifier luminary-prod-postgres \
  --target-db-instance-identifier luminary-prod-postgres-restore-$(date +%Y%m%d) \
  --restore-time "2026-02-20T14:30:00Z" \
  --db-instance-class db.r6g.2xlarge \
  --multi-az \
  --db-subnet-group-name luminary-prod-db-subnet-group \
  --vpc-security-group-ids sg-0123456789abcdef0
```

**Step 3: Wait for the restored instance to be available**

```shell
aws rds wait db-instance-available \
  --db-instance-identifier luminary-prod-postgres-restore-$(date +%Y%m%d)
```

This typically takes 15–25 minutes.

**Step 4: Verify the data**

Connect to the restored instance and verify that the data looks correct at the target timestamp:

```shell
psql -h luminary-prod-postgres-restore-YYYYMMDD.us-east-1.rds.amazonaws.com \
     -U luminary_admin -d luminary_prod

-- Spot check
SELECT COUNT(*) FROM workspaces;
SELECT MAX(created_at) FROM events LIMIT 1;
```

**Step 5: Update application connection string**

If promoting the restored instance as the new production primary, update the RDS endpoint in AWS Secrets Manager and trigger a rolling restart of affected services:

```shell
aws secretsmanager update-secret \
  --secret-id prod/postgres/connection-string \
  --secret-string '{"host":"luminary-prod-postgres-restore-YYYYMMDD.us-east-1.rds.amazonaws.com","port":5432,...}'

# Rolling restart (zero-downtime)
kubectl rollout restart deployment/api-service deployment/auth-service deployment/worker-service -n production
```

**Step 6: Monitor and clean up**

Monitor error rates for 30 minutes. Once the restored instance is confirmed stable, rename it to match the original naming convention and delete the corrupted original after 48 hours (keep it as a safety net).

### Snapshot Restore (Full Instance)

For restoring from a specific snapshot rather than PITR:

```shell
aws rds restore-db-instance-from-db-snapshot \
  --db-instance-identifier luminary-prod-postgres-snap-restore \
  --db-snapshot-identifier pre-migration-20260215-143022 \
  --db-instance-class db.r6g.2xlarge \
  --db-subnet-group-name luminary-prod-db-subnet-group
```

## ClickHouse

### Backup Configuration

ClickHouse backups use [clickhouse-backup](https://github.com/AlexAkulov/clickhouse-backup), running as a CronJob in Kubernetes. Backups are stored in S3 at `s3://luminary-clickhouse-backups-prod/`.

- **Schedule**: Incremental every hour, full backup every Sunday 02:00 UTC
- **Retention**: 30 days of hourly incrementals, 90 days of weekly fulls
- **Compression**: `zstd` (balanced CPU/size)

CronJob configuration:

```yaml
# infrastructure/k8s/clickhouse/backup-cronjob.yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: clickhouse-backup
  namespace: data
spec:
  schedule: "0 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: clickhouse-backup
            image: ghcr.io/alexakulov/clickhouse-backup:2.5.0
            args:
              - create_remote
              - --tables=analytics.*
            env:
            - name: CLICKHOUSE_HOST
              value: clickhouse.data.svc.cluster.local
            - name: S3_BUCKET
              value: luminary-clickhouse-backups-prod
            - name: S3_REGION
              value: us-east-1
            - name: BACKUPS_TO_KEEP_REMOTE
              value: "720"  # 30 days of hourly
```

### Restore Procedure

**Step 1: List available backups**

```shell
clickhouse-backup list remote
# Output example:
# 2026-02-20T01:00:00Z  incremental  12.4 GB
# 2026-02-20T00:00:00Z  incremental  11.8 GB
# 2026-02-16T02:00:00Z  full         340 GB
```

**Step 2: Download the backup**

```shell
clickhouse-backup download 2026-02-20T01:00:00Z
```

**Step 3: Stop writes to ClickHouse** (prevent partially-restored state from being queried)

```shell
kubectl scale deployment ingestion-service --replicas=0 -n production
kubectl scale deployment query-service --replicas=0 -n production
```

**Step 4: Restore**

```shell
clickhouse-backup restore --tables=analytics.* 2026-02-20T01:00:00Z
```

**Step 5: Verify**

```sql
-- Connect and check row counts against expected values
SELECT table, sum(rows) AS total_rows
FROM system.parts
WHERE active AND database = 'analytics'
GROUP BY table
ORDER BY total_rows DESC;

-- Spot check recent data
SELECT max(event_timestamp) FROM analytics.events;
```

**Step 6: Resume services**

```shell
kubectl scale deployment ingestion-service --replicas=3 -n production
kubectl scale deployment query-service --replicas=2 -n production
```

### Verification

After every scheduled backup, a verification job runs that queries row counts from the backup metadata and compares them to the live ClickHouse instance. Mismatches alert to `#alerts-data-platform` Slack channel.

## Redis (ElastiCache)

### Backup Configuration

ElastiCache (Redis 7.x cluster mode enabled) takes automatic daily snapshots at 04:00 UTC. Retention: 7 days. Snapshots are stored in S3 in the same region.

Redis is used exclusively as a cache (query result cache, session tokens, rate limit counters). The RPO is intentionally high (24 hours) because the underlying data is always recoverable from Postgres or ClickHouse — a cache miss is a performance degradation, not a data loss event.

### Restore Procedure

Redis restore is typically needed after cluster corruption or accidental deletion, not data recovery.

**Step 1: Identify the snapshot**

```shell
aws elasticache describe-snapshots \
  --cache-cluster-id luminary-prod-redis \
  --query 'Snapshots[].{Name:SnapshotName,Date:SnapshotCreateTime}' \
  --output table
```

**Step 2: Restore to a new cluster**

```shell
aws elasticache create-replication-group \
  --replication-group-id luminary-prod-redis-restored \
  --replication-group-description "Restored from snapshot" \
  --snapshot-name "luminary-prod-redis-2026-02-20" \
  --cache-node-type cache.r6g.xlarge \
  --cache-subnet-group-name luminary-prod-cache-subnet-group \
  --security-group-ids sg-0123456789abcdef1
```

**Step 3: Update connection string**

Update the Redis endpoint in Secrets Manager and restart affected services (same procedure as Postgres step 5 above).

## Kafka (MSK)

Kafka topic data is not "restored" in the traditional sense — once events are consumed, they're processed and stored in ClickHouse. Kafka's role is as a durable transit buffer, not a system of record. The MSK cluster is configured with:

- **Retention**: 7 days per topic (sufficient to replay any event stream)
- **Replication factor**: 3 across 3 AZs
- **Minimum in-sync replicas**: 2

### MirrorMaker 2 Replication

MirrorMaker 2 replicates all production topics to a standby MSK cluster in `eu-west-1` (currently used for EU data residency compliance, not active failover). Replication lag is monitored via Datadog — alert fires if lag exceeds 60 seconds.

```shell
# Check replication lag for all topics
kafka-consumer-groups.sh \
  --bootstrap-server eu-standby-msk:9092 \
  --describe \
  --group mirror-maker-group
```

### Topic Recreation

If a topic is accidentally deleted, recreate it with the correct configuration:

```shell
kafka-topics.sh \
  --bootstrap-server prod-msk:9092 \
  --create \
  --topic analytics.events \
  --partitions 64 \
  --replication-factor 3 \
  --config retention.ms=604800000 \
  --config min.insync.replicas=2
```

After recreation, the ingestion service will resume writing from the current offset. Historical events in the retention window can be replayed from the EU standby cluster if needed.

### Offset Backup Strategy

Consumer group offsets are backed up daily using a custom script (`scripts/backup-kafka-offsets.sh`) that snapshots all consumer group offsets to S3. This allows replaying from a known point if a consumer group is accidentally reset.

## Backup Status Monitoring

Backup job completion is monitored in Datadog. The following table summarizes the monitors:

| Store | Monitor Name | Alert Condition | Channel |
| --- | --- | --- | --- |
| Postgres | RDS Backup Age | Last backup > 26 hours ago | `#alerts-platform` + PagerDuty high |
| ClickHouse | ClickHouse Backup Age | Last incremental > 2 hours ago | `#alerts-data-platform` |
| ClickHouse | ClickHouse Backup Verification | Verification job failed | `#alerts-data-platform` + PagerDuty high |
| Redis | ElastiCache Backup Age | Last snapshot > 26 hours ago | `#alerts-platform` |
| Kafka | MirrorMaker Lag | Replication lag > 60s | `#alerts-data-platform` |

## Related

- [Monitoring Stack](https://placeholder.invalid/page/infrastructure%2Fmonitoring-stack.md) — Datadog setup
- [Networking and DNS](https://placeholder.invalid/page/infrastructure%2Fnetworking-dns.md)
- [Query Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-query-service.md) — ClickHouse failure scenarios
- [Change Management](https://placeholder.invalid/page/operations%2Fchange-management.md) — pre-migration snapshot requirements
