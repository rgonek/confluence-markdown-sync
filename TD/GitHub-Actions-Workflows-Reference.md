---
title: GitHub Actions Workflows Reference
id: "5046448"
space: TD
version: 2
labels:
    - infrastructure
    - ci-cd
    - github-actions
    - developer-guide
author: Robert Gonek
created_at: "2026-02-24T14:56:39Z"
last_modified_at: "2026-02-24T14:56:40Z"
last_modified_by: Robert Gonek
---
# GitHub Actions Workflows Reference

This page documents all reusable GitHub Actions workflows in the `luminary-platform` repository. Reusable workflows live in `.github/workflows/` and are called from service-specific repositories using `workflow_call`.

## Reusable Workflows

### `go-test.yml`

Runs Go tests with race detection, coverage reporting, and optional integration test mode.

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `go-version` | string | no | `1.22` | Go toolchain version |
| `run-integration` | boolean | no | `false` | Include tests tagged `//go:build integration` |
| `coverage-threshold` | number | no | `80` | Minimum coverage %, fails if below |
| `working-directory` | string | no | `.` | Directory containing `go.mod` |

**Outputs**

| Output | Description |
| --- | --- |
| `coverage-pct` | Test coverage percentage as a number |
| `test-report-url` | URL to the uploaded test report artifact |

**Example usage**

```yaml
# .github/workflows/ci.yml (in a service repo)
jobs:
  test:
    uses: luminary-platform/.github/.github/workflows/go-test.yml@main
    with:
      go-version: "1.22"
      run-integration: true
      coverage-threshold: 85
    secrets: inherit
```

---

### `node-test.yml`

Runs Node.js tests using Vitest, with optional Playwright E2E step.

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `node-version` | string | no | `20` | Node.js LTS version |
| `package-manager` | string | no | `pnpm` | `npm`, `pnpm`, or `yarn` |
| `test-command` | string | no | `test` | npm script name to run |
| `run-e2e` | boolean | no | `false` | Run Playwright E2E suite |
| `working-directory` | string | no | `.` | Monorepo sub-package path |

**Outputs**

| Output | Description |
| --- | --- |
| `coverage-pct` | Coverage percentage from Vitest |

**Example usage**

```yaml
jobs:
  frontend-test:
    uses: luminary-platform/.github/.github/workflows/node-test.yml@main
    with:
      working-directory: apps/dashboard
      run-e2e: false
```

---

### `docker-build.yml`

Builds and pushes a Docker image to ECR. Supports multi-platform builds (amd64 + arm64) using buildx with QEMU.

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `image-name` | string | yes | — | ECR repository name (e.g. `luminary/query-service`) |
| `dockerfile` | string | no | `Dockerfile` | Path to Dockerfile |
| `context` | string | no | `.` | Docker build context |
| `platforms` | string | no | `linux/amd64,linux/arm64` | Target platforms |
| `push` | boolean | no | `true` | Push to ECR (set false for PR validation) |
| `tags` | string | no | `""` | Additional tags (comma-separated) |

**Outputs**

| Output | Description |
| --- | --- |
| `image-digest` | SHA digest of the pushed image |
| `image-tag` | Primary image tag (git SHA) |

**Example usage**

```yaml
jobs:
  build:
    uses: luminary-platform/.github/.github/workflows/docker-build.yml@main
    with:
      image-name: luminary/query-service
      push: ${{ github.ref == 'refs/heads/main' }}
    secrets: inherit
```

---

### `helm-lint.yml`

Lints Helm charts using `helm lint` and validates rendered templates against the Kubernetes API using `helm template | kubectl --dry-run=client`.

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `chart-path` | string | yes | — | Path to the Helm chart directory |
| `values-file` | string | no | `values.yaml` | Values file for lint |
| `kubernetes-version` | string | no | `1.29` | Target Kubernetes version for validation |

**Outputs**: None.

**Example usage**

```yaml
jobs:
  helm-lint:
    uses: luminary-platform/.github/.github/workflows/helm-lint.yml@main
    with:
      chart-path: charts/query-service
      values-file: charts/query-service/values.production.yaml
```

---

### `argocd-deploy.yml`

Triggers an ArgoCD sync for a named application. Updates the image tag in the ArgoCD Application's Helm values via `argocd app set`, then waits for the sync to complete and the application to be healthy.

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `app-name` | string | yes | — | ArgoCD Application name |
| `image-tag` | string | yes | — | New Docker image tag to deploy |
| `environment` | string | yes | — | `staging` or `production` |
| `timeout` | number | no | `300` | Seconds to wait for sync completion |

**Outputs**

| Output | Description |
| --- | --- |
| `sync-status` | Final ArgoCD sync status (`Synced` or `OutOfSync`) |
| `health-status` | Final ArgoCD health status (`Healthy`, `Degraded`, etc.) |

**Example usage**

```yaml
jobs:
  deploy:
    needs: [build, test]
    uses: luminary-platform/.github/.github/workflows/argocd-deploy.yml@main
    with:
      app-name: query-service
      image-tag: ${{ needs.build.outputs.image-tag }}
      environment: production
    secrets: inherit
```

