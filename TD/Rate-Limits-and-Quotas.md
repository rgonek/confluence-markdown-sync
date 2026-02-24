---
title: Rate Limits and Quotas
id: "6553820"
space: TD
version: 2
labels:
    - api
    - rate-limits
    - quotas
    - reliability
author: Robert Gonek
created_at: "2026-02-24T14:55:00Z"
last_modified_at: "2026-02-24T14:55:01Z"
last_modified_by: Robert Gonek
---
# Rate Limits and Quotas

Luminary enforces two distinct types of limits to ensure fair resource allocation and platform stability:

- **Rate limits** — short-window request throttles (requests per minute). Exceeding a rate limit returns `429 Too Many Requests` and is temporary.
- **Quotas** — longer-horizon consumption limits (events per day, compute units per day). Exceeding a quota blocks the relevant operations until the quota resets.

Understanding both is important for designing reliable integrations. See the [API Reference overview](API-Reference.md) for links to API-specific limits documented alongside each endpoint.

---

## Per-Endpoint Rate Limits

The table below shows the default per-minute rate limits applied at the API key or access token level. Each key or token has an independent limit counter.

| API | Endpoint | Starter | Growth | Enterprise |
| --- | --- | --- | --- | --- |
| Auth | `POST /auth/token` | 20 req/min | 60 req/min | 120 req/min |
| Auth | `GET /auth/me` | 60 req/min | 300 req/min | 600 req/min |
| Events | `POST /v2/events` | 300 req/min | 3,000 req/min | Custom |
| Events | `POST /v2/events/batch` | 60 req/min | 600 req/min | Custom |
| Events | `GET /v2/events/status/{batchId}` | 120 req/min | 1,200 req/min | Custom |
| Query | `POST /v2/query` | 30 req/min | 120 req/min | Custom |
| Query | `GET /v2/query/{queryId}` | 120 req/min | 600 req/min | Custom |
| Query | `GET /v2/query/{queryId}/download` | 10 req/min | 60 req/min | Custom |
| Webhooks | All `/v1/webhooks` endpoints | 30 req/min | 120 req/min | 300 req/min |
| Management | All `/v1/mgmt` endpoints | 60 req/min | 300 req/min | 600 req/min |

> **Note:** Rate limits apply per API key or access token. If multiple services share a single API key, they share the same rate limit bucket. Create separate API keys per service to get independent limit buckets.

---

## Per-Plan Daily Quotas

Daily quotas reset at `00:00:00 UTC`.

| Quota | Starter | Growth | Enterprise |
| --- | --- | --- | --- |
| Events ingested / day | 500,000 | 10,000,000 | Custom |
| Query compute units / day | 500 | 5,000 | Custom |
| Data export jobs / day | 5 | 50 | Custom |
| Webhook deliveries / day | 10,000 | 500,000 | Custom |
| API keys (total) | 5 | 50 | 500 |
| Workspace members (total) | 5 | 50 | Unlimited |
| Data retention (days) | 90 | 365 | 730 |
| Max query result rows | 10,000 | 100,000 | 1,000,000 |
| Max concurrent queries | 2 | 10 | Custom |

Quotas for **Enterprise** plans are negotiated at contract time. Contact your account team to adjust limits.

---

## Rate Limit Headers

Every API response includes headers describing the current state of the rate limit bucket for the calling token and endpoint:

| Header | Type | Description |
| --- | --- | --- |
| `X-RateLimit-Limit` | integer | Maximum requests allowed in the current window |
| `X-RateLimit-Remaining` | integer | Requests remaining in the current window |
| `X-RateLimit-Reset` | integer | Unix timestamp (seconds) when the current window resets and the counter returns to `X-RateLimit-Limit` |
| `X-RateLimit-Window` | integer | Window duration in seconds (always `60` for per-minute limits) |
| `Retry-After` | integer | Only present on `429` responses. Seconds to wait before retrying. Equivalent to `X-RateLimit-Reset - now`. |

**Example response headers on a successful request:**

```
HTTP/1.1 200 OK
X-RateLimit-Limit: 3000
X-RateLimit-Remaining: 2847
X-RateLimit-Reset: 1748980060
X-RateLimit-Window: 60
X-Request-ID: req_01HZ9KQVXPN4M3T7BWCJ8DREF
```

**Example response headers on a rate-limited request:**

```
HTTP/1.1 429 Too Many Requests
X-RateLimit-Limit: 3000
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1748980060
X-RateLimit-Window: 60
Retry-After: 23
Content-Type: application/json
```

```json
{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit of 3000 requests/minute exceeded for endpoint POST /v2/events. Retry after 23 seconds.",
    "requestId": "req_01HZ9KQVXPN4M3T7BWCJ8DREF"
  }
}
```

---

## Quota Exhaustion Responses

When a **daily quota** is exceeded (as opposed to a per-minute rate limit), the response uses `403 Forbidden` with a `WORKSPACE_QUOTA_EXCEEDED` error code:

```json
{
  "error": {
    "code": "WORKSPACE_QUOTA_EXCEEDED",
    "message": "Daily event ingestion quota of 500,000 events has been reached for workspace 'acme-corp'. Quota resets at 2025-06-04T00:00:00Z.",
    "requestId": "req_01HZ9KQVXPN4M3T7BWCJ8DREE",
    "details": {
      "quotaType": "events_ingested_daily",
      "limit": 500000,
      "used": 500000,
      "resetsAt": "2025-06-04T00:00:00Z"
    }
  }
}
```

