---
title: Deployment Guide
id: "7176313"
space: TD
version: 2
labels:
    - developer-guide
    - deployment
    - argocd
    - operations
author: Robert Gonek
created_at: "2026-02-24T14:56:04Z"
last_modified_at: "2026-02-24T14:56:05Z"
last_modified_by: Robert Gonek
---
# Deployment Guide

This guide covers the mechanics of deploying changes to production at Luminary: how builds are promoted through environments, how to scope a rollout, what to monitor during a deploy, and how to handle database migrations. For the complete SRE procedure with rollback steps, see the [Operations Deployment Runbook](https://placeholder.invalid/page/operations%2Fdeployment-runbook.md).

---

## Deployment Flow Overview

Luminary uses ArgoCD for GitOps-based deployments. The source of truth for what runs in each environment is the `luminary/k8s-config` repository. CI builds Docker images, updates the image tag in `k8s-config`, and ArgoCD detects the change and syncs.

```
Code PR merged to main
    → CI builds & tests
    → CI pushes image to ECR with tag {git-sha}
    → CI updates image tag in k8s-config/staging/
    → ArgoCD auto-syncs staging within 3 minutes
    → Staging smoke tests run
    → Engineer opens promotion PR: staging → production
    → PR review + approval
    → CI updates image tag in k8s-config/production/
    → ArgoCD requires manual sync for production (auto-sync disabled)
    → Engineer triggers sync via ArgoCD UI or CLI
```

---

## Promoting a Build from Staging to Production

### Step 1: Verify Staging

Before promoting, confirm the build is healthy in staging:

```shell
# Check pod status in staging
kubectl get pods -n staging -l app.kubernetes.io/name=query-service

# Check the image tag running in staging
kubectl get deployment query-service -n staging -o jsonpath='{.spec.template.spec.containers[0].image}'
# Output: 123456789.dkr.ecr.us-east-1.amazonaws.com/query-service:a3f8c2d1

# Check error rate in staging (last 30 min)
# Use Datadog or check pod logs
kubectl logs -n staging -l app.kubernetes.io/name=query-service --since=30m | grep -c ERROR
```

### Step 2: Open Promotion PR

```shell
# The promotion script updates the production image tag to match staging
cd k8s-config
./scripts/promote.sh query-service staging production
# Creates a branch and opens a PR automatically
```

The promotion PR shows exactly one change: the image tag in `production/apps/query-service/values.yaml`. Review the diff:

```yaml
# Before
image:
  tag: "b9e7a1c0"

# After
image:
  tag: "a3f8c2d1"
```

### Step 3: ArgoCD App Diff

Before syncing, review what ArgoCD will change:

```shell
argocd app diff query-service-prod --server argocd.luminary.internal
```

Confirm the diff matches only what you expect (the image tag). If you see unexpected changes (e.g., Helm value drift), investigate before proceeding.

### Step 4: Sync

```shell
# Sync via CLI
argocd app sync query-service-prod \
  --server argocd.luminary.internal \
  --strategy hook

# Or via ArgoCD UI: navigate to the app → click Sync → confirm
```

ArgoCD applies the rollout strategy defined in the Argo Rollout resource (Kubernetes `Rollout` object, not a standard `Deployment` for most services).

---

## Scoping a Rollout to One Region First

Production uses weighted traffic shifting via Argo Rollouts to progressively deploy changes. The default rollout strategy for most services is:

```yaml
# argo-rollout.yaml (example for query-service)
strategy:
  canary:
    steps:
    - setWeight: 10     # 10% of traffic → new version
    - pause: {duration: 5m}
    - setWeight: 50
    - pause: {duration: 5m}
    - setWeight: 100
    canaryService: query-service-canary
    stableService: query-service-stable
```

For high-risk changes (schema migrations, significant algorithm changes), you can manually pause the rollout at 10% and monitor before proceeding:

```shell
# Pause rollout at 10%
argocd app sync query-service-prod
# Wait for 10% step, then pause manually if needed
kubectl argo rollouts pause query-service -n production

# Watch rollout status
kubectl argo rollouts get rollout query-service -n production --watch

# Promote when satisfied
kubectl argo rollouts promote query-service -n production

# Or abort and roll back
kubectl argo rollouts abort query-service -n production
```

---

## Monitoring a Deploy

While a deploy is in progress, watch these signals:

### Metrics to Watch (Datadog)

Open the **Deploy Monitoring** dashboard before starting the sync: `https://app.datadoghq.com/dashboard/luminary-deploy-monitor`

| Metric | Baseline | Investigate If |
| --- | --- | --- |
| HTTP 5xx rate | < 0.1% | Rises above 0.5% |
| HTTP P99 latency | < 500ms | Rises above 1,500ms |
| Kafka consumer lag (events pipeline) | < 20,000 | Grows continuously |
| Pod restart count | 0 | Any restarts during rollout |
| Database connection pool wait time | < 20ms | Rises above 100ms |

### Log Patterns to Check

```shell
# Watch logs on new pods (canary pods)
kubectl logs -n production -l app.kubernetes.io/name=query-service,rollouts-pod-template-hash=NEW_HASH -f

# Check for panic logs
kubectl logs -n production -l app.kubernetes.io/name=query-service --since=10m | grep -i "panic\|fatal\|CRITICAL"

# Check for elevated error rates
kubectl logs -n production -l app.kubernetes.io/name=query-service --since=5m \
  | jq 'select(.level == "error")' | jq -r '.msg' | sort | uniq -c | sort -rn | head -10
```

---

## Feature Flags as a Deployment Safety Mechanism

Luminary uses an internal feature flag service backed by Postgres. Feature flags allow shipping code that's dark (deployed but inactive) and enabling it independently of a code deploy.

```go
if featureflags.IsEnabled(ctx, "new-funnel-algorithm", workspaceID) {
    return computeFunnelV2(params)
}
return computeFunnelV1(params)
```

**Deployment strategy for risky changes:**

1. Ship the new code behind a feature flag (flag defaults to `false`)
2. Enable the flag for internal Luminary workspaces first
3. Enable for a 5% canary of production workspaces
4. Monitor for 48 hours
5. Gradually roll out to 100%
6. Remove the flag in the next cleanup PR

Feature flags are managed in the internal admin panel at `https://admin.luminary.internal/feature-flags`. Flag changes take effect within 60 seconds (the application re-fetches flag state from Postgres every 60 seconds via a background goroutine).

---

## Deploying Database Migrations Safely

Database migrations are the riskiest part of any deploy. The cardinal rule: **schema changes must be backward compatible with the version of code currently running in production at the time the migration runs**.

### Migration Ordering

Migrations always run **before** the code deploy, not simultaneously. The sequence:

1. Merge the migration PR to `main` → migration runs in staging automatically via `atlas migrate apply`
2. Verify staging is healthy post-migration
3. Run the migration in production: `atlas migrate apply --url $PROD_DB_URL` (requires `eng-ops` access)
4. Verify production DB state: `atlas migrate status --url $PROD_DB_URL`
5. Only after migration is confirmed: promote the code deploy

### Safe Migration Patterns

| Change | Safe? | Notes |
| --- | --- | --- |
| Add a nullable column | Yes | Old code ignores new column |
| Add a column with a DEFAULT | Yes | Old code ignores new column |
| Add a NOT NULL column without DEFAULT | No | Old code doesn't set it → constraint violation |
| Drop a column | No (2-phase) | Phase 1: stop reading in code. Phase 2 (separate deploy): drop column |
| Rename a column | No (2-phase) | Add new column, dual-write, migrate, drop old |
| Add an index CONCURRENTLY | Yes | Non-blocking in Postgres |
| Add an index without CONCURRENTLY | No | Locks table; never do this in production |
| Change column type | No (2-phase) | Requires new column + backfill + cutover |

### Migration Tool

Luminary uses [Atlas](https://atlasgo.io/) for migration management. Migrations live in `luminary/migrations/`.

```shell
# Check migration status
atlas migrate status --url "postgres://..."

# Apply pending migrations (staging)
atlas migrate apply --url "postgres://..." --dir file://luminary/migrations

# Dry run (shows SQL without applying)
atlas migrate apply --dry-run --url "postgres://..." --dir file://luminary/migrations
```

All production migration runs are logged to the `#deployments` Slack channel by the CI bot, including the SQL that was executed and the execution time. If a migration takes more than 30 seconds, a warning is posted — long migrations should be investigated before the code deploy proceeds.
