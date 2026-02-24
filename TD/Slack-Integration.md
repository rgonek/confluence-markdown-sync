---
title: Slack Integration
id: "4849826"
space: TD
version: 2
labels:
    - integrations
    - slack
    - notifications
author: Robert Gonek
created_at: "2026-02-24T14:57:05Z"
last_modified_at: "2026-02-24T14:57:07Z"
last_modified_by: Robert Gonek
---
# Slack Integration

The Luminary Slack integration lets you route alert notifications and scheduled report summaries directly to Slack channels. It uses a Slack App installed into your workspace via OAuth 2.0, and supports fine-grained routing rules to send different alerts to different channels.

## Table of Contents

- [Setting Up the Slack App](#setting-up-the-slack-app)
- [Configuring Notification Channels](#configuring-notification-channels)
- [Alert Routing Rules](#alert-routing-rules)
- [Message Format](#message-format)
- [Troubleshooting](#troubleshooting)

---

## Setting Up the Slack App

### Required Slack Scopes

The Luminary Slack App requests the following OAuth scopes:

| Scope | Reason |
| --- | --- |
| `chat:write` | Post messages to channels the app has been invited to. |
| `chat:write.public` | Post messages to public channels without needing an explicit invite. |
| `channels:read` | List public channels to populate the channel picker in Luminary settings. |
| `groups:read` | List private channels the app has been invited to. |
| `users:read` | Resolve user mentions in routing rules (e.g., notify `@alice` on critical alerts). |
| `users:read.email` | Match Luminary user accounts to Slack users by email for mention resolution. |

Luminary does **not** request `channels:history`, `files:write`, or any scope that allows reading message history.

### Installation Steps

1. In Luminary, go to **Settings → Integrations → Slack**.
2. Click **Connect to Slack**. You will be redirected to Slack's OAuth authorization page.
3. Select the Slack workspace to install into (you must be a Slack workspace admin or have the "Manage apps" permission).
4. Review the requested scopes and click **Allow**.
5. You are redirected back to Luminary. A green confirmation banner confirms the installation.

After installation, Luminary stores the bot OAuth token securely in Vault (same mechanism as [Auth Service](https://placeholder.invalid/page/services%2Fauth-service.md) signing keys). Tokens are scoped per Luminary workspace — installing the Slack app for workspace A does not affect workspace B.

### Re-authorization and Revocation

If your Slack workspace admin revokes the app's access, all scheduled deliveries and alert notifications will fail silently until the app is re-authorized. Luminary surfaces OAuth token errors in **Settings → Integrations → Slack → Status**.

---

## Configuring Notification Channels

After connecting Slack, configure which channels receive which types of notifications.

1. Go to **Settings → Integrations → Slack → Notification Channels**.
2. Click **Add Channel**.
3. Select a Slack channel from the dropdown (populated via `channels:read` and `groups:read`).
4. Choose the **notification types** to route to this channel:

   - Alert notifications (threshold breaches, anomaly alerts)
   - Scheduled report summaries
   - Digest emails (daily/weekly event volume summaries)
5. Optionally configure a **mention** (a Slack user or user group to `@`-mention in messages sent to this channel).
6. Click **Save**.

You can configure multiple channels with overlapping notification types. The same alert will be sent to all matching channels.

---

## Alert Routing Rules

Routing rules let you filter which alerts reach which channels beyond the broad notification type selection. Rules are evaluated in order; the first matching rule determines the channel(s). If no rule matches, the alert falls through to the default channel (if one is configured).

### Rule Conditions

| Condition | Operators | Example |
| --- | --- | --- |
| Alert severity | `is`, `is not` | Severity is `critical` |
| Alert name | `contains`, `matches regex` | Name contains `"revenue"` |
| Dashboard name | `is`, `contains` | Dashboard is `"Executive KPIs"` |
| Metric name | `contains`, `matches regex` | Metric contains `"error_rate"` |
| Tag | `has tag` | Has tag `"team:backend"` |

### Example Rule

Route all `critical` severity alerts tagged with `team:backend` to `#backend-oncall`, and all other alerts to `#analytics-alerts`:

```
Rule 1: severity = critical AND tag = team:backend  → #backend-oncall
Rule 2: (default)                                   → #analytics-alerts
```

---

## Message Format

Luminary sends messages using Slack's Block Kit format for rich, structured notifications.

### Alert Notification Example

```json
{
  "blocks": [
    {
      "type": "header",
      "text": {
        "type": "plain_text",
        "text": "🔴 Critical Alert: Error Rate Spike"
      }
    },
    {
      "type": "section",
      "fields": [
        { "type": "mrkdwn", "text": "*Metric:*\nHTTP Error Rate (5xx)" },
        { "type": "mrkdwn", "text": "*Current Value:*\n4.7% (threshold: 1%)" },
        { "type": "mrkdwn", "text": "*Dashboard:*\nAPI Health Overview" },
        { "type": "mrkdwn", "text": "*Triggered:*\n<!date^1736856000^{date_short_pretty} at {time}|Jan 14, 2025 at 12:00 PM>" }
      ]
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": { "type": "plain_text", "text": "View in Luminary" },
          "url": "https://app.luminaryapp.io/workspaces/ws_889f/alerts/alrt_01hx",
          "style": "danger"
        },
        {
          "type": "button",
          "text": { "type": "plain_text", "text": "Silence for 1 hour" },
          "action_id": "silence_alert",
          "value": "alrt_01hx:60"
        }
      ]
    },
    {
      "type": "context",
      "elements": [
        { "type": "mrkdwn", "text": "Workspace: *Meridian Analytics* · Severity: *critical* · Tags: `team:backend`" }
      ]
    }
  ]
}
```

### Scheduled Report Summary Example

Report summaries use a simpler format with a table-like section:

```json
{
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*Weekly Report: Revenue Dashboard*\nWeek of Jan 8–14, 2025"
      }
    },
    {
      "type": "section",
      "fields": [
        { "type": "mrkdwn", "text": "*Total Revenue:*\n$1,240,500 ↑ 8.3%" },
        { "type": "mrkdwn", "text": "*New Customers:*\n342 ↑ 12.1%" },
        { "type": "mrkdwn", "text": "*Churn Rate:*\n1.4% ↓ 0.2pp" },
        { "type": "mrkdwn", "text": "*Avg Contract Value:*\n$3,625 ↑ 4.5%" }
      ]
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": { "type": "plain_text", "text": "Open Full Report" },
          "url": "https://app.luminaryapp.io/workspaces/ws_889f/reports/rpt_weekly_revenue"
        }
      ]
    }
  ]
}
```

The **Silence for 1 hour** action button sends a POST back to Luminary's Slack interactivity endpoint (`https://api.luminaryapp.io/integrations/slack/actions`). This requires the Slack App to have the **Interactivity** feature enabled, which is configured automatically during the OAuth installation.

---

## Troubleshooting

### Messages are not arriving

1. Check **Settings → Integrations → Slack → Status** for any token error banner.
2. Verify the Luminary Slack App has been invited to the target channel. Even with `chat:write.public`, private channels require an explicit `/invite @Luminary`.
3. Check that the routing rule conditions match the alert. Use **Settings → Integrations → Slack → Event Log** to see recent delivery attempts and their outcomes.

### `not_in_channel` error

The Luminary bot is not a member of the target channel. Run `/invite @Luminary` in the channel, or switch to a public channel where `chat:write.public` applies.

### `token_revoked` error

The Slack workspace admin revoked the Luminary app's access. Re-authorize via **Settings → Integrations → Slack → Reconnect**.

### Duplicate messages

If you have multiple Luminary workspaces connected to the same Slack workspace, and both have routing rules pointing to the same channel, you may see duplicate notifications. Each Luminary workspace has an independent Slack connection. Review routing rules across all connected Luminary workspaces.

### The "Silence" button is not working

Interactive components require the Slack App to have a valid **Request URL** configured in the Interactivity settings of your Slack App manifest. If you installed the app before May 2024, the interactivity endpoint may not have been configured. Re-install the app via **Settings → Integrations → Slack → Reinstall** to apply the latest app manifest.
