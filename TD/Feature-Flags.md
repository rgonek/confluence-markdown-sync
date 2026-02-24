---
title: Feature Flags
id: "4948112"
space: TD
version: 2
labels:
    - developer-guide
    - feature-flags
    - launchdarkly
    - rollout
author: Robert Gonek
created_at: "2026-02-24T14:56:09Z"
last_modified_at: "2026-02-24T14:56:10Z"
last_modified_by: Robert Gonek
---
# Feature Flags

Luminary uses [LaunchDarkly](https://launchdarkly.com/) for runtime feature flag evaluation. Feature flags allow us to decouple deployment from release, roll out features progressively, and disable broken functionality without redeploying.

The Go SDK wrapper lives in `pkg/featureflags`. All application code must use this wrapper rather than calling the LaunchDarkly SDK directly — it handles local offline mode, CI mock injection, and structured logging of flag evaluations.

---

## Table of Contents

1. [Creating a Flag](#creating-a-flag)
2. [Targeting Rules](#targeting-rules)
3. [Gradual Rollout](#gradual-rollout)
4. [Kill Switch Pattern](#kill-switch-pattern)
5. [Evaluating Flags in Go](#evaluating-flags-in-go)
6. [Testing with Mock Flags in CI](#testing-with-mock-flags-in-ci)
7. [Active Flags Registry](#active-flags-registry)

---

## Creating a Flag

All flags are created in the LaunchDarkly dashboard under the **Luminary Production** project. Follow this process:

1. **Open a PR** that includes the flag key as a constant in `pkg/featureflags/keys.go` and a code path that evaluates it.
2. **Create the flag in LaunchDarkly** before merging the PR. Use the following naming conventions:

   - Key: `kebab-case`, starting with the owning team prefix: `platform-`, `query-`, `billing-`, `frontend-`.
   - Name: Human-readable title used in the dashboard.
   - Description: One sentence explaining what the flag controls and when it will be removed.
   - Tags: `team:<team>`, `type:<boolean|multivariate>`, `status:<development|rollout|permanent>`.
3. **Set the default rule** to `false` (off) for new flags.
4. **Create the flag in all environments**: Development, Staging, Production.
5. **Add the flag to the [Active Flags Registry](#active-flags-registry)** in this page.

Flag keys must be registered as constants to prevent typos and enable `grep`-based discovery:

```go
// pkg/featureflags/keys.go

const (
    FlagQueryCacheV2          = "query-cache-v2"
    FlagBillingUsageAlerts    = "billing-usage-alerts"
    FlagFrontendNewNavigation = "frontend-new-navigation"
    FlagPlatformRateLimitV3   = "platform-rate-limit-v3"
    FlagQueryExportParquet    = "query-export-parquet"
)
```

---

## Targeting Rules

LaunchDarkly evaluates flag rules in order. Each rule specifies:

- **Attribute**: A property of the evaluation context (e.g., `workspaceId`, `plan`, `userEmail`).
- **Operator**: `is one of`, `is not one of`, `contains`, `starts with`, `matches regex`, etc.
- **Variation**: The flag value returned when the rule matches.

The evaluation context for server-side requests is built from the authenticated request:

```go
// pkg/featureflags/context.go

func ContextFromRequest(r *http.Request, claims *auth.Claims) ldcontext.Context {
    return ldcontext.NewMulti(
        ldcontext.NewBuilder("user").
            Key(claims.Subject).
            SetString("email", claims.Email).
            SetString("workspaceId", claims.WorkspaceID.String()).
            Build(),
        ldcontext.NewBuilder("workspace").
            Key(claims.WorkspaceID.String()).
            SetString("plan", string(claims.Plan)).
            SetBool("isInternal", strings.HasSuffix(claims.Email, "@luminary.io")).
            Build(),
    )
}
```

Use workspace-level targeting for B2B rollouts (rolling out to specific customers). Use user-level targeting for internal dogfooding or beta groups.

---

## Gradual Rollout

For production rollouts, use LaunchDarkly's **percentage rollout** rule rather than enabling flags globally. The standard rollout cadence is:

| Phase | Rollout % | Duration | Criteria to Advance |
| --- | --- | --- | --- |
| Internal | 100% of `isInternal=true` users | 1–3 days | No errors or regressions in Datadog |
| Canary | 5% of all workspaces | 2–3 days | P95 latency within SLO, error rate < 0.5% |
| Early Adopters | 25% of all workspaces | 3–5 days | Customer support tickets below baseline |
| General Availability | 100% | — | Full rollout complete |

Monitor rollouts using the **flag evaluation** dashboard in LaunchDarkly combined with the `feature_flag_evaluation` metric in Datadog. Create a rollout monitor in Datadog that alerts if the error rate on flag-gated code paths exceeds 1%.

---

## Kill Switch Pattern

A kill switch is a feature flag that is **always on in normal operation** and disabled only in an emergency to disable a misbehaving feature path. Unlike rollout flags, kill switches are not removed after GA.

Kill switches should be used for:

- Expensive or resource-intensive features (complex queries, bulk exports)
- Features with known blast-radius risk (schema migrations mid-flight)
- Third-party integrations that may become unavailable

Implement a kill switch by checking the flag and falling back gracefully:

```go
func (s *ExportService) ExportParquet(ctx context.Context, req ExportRequest) (*ExportJob, error) {
    if !s.flags.BoolVariation(ctx, featureflags.FlagQueryExportParquet, false) {
        return nil, apierr.New(apierr.CodeFeatureNotAvailable,
            "Parquet export is temporarily unavailable. Please try CSV export instead.")
    }
    return s.runParquetExport(ctx, req)
}
```

The fallback value (`false`) ensures that if LaunchDarkly is unreachable, the kill switch activates automatically — fail safe.

---

## Evaluating Flags in Go

```go
// Inject the client via dependency injection (not as a global)
type QueryService struct {
    db    *pgxpool.Pool
    flags featureflags.Client
}

func NewQueryService(db *pgxpool.Pool, flags featureflags.Client) *QueryService {
    return &QueryService{db: db, flags: flags}
}

// Boolean flag evaluation
func (s *QueryService) RunQuery(ctx context.Context, req QueryRequest) (*QueryResult, error) {
    useNewCache := s.flags.BoolVariation(ctx, featureflags.FlagQueryCacheV2, false)

    if useNewCache {
        return s.runWithCacheV2(ctx, req)
    }
    return s.runWithCacheV1(ctx, req)
}

// Multivariate (string) flag evaluation
func (s *QueryService) GetCacheBackend(ctx context.Context) string {
    backend := s.flags.StringVariation(ctx, "query-cache-backend", "redis")
    // Returns "redis", "memcached", or "in-process" depending on flag value
    return backend
}

// JSON flag evaluation (for complex configuration)
func (s *RateLimiter) GetLimits(ctx context.Context) RateLimitConfig {
    var cfg RateLimitConfig
    s.flags.JSONVariation(ctx, featureflags.FlagPlatformRateLimitV3, &defaultRateLimitConfig, &cfg)
    return cfg
}
```

The `featureflags.Client` interface is defined in `pkg/featureflags/client.go`. It wraps the LaunchDarkly Go SDK and enriches evaluations with:

- Structured log output at `DEBUG` level for every evaluation.
- OpenTelemetry span attributes (`feature_flag.key`, `feature_flag.value`, `feature_flag.provider_name`).
- A Prometheus counter `luminary_feature_flag_evaluations_total` labelled by `flag`, `variation`, and `context_kind`.

---

## Testing with Mock Flags in CI

In CI and unit tests, inject a mock `featureflags.Client` that returns deterministic values without connecting to LaunchDarkly:

```go
// pkg/featureflags/mock/mock.go

type MockClient struct {
    bools   map[string]bool
    strings map[string]string
}

func NewMock() *MockClient {
    return &MockClient{
        bools:   make(map[string]bool),
        strings: make(map[string]string),
    }
}

func (m *MockClient) Set(key string, value interface{}) *MockClient {
    switch v := value.(type) {
    case bool:
        m.bools[key] = v
    case string:
        m.strings[key] = v
    }
    return m
}

// In a test:
flags := featureflags.NewMock().
    Set(featureflags.FlagQueryCacheV2, true).
    Set(featureflags.FlagQueryExportParquet, false)

svc := NewQueryService(db, flags)
```

When `LAUNCHDARKLY_SDK_KEY` is set to `sdk-fake-dev-key` (the default in `.env.example`), the SDK client automatically switches to offline mode and uses the values set in `config/local-flags.json`:

```json
// config/local-flags.json
{
  "flagValues": {
    "query-cache-v2":            true,
    "billing-usage-alerts":      true,
    "frontend-new-navigation":   false,
    "platform-rate-limit-v3":    false,
    "query-export-parquet":      true
  }
}
```

Update this file when adding a new flag to ensure local development has a sensible default.

---

## Active Flags Registry

This table is the source of truth for all currently active flags. Update it when creating or retiring a flag.

| Flag Key | Type | Owner | Status | Default (Prod) | Purpose | Expiry |
| --- | --- | --- | --- | --- | --- | --- |
| `query-cache-v2` | boolean | query-team | Rollout (45%) | `false` | New Redis-backed query result cache with TTL-aware invalidation | Q3 2025 |
| `billing-usage-alerts` | boolean | billing-team | GA | `true` | Email/Slack alerts when workspace approaches quota thresholds | Permanent |
| `frontend-new-navigation` | boolean | frontend-team | Internal | `false` | Redesigned left-nav sidebar with collapsible sections | Q2 2025 |
| `platform-rate-limit-v3` | JSON | platform-team | Development | `false` | New per-workspace adaptive rate limiting config | Q3 2025 |
| `query-export-parquet` | boolean | query-team | GA | `true` | Kill switch for Parquet export endpoint | Permanent |
| `query-streaming-results` | boolean | query-team | Development | `false` | Server-sent events for real-time query progress streaming | Q4 2025 |
| `billing-annual-plans` | boolean | billing-team | Rollout (10%) | `false` | Annual subscription plan option in billing UI | Q2 2025 |
| `security-mtls-internal` | boolean | platform-team | Canary (5%) | `false` | Enforce mTLS for all internal service-to-service calls | Q3 2025 |
| `data-source-bigquery` | boolean | integrations-team | Internal | `false` | BigQuery as a native data source connector | Q4 2025 |

Flags with status **GA** and no expiry are **permanent kill switches** and should never be removed. Flags with an expiry date must be cleaned up (code removed, flag deleted from LaunchDarkly) by the listed quarter. Stale flags older than their expiry create a GitHub issue automatically via the `flag-hygiene` scheduled workflow.
