---
title: Stripe Integration
id: "7078072"
space: TD
version: 2
labels:
    - integrations
    - stripe
    - billing
author: Robert Gonek
created_at: "2026-02-24T14:57:07Z"
last_modified_at: "2026-02-24T14:57:09Z"
last_modified_by: Robert Gonek
---
# Stripe Integration

This document covers how Luminary uses Stripe for subscription management and metered billing. This is internal engineering documentation — not a customer-facing guide.

Luminary's Stripe account is in the `Platform → Billing` 1Password vault. Only the billing service and the Worker Service interact with the Stripe API directly.

## How Luminary Uses Stripe

| Feature | Description |
| --- | --- |
| **Subscriptions** | Each Luminary workspace maps to a Stripe Customer and a Stripe Subscription. Plan changes are reflected as Subscription updates. |
| **Metered billing** | Event volume above the included plan limit is billed via Stripe Billing Meters. The Worker Service reports metered usage hourly (see [Worker Service: BillingMeteringJob](https://placeholder.invalid/page/services%2Fworker-service.md)). |
| **Invoices** | Stripe generates invoices automatically at the end of each billing period. Luminary does not generate invoices directly. |
| **Customer portal** | Enterprise customers with custom contracts are managed outside Stripe. SMB customers use the Stripe-hosted customer portal for plan management and payment method updates. |

## Webhook Event Handling

Luminary listens to Stripe webhook events at `POST /internal/webhooks/stripe` (internal endpoint, not exposed through the public API gateway). Webhook events are processed by the billing service.

All webhook endpoints verify the `Stripe-Signature` header using `stripe.ConstructEvent` before processing any payload.

| Event | Luminary Action |
| --- | --- |
| `customer.subscription.created` | Create workspace billing record; set initial plan limits |
| `customer.subscription.updated` | Update workspace plan and limits in Postgres; emit `subscription_updated` internal event |
| `customer.subscription.deleted` | Mark workspace as churned; begin grace period before suspension |
| `invoice.payment_succeeded` | Mark workspace billing period as paid; reset any suspension status |
| `invoice.payment_failed` | Increment payment failure count; trigger dunning email sequence via SendGrid (see [SendGrid Integration](SendGrid-Integration.md)); after 3 failures, suspend workspace |
| `invoice.finalized` | Store invoice ID in Postgres for customer support lookup |
| `customer.updated` | Sync billing contact email to workspace admin record |
| `payment_method.attached` | Clear any `payment_method_missing` flags on the workspace |

### Idempotency Handling

Stripe may deliver the same webhook event multiple times (at-least-once delivery). The billing service uses the Stripe event ID (`evt_...`) as an idempotency key:

```go
// billing-service/internal/stripe/handler.go
func (h *Handler) HandleEvent(ctx context.Context, event stripe.Event) error {
    if processed, err := h.store.IsEventProcessed(ctx, event.ID); err != nil {
        return err
    } else if processed {
        // Already handled — return nil (200 OK to Stripe, don't retry)
        return nil
    }

    if err := h.processEvent(ctx, event); err != nil {
        return err // Return error → Stripe will retry
    }

    return h.store.MarkEventProcessed(ctx, event.ID)
}
```

Processed event IDs are stored in the `stripe_events_processed` Postgres table with a 30-day TTL.

## Test Mode vs. Live Mode

Stripe has separate API keys for test mode and live mode. The environment determines which key is used:

| Environment | Stripe Mode | Key Source |
| --- | --- | --- |
| Local development | Test | `.env.local` (get from 1Password) |
| Staging | Test | AWS Secrets Manager `staging/stripe/api-key` |
| Production | Live | AWS Secrets Manager `prod/stripe/api-key` |

**Never use the live mode key in development or staging.** The `billing-service` startup checks that the key prefix matches the expected mode for the environment (`sk_test_` in staging, `sk_live_` in production) and panics if there is a mismatch.

## Triggering Test Webhook Events in Development

The Stripe CLI makes it easy to replay webhook events against a local or staging billing service:

```shell
# Install Stripe CLI
brew install stripe/stripe-cli/stripe

# Login (uses your Luminary Stripe test account credentials)
stripe login

# Forward webhooks to local billing service
stripe listen --forward-to http://localhost:8082/internal/webhooks/stripe

# In a separate terminal, trigger a specific event
stripe trigger invoice.payment_failed

# Trigger with custom fixture
stripe trigger customer.subscription.updated \
  --add subscription:items[0][price]=price_test_growth_monthly
```

To trigger a webhook in the staging environment:

```shell
# Trigger directly via Stripe API (staging test mode)
curl https://api.stripe.com/v1/test_helpers/... \
  -u sk_test_staging_key_here:
```

For more complex scenarios (e.g., simulating a subscription upgrade with metered billing), use the Stripe test dashboard at `dashboard.stripe.com` (test mode) to create fixtures directly.

## Related

- [Worker Service](https://placeholder.invalid/page/services%2Fworker-service.md) — BillingMeteringJob reports metered usage
- [SendGrid Integration](SendGrid-Integration.md) — email notifications on payment failure
- [Data Classification Policy](https://placeholder.invalid/page/security%2Fdata-classification.md) — Stripe customer IDs are Confidential
