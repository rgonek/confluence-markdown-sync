---
title: Webhooks API
id: "5996765"
space: TD
version: 2
labels:
    - api
    - webhooks
    - integrations
    - events
author: Robert Gonek
created_at: "2026-02-24T14:55:02Z"
last_modified_at: "2026-02-24T14:55:04Z"
last_modified_by: Robert Gonek
---
# Webhooks API

Webhooks allow your systems to receive real-time HTTP notifications when specific events occur in your Luminary workspace. Instead of polling the [Query API](Query-API.md) for changes, Luminary pushes a signed HTTP POST payload to a URL you register whenever a subscribed event fires.

**Base URL:** `https://api.luminary.io/v1/webhooks`

**API Version:** v1 (Stable)

**Owner:** Integrations Team

---

## Overview

Luminary webhooks are designed for workload-triggered automations: updating a CRM record when a high-value purchase is completed, alerting a Slack channel when a customer churns, or triggering a downstream data pipeline when a daily export finishes.

Webhooks are distinct from the analytics event stream: they fire on **platform events** (things Luminary itself does, such as completing a data export or detecting anomalous event volume) as well as on **ingested events** that match a filter you define.

---

## Available Webhook Event Types

### Platform Events

These fire based on actions within Luminary itself:

| Event Type | Description |
| --- | --- |
| `workspace.data_export.completed` | A scheduled or manual data export finished successfully |
| `workspace.data_export.failed` | An export job failed after all retries |
| `workspace.quota.warning` | Workspace has consumed 80% of a daily quota |
| `workspace.quota.exceeded` | A daily quota has been fully exhausted |
| `workspace.api_key.created` | A new API key was created |
| `workspace.api_key.revoked` | An API key was revoked |
| `workspace.member.invited` | A new user was invited to the workspace |
| `workspace.member.removed` | A user was removed from the workspace |
| `anomaly.detected` | The Luminary anomaly detection model flagged an unusual metric movement |

### Ingested Event Passthrough

You can configure a webhook to fire whenever a specific event type arrives via the [Events Ingestion API](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=6258833). Provide the `eventType` and optional `filters` in the webhook registration to narrow which ingested events trigger the webhook.

For example: fire a webhook every time a `purchase_completed` event arrives with `properties.revenue > 5000`.

Ingested event passthrough webhooks are rate-limited to prevent overwhelming your endpoint during high-volume ingestion. If your endpoint cannot keep up, consider using the Query API on a schedule instead.

---

## Webhook Registration

### POST /v1/webhooks

Creates a new webhook endpoint registration.

**Required scope:** `webhooks:manage`