---

### `security-scan.yml`

Runs security scans using Trivy (container image vulnerabilities) and Snyk (dependency vulnerabilities). Results are uploaded to GitHub Security tab as SARIF.

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `image-ref` | string | no | `""` | Docker image to scan (ECR URI). If empty, skips Trivy. |
| `sarif-upload` | boolean | no | `true` | Upload SARIF to GitHub Security |
| `fail-on-severity` | string | no | `HIGH` | Fail workflow on this severity or above (`LOW`, `MEDIUM`, `HIGH`, `CRITICAL`) |
| `snyk-org` | string | no | `luminary` | Snyk organization slug |

**Outputs**

| Output | Description |
| --- | --- |
| `trivy-findings` | Count of Trivy findings at or above `fail-on-severity` |
| `snyk-findings` | Count of Snyk findings at or above `fail-on-severity` |

**Example usage**

```yaml
jobs:
  security:
    uses: luminary-platform/.github/.github/workflows/security-scan.yml@main
    with:
      image-ref: ${{ needs.build.outputs.image-digest }}
      fail-on-severity: CRITICAL
    secrets:
      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}
```

---

### `e2e-test.yml`

Runs Playwright end-to-end tests against a deployed environment. Requires the target environment URL and a valid test user API key (injected as a secret).

**Inputs**

| Input | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `base-url` | string | yes | — | Base URL of the environment to test |
| `test-suite` | string | no | `all` | `all`, `smoke`, or `regression` |
| `browser` | string | no | `chromium` | Browser: `chromium`, `firefox`, `webkit` |
| `workers` | number | no | `4` | Parallel Playwright workers |

**Outputs**

| Output | Description |
| --- | --- |
| `passed` | Number of passing tests |
| `failed` | Number of failing tests |
| `report-url` | URL to the uploaded Playwright HTML report |

**Example usage**

```yaml
jobs:
  e2e:
    needs: deploy
    uses: luminary-platform/.github/.github/workflows/e2e-test.yml@main
    with:
      base-url: https://staging.luminary.io
      test-suite: smoke
    secrets: inherit
```

## Self-Hosted Runners

### Why Self-Hosted Runners

GitHub-hosted runners are used for most jobs. Self-hosted runners are required for:

1. **Docker builds with arm64**: Cross-compilation via QEMU is very slow on GitHub-hosted runners. We run native arm64 builds on self-hosted Graviton3 EC2 instances (`c7g.2xlarge`) to keep build times under 5 minutes.
2. **Integration tests against VPC resources**: Integration tests for the query service need to reach ClickHouse and Kafka inside the VPC. Self-hosted runners run inside the VPC.
3. **Large artifact jobs**: Data pipeline tests generate multi-GB fixtures that exceed GitHub-hosted runner disk limits.

### Runner Configuration

Self-hosted runners are registered to the `luminary-platform` GitHub organization and tagged with labels for job targeting:

| Runner Label | Instance Type | Count | Use Case |
| --- | --- | --- | --- |
| `self-hosted, arm64` | `c7g.2xlarge` | 4 | arm64 Docker builds |
| `self-hosted, vpc, large` | `c6i.4xlarge` | 3 | Integration tests, large artifacts |

Runners are managed by the `actions-runner-controller` (ARC) Helm chart running in the production Kubernetes cluster. Runner autoscaling is configured to scale from 0 to max based on queued job count.

### Which Jobs Use Self-Hosted Runners

| Workflow | Jobs on Self-Hosted | Runner Label |
| --- | --- | --- |
| `docker-build.yml` | arm64 buildx node | `self-hosted, arm64` |
| `go-test.yml` (integration mode) | `integration-test` job | `self-hosted, vpc, large` |
| `e2e-test.yml` | All jobs | `self-hosted, vpc, large` |
| Data pipeline CI | Fixture generation, dbt tests | `self-hosted, vpc, large` |

### Runner Maintenance

**Image updates**: Runner images are rebuilt weekly from a base GitHub Actions runner image with Luminary-specific tooling (Go, Node.js, Helm, kubectl, etc.). Images are pushed to ECR. ARC pulls the new image and cycles runners during low-traffic hours (weekends).

**Debugging stuck runners**: If a runner is stuck (showing as busy but no active job):

```shell
# List runner pods
kubectl get pods -n actions-runner-system -l app=luminary-runner

# Describe a specific runner pod
kubectl describe pod luminary-runner-xyz -n actions-runner-system

# Force-delete a stuck runner pod (ARC will recreate it)
kubectl delete pod luminary-runner-xyz -n actions-runner-system
```

**Adding a new runner label**: Update the `RunnerDeployment` spec in `infrastructure/k8s/actions-runner/runner-deployment.yaml` and re-apply.

## Related

- [Git Workflow](Git-Workflow.md)
- [ArgoCD deployment process](https://placeholder.invalid/page/infrastructure)
- [Security: Vulnerability Management](https://placeholder.invalid/page/security%2Fvulnerability-management.md) — Trivy/Snyk context
- [Monitoring Stack](https://placeholder.invalid/page/infrastructure%2Fmonitoring-stack.md)
