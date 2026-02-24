---
title: Management API
id: "7077996"
space: TD
version: 2
labels:
    - api
    - management
    - admin
    - users
    - api-keys
author: Robert Gonek
created_at: "2026-02-24T14:54:55Z"
last_modified_at: "2026-02-24T14:54:57Z"
last_modified_by: Robert Gonek
---
# Management API

The Luminary Management API provides programmatic access to workspace administration functions: managing team members, creating and revoking API keys, configuring workspace settings, and auditing access. All management operations require an authenticated user token with an appropriate administrative role—API keys cannot be used to call the Management API (except where explicitly noted).

**Base URL:** `https://api.luminary.io/v1/mgmt`

**API Version:** v1 (Stable)

**Owner:** Platform Identity Team

---

## Role Requirements

All Management API endpoints enforce role-based access control. The roles available in Luminary are:

| Role | Description |
| --- | --- |
| `viewer` | Read-only access to analytics dashboards and reports |
| `analyst` | Can run queries, create reports, and manage their own API keys |
| `member` | All analyst permissions plus event ingestion and workspace data access |
| `admin` | Full workspace administration including user and API key management |
| `owner` | Single superuser per workspace; can delete the workspace and transfer ownership |

---

## Endpoint Reference

### Users

| Method | Path | Description | Required Role |
| --- | --- | --- | --- |
| `GET` | `/v1/mgmt/users` | List all workspace members | `admin` |
| `GET` | `/v1/mgmt/users/{userId}` | Get a single user's profile and roles | `admin` |
| `POST` | `/v1/mgmt/users/invite` | Invite a new user to the workspace by email | `admin` |
| `PATCH` | `/v1/mgmt/users/{userId}` | Update a user's role or display name | `admin` |
| `DELETE` | `/v1/mgmt/users/{userId}` | Remove a user from the workspace | `admin` |
| `POST` | `/v1/mgmt/users/{userId}/suspend` | Suspend a user account (blocks login and API access) | `admin` |
| `POST` | `/v1/mgmt/users/{userId}/unsuspend` | Restore a suspended user account | `admin` |
| `GET` | `/v1/mgmt/users/me` | Get the calling user's own profile | Any authenticated user |
| `PATCH` | `/v1/mgmt/users/me` | Update the calling user's own display name and preferences | Any authenticated user |

### API Keys

| Method | Path | Description | Required Role |
| --- | --- | --- | --- |
| `GET` | `/v1/mgmt/keys` | List all API keys in the workspace | `admin` |
| `POST` | `/v1/mgmt/keys` | Create a new API key | `admin` |
| `GET` | `/v1/mgmt/keys/{keyId}` | Get metadata for a specific API key | `admin` |
| `PATCH` | `/v1/mgmt/keys/{keyId}` | Update key description or expiry | `admin` |
| `DELETE` | `/v1/mgmt/keys/{keyId}` | Permanently revoke an API key | `admin` |
| `GET` | `/v1/mgmt/keys/me` | List API keys owned by the calling user | `analyst` |
| `POST` | `/v1/mgmt/keys/me` | Create a personal API key (scoped to own permissions) | `analyst` |

### Workspace Settings

| Method | Path | Description | Required Role |
| --- | --- | --- | --- |
| `GET` | `/v1/mgmt/workspace` | Get current workspace settings | `admin` |
| `PATCH` | `/v1/mgmt/workspace` | Update workspace settings | `admin` |
| `GET` | `/v1/mgmt/workspace/plan` | Get current plan and quota usage | `admin` |
| `GET` | `/v1/mgmt/workspace/audit-log` | Fetch the admin audit log | `admin` |

---

## Key Endpoints — Full Documentation

### POST /v1/mgmt/users/invite

Sends an email invitation to a new user. The invited user receives an email with a one-time link valid for **72 hours**. If the link expires, an admin must re-send the invite.

#### Request Body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `email` | string | Yes | Email address to invite |
| `role` | string | Yes | Role to assign on acceptance: `viewer`, `analyst`, `member`, or `admin` |
| `displayName` | string | No | Pre-fills the invited user's display name |

**Request example:**

```json
{
  "email": "bob@acme-corp.com",
  "role": "analyst",
  "displayName": "Bob Okonkwo"
}
```

**Response (201 Created):**

```json
{
  "inviteId": "inv_01HZ9KQVXPN4M3T7BWCJ8DREX",
  "email": "bob@acme-corp.com",
  "role": "analyst",
  "status": "pending",
  "expiresAt": "2025-06-06T15:00:00Z",
  "invitedBy": "usr_01HZ9KQVXPN4M3T7"
}
```

