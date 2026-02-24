---
title: API Reference
id: "4882775"
space: TD
version: 2
labels:
    - api
    - reference
    - overview
author: Robert Gonek
created_at: "2026-02-24T14:54:53Z"
last_modified_at: "2026-02-24T14:54:54Z"
last_modified_by: Robert Gonek
---
# Luminary API Reference

The Luminary platform exposes a set of REST APIs that let you ingest analytics events, query your data, configure your workspace, and receive real-time notifications via webhooks. All APIs share a common base URL scheme and authentication mechanism.

This page provides a high-level overview of every API surface. Follow the links in the table below to reach full documentation for each API.

## Base URL

All production API requests are made to:

```
https://api.luminary.io
```

Sandbox environments use a tenant-scoped subdomain:

```
https://api.{tenant}.sandbox.luminary.io
```

Replace `{tenant}` with the sandbox slug shown on your workspace's **Settings → Developer** page.

## API Overview

| API | Current Version | Status | Owner Team | Base URL |
| --- | --- | --- | --- | --- |
| [Authentication API](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=4980890) | v1 | Stable | Platform Identity | `https://api.luminary.io/auth` |
| [Events Ingestion API](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6258833) | v2 | Stable | Data Pipeline | `https://api.luminary.io/v2/events` |
| [Query API](https://placeholder.invalid/page/api-reference%2Fquery-api.md) | v2 | Stable | Analytics Engine | `https://api.luminary.io/v2/query` |
| [Webhooks API](https://placeholder.invalid/page/api-reference%2Fwebhooks-api.md) | v1 | Stable | Integrations | `https://api.luminary.io/v1/webhooks` |
| [Management API](https://placeholder.invalid/page/api-reference%2Fmanagement-api.md) | v1 | Stable | Platform Identity | `https://api.luminary.io/v1/mgmt` |
| Events Ingestion API (v1) | v1 | Deprecated | Data Pipeline | `https://api.luminary.io/v1/events` |
| Segments API | v1 | Beta | Analytics Engine | `https://api.luminary.io/v1/segments` |
| Exports API | v1 | Beta | Data Pipeline | `https://api.luminary.io/v1/exports` |

> **Deprecation notice:** Events Ingestion API v1 will reach end-of-life on **2026-09-01**. All customers must migrate to v2 before that date. See the [API Changelog](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6586589) for migration guidance.

## Authentication

Every API call (except the token issuance endpoints themselves) requires a Bearer token in the `Authorization` header:

```
Authorization: Bearer <access_token>
```

Access tokens are short-lived JWTs issued by the [Authentication API](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=4980890). Tokens expire after **3600 seconds** by default. Use the refresh token flow to obtain a new access token without re-entering credentials.

API keys (long-lived, opaque tokens) can be created via the [Management API](https://placeholder.invalid/page/api-reference%2Fmanagement-api.md) for server-to-server integrations where interactive login is not practical. API keys use the same `Authorization: Bearer` header format.

## Versioning

Luminary APIs are versioned via the URL path (`/v1/`, `/v2/`, etc.). We follow a **no-breaking-changes within a version** guarantee: once a version is marked Stable, we will only add optional fields and new endpoints. We will never remove fields, rename endpoints, or change response semantics within a stable version.

When breaking changes are necessary, a new API version is introduced and the previous version enters a **Deprecation** period of at least 12 months before end-of-life.

## Request and Response Format

All request and response bodies use `application/json` unless otherwise noted. Always include the `Content-Type: application/json` header on requests with a body.

Dates and timestamps use [RFC 3339](https://datatracker.ietf.org/doc/html/rfc3339) format (`2025-06-15T14:32:00Z`).

Pagination uses cursor-based pagination. Responses that may return multiple items include a `nextCursor` field in the response envelope. Pass this value as the `cursor` query parameter on the next request.

## Rate Limits and Quotas

All APIs share a common rate limiting infrastructure. See the [Rate Limits and Quotas](https://placeholder.invalid/page/api-reference%2Frate-limits-and-quotas.md) page for per-endpoint limits, per-plan quotas, and guidance on handling `429 Too Many Requests` responses.

## Error Format

All error responses use a consistent JSON envelope:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "The 'timestamp' field must be a valid RFC 3339 datetime.",
    "requestId": "req_01HZ9KQVXPN4M3T7BWCJ8DREF",
    "details": [
      {
        "field": "events[2].timestamp",
        "issue": "invalid_format",
        "received": "2025-13-01T00:00:00Z"
      }
    ]
  }
}
```

The `requestId` field is present on every response (success and error) as the `X-Request-ID` header. Include this value when contacting Luminary Support.

## SDKs and Client Libraries

Official SDKs are available for the following languages:

| Language | Package | Source |
| --- | --- | --- |
| JavaScript / TypeScript | `@luminary/sdk` | github.com/luminary-io/sdk-js |
| Python | `luminary-sdk` | github.com/luminary-io/sdk-python |
| Go | `github.com/luminary-io/sdk-go` | github.com/luminary-io/sdk-go |
| Java | `io.luminary:sdk` | github.com/luminary-io/sdk-java |
| Ruby | `luminary` (gem) | github.com/luminary-io/sdk-ruby |

SDK documentation and changelog are maintained in each repository's `CHANGELOG.md`. The SDKs handle token refresh, retry logic, and rate limit backoff automatically—using an SDK is strongly recommended over raw HTTP for production integrations.
