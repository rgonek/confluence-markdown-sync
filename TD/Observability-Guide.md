---
title: Observability Guide
id: "5865704"
space: TD
version: 2
labels:
    - metrics
    - tracing
    - opentelemetry
    - developer-guide
    - observability
    - logging
author: Robert Gonek
created_at: "2026-02-24T14:56:17Z"
last_modified_at: "2026-02-24T14:56:19Z"
last_modified_by: Robert Gonek
---
# Observability Guide

This guide covers how Luminary engineers instrument their services. Following these conventions ensures that logs, metrics, and traces are consistent across services and can be correlated in Datadog.

All Luminary backend services are written in Go unless otherwise noted. The examples below are Go-first.

## Table of Contents

- [Structured Logging](#structured-logging)
- [Metrics](#metrics)
- [Distributed Tracing](#distributed-tracing)
- [Datadog Dashboards and Monitors](#datadog-dashboards-and-monitors)

---

## Structured Logging

### Logger Setup

All services use [go.uber.org/zap](https://pkg.go.dev/go.uber.org/zap) in production mode (JSON output). The development mode (human-readable) is enabled when `LOG_FORMAT=text` or `APP_ENV=development`.

```go
package logger

import (
    "go.uber.org/zap"
    "go.uber.org/zap/zapcore"
)

func New(env, level string) (*zap.Logger, error) {
    var cfg zap.Config
    if env == "development" {
        cfg = zap.NewDevelopmentConfig()
        cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
    } else {
        cfg = zap.NewProductionConfig()
        // Always use UTC timestamps in production
        cfg.EncoderConfig.TimeKey = "ts"
        cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
    }

    lvl, err := zap.ParseAtomicLevel(level)
    if err != nil {
        return nil, fmt.Errorf("invalid log level %q: %w", level, err)
    }
    cfg.Level = lvl

    return cfg.Build(
        zap.AddCallerSkip(0),
        zap.Fields(
            zap.String("service", ServiceName),
            zap.String("env", env),
            zap.String("version", BuildVersion),
        ),
    )
}
```

Initialize once in `main.go` and propagate via context or dependency injection. Do not use a package-level logger variable.

### Required Fields

Every log entry emitted in production must include these fields. The fields in the base logger cover `service`, `env`, and `version`. The remaining fields are context-dependent and must be added where applicable:

| Field | Type | Source | Description |
| --- | --- | --- | --- |
| `service` | string | Base logger | Service name, e.g. `query-service`. Set at logger construction. |
| `env` | string | Base logger | `production`, `staging`, or `development`. |
| `version` | string | Base logger | Service binary version (set via `ldflags` in the build). |
| `trace_id` | string | OTel context | W3C trace ID from the active span. |
| `span_id` | string | OTel context | Current span ID. |
| `request_id` | string | HTTP middleware | Per-request ID from `X-Request-ID` header or generated. |
| `workspace_id` | string | Request context | Active workspace ID, when processing a workspace-scoped request. |
| `user_id` | string | Request context | Authenticated user ID, when available. |

### Injecting Trace Context into Logs

Use the `zapotl` helper from our internal `pkg/zapotl` package to extract trace and span IDs from the OpenTelemetry context and append them to the logger:

```go
import (
    "go.opentelemetry.io/otel/trace"
    "go.uber.org/zap"
    "github.com/luminary-io/platform/pkg/zapotl"
)

func (h *QueryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Derive a request-scoped logger with trace context
    log := zapotl.WithContext(h.log, r.Context())

    log.Info("query request received",
        zap.String("workspace_id", workspaceID),
        zap.String("request_id", requestID),
    )
}
```

`zapotl.WithContext` reads the current span from the context (set by the OTel HTTP middleware) and appends `trace_id` and `span_id` fields.

### Log Levels

| Level | When to use |
| --- | --- |
| `DEBUG` | Detailed diagnostic info. Only emitted when `LOG_LEVEL=debug`. Never log user data at debug level in production. |
| `INFO` | Normal operational events. Request received, dependency health check passed, configuration loaded. |
| `WARN` | Unexpected but recoverable situations. Cache miss when cache should be warm, retrying after transient failure, slow query detected. |
| `ERROR` | Failures that require attention. Request failed after all retries, dependency unreachable, data integrity violation. |
| `FATAL` | Unrecoverable startup failure. After logging at Fatal, zap calls `os.Exit(1)`. Do not use Fatal after startup. |

### What Not to Log

- **PII**: email addresses, names, phone numbers. Log user IDs only.
- **Credentials**: write keys, tokens, passwords. If you must log that an auth failure occurred, log the key prefix only (first 6 characters).
- **Large payloads**: do not log full request/response bodies unless actively debugging a specific incident. Log metadata (size, event count) instead.

---

## Metrics

### Prometheus Client Setup

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Namespace: "luminary",
        Subsystem: "query_service",
        Name:      "request_duration_seconds",
        Help:      "HTTP request latency in seconds.",
        Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
    }, []string{"method", "path", "status_code"})

    QueryCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
        Namespace: "luminary",
        Subsystem: "query_service",
        Name:      "cache_hits_total",
        Help:      "Number of queries served from cache.",
    }, []string{"workspace_id"})
)
```

Expose the default registry at `/metrics`:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

mux.Handle("/metrics", promhttp.Handler())
```

