---
title: Performance Profiling Guide
id: "6127755"
space: TD
version: 2
labels:
    - performance
    - profiling
    - go
    - clickhouse
    - developer-guide
author: Robert Gonek
created_at: "2026-02-24T14:56:22Z"
last_modified_at: "2026-02-24T14:56:23Z"
last_modified_by: Robert Gonek
---
# Performance Profiling Guide

Performance problems at Luminary tend to cluster in a few places: the ClickHouse query execution path, the Kafka Streams session stitcher under burst load, and occasionally Postgres query planning regressions after schema changes. This guide covers the tools and techniques the engineering team uses to diagnose and fix performance problems.

---

## Go CPU and Memory Profiling (pprof)

All Luminary Go services expose the standard `net/http/pprof` endpoints on port `6060` (not the same port as the HTTP API). These endpoints are not exposed through the ALB or CloudFront — they are only accessible via `kubectl port-forward`.

### Capturing a CPU Profile

CPU profiles sample the goroutine stack every 10ms. A 30-second CPU profile under production load is usually enough to identify hot paths.

```shell
# Port-forward to a specific pod
kubectl port-forward -n production pod/query-service-7d9f8c-xk2pq 6060:6060

# In another terminal: capture 30-second CPU profile
go tool pprof -seconds 30 http://localhost:6060/debug/pprof/profile

# Or download and analyze offline
curl -s "http://localhost:6060/debug/pprof/profile?seconds=30" -o cpu.pprof
go tool pprof cpu.pprof
```

**Interpreting CPU profile output:**

```
(pprof) top15
Showing nodes accounting for 8.23s, 91.08% of 9.04s total
Dropped 42 nodes (cum <= 0.05s)
Showing top 15 nodes out of 87
      flat  flat%   sum%        cum   cum%
     2.14s 23.67% 23.67%      2.14s 23.67%  runtime.futex
     1.83s 20.24% 43.91%      3.22s 35.62%  encoding/json.Marshal
     0.97s 10.73% 54.64%      1.21s 13.39%  compress/flate.(*compressor).deflate
     0.68s 7.52%  62.16%      0.68s  7.52%  runtime.memmove
```

`flat` = time spent in the function itself. `cum` = time including callees. In this example, `encoding/json.Marshal` consuming 20% of CPU is suspicious — investigate where JSON marshaling is happening in the hot path.

```shell
# Visualize as a flame graph (requires graphviz)
go tool pprof -http=:8080 cpu.pprof
# Opens browser at localhost:8080 with interactive flame graph
```

### Capturing a Memory Heap Profile

```shell
# Current heap allocation
curl -s "http://localhost:6060/debug/pprof/heap" -o heap.pprof
go tool pprof heap.pprof
(pprof) top10 -cum

# Allocation profile (shows where objects were allocated, not current live objects)
curl -s "http://localhost:6060/debug/pprof/allocs" -o allocs.pprof
go tool pprof allocs.pprof
(pprof) top10
```

**What to look for**: Large `cum` allocations in unexpected places (e.g., `bytes.Buffer` allocations inside a tight loop that should be using a sync.Pool, or large `[]byte` allocations in the gRPC serialization path).

### Goroutine Profile

Goroutine profiles are invaluable for diagnosing goroutine leaks and lock contention:

```shell
curl -s "http://localhost:6060/debug/pprof/goroutine?debug=2" | head -100
```

Healthy services have a stable goroutine count. If `kubectl top pod` shows memory growing slowly without increased load, check for goroutine leaks: a goroutine count growing over hours (visible in `debug/pprof/goroutine` count) indicates a leak.

---

## ClickHouse EXPLAIN

ClickHouse's `EXPLAIN` family of statements shows the query execution plan and can reveal why a query is slow.

### EXPLAIN PLAN

```sql
EXPLAIN PLAN
SELECT
    event_name,
    count() AS cnt
FROM luminary.events
WHERE workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND ingested_date BETWEEN '2025-09-01' AND '2025-09-30'
GROUP BY event_name
ORDER BY cnt DESC
LIMIT 20;
```

