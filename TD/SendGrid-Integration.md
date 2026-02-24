---
title: SendGrid Integration
id: "6652035"
space: TD
version: 2
labels:
    - integrations
    - sendgrid
    - email
author: Robert Gonek
created_at: "2026-02-24T14:57:03Z"
last_modified_at: "2026-02-24T14:57:04Z"
last_modified_by: Robert Gonek
---
# SendGrid Integration

Luminary uses SendGrid for all transactional email delivery. Marketing emails are handled separately via HubSpot and are out of scope for this document.

**Account**: `platform@luminary.io` (owner), access via 1Password `Platform → SendGrid`.

## Purpose

| Email Type | Trigger | Volume |
| --- | --- | --- |
| Signup verification | New user registration | ~500/day |
| Password reset | User requests password reset | ~120/day |
| Weekly digest | Every Monday 08:00 UTC, per workspace | ~2,400/week |
| Usage alert | Workspace approaches plan event limit (80%, 100%) | Variable |
| Payment failure dunning | Stripe `invoice.payment_failed` (see [Stripe Integration](https://placeholder.invalid/page/integrations%2Fstripe-integration.md)) | ~30/day |
| Export ready notification | Export job completes (see [Worker Service](https://placeholder.invalid/page/services%2Fworker-service.md)) | ~200/day |
| Workspace invitation | User invited to a workspace | ~300/day |

## Template IDs

All transactional emails use SendGrid Dynamic Templates. Template IDs are stored in AWS Secrets Manager at `prod/sendgrid/templates` as a JSON object, and also listed here for reference.

| Template Name | Template ID | Triggered By | Owner |
| --- | --- | --- | --- |
| Signup Email Verification | `d-a1b2c3d4e5f6...` | Auth service (user registration) | Growth team |
| Password Reset | `d-b2c3d4e5f6a1...` | Auth service (reset request) | Growth team |
| Weekly Digest | `d-c3d4e5f6a1b2...` | Worker Service (NotificationDispatchJob) | Analytics team |
| Usage Alert: 80% | `d-d4e5f6a1b2c3...` | Worker Service (NotificationDispatchJob) | Platform team |
| Usage Alert: 100% | `d-e5f6a1b2c3d4...` | Worker Service (NotificationDispatchJob) | Platform team |
| Payment Failed (Attempt 1) | `d-f6a1b2c3d4e5...` | Billing service (Stripe webhook handler) | Platform team |
| Payment Failed (Final Warning) | `d-a1b2c3d4f5e6...` | Billing service (Stripe webhook handler) | Platform team |
| Export Ready | `d-b2c3d4f5e6a1...` | Worker Service (ExportJob) | Analytics team |
| Workspace Invitation | `d-c3d4f5e6a1b2...` | API service (invite endpoint) | Growth team |

Template IDs in production are different from staging. Staging template IDs are in AWS Secrets Manager at `staging/sendgrid/templates`.

## Previewing and Testing Templates

To preview a template with test data:

1. Log in to the SendGrid dashboard (credentials in 1Password)
2. Navigate to **Email API → Dynamic Templates**
3. Select the template and click **Preview**
4. Paste a sample JSON object representing the template's dynamic data

To send a test email from the command line (uses the staging API key):

```shell
curl -X POST https://api.sendgrid.com/v3/mail/send \
  -H "Authorization: Bearer $SENDGRID_STAGING_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "personalizations": [{
      "to": [{"email": "your-email@luminary.io"}],
      "dynamic_template_data": {
        "user_name": "Test User",
        "verification_url": "https://app.luminary.io/verify?token=test"
      }
    }],
    "from": {"email": "noreply@luminary.io", "name": "Luminary"},
    "template_id": "d-a1b2c3d4e5f6..."
  }'
```

## IP Warmup Status

Luminary uses a dedicated sending IP pool (`luminary-transactional`). The IP warmup was completed in October 2024. Current status: **Fully warmed**. Reputation score in SendGrid: 97/100.

If reputation drops below 90, investigate bounce rates and spam reports before sending further volume.

## Bounce Handling

Bounces are handled automatically by SendGrid. Hard bounces (invalid addresses) are added to the SendGrid suppression list and will not receive future emails. The billing service subscribes to the SendGrid Event Webhook to record bounce events:

- Hard bounce → mark user's `email_deliverable = false` in Postgres
- Spam report → unsubscribe user from all non-critical emails; log to `#alerts-email-health`

Bounce rate is monitored in Datadog via the SendGrid Event Webhook processor. Alert fires if the bounce rate for any email category exceeds 2% over a 24-hour window.

## Related

- [Stripe Integration](https://placeholder.invalid/page/integrations%2Fstripe-integration.md) — payment failure emails
- [Worker Service](https://placeholder.invalid/page/services%2Fworker-service.md) — export and notification dispatch