### Naming Conventions

All metrics follow the pattern: `luminary_<service_name>_<metric_name>_<unit>`.

- Use snake_case.
- Include the unit in the name for clarity: `_seconds`, `_bytes`, `_total` (counters), `_ratio`.
- Counters always end with `_total`.
- Histograms and summaries include the unit but not `_total`.

**Good examples:**

```
luminary_ingestion_service_events_received_total
luminary_ingestion_service_batch_size_bytes
luminary_query_service_clickhouse_query_duration_seconds
luminary_auth_service_token_issuance_duration_seconds
```

**Bad examples:**

```
luminary_events        (too vague, no service, no unit)
query_latency          (no namespace, no unit)
luminary_qs_reqs       (abbreviations — spell it out)
```

### Label Cardinality Rules

High-cardinality labels cause Prometheus performance degradation and Datadog cost spikes. Follow these rules:

1. **Never use unbounded values as labels**: user IDs, workspace IDs on high-volume metrics (>1,000 workspaces), request IDs, email addresses, IP addresses.
2. **Workspace ID exception**: `workspace_id` is acceptable on metrics where per-workspace granularity is essential (e.g., query cache hit rate). Before adding `workspace_id` as a label, confirm the expected cardinality with the Data Platform team.
3. **Status codes**: use the numeric code (`200`, `404`, `500`), not descriptive strings.
4. **HTTP paths**: normalize paths with dynamic segments before using as labels. Replace `/users/7f3a2c/profile` with `/users/:id/profile`. Use a path normalization middleware.

### Histogram Bucket Guidelines

The default Prometheus histogram buckets (`0.005, 0.01, 0.025, ...`) are not well-suited for most Luminary workloads. Use custom buckets that match expected latency ranges:

| Latency Profile | Suggested Buckets |
| --- | --- |
| In-process (cache lookups, serialization) | `1ms, 5ms, 10ms, 25ms, 50ms, 100ms` |
| Fast external calls (Redis, GeoIP) | `5ms, 10ms, 25ms, 50ms, 100ms, 250ms, 500ms` |
| Database queries (ClickHouse, Postgres) | `10ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s, 10s` |
| End-to-end HTTP | `10ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s` |

---

## Distributed Tracing

### OpenTelemetry Go SDK Setup

All services initialize the OTel SDK in `main.go`. The canonical setup:

```go
package main

import (
    "context"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
    "google.golang.org/grpc"
)

func initTracer(ctx context.Context, cfg Config) (func(), error) {
    exporter, err := otlptracegrpc.New(ctx,
        otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
        otlptracegrpc.WithDialOption(grpc.WithBlock()),
    )
    if err != nil {
        return nil, fmt.Errorf("creating OTLP exporter: %w", err)
    }

    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceNameKey.String(ServiceName),
            semconv.ServiceVersionKey.String(BuildVersion),
            semconv.DeploymentEnvironmentKey.String(cfg.Env),
        ),
    )
    if err != nil {
        return nil, fmt.Errorf("creating OTel resource: %w", err)
    }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter,
            sdktrace.WithBatchTimeout(5*time.Second),
        ),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(sdktrace.ParentBased(
            sdktrace.TraceIDRatioBased(cfg.TracingSampleRate),
        )),
    )

    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))

    return func() {
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        tp.Shutdown(shutdownCtx)
    }, nil
}
```

Call the returned shutdown function in your graceful shutdown sequence before the main context is cancelled.