Key things to look for in `EXPLAIN PLAN` output:

- `ReadFromMergeTree` — the table scan. Check the `conditions` to confirm partition pruning is applied
- `Selected N/M parts` — how many parts are read vs total. N should be << M for a date-ranged query
- `Aggregating` — aggregation nodes; check if they're being pushed down to the scan level

### EXPLAIN PIPELINE

```sql
EXPLAIN PIPELINE
SELECT ...;
```

Shows the full execution pipeline including parallel threads. Useful to confirm a query is actually parallelized. If the pipeline shows a single-threaded execution (no `Resize` nodes), it may indicate a configuration issue or a query structure that prevents parallelism.

### EXPLAIN indexes = 1

```sql
EXPLAIN indexes = 1
SELECT ...;
```

Shows which skipping indexes are being evaluated and whether they're used. Look for `Skip: idx_user_id (used)` to confirm a bloom filter index is active.

### system.query_log

For queries that already ran (e.g., after a customer complaint about a slow chart):

```sql
SELECT
    query_id,
    query,
    read_rows,
    read_bytes,
    memory_usage,
    event_time,
    query_duration_ms,
    ProfileEvents['RealTimeMicroseconds'] / 1e6 AS wall_seconds
FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= now() - INTERVAL 1 HOUR
  AND query LIKE '%018e2f1a%'   -- filter by workspace_id
ORDER BY query_duration_ms DESC
LIMIT 10;
```

---

## Postgres EXPLAIN ANALYZE

For Postgres query performance issues, always run `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)`:

```sql
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT d.id, d.title, d.updated_at
FROM dashboards d
WHERE d.workspace_id = '018e2f1a-b3c4-7d8e-9f0a-1b2c3d4e5f6a'
  AND d.is_deleted = false
ORDER BY d.updated_at DESC
LIMIT 20;
```

**What to look for:**

```
Limit  (cost=0.43..8.73 rows=20 width=48) (actual time=0.082..0.134 rows=20 loops=1)
  ->  Index Scan Backward using dashboards_workspace_updated_idx on dashboards d
        (cost=0.43..217.5 rows=524 width=48) (actual time=0.080..0.128 rows=20 loops=1)
      Index Cond: (workspace_id = '018e2f1a-...' AND is_deleted = false)
Buffers: shared hit=25
Planning Time: 0.342 ms
Execution Time: 0.162 ms
```

Good signs: `Index Scan` (not `Seq Scan`), low `Execution Time`, `Buffers: shared hit` (served from buffer cache). Bad signs: `Seq Scan` on a large table, `Buffers: shared read` (disk I/O), `Sort Method: external merge Disk`.

**Common Postgres performance fixes:**

- Missing index on `(workspace_id, is_deleted, updated_at)` for workspace-scoped queries
- Statistics out of date: `ANALYZE table_name;` or wait for autovacuum
- Planner choosing wrong index: add explicit `WHERE workspace_id = $1` before other conditions to help the planner

---

## Kafka Consumer Group Lag Analysis

Consumer lag is the primary health signal for the streaming pipeline. High lag means events are not being processed fast enough.

```shell
# Check current lag for all groups on the events pipeline
kafka-consumer-groups.sh \
  --bootstrap-server kafka.luminary.internal:9092 \
  --command-config /etc/kafka/client.properties \
  --describe \
  --group enrichment-service-prod

# Output columns: TOPIC | PARTITION | CURRENT-OFFSET | LOG-END-OFFSET | LAG | CONSUMER-ID | HOST
```

**Diagnosing sustained lag growth:**

1. **Compare lag growth rate vs throughput**: If lag grows at exactly the same rate as event volume is increasing, the processor is maxed out — scale up.
2. **Check for partition skew**: If lag is high on specific partitions but not others, a hot workspace (one workspace sending a huge volume) may be overloading those partitions. Check which `workspace_id` is most active: `SELECT workspace_id, count() FROM luminary.events WHERE ingested_date = today() GROUP BY workspace_id ORDER BY count() DESC LIMIT 5`.
3. **Check for slow enrichment**: If the Geo-IP lookup or bot detection model is taking too long per event, overall throughput drops. Check the `enrichment_ms` histogram metric in Datadog.

