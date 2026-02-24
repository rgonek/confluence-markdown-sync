---
title: 'ADR-005: GraphQL API Evaluation'
id: "6619333"
space: TD
version: 2
labels:
    - adr
    - api
    - deferred
    - architecture
author: Robert Gonek
created_at: "2026-02-24T14:55:13Z"
last_modified_at: "2026-02-24T14:55:15Z"
last_modified_by: Robert Gonek
---
# ADR-005: Adopt GraphQL for Public API

| Field | Value |
| --- | --- |
| **Status** | Deferred |
| **Date** | 2024-10-04 |
| **Author** | Marcus Webb |
| **Reviewers** | Priya Nair, Dana Kim |
| **Raised in** | January 2026 Architecture Review |

## Context

GraphQL adoption came up in the January 2026 architecture review (notes in SD space: Architecture Review 2026-01) following customer feedback that the REST API returns more fields than needed in list endpoints, causing unnecessary data transfer for SDK clients on mobile connections. Two enterprise customers have also specifically requested GraphQL support in their renewal discussions.

The main proposed benefit is field selection — clients can request only the fields they need, reducing payload size and over-fetching. Secondary benefits cited: a single endpoint simplifying client routing, and a self-describing schema for third-party integrations.

## Considered Approaches

**Full GraphQL migration**: Replace the REST API with a GraphQL API at `api.luminary.io/graphql`. High migration cost, breaks all existing integrations.

**GraphQL alongside REST**: Run both APIs. Doubles maintenance burden indefinitely.

**Field selection on REST**: Add `?fields=id,name,createdAt` query parameter support to the most frequently over-fetched endpoints (`GET /events`, `GET /workspaces`, `GET /reports`). Lower cost, no new paradigm to support.

## Decision

**Deferred.** We will not adopt GraphQL at this time.

Instead, we will implement sparse fieldset selection (`?fields=`) on the top three over-fetched endpoints identified by API usage analytics. This directly addresses the payload size concern without introducing a new query language, new tooling requirements, or a client migration.

The GraphQL question should be revisited in the Q3 2026 architecture review with data from the field selection rollout — if field selection is insufficient or customer demand for GraphQL grows materially, we should re-evaluate with a clearer picture of ROI.

## Consequences

- ENG-4102: Implement `?fields=` on `GET /events`, `GET /workspaces`, `GET /reports` — targeting v3.3
- Arch review calendar: GraphQL re-evaluation blocked until Q3 2026
- No changes to API versioning or SDK compatibility

## Related

- [ADR-004: Postgres to CockroachDB](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6521001)
- API Reference (see `api-reference/` for current REST API documentation)
