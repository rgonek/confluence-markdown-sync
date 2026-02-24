---
title: AWS Cost Optimization
id: "5341373"
space: TD
version: 2
labels:
    - infrastructure
    - aws
    - cost
    - finops
author: Robert Gonek
created_at: "2026-02-24T14:56:30Z"
last_modified_at: "2026-02-24T14:56:32Z"
last_modified_by: Robert Gonek
---
# AWS Cost Optimization

This document covers Luminary's AWS spend profile, cost reduction initiatives, tagging strategy, and the FinOps review cadence. It is the reference for understanding where infrastructure money goes and what levers are available to reduce it.

**Last updated**: 2024-10-04\
**FinOps lead**: Infrastructure Team\
**Budget owner**: VP Engineering

---

## Monthly Spend Breakdown (October 2025)

| Service | Monthly Cost | % of Total | Notes |
| --- | --- | --- | --- |
| EC2 | $41,800 | 34.2% | EKS nodes (majority), ClickHouse cluster, bastion |
| RDS | $8,400 | 6.9% | Postgres Multi-AZ + 2 read replicas |
| MSK (Kafka) | $7,200 | 5.9% | 3 `kafka.m5.4xlarge` brokers |
| ElastiCache | $5,600 | 4.6% | Redis cluster mode, 6 shards `cache.r7g.large` |
| S3 | $9,100 | 7.4% | Event archive (6.2 TB active, 28 TB Glacier IR), exports, backups |
| CloudFront | $3,200 | 2.6% | ~48 TB data transfer/month |
| Data Transfer | $6,400 | 5.2% | Cross-AZ (largest driver: ClickHouse replication, MSK replication) |
| NAT Gateway | $4,100 | 3.4% | ~18 TB processed; significant driver is Lambda → Secrets Manager |
| EKS Control Plane | $1,080 | 0.9% | 3 clusters (prod, staging, tooling) |
| CloudWatch | $2,800 | 2.3% | Log ingestion (8 TB/month) and custom metrics |
| Secrets Manager | $1,100 | 0.9% | 340 secrets × $0.40/month + API calls |
| Route53 | $420 | 0.3% | Hosted zones + health checks |
| WAF | $2,600 | 2.1% | Web ACL + rule groups + request volume |
| Other (ACM, ECR, SSM, SES, etc.) | $1,680 | 1.4% | Miscellaneous |
| **Reserved/Savings Plans credit** | **-$42,000** | — | See Savings Plans section below |
| **Total (net)** | **~$54,480** |  | Gross ~$96,480 before Savings Plans |

The gross spend is ~$96.5k/month; Savings Plans reduce this by~44%.

---

## Savings Plans and Reserved Capacity

### Compute Savings Plans

In Q1 2025, Luminary committed to a 1-year Compute Savings Plan covering $42,000/month of EC2-equivalent usage. This provides approximately 40% discount versus on-demand pricing for flexible EC2 usage across all instance families and regions.

**Coverage**: The Savings Plan covers ~78% of EC2 spend. The remaining 22% (~$9,200/month) runs on-demand, primarily for:

- Spot instance fallback capacity (when Spot is unavailable, it falls back to on-demand)
- Burst capacity for load spikes
- Instances that run fewer than 10 hours/day (not cost-effective to cover with Savings Plan)

**Renewal**: The current plan expires 2024-10-04. A review to size the Q2 2026 commitment should begin 2024-10-04. If Flink migration (RFC-002) proceeds, stream processor EC2 costs may decrease; factor this into sizing.

### Spot Instances for Batch Workloads

Introduced in Q2 2025, the following workloads now run on EC2 Spot:

- Daily ClickHouse aggregation batch job (Spot `c6i.4xlarge`, ~$0.24/hr vs $0.68/hr on-demand)
- ML bot detection model training (monthly, Spot `c6i.8xlarge`)
- Test environment EKS node group (all nodes on Spot)

Spot saves approximately $2,800/month across these workloads. Interruption handling is implemented via instance termination notice hooks — jobs checkpoint progress every 2 minutes and resume on a new Spot instance if interrupted.

### RDS Reserved Instances

The two production RDS instances (`db.r6g.2xlarge` primary and `db.r6g.xlarge` read replicas) are on 1-year Reserved Instances (partial upfront). Discount vs on-demand: 38%. Annual savings: ~$14,400.

---

## 2025 Cost Reduction Initiatives

| Initiative | Quarter | Savings/Month | Status |
| --- | --- | --- | --- |
| Compute Savings Plan (1yr) | Q1 2025 | $11,200 incremental vs pay-as-you-go | Live |
| Spot instances for batch jobs | Q2 2025 | $2,800 | Live |
| S3 Intelligent-Tiering on event archive | Q2 2025 | $1,400 | Live |
| CloudWatch log compression + retention reduction | Q3 2025 | $800 | Live |
| NAT Gateway reduction (VPC endpoints for ECR/Logs) | Q3 2025 | $1,100 | Live |
| RDS Reserved Instances | Q3 2025 | $1,200 | Live |
| Elasticsearch → OpenSearch migration (cost reduction) | Q4 2025 | $600 (est) | In progress |