---

## Datadog APM Trace Analysis

Luminary uses Datadog APM with the Go `dd-trace` library. All inbound HTTP requests and outbound database queries are instrumented automatically via the middleware stack.

### Identifying Bottlenecks in a Trace

In Datadog APM, navigate to a slow trace for the endpoint in question (e.g., `GET /v1/api/dashboards/{id}/funnel-results`). The flame chart shows the relative time spent in each operation:

```
GET /v1/api/dashboards/.../funnel-results          1,240ms
├── auth.ValidateJWT                                   8ms
├── ratelimit.Check                                    3ms
├── querybuilder.Build                                 2ms
├── clickhouse.Query (SELECT funnel_agg...)           980ms  ← HOT
│   └── [ClickHouse server processing]
├── cache.Set                                         12ms
└── json.Marshal (response serialization)             235ms  ← SUSPICIOUS
```

In this example, the ClickHouse query is expected to be slow (complex funnel query). But 235ms on JSON serialization for the response is suspicious — the response may be very large. Add a span tag for response size to confirm, or check the `response_bytes` Datadog metric for that endpoint.

### Using Service Map for Dependency Analysis

The Datadog Service Map (`APM → Service Map`) shows which services are calling each other and their error rates. If the API Service's error rate spikes, check the Service Map to see which downstream dependency (ClickHouse, Postgres, Redis) is returning errors.

---

## Case Study: Ingestion Team Performance Incident (August 2025)

**Symptom**: Kafka consumer lag on `luminary.events.validated` → `luminary.events.enriched` grew from the normal 1,200 message baseline to 800,000 messages over 45 minutes on 2024-10-04. End-to-end pipeline latency reached 12 minutes.

**Initial investigation**:

1. Stream processor pods appeared healthy (no crash loops, memory/CPU below limits).
2. Lag was growing uniformly across all 64 partitions — not a hot-partition issue.
3. Pod CPU usage was at 95% limit — clearly CPU-saturated.

**Profiling**:

```shell
# Captured 60-second CPU profile from a stream-processor pod under load
kubectl port-forward -n data-pipeline pod/stream-processor-6d8f7c-mp9kq 6060:6060
go tool pprof -seconds 60 http://localhost:6060/debug/pprof/profile
```

Profile output (top 5 by flat time):

```
      flat  flat%   sum%        cum   cum%
     8.12s 48.40% 48.40%      8.12s 48.40%  github.com/oschwald/maxminddb-golang.(*Reader).lookupPointer
     3.44s 20.50% 68.90%      3.44s 20.50%  net.ParseIP
     1.21s  7.21% 76.11%      9.61s 57.30%  com/luminary/stream-processor/enrichment.GeoEnrich
     0.88s  5.25% 81.36%      0.88s  5.25%  runtime.mallocgc
```

**Root cause**: The MaxMind GeoLite2 database lookup (`lookupPointer`) was consuming 48% of CPU. Investigation revealed that a dependency update 3 days earlier had bumped `maxminddb-golang` from v1.11.0 to v1.12.0, which changed the internal B-tree lookup to a sequential scan for a specific database format version. The new MaxMind database file (updated weekly) had a format that triggered the regression.

**Fix**: Pinned `maxminddb-golang` to v1.11.0 and opened an issue upstream. Deployed in 18 minutes. Consumer lag returned to baseline within 6 minutes of the fix deploy.

**Follow-up actions**:

- Added a CPU throughput benchmark for the Geo-IP enrichment processor to the CI suite — now caught by `make benchmark` on every PR that touches the enrichment code.
- Added a Datadog alert for stream processor CPU utilization > 80% sustained for 5 minutes.
- Pinned all critical data-path dependency versions in `go.mod` with a `# pinned: reason` comment.
