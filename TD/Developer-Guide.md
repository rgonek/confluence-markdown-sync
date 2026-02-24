---
title: Developer Guide
id: "5341350"
space: TD
version: 2
labels:
    - developer-guide
    - onboarding
    - overview
author: Robert Gonek
created_at: "2026-02-24T14:56:13Z"
last_modified_at: "2026-02-24T14:56:14Z"
last_modified_by: Robert Gonek
---
# Developer Guide

Welcome to the Luminary engineering wiki. This guide covers everything you need to build, test, and ship changes to the Luminary analytics platform. Whether you're onboarding for the first time or looking up a specific process, start here.

Luminary's backend is a Go monorepo with a React/TypeScript frontend. Infrastructure runs on AWS EKS with Terraform-managed resources. The data pipeline uses Apache Kafka and Apache Flink, with results stored in a multi-tenant PostgreSQL cluster and a ClickHouse OLAP warehouse.

## Quick-Start Checklist

Use this checklist when onboarding a new engineer. All tasks should be completed within your first week.

| # | Task | Guide | Done? |
| --- | --- | --- | --- |
| 1 | Install prerequisites (Go, Docker, Node, kubectl) | [Local Development Setup](https://placeholder.invalid/page/developer-guide%2Flocal-development-setup.md) | ☐ |
| 2 | Clone the monorepo and configure environment | [Local Development Setup](https://placeholder.invalid/page/developer-guide%2Flocal-development-setup.md) | ☐ |
| 3 | Start the local stack with docker-compose | [Local Development Setup](https://placeholder.invalid/page/developer-guide%2Flocal-development-setup.md) | ☐ |
| 4 | Run the full test suite | [Local Development Setup](https://placeholder.invalid/page/developer-guide%2Flocal-development-setup.md) | ☐ |
| 5 | Read the API design standards | [API Design Guide](API-Design-Guide.md) | ☐ |
| 6 | Understand the testing philosophy | [Testing Strategy](https://placeholder.invalid/page/developer-guide%2Ftesting-strategy.md) | ☐ |
| 7 | Familiarise yourself with error codes | [Error Codes Reference](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6422734) | ☐ |
| 8 | Learn how feature flags are used | [Feature Flags](Feature-Flags.md) | ☐ |
| 9 | Understand the database migration workflow | [Database Migrations](Database-Migrations.md) | ☐ |
| 10 | Read the security overview | [Security Overview](https://placeholder.invalid/page/security%2Findex.md) | ☐ |

## Sections

- [**Local Development Setup**](https://placeholder.invalid/page/developer-guide%2Flocal-development-setup.md) — Prerequisites, environment variables, docker-compose, running tests, and troubleshooting.
- [**API Design Guide**](API-Design-Guide.md) — REST conventions, pagination, error envelopes, versioning, and OpenAPI requirements.
- [**Testing Strategy**](https://placeholder.invalid/page/developer-guide%2Ftesting-strategy.md) — Test pyramid, coverage thresholds, contract testing, and performance testing.
- [**Error Codes Reference**](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6422734) — Canonical list of all platform error codes with HTTP status, cause, and resolution.
- [**Feature Flags**](Feature-Flags.md) — LaunchDarkly flag lifecycle, targeting rules, kill switches, and CI mock patterns.
- [**Database Migrations**](Database-Migrations.md) — golang-migrate conventions, zero-downtime expand/contract pattern, and CI/prod workflows.

## Getting Help

- **#eng-platform** — Infra, CI/CD, developer tooling.
- **#eng-backend** — Go services, APIs, data pipeline.
- **#eng-frontend** — React app, design system, browser compatibility.
- **#security** — Auth questions, vulnerability reports, secrets handling.

For urgent production issues, see the [Incident Response Playbook](https://placeholder.invalid/page/security%2Fincident-response-playbook.md).