#### curl Example

```shell
curl -X POST https://api.luminary.io/v1/mgmt/users/invite \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "Content-Type: application/json" \
  -d '{"email": "bob@acme-corp.com", "role": "analyst"}'
```

---

### POST /v1/mgmt/keys — Create API Key

Creates a long-lived API key for programmatic (non-interactive) access. API keys use the same `Authorization: Bearer` scheme as JWT access tokens but do not expire unless given an explicit expiry or revoked.

#### Request Body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | Yes | Human-readable name (e.g., `"Production Event Pipeline"`) |
| `scopes` | array of strings | Yes | Scopes to grant. Must be a subset of the creating admin's own scopes. |
| `expiresAt` | string | No | RFC 3339 expiry timestamp. If omitted, the key never expires. |
| `description` | string | No | Optional notes about the intended use |

**Request example:**

```json
{
  "name": "Production Event Pipeline",
  "scopes": ["events:ingest"],
  "expiresAt": "2026-06-03T00:00:00Z",
  "description": "Used by the data-pipeline service for server-side event ingestion"
}
```

**Response (201 Created):**

```json
{
  "keyId": "key_01HZ9KQVXPN4M3T7BWCJ8DREY",
  "name": "Production Event Pipeline",
  "key": "lum_key_01HZ9KQVXPN4M3T7_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6",
  "scopes": ["events:ingest"],
  "expiresAt": "2026-06-03T00:00:00Z",
  "createdAt": "2025-06-03T15:00:00Z",
  "createdBy": "usr_01HZ9KQVXPN4M3T7"
}
```

**The** `key` **value is only returned on creation.** Store it securely. Subsequent reads return only the key prefix and last 4 characters as a hint.

#### curl Example

```shell
curl -X POST https://api.luminary.io/v1/mgmt/keys \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Production Event Pipeline",
    "scopes": ["events:ingest"],
    "description": "Server-side ingestion key for the main application"
  }'
```

---

### GET /v1/mgmt/workspace/audit-log

Returns a time-ordered log of all administrative actions taken in the workspace. Useful for compliance, security auditing, and incident investigation.

#### Query Parameters

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `from` | string | 30 days ago | RFC 3339 start of the time range |
| `to` | string | now | RFC 3339 end of the time range |
| `actorId` | string | — | Filter to actions taken by a specific user ID |
| `action` | string | — | Filter by action type (e.g., `"api_key.created"`, `"user.role_changed"`) |
| `cursor` | string | — | Pagination cursor |
| `pageSize` | integer | 100 | Results per page (max 500) |

**Response example:**

```json
{
  "entries": [
    {
      "auditId": "aud_01HZ9KQVXPN4M3T7BWCJ8Z01",
      "timestamp": "2025-06-03T14:58:00Z",
      "action": "api_key.created",
      "actorId": "usr_01HZ9KQVXPN4M3T7",
      "actorEmail": "alice@acme-corp.com",
      "targetType": "api_key",
      "targetId": "key_01HZ9KQVXPN4M3T7BWCJ8DREY",
      "metadata": {
        "keyName": "Production Event Pipeline",
        "scopes": ["events:ingest"]
      },
      "ipAddress": "203.0.113.45",
      "userAgent": "curl/7.88.1"
    }
  ],
  "nextCursor": null
}
```

---

### PATCH /v1/mgmt/workspace — Update Workspace Settings

Updates workspace-level configuration. All fields are optional; supply only the fields you want to change.

#### Request Body

| Field | Type | Description |
| --- | --- | --- |
| `displayName` | string | Workspace display name shown in the UI |
| `enforceMfa` | boolean | If `true`, all users must complete MFA enrollment before logging in |
| `defaultUserRole` | string | Role assigned to newly invited users if not specified at invite time |
| `allowedEmailDomains` | array of strings | If set, only email addresses from these domains can be invited (e.g., `["acme-corp.com"]`). Empty array means no restriction. |
| `sessionTimeoutMinutes` | integer | Idle session timeout in minutes. Min 15, max 10080 (7 days). |
| `dataRetentionDays` | integer | How long raw event data is retained. Min 90. Max determined by plan. |

**Request example:**

```json
{
  "enforceMfa": true,
  "allowedEmailDomains": ["acme-corp.com"],
  "sessionTimeoutMinutes": 480
}
```

**Response (200 OK):** Returns the full updated workspace settings object.
