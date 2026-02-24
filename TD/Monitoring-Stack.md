---
title: Monitoring Stack
id: "4882852"
space: TD
version: 2
labels:
    - datadog
    - observability
    - infrastructure
    - monitoring
author: Robert Gonek
created_at: "2026-02-24T14:56:46Z"
last_modified_at: "2026-02-24T14:56:47Z"
last_modified_by: Robert Gonek
---
# Monitoring Stack

Luminary's monitoring infrastructure is built on Datadog. We migrated from a self-hosted Prometheus + Grafana stack in January 2026. This document covers the Datadog agent deployment, log pipeline, APM configuration, synthetic monitors, SLO monitors, and PagerDuty alerting integration.

For the alert-response side (what to do when pages fire), see the relevant runbooks in [operations/](https://placeholder.invalid/page/operations).

## Architecture Overview

```
Kubernetes Pods
    │
    ├── stdout/stderr logs
    │       └──→ Fluent Bit DaemonSet ──→ Datadog Logs API
    │
    ├── APM traces (via DD_TRACE_* env vars)
    │       └──→ Datadog Agent (DaemonSet) ──→ Datadog APM
    │
    ├── StatsD/DogStatsD custom metrics
    │       └──→ Datadog Agent ──→ Datadog Metrics
    │
    └── Host metrics (CPU, mem, disk, network)
            └──→ Datadog Agent ──→ Datadog Infra

External synthetic checks ──→ Datadog Synthetics ──→ Datadog Monitors
                                                           │
                                                     PagerDuty (via Datadog webhook)
```

## Datadog Agent Deployment

The Datadog agent runs as a DaemonSet in every cluster namespace. Configuration is managed via the Datadog Operator.

```yaml
# infrastructure/k8s/datadog/datadog-agent.yaml
apiVersion: datadoghq.com/v2alpha1
kind: DatadogAgent
metadata:
  name: datadog
  namespace: datadog
spec:
  global:
    clusterName: luminary-prod-us-east-1
    credentials:
      apiSecret:
        secretName: datadog-secrets
        keyName: api-key
      appSecret:
        secretName: datadog-secrets
        keyName: app-key
    tags:
      - env:production
      - region:us-east-1
      - team:platform
  features:
    apm:
      enabled: true
      hostPortConfig:
        enabled: true
        hostPort: 8126
    dogstatsd:
      enabled: true
      hostPortConfig:
        enabled: true
        hostPort: 8125
    logCollection:
      enabled: true
      containerCollectAll: true
    liveProcesses:
      enabled: true
    orchestratorExplorer:
      enabled: true
    npm:
      enabled: true
```

### Custom Metrics Forwarding

Custom business metrics are emitted via DogStatsD. Each service uses a thin wrapper:

```go
// Go services
import "github.com/DataDog/datadog-go/v5/statsd"

var metrics *statsd.Client

func init() {
    metrics, _ = statsd.New("127.0.0.1:8125",
        statsd.WithTags([]string{"service:query-service", "env:production"}),
    )
}

// Usage
metrics.Incr("api.requests", []string{"endpoint:/v1/query", "status:200"}, 1)
metrics.Timing("query.duration_ms", duration, []string{"workspace_id:w_123"}, 1)
metrics.Gauge("queue.depth", float64(queueDepth), []string{"queue:exports"}, 1)
```

## Log Pipeline (Fluent Bit → Datadog)

Fluent Bit runs as a DaemonSet and tails container logs from `/var/log/containers/`. It parses JSON-structured logs and forwards to the Datadog Logs API.

```ini
# infrastructure/k8s/fluent-bit/fluent-bit.conf
[SERVICE]
    Flush         5
    Daemon        off
    Log_Level     info
    Parsers_File  parsers.conf

[INPUT]
    Name              tail
    Path              /var/log/containers/*.log
    multiline.parser  docker, cri
    Tag               kube.*
    Refresh_Interval  10
    Mem_Buf_Limit     50MB
    Skip_Long_Lines   on

[FILTER]
    Name                kubernetes
    Match               kube.*
    Kube_URL            https://kubernetes.default.svc:443
    Kube_CA_File        /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    Kube_Token_File     /var/run/secrets/kubernetes.io/serviceaccount/token
    Merge_Log           on
    Merge_Log_Key       log_processed
    K8S-Logging.Parser  on

[OUTPUT]
    Name        datadog
    Match       kube.*
    Host        http-intake.logs.datadoghq.com
    TLS         on
    compress    gzip
    apikey      ${DD_API_KEY}
    dd_service  ${FLUENT_BIT_SERVICE_NAME}
    dd_source   ${FLUENT_BIT_SOURCE}
    dd_tags     env:production,cluster:luminary-prod-us-east-1
```

All services are required to emit structured JSON logs. The log schema is documented in the developer guide. Key mandatory fields: `timestamp` (RFC3339), `level`, `service`, `trace_id`, `span_id`.

## APM Configuration

### Go Services

```go
// main.go — before any HTTP handlers
import (
    "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
    httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

func main() {
    tracer.Start(
        tracer.WithEnv(os.Getenv("DD_ENV")),
        tracer.WithService(os.Getenv("DD_SERVICE")),
        tracer.WithServiceVersion(os.Getenv("DD_VERSION")),
        tracer.WithAgentAddr("localhost:8126"),
        tracer.WithRuntimeMetrics(),
    )
    defer tracer.Stop()

    // Wrap router
    mux := http.NewServeMux()
    http.ListenAndServe(":8080", httptrace.WrapHandler(mux, "query-service", "/"))
}
```

Each service pod has the following environment variables set via Helm values:

```yaml
env:
  - name: DD_ENV
    value: production
  - name: DD_SERVICE
    valueFrom:
      fieldRef:
        fieldPath: metadata.labels['app']
  - name: DD_VERSION
    value: "{{ .Values.image.tag }}"
  - name: DD_AGENT_HOST
    valueFrom:
      fieldRef:
        fieldPath: status.hostIP
```

### Node.js Services

```javascript
// Must be the very first line of the entrypoint
const tracer = require('dd-trace').init({
  env: process.env.DD_ENV,
  service: process.env.DD_SERVICE,
  version: process.env.DD_VERSION,
  runtimeMetrics: true,
  logInjection: true,   // injects trace_id and span_id into pino/winston logs
});
```

## Synthetic Monitors

Synthetic monitors validate that customer-facing endpoints are available and responding correctly from external vantage points. Monitors run every minute from three Datadog-managed locations (US East, EU West, AP Southeast).

| Monitor Name | URL | Assertion | Alert Threshold |
| --- | --- | --- | --- |
| Public API health | `GET api.luminary.io/health` | Status 200, body contains `"status":"ok"` | 2 failures in 3 checks |
| Dashboard app loads | `GET app.luminary.io` | Status 200, HTML contains `<div id="app">` | 2 failures in 3 checks |
| Ingest API accepts events | `POST ingest.luminary.io/v1/track` | Status 200, latency < 500ms | 2 failures in 3 checks |
| Auth login flow | Multi-step: GET login, POST credentials | Final redirect to dashboard | 1 failure |

Synthetic monitor Terraform config:

```hcl
# infrastructure/terraform/datadog/synthetics.tf
resource "datadog_synthetics_test" "api_health" {
  type    = "api"
  subtype = "http"
  name    = "Public API Health Check"
  status  = "live"
  tags    = ["env:production", "team:platform"]

  request_definition {
    method = "GET"
    url    = "https://api.luminary.io/health"
  }

  assertion {
    type     = "statusCode"
    operator = "is"
    target   = "200"
  }

  assertion {
    type     = "responseTime"
    operator = "lessThan"
    target   = "2000"
  }

  locations = [
    "aws:us-east-1",
    "aws:eu-west-1",
    "aws:ap-southeast-1",
  ]

  options_list {
    tick_every = 60
    retry {
      count    = 2
      interval = 300
    }
  }
}
```

## SLO Monitors

We define SLOs in Datadog backed by monitors. Current SLOs:

| SLO | Target | Window | Monitor |
| --- | --- | --- | --- |
| API availability | 99.9% | 30 days | Synthetic: API health check |
| Query API p99 latency < 2s | 99.5% | 7 days | Metric: `trace.http.request.duration` |
| Ingest pipeline lag < 60s | 99.5% | 7 days | Metric: `kafka.consumer.lag` |
| Auth service availability | 99.95% | 30 days | Synthetic: Auth login flow |

SLO Terraform:

```hcl
resource "datadog_service_level_objective" "api_availability" {
  name        = "API Availability"
  type        = "monitor"
  description = "Percentage of time the public API health check is passing"
  tags        = ["env:production", "team:platform"]

  monitor_ids = [datadog_monitor.api_synthetic.id]

  thresholds {
    timeframe       = "30d"
    target          = 99.9
    warning         = 99.95
  }
}
```

## PagerDuty Routing and Escalation

Datadog monitors alert to PagerDuty via a webhook integration. The `@pagerduty-*` handle in a monitor message routes to the corresponding PagerDuty service.

### Routing Rules

| Severity | Monitor Tag | PagerDuty Service | Escalation Policy |
| --- | --- | --- | --- |
| Critical | `severity:critical` | Luminary-Production | Production Escalation |
| High | `severity:high` | Luminary-Production | Production Escalation |
| Medium | `severity:medium` | Luminary-Non-Urgent | Business Hours Only |
| Low | `severity:low` | Luminary-Backlog | No page — creates ticket |

### Escalation Policies

**Production Escalation**:

1. Immediately notify the on-call engineer (PagerDuty mobile + SMS)
2. If not acknowledged within 5 minutes: notify secondary on-call
3. If not acknowledged within 15 minutes: notify on-call engineering manager

**Business Hours Only**:

1. Notify on-call during business hours only (Mon–Fri 09:00–18:00 UTC)
2. If not acknowledged in 30 minutes: auto-resolve and create Jira ticket

### Monitor Message Template

All monitors use this standard message format:

```
{{#is_alert}}
**[ALERT] {{ monitor.name }}**
Environment: {{ env }}
Service: {{ service }}

**Details**: {{ log.message }}
**Dashboard**: https://app.datadoghq.com/...

@pagerduty-luminary-production
{{/is_alert}}
{{#is_recovery}}
**[RECOVERY] {{ monitor.name }}** resolved.
{{/is_recovery}}
```

## Adding a New Monitor

1. Add a Terraform resource in `infrastructure/terraform/datadog/monitors/`
2. Follow the naming convention: `[Service] - [Condition]` (e.g., `Query Service - High Error Rate`)
3. Always include tags: `env:production`, `service:<name>`, `severity:<level>`, `team:<team>`
4. Open a PR, get review from the Platform team, and apply via `terraform apply` in the `datadog` workspace

## Related

- [Networking and DNS](https://placeholder.invalid/page/infrastructure%2Fnetworking-dns.md)
- [Auth Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-auth-service.md)
- [Query Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-query-service.md)
- [On-Call Onboarding](Onboarding-to-On-Call.md)