**Total 2025 savings**: ~$19,100/month ($229,200 annualized) vs baseline spend.

### S3 Intelligent-Tiering

The event archive bucket (`luminary-events-archive`) was migrated to S3 Intelligent-Tiering in April 2025. Objects with access patterns are automatically moved between Frequent Access, Infrequent Access, and Archive Instant Access tiers.

Results after 6 months:

- 68% of archive objects in Infrequent Access tier (objects > 30 days old without access)
- 21% in Frequent Access (recent data, backfill re-reads)
- 11% in Archive Instant Access (objects > 6 months with zero recent access)
- Effective storage cost: $0.012/GB vs $0.023/GB for standard S3

The 90-day monitoring period for Intelligent-Tiering adds $0.0025/1,000 objects. At Luminary's object count (~4.2B objects), this costs~$10,500/month in monitoring fees — which is offset by the tiering savings. Net savings: ~$1,400/month.

---

## 2026 Cost Reduction Targets

| Target | Expected Savings | Approach | Owner | Timeline |
| --- | --- | --- | --- | --- |
| ClickHouse EC2 → Graviton3 | $1,800/month | Migrate from `c5d.4xlarge` to `c7g.4xlarge` (ARM); ~30% cheaper, comparable performance per benchmark | Infra + Data Eng | Q1 2026 |
| EKS node right-sizing | $2,400/month | Current p99 CPU utilization on EKS nodes is 42%; oversized by 30%. VPA + cluster autoscaler tuning. | Infra | Q1 2026 |
| MSK broker right-sizing | $1,200/month | `kafka.m5.4xlarge` → `kafka.m5.2xlarge` after stream processor throughput profiling | Data Eng + Infra | Q2 2026 |
| Cross-AZ traffic reduction | $1,500/month | Move ClickHouse replication to same-AZ where possible; review session affinity for EKS services | Infra | Q2 2026 |
| Savings Plan renewal sizing | TBD | Right-size Q2 2026 1-year Compute Savings Plan based on actual usage trend | Infra | Q1 2026 |
| CloudWatch → Datadog log forwarding reduction | $400/month | Reduce duplicate log shipping; some logs go to both CloudWatch and Datadog unnecessarily | Platform | Q1 2026 |

**2026 target**: Maintain total infrastructure spend below $55k/month net despite projected 40% event volume growth.

---

## Required Tagging Strategy

All AWS resources must have the following tags applied at creation time. Resources without required tags are flagged in the weekly FinOps report and their owners are notified.

| Tag Key | Required | Allowed Values / Format | Purpose |
| --- | --- | --- | --- |
| `Environment` | Yes | `production`, `staging`, `tooling`, `development` | Cost allocation by environment |
| `Team` | Yes | `data-engineering`, `platform`, `infrastructure`, `security` | Cost allocation by team |
| `Service` | Yes | Service name (e.g., `stream-processor`, `query-service`) | Cost allocation by service |
| `CostCenter` | Yes | `cc-001` (Engineering), `cc-002` (Infrastructure) | Finance allocation |
| `ManagedBy` | Yes | `terraform`, `manual`, `crossplane` | Drift detection |
| `Project` | No | Optional project or initiative name | Cross-cutting cost tracking |

Tags are enforced via AWS Config rules (`required-tags`) which trigger a compliance notification for non-compliant resources. Terraform modules automatically apply all required tags via the `default_tags` provider block. Manually created resources are the main source of tagging violations.

```hcl
# provider.tf - all resources get these tags automatically
provider "aws" {
  region = "us-east-1"
  default_tags {
    tags = {
      Environment = var.environment
      Team        = var.team
      CostCenter  = "cc-001"
      ManagedBy   = "terraform"
    }
  }
}
```

---

## FinOps Review Process

A FinOps review is conducted on the first Tuesday of each month. Attendees: Infrastructure lead, VP Engineering, and the team lead for whichever team had the largest month-over-month spend increase.

**Review agenda**:

1. Total spend vs budget (5 min)
2. Month-over-month variance analysis by service (10 min)
3. Top 5 cost drivers and whether they're expected (10 min)
4. Savings Plan utilization and coverage (5 min)
5. Upcoming spend changes (planned scaling, new services) (5 min)
6. Action items from last review (5 min)

**Inputs**: AWS Cost Explorer export, Datadog infrastructure metrics, and the monthly tagging compliance report (from AWS Config).

**Alert thresholds**: Automated Slack alerts fire in `#finops-alerts` when:

- Daily spend exceeds $4,200 (10% above daily average at $54,480/month)
- Any single service exceeds 150% of its 30-day moving average spend
- Savings Plan utilization drops below 85%

The FinOps dashboard in Datadog is at: `https://app.datadoghq.com/dashboard/luminary-finops` (internal access only).