#### Request Body

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `url` | string | Yes | HTTPS URL to deliver the webhook payload to. Must be HTTPS; HTTP URLs are rejected. |
| `description` | string | No | Human-readable name for this webhook (e.g., `"CRM churn sync"`) |
| `events` | array of strings | Yes | List of event type strings to subscribe to. Use `["*"]` to subscribe to all platform events. |
| `ingestedEventType` | string | No | Ingested event type to mirror (e.g., `"purchase_completed"`). Only valid when `events` includes `"ingested_event"`. |
| `ingestedEventFilters` | array | No | Filter conditions applied to ingested events before triggering. Same filter syntax as the [Query API](Query-API.md#filters). |
| `active` | boolean | No | Whether the webhook is enabled. Defaults to `true`. |
| `secret` | string | No | If provided, Luminary will use this value to sign payloads (see [Signature Verification](#signature-verification)). If omitted, Luminary generates a secret automatically. |

**Request example:**

```json
{
  "url": "https://integrations.acme-corp.com/luminary/webhooks",
  "description": "High-value purchase CRM sync",
  "events": ["ingested_event"],
  "ingestedEventType": "purchase_completed",
  "ingestedEventFilters": [
    {
      "field": "properties.revenue",
      "operator": "gte",
      "value": 500
    }
  ],
  "active": true
}
```

**Response (201 Created):**

```json
{
  "webhookId": "wh_01HZ9KQVXPN4M3T7BWCJ8DREF",
  "url": "https://integrations.acme-corp.com/luminary/webhooks",
  "description": "High-value purchase CRM sync",
  "events": ["ingested_event"],
  "ingestedEventType": "purchase_completed",
  "ingestedEventFilters": [
    {"field": "properties.revenue", "operator": "gte", "value": 500}
  ],
  "active": true,
  "secret": "whsec_4a8b2c9f1e3d7a6b5c4d8e9f2a1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
  "createdAt": "2025-06-03T15:00:00Z"
}
```

**Store the** `secret` **value immediately.** Luminary only returns the full secret on creation. Subsequent GET requests return only the last 4 characters as a hint (e.g., `"...9a0b"`).

### Other Management Endpoints

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/webhooks` | List all registered webhooks |
| `GET` | `/v1/webhooks/{webhookId}` | Get a single webhook registration |
| `PATCH` | `/v1/webhooks/{webhookId}` | Update `url`, `description`, `events`, `active`, or filters |
| `DELETE` | `/v1/webhooks/{webhookId}` | Permanently delete a webhook registration |
| `POST` | `/v1/webhooks/{webhookId}/test` | Send a synthetic test payload to the endpoint |

---

## Webhook Payload

Every webhook delivery is a `POST` request to your registered URL with a JSON body. The outer envelope is consistent regardless of event type:

```json
{
  "webhookId": "wh_01HZ9KQVXPN4M3T7BWCJ8DREF",
  "deliveryId": "del_01HZ9KQVXPN4M3T7BWCJ8DRX1",
  "eventType": "ingested_event",
  "workspaceId": "ws_acmecorp",
  "timestamp": "2025-06-03T15:04:32.118Z",
  "payload": {
    "type": "purchase_completed",
    "eventId": "evt_01HZ9KQVXPN4M3T7BWCJ8DRA",
    "userId": "usr_01HZ9KQVXPN4M3T7",
    "timestamp": "2025-06-03T15:04:31.000Z",
    "properties": {
      "orderId": "ord_01HZ9KQVXPN4M3T7BWCJ",
      "revenue": 1499.00,
      "currency": "USD",
      "itemCount": 1,
      "couponCode": null
    }
  }
}
```

The `payload` field contains the original event data (for `ingested_event` types) or a platform-specific payload for platform event types.

---

## Signature Verification

Every webhook request from Luminary includes the `X-Luminary-Signature` header. **Always verify this signature before processing the payload.** Without verification, an attacker could send arbitrary POST requests to your webhook endpoint.

Luminary computes the signature as:

```
HMAC-SHA256(secret, timestamp + "." + raw_request_body)
```

Where:

- `secret` is the webhook secret from the registration response
- `timestamp` is the value of the `X-Luminary-Timestamp` header (Unix seconds as a string)
- `raw_request_body` is the raw, unmodified UTF-8 bytes of the request body

The `X-Luminary-Signature` header contains a comma-separated list of signatures (to support secret rotation):

```
X-Luminary-Signature: v1=a3f8e2c14b7d4e9f8a2bc1d3e5f7a9b0c2d4e6f8a0b2c4d6e8f0a2b4c6d8e0f2
X-Luminary-Timestamp: 1748980000
```

**Reject requests** where `|now - timestamp| > 300` seconds to prevent replay attacks.

### Node.js Webhook Handler Example

```javascript
const express = require('express');
const crypto = require('crypto');

const app = express();

// IMPORTANT: Use express.raw() to get the raw body for signature verification
// Do NOT use express.json() on this route
app.use('/luminary/webhook', express.raw({ type: 'application/json' }));

const WEBHOOK_SECRET = process.env.LUMINARY_WEBHOOK_SECRET;
const SIGNATURE_TOLERANCE_SECONDS = 300;

function verifyLuminarySignature(rawBody, signature, timestamp) {
  const now = Math.floor(Date.now() / 1000);
  const age = Math.abs(now - parseInt(timestamp, 10));

  if (age > SIGNATURE_TOLERANCE_SECONDS) {
    throw new Error(`Webhook timestamp is ${age}s old (max ${SIGNATURE_TOLERANCE_SECONDS}s)`);
  }

  const expectedSig = crypto
    .createHmac('sha256', WEBHOOK_SECRET)
    .update(`${timestamp}.${rawBody}`)
    .digest('hex');

  // Support multiple signatures for rotation (compare each v1= entry)
  const signatures = signature.split(',').map(s => s.trim());
  const valid = signatures.some(sig => {
    const [prefix, value] = sig.split('=');
    if (prefix !== 'v1') return false;
    // Use timingSafeEqual to prevent timing attacks
    const expected = Buffer.from(expectedSig, 'hex');
    const received = Buffer.from(value, 'hex');
    if (expected.length !== received.length) return false;
    return crypto.timingSafeEqual(expected, received);
  });

  if (!valid) {
    throw new Error('Webhook signature verification failed');
  }
}

app.post('/luminary/webhook', (req, res) => {
  const signature = req.headers['x-luminary-signature'];
  const timestamp = req.headers['x-luminary-timestamp'];

  if (!signature || !timestamp) {
    return res.status(400).json({ error: 'Missing signature headers' });
  }

  try {
    verifyLuminarySignature(req.body, signature, timestamp);
  } catch (err) {
    console.error('Signature verification failed:', err.message);
    return res.status(401).json({ error: 'Invalid signature' });
  }

  const event = JSON.parse(req.body.toString('utf8'));

  // Acknowledge receipt immediately — do heavy processing asynchronously
  res.status(200).json({ received: true });

  // Process asynchronously to avoid Luminary timing out the delivery
  setImmediate(async () => {
    try {
      switch (event.eventType) {
        case 'ingested_event':
          if (event.payload.type === 'purchase_completed') {
            await syncPurchaseToCRM(event.payload);
          }
          break;
        case 'workspace.quota.warning':
          await notifySlack(`Luminary quota warning: ${JSON.stringify(event.payload)}`);
          break;
        default:
          console.log('Unhandled webhook event type:', event.eventType);
      }
    } catch (err) {
      console.error('Async webhook processing error:', err);
    }
  });
});

app.listen(3000, () => console.log('Webhook server running on :3000'));
```

---

## Retry Behavior

If your endpoint does not return a `2xx` response within **10 seconds**, Luminary marks the delivery as failed and schedules a retry.

| Attempt | Delay after previous failure |
| --- | --- |
| 2nd | 30 seconds |
| 3rd | 5 minutes |
| 4th | 30 minutes |
| 5th | 2 hours |
| 6th (final) | 12 hours |

After 6 failed attempts, the delivery is abandoned and recorded as permanently failed. The webhook remains active but that particular delivery is not retried further.

**Delivery timeouts count as failures.** Ensure your endpoint acknowledges the request quickly (as shown in the Node.js example above) and offloads heavy processing to a background queue.

---

## Failure Handling and Monitoring

### Automatic Suspension

If a webhook endpoint accumulates more than **100 consecutive delivery failures**, the endpoint is automatically suspended. You will receive an email notification and a `workspace.webhook.suspended` platform event (delivered to any other active webhooks).

To re-enable a suspended webhook:

```shell
curl -X PATCH https://api.luminary.io/v1/webhooks/wh_01HZ9KQVXPN4M3T7BWCJ8DREF \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "Content-Type: application/json" \
  -d '{"active": true}'
```

### Delivery Logs

All delivery attempts (successful and failed) are logged and accessible for 7 days via:

```
GET /v1/webhooks/{webhookId}/deliveries
```

Each log entry includes the attempt timestamp, HTTP status returned by your endpoint, response time, and (on failure) the error message.

### Testing Your Endpoint

Use the test endpoint to send a synthetic payload without triggering a real event:

```shell
curl -X POST https://api.luminary.io/v1/webhooks/wh_01HZ9KQVXPN4M3T7BWCJ8DREF/test \
  -H "Authorization: Bearer eyJhbGciOiJFUzI1NiJ9..." \
  -H "Content-Type: application/json" \
  -d '{"eventType": "ingested_event"}'
```

The test endpoint uses the same signing and delivery mechanism as real events, so it validates your signature verification code.
