---
title: API Design Guide
id: "6455502"
space: TD
version: 2
labels:
    - developer-guide
    - api
    - standards
    - rest
author: Robert Gonek
created_at: "2026-02-24T14:55:54Z"
last_modified_at: "2026-02-24T14:55:56Z"
last_modified_by: Robert Gonek
---
# API Design Guide

This document defines the internal standards for designing HTTP APIs at Luminary. All public-facing REST endpoints and internal service APIs must conform to these guidelines. Consistency across our API surface reduces cognitive load for consumers, simplifies generated client code, and makes API versioning tractable.

Deviations require approval from the Platform team and must be documented in the relevant service's `DECISIONS.md`.

---

## Table of Contents

1. [Resource Naming](#resource-naming)
2. [HTTP Method Semantics](#http-method-semantics)
3. [Pagination](#pagination)
4. [Error Response Format](#error-response-format)
5. [Versioning Strategy](#versioning-strategy)
6. [OpenAPI Specification Requirements](#openapi-specification-requirements)
7. [Backward Compatibility Rules](#backward-compatibility-rules)

---

## Resource Naming

Resources are the nouns of our API. Names should reflect the domain model, not internal implementation details.

### Rules

- Use **plural nouns** for collection resources: `/workspaces`, `/reports`, `/users`.
- Use **kebab-case** for multi-word resource names: `/data-sources`, `/api-keys`.
- Nest sub-resources only when the relationship is genuinely ownership-based (the child cannot exist without the parent): `/workspaces/{workspaceId}/reports`.
- Do not exceed **three levels of nesting**. If you need a fourth level, reconsider the resource model.
- Resource IDs in path segments are always **UUIDs**: `/reports/01234567-89ab-cdef-0123-456789abcdef`.
- Do not encode state into resource names. Prefer query parameters or sub-resources for filtering by state.

### Good vs. Bad Examples

| Bad | Good | Reason |
| --- | --- | --- |
| `GET /getWorkspaces` | `GET /workspaces` | No verbs in resource paths |
| `GET /workspace_list` | `GET /workspaces` | Use plural, use kebab-case |
| `POST /createReport` | `POST /workspaces/{id}/reports` | Actions expressed via HTTP method on resource |
| `GET /reports?status=PUBLISHED` | `GET /reports?status=published` | Enum values are lowercase |
| `DELETE /workspaces/{id}/reports/{rid}/charts/{cid}/data-points/{dpid}` | Flatten or introduce a dedicated `chart-data` resource | Four levels of nesting is too deep |
| `GET /users/{id}/getPermissions` | `GET /users/{id}/permissions` | No verbs |
| `POST /reports/{id}/publish` | `POST /reports/{id}/publication` | Noun-ify actions when necessary |
| `PUT /datasource` | `PUT /data-sources/{id}` | Plural, kebab-case, includes ID |

### Action Sub-Resources

When an action cannot be cleanly modeled as a state change on a resource via a standard method, use an **action sub-resource** with `POST`:

```
POST /reports/{id}/exports          # Trigger an export job
POST /workspaces/{id}/invitations   # Invite a user to a workspace
POST /ingestion-jobs/{id}/retries   # Retry a failed ingestion job
```

This keeps paths noun-based while allowing imperative operations.

---

## HTTP Method Semantics

| Method | Semantics | Idempotent | Safe | Body |
| --- | --- | --- | --- | --- |
| `GET` | Retrieve a resource or collection | Yes | Yes | No |
| `POST` | Create a new resource or trigger an action | No | No | Yes |
| `PUT` | Replace a resource entirely (full update) | Yes | No | Yes |
| `PATCH` | Partial update using JSON Merge Patch (RFC 7396) | No* | No | Yes |
| `DELETE` | Remove a resource | Yes | No | No |
| `HEAD` | Same as GET but without response body | Yes | Yes | No |
| `OPTIONS` | Preflight / capability discovery | Yes | Yes | No |

*`PATCH` is idempotent only if the patch document contains no relative operations. Treat it as non-idempotent for retry logic.

### Rules

- `GET` requests must never mutate state. Do not use `GET` for actions that have side effects.
- `PUT` must accept and replace the **full** resource representation. Missing fields are treated as null/zero-value, not preserved from the previous state. Use `PATCH` for partial updates.
- Use `POST` for non-idempotent creates. Include an `Idempotency-Key` header support on `POST` endpoints that create expensive resources (exports, invitations, billing operations).
- `DELETE` returns `204 No Content` on success. It must be idempotent: deleting an already-deleted resource returns `204`, not `404`.
- Use `202 Accepted` for asynchronous operations. The response body must include a job/task resource URL that the client can poll.

---

## Pagination

All collection endpoints that can return more than 100 items must implement **cursor-based pagination**. Offset-based pagination (`?page=2&per_page=50`) is prohibited for new endpoints because it is inconsistent under concurrent writes.

### Request Parameters

| Parameter | Type | Description |
| --- | --- | --- |
| `cursor` | string | Opaque cursor returned by the previous page. Omit to fetch the first page. |
| `limit` | integer | Maximum items to return. Defaults to `20`. Maximum is `200`. |
| `sort` | string | Field to sort by. Prefix with `-` for descending: `sort=-created_at`. |

### Response Envelope

```json
{
  "data": [ ... ],
  "pagination": {
    "next_cursor": "eyJpZCI6IjAxMjM0NTY3LTg5YWItY2RlZi0wMTIzLTQ1Njc4OWFiY2RlZiIsImRpciI6Im5leHQifQ==",
    "prev_cursor": "eyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImRpciI6InByZXYifQ==",
    "has_next": true,
    "has_prev": false,
    "total_count": 1482
  }
}
```

- `next_cursor` and `prev_cursor` are base64-encoded JSON blobs. Clients must treat them as **opaque** — never parse or construct cursors on the client side.
- `total_count` is included when it can be computed cheaply (e.g., from a database count query under 50ms). Omit it for large tables where a full count is too expensive; set `total_count` to `-1` to signal that the count is unavailable.
- When `has_next` is `false`, `next_cursor` is omitted from the response entirely.

### Cursor Implementation (Go)

```go
// pkg/pagination/cursor.go

type Cursor struct {
    ID        uuid.UUID `json:"id"`
    Direction string    `json:"dir"` // "next" or "prev"
}

func Encode(c Cursor) string {
    b, _ := json.Marshal(c)
    return base64.URLEncoding.EncodeToString(b)
}

func Decode(s string) (Cursor, error) {
    b, err := base64.URLEncoding.DecodeString(s)
    if err != nil {
        return Cursor{}, apierr.New(apierr.CodeInvalidCursor, "invalid pagination cursor")
    }
    var c Cursor
    if err := json.Unmarshal(b, &c); err != nil {
        return Cursor{}, apierr.New(apierr.CodeInvalidCursor, "malformed pagination cursor")
    }
    return c, nil
}
```

---

## Error Response Format

All error responses, regardless of HTTP status code, must use the standard Luminary error envelope. This format is defined in `pkg/apierr` and used by all services.

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "Request validation failed. See 'details' for field-level errors.",
    "request_id": "01HXYZ1234ABCDEFG567890JKL",
    "details": [
      {
        "field": "filters[0].operator",
        "issue": "unsupported_value",
        "message": "'between' is not a valid operator for string fields"
      },
      {
        "field": "time_range.end",
        "issue": "required",
        "message": "end timestamp is required when start is provided"
      }
    ],
    "docs_url": "https://docs.luminary.io/errors/VALIDATION_FAILED"
  }
}
```

### Envelope Fields

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error.code` | string | Yes | Machine-readable error code (see [Error Codes](https://placeholder.invalid/page/developer-guide%2Ferror-codes.md)) |
| `error.message` | string | Yes | Human-readable summary. Safe to display in developer tooling but not end-user UI. |
| `error.request_id` | string | Yes | ULID that correlates this error to server-side logs and traces. |
| `error.details` | array | No | Array of field-level error objects. Populated for `422 Unprocessable Entity` responses. |
| `error.details[].field` | string | Yes (in details) | JSON path to the problematic field using dot and bracket notation. |
| `error.details[].issue` | string | Yes (in details) | Machine-readable issue code: `required`, `too_long`, `invalid_format`, `unsupported_value`, `out_of_range`. |
| `error.details[].message` | string | Yes (in details) | Human-readable explanation of the field-level problem. |
| `error.docs_url` | string | No | Link to public documentation for this error code. |

### HTTP Status Code Mapping

Use the most specific status code. Avoid overloading `400 Bad Request` for everything.

| Scenario | HTTP Status |
| --- | --- |
| Malformed JSON body or missing `Content-Type` | 400 |
| Semantic validation failure (field-level errors) | 422 |
| Missing or invalid authentication credential | 401 |
| Valid credential but insufficient permissions | 403 |
| Resource not found | 404 |
| Method not allowed for resource | 405 |
| Request conflicts with current state (e.g., duplicate) | 409 |
| Rate limit exceeded | 429 |
| Unhandled server error | 500 |
| Upstream dependency unavailable | 503 |

For the full list of Luminary-specific error codes, see the [Error Codes Reference](https://placeholder.invalid/page/developer-guide%2Ferror-codes.md).

---

## Versioning Strategy

Luminary uses **URL path versioning** for public-facing APIs and **header versioning** for internal service-to-service APIs.

### Public API Versioning

```
/v1/workspaces
/v2/workspaces    # introduced when a breaking change is unavoidable
```

- The current stable version is **v1**. All new endpoints must be added under `/v1`.
- A new major version (`/v2`) is introduced only when a breaking change cannot be avoided by other means (see [Backward Compatibility Rules](#backward-compatibility-rules)).
- Old versions are maintained for a minimum of **12 months** after a new version is published. Deprecation notices are communicated via `Deprecation` and `Sunset` response headers.
- Version numbers are monotonically increasing integers. Do not use dates, semver strings, or beta labels in URL paths.

### Internal API Versioning

Internal gRPC and HTTP APIs use the `X-Luminary-API-Version` header:

```
X-Luminary-API-Version: 2024-06-01
```

The version is a date string in `YYYY-MM-DD` format representing the API contract date. Services negotiate the highest mutually supported version during connection setup.

---

## OpenAPI Specification Requirements

Every service exposing HTTP endpoints must maintain an OpenAPI 3.1 specification.

### Location

Store the spec at `cmd/<service-name>/openapi.yaml` in the monorepo root. The spec is validated in CI and used to generate client SDKs and documentation.

### Required Fields

Every operation must include:

```yaml
paths:
  /workspaces/{workspaceId}/reports:
    get:
      operationId: listReports          # required: unique, camelCase verb+noun
      summary: List reports             # required: one-line summary
      description: |                    # required for non-trivial endpoints
        Returns a cursor-paginated list of reports in the given workspace.
        Results are sorted by creation time descending by default.
      tags:
        - Reports                       # required: matches resource name
      security:
        - BearerAuth: []                # required: explicitly declare auth
      parameters:
        - $ref: '#/components/parameters/WorkspaceId'
        - $ref: '#/components/parameters/PaginationCursor'
        - $ref: '#/components/parameters/PaginationLimit'
      responses:
        '200':
          description: Successful response
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ReportListResponse'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '422':
          $ref: '#/components/responses/ValidationFailed'
```

### Shared Components

Do not inline repeated schemas. Use `$ref` to reference shared components from `components/schemas`, `components/parameters`, and `components/responses`. Shared components are defined in `pkg/openapi/shared-components.yaml` and imported into each service spec.

### Code Generation

Client SDKs and server stubs are generated via `make codegen`. Run this after modifying any `openapi.yaml`. Generated files live in `gen/` directories and must not be hand-edited.

---

## Backward Compatibility Rules

A change is **breaking** if it causes a correctly implemented v1 client to fail, receive unexpected data, or change behaviour. Breaking changes require a new major API version.

### Breaking Changes (Require New Version)

- Removing an endpoint, field, or enum value.
- Changing the type of a field (e.g., `string` → `integer`).
- Changing the semantics of an existing field without renaming it.
- Adding a new **required** request parameter or request body field.
- Changing pagination from cursor-based to offset-based (or vice versa).
- Modifying error codes for existing error conditions.

### Non-Breaking Changes (Allowed in Existing Version)

- Adding a new optional request field (with a documented default).
- Adding a new response field.
- Adding a new endpoint.
- Adding a new enum value to a response field (clients must handle unknown enum values gracefully).
- Relaxing validation (accepting a previously rejected value).
- Changing response time or performance characteristics.

### Compatibility Testing

The API compatibility test suite (`make test-compat`) runs a recorded set of canonical HTTP request/response pairs against the current server. A test failure means a breaking change was introduced. Do not suppress compatibility test failures — fix the regression or open a PR for a version bump.
