---
title: Infrastructure Overview
id: "6947000"
space: TD
version: 2
labels:
    - infrastructure
    - index
    - overview
author: Robert Gonek
created_at: "2026-02-24T14:56:41Z"
last_modified_at: "2026-02-24T14:56:43Z"
last_modified_by: Robert Gonek
---
# Infrastructure Overview

This section documents Luminary's production infrastructure: how our Kubernetes clusters are configured, how infrastructure is provisioned with Terraform, and how code moves from a developer's laptop to production.

All production infrastructure runs on **AWS** (primary region: `eu-west-1`, DR region: `us-east-1`). Infrastructure-as-code lives in the [luminary/infra](https://github.com/luminary/infra) GitHub repository.

---

## Section Contents

| Page | Description |
| --- | --- |
| [Kubernetes Cluster Setup](https://placeholder.invalid/page/infrastructure%2Fkubernetes-cluster-setup.md) | EKS cluster architecture, node pools, networking, and add-ons |
| [Terraform Modules](https://placeholder.invalid/page/infrastructure%2Fterraform-modules.md) | Internal Terraform module library: usage, inputs/outputs, caveats |
| [CI/CD Pipeline](CI-CD-Pipeline-—-GitHub-Actions-and-ArgoCD.md) | GitHub Actions + ArgoCD pipeline, branch strategy, rollback procedures |

---

## Quick Reference

| System | Technology | Region | AWS Account |
| --- | --- | --- | --- |
| Kubernetes | EKS 1.30 | eu-west-1 | `luminary-prod` |
| Kafka | MSK (Kafka 3.6) | eu-west-1 | `luminary-prod` |
| ClickHouse | Self-managed on EKS | eu-west-1 | `luminary-prod` |
| PostgreSQL | RDS Multi-AZ (pg 15) | eu-west-1 | `luminary-prod` |
| Redis | ElastiCache (Redis 7) | eu-west-1 | `luminary-prod` |
| Container Registry | ECR | eu-west-1 | `luminary-shared` |
| Terraform State | S3 + DynamoDB lock | eu-west-1 | `luminary-infra` |

For the application-layer architecture that runs on top of this infrastructure, see [Architecture](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6389968).

---

## Ownership

Platform Engineering owns all infrastructure in this section. For urgent issues outside business hours, page the `platform-oncall` rotation in PagerDuty.

For access requests or changes to IAM roles, open a ticket in Jira under the `PLAT` project.