### Creating Spans

Use the tracer from the global provider:

```go
import "go.opentelemetry.io/otel"

var tracer = otel.Tracer("github.com/luminary-io/query-service")

func (s *QueryService) Execute(ctx context.Context, q *Query) (*Result, error) {
    ctx, span := tracer.Start(ctx, "QueryService.Execute",
        trace.WithAttributes(
            attribute.String("workspace_id", q.WorkspaceID),
            attribute.Int("complexity_score", q.ComplexityScore),
        ),
    )
    defer span.End()

    result, err := s.clickhouse.RunQuery(ctx, q)
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
        return nil, fmt.Errorf("running query: %w", err)
    }

    span.SetAttributes(attribute.Int("rows_returned", len(result.Rows)))
    return result, nil
}
```

### Propagation Headers

Luminary uses the [W3C Trace Context](https://www.w3.org/TR/trace-context/) standard (`traceparent` / `tracestate` headers). Do not use the `X-B3-*` Zipkin headers — they are not configured on our propagator.

Inbound HTTP requests from external clients or the API Gateway always include a `traceparent` header. The OTel HTTP server middleware (`otelhttp.NewHandler`) extracts this automatically.

For outbound calls from services to other internal services, use `otelhttp.NewTransport` or `otelgrpc` interceptors to inject the header:

```go
// Outbound HTTP client with trace propagation
client := &http.Client{
    Transport: otelhttp.NewTransport(http.DefaultTransport),
}

// gRPC client with trace propagation
conn, err := grpc.Dial(addr,
    grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
    grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
)
```

### Sampling Configuration

The default sampling rate is controlled by the `TRACING_SAMPLE_RATE` environment variable (a float between 0 and 1, default `0.1` in production). The `ParentBased` sampler means:

- If the incoming request has a sampled trace context, the service continues that trace.
- If there is no incoming trace context, the service samples at `TRACING_SAMPLE_RATE`.

For the ingestion service, the default rate is set to `0.01` (1%) due to high volume. Adjust on a per-service basis.

---

## Datadog Dashboards and Monitors

### Naming Conventions

Consistent naming makes dashboards discoverable and ownership clear.

**Dashboards:**

```
<Team> - <Service or Area> - <Purpose>
```

Examples:

- `SDK Platform - JS SDK - Browser Error Rates`
- `Data Platform - ClickHouse - Query Performance`
- `Infra - Kafka - Consumer Lag`

**Monitors:**

```
[<Team>] <Service>: <What is wrong>
```

Examples:

- `[SDK Platform] Ingestion Service: High batch rejection rate`
- `[Data Platform] Query Service: p99 latency > 2s`
- `[Security] Auth Service: Elevated failed login attempts`

### Dashboard Structure

Each service team owns a primary service dashboard with the following sections:

1. **Overview** — golden signals: error rate, request rate, latency (p50/p95/p99), saturation.
2. **Dependencies** — key metrics from downstream dependencies (ClickHouse pool, Redis hit/miss rate, Kafka lag).
3. **Business Metrics** — service-specific operational metrics (e.g., for the query service: cache hit rate, complexity rejection rate).
4. **Infrastructure** — pod CPU/memory, GC stats, goroutine count.

Use Datadog template variables for `env` and `service` to make dashboards reusable across environments.

### Creating a Monitor

Monitors must have:

- **Tags**: `team:<team-name>`, `service:<service-name>`, `env:production`.
- **Notify**: `@pagerduty-<team>-critical` for P1 monitors, `@slack-<team>-alerts` for P2/P3.
- **Recovery message**: brief guidance on what to check first.
- **Runbook link**: every P1 monitor must link to a runbook in `docs/runbooks/`.

Example monitor message body:

```
{{#is_alert}}
The p99 query latency has exceeded 2s for the past 5 minutes.

Current value: {{value}}s

**Initial steps:**
1. Check ClickHouse query queue depth: https://internal.luminaryapp.io/datadog/clickhouse-queue
2. Check for workspace-level query complexity spikes in the Query Service dashboard.
3. If ClickHouse is healthy, check for Redis eviction alerts.

**Runbook:** https://docs.internal.luminaryapp.io/runbooks/query-service-high-latency
{{/is_alert}}

{{#is_recovery}}
p99 query latency has returned to normal ({{value}}s).
{{/is_recovery}}
```