Current quota usage can be checked at any time via `GET /v1/mgmt/workspace/plan` (see the [Management API](Management-API.md)).

---

## Handling 429 Errors: Exponential Backoff

The correct response to a `429` is to pause and retry. A naive immediate retry will simply consume another request against the same exhausted bucket. Use **exponential backoff with jitter** to spread retries over time and avoid thundering-herd problems when multiple processes hit the limit simultaneously.

The following Python example shows a production-grade retry loop with exponential backoff:

```python
import time
import random
import httpx

def luminary_request_with_retry(
    method: str,
    url: str,
    headers: dict,
    json: dict | None = None,
    max_retries: int = 6,
    base_delay: float = 1.0,
    max_delay: float = 60.0,
) -> httpx.Response:
    """
    Makes an HTTP request to the Luminary API with exponential backoff on 429 and 5xx errors.

    Args:
        method: HTTP method string ('GET', 'POST', etc.)
        url: Full request URL
        headers: Request headers (must include Authorization)
        json: Optional JSON request body
        max_retries: Maximum number of retry attempts (default 6)
        base_delay: Initial backoff delay in seconds (default 1.0)
        max_delay: Maximum backoff delay in seconds (default 60.0)

    Returns:
        The final successful httpx.Response

    Raises:
        httpx.HTTPStatusError: If max retries exhausted or non-retryable error
    """
    attempt = 0

    while attempt <= max_retries:
        response = httpx.request(method, url, headers=headers, json=json, timeout=30.0)

        if response.status_code == 429:
            # Respect the Retry-After header if present; otherwise use backoff
            retry_after = response.headers.get("Retry-After")
            if retry_after:
                wait = float(retry_after)
            else:
                # Exponential backoff: base_delay * 2^attempt + random jitter
                wait = min(base_delay * (2 ** attempt), max_delay)
                wait += random.uniform(0, wait * 0.1)  # ±10% jitter

            if attempt < max_retries:
                print(f"Rate limited (attempt {attempt + 1}/{max_retries + 1}). "
                      f"Waiting {wait:.1f}s before retry...")
                time.sleep(wait)
                attempt += 1
                continue
            else:
                response.raise_for_status()  # Re-raise after exhausting retries

        elif response.status_code >= 500:
            # Retry on server errors (502, 503, 504) but not on 4xx client errors
            if attempt < max_retries:
                wait = min(base_delay * (2 ** attempt), max_delay)
                wait += random.uniform(0, wait * 0.1)
                print(f"Server error {response.status_code} (attempt {attempt + 1}/{max_retries + 1}). "
                      f"Waiting {wait:.1f}s before retry...")
                time.sleep(wait)
                attempt += 1
                continue
            else:
                response.raise_for_status()

        else:
            # Success or non-retryable error (4xx): return immediately
            return response

    # Should not reach here, but satisfy type checker
    return response


# Usage example
import os

response = luminary_request_with_retry(
    method="POST",
    url="https://api.luminary.io/v2/events",
    headers={
        "Authorization": f"Bearer {os.environ['LUMINARY_API_KEY']}",
        "Content-Type": "application/json",
        "X-Luminary-Workspace": "acme-corp",
    },
    json={
        "type": "page_viewed",
        "userId": "usr_01HZ9KQVXPN4M3T7",
        "properties": {"url": "https://app.acme-corp.com/"},
    },
)
print(f"Remaining rate limit: {response.headers.get('X-RateLimit-Remaining')}")
```

### Key Backoff Principles

- **Always respect** `Retry-After` — Luminary sets this to the exact number of seconds remaining in the window. Retrying sooner will simply get another 429.
- **Add jitter** — Random jitter (±10% of the wait time in the example above) prevents all retrying clients from hitting the endpoint simultaneously when the window resets.
- **Do not retry 4xx errors** (other than 429) — A `400 VALIDATION_ERROR` or `403 INSUFFICIENT_SCOPE` will not resolve itself on retry.
- **Cap your max delay** — A `max_delay` of 60 seconds is appropriate for most use cases. For batch pipelines that can tolerate longer delays, 5 minutes is reasonable.
- **Log and alert** — Frequent 429s indicate your integration is not batching efficiently or your plan tier is insufficient. Set up monitoring on rate limit headers before you hit `0` remaining.

---

## Proactive Rate Limit Monitoring

Rather than reacting to 429 responses, monitor `X-RateLimit-Remaining` on responses to detect when you're approaching limits:

```python
def check_rate_limit_health(response: httpx.Response, warn_threshold: float = 0.1):
    """Warn when fewer than 10% of rate limit tokens remain."""
    limit = int(response.headers.get("X-RateLimit-Limit", 0))
    remaining = int(response.headers.get("X-RateLimit-Remaining", 0))

    if limit > 0 and remaining / limit < warn_threshold:
        reset_ts = int(response.headers.get("X-RateLimit-Reset", 0))
        seconds_to_reset = max(0, reset_ts - int(time.time()))
        print(f"WARNING: {remaining}/{limit} rate limit tokens remaining. "
              f"Resets in {seconds_to_reset}s.")
```

For server-side pipelines with predictable throughput, pre-calculate your request budget:

```
budget = (rate_limit_per_minute / 60) * flush_interval_seconds
```

For example, on the Growth plan with a `POST /v2/events/batch` limit of 600 req/min and a 5-second flush interval:

```
budget = (600 / 60) * 5 = 50 batch requests per flush interval
```

If your pipeline flushes faster than the budget allows, increase the flush interval, increase batch size (up to 1,000 events per batch), or upgrade your plan.
