---
title: Onboarding to On-Call
id: "7307360"
space: TD
version: 2
labels:
    - developer-guide
    - on-call
    - operations
    - onboarding
author: Robert Gonek
created_at: "2026-02-24T14:56:19Z"
last_modified_at: "2026-02-24T14:56:21Z"
last_modified_by: Robert Gonek
---
# Onboarding to On-Call

This guide is for engineers joining the production on-call rotation for the first time. On-call at Luminary means you are the first responder for production incidents during your rotation week. This document covers what you need before you're added to the schedule, what the shadowing period looks like, how to set up your tools, and what to do when your first page comes in.

If you feel underprepared at any point, say so. Asking for help during an incident is not a failure — it is the right call.

## Prerequisites

Before you are added to the primary on-call rotation, you must have:

1. **Completed engineering onboarding** — you have a working local development environment, you understand the system architecture at a high level, and you've read the incident response playbook in the SD space.
2. **Shipped at least one change to production** — you've gone through the full deploy pipeline and understand how deployments work, how to roll back, and where to find deployment status.
3. **Attended at least two post-incident reviews** — you understand how we think about incidents, what blameless looks like in practice, and what kinds of things go wrong.

If you haven't met these, talk to your engineering manager. Getting these done first makes the on-call experience significantly less stressful.

## Shadowing Period

You will shadow the on-call engineer for **two full sprint cycles** (approximately four weeks) before taking primary. During this time:

- You are added to PagerDuty as the **secondary on-call** responder. You receive all the same pages as the primary.
- You join every incident bridge the primary opens, but the primary leads the response. Your job is to observe, ask questions in the thread (not during the call), and understand the patterns.
- After each incident (paged or not), read the timeline in the incident channel and understand what happened.
- If you spot something the primary missed or a runbook that's out of date, mention it after the incident is resolved — not during.

At the end of the shadowing period, your engineering manager will check in with the outgoing primary to confirm you're ready. This is not a formal test — it's a conversation.

## Tools Setup

Set these up before your first shadow week, not during an incident at 2am.

### PagerDuty

1. Install the PagerDuty mobile app (iOS or Android) and enable high-urgency push notifications. Do not rely on SMS alone.
2. Set your notification rules: immediate push + phone call for high urgency. High-urgency pages require acknowledgement within 5 minutes or they escalate.
3. Verify you can see the `Luminary-Production` service and the current on-call schedule.
4. Test the notification by asking the current primary to trigger a test incident and assign it to you.

### VPN

You need VPN access to reach internal dashboards and Kubernetes clusters. If your VPN client isn't installed or your cert has expired, fix this now:

```shell
# Check VPN connectivity
ping internal.luminary.io

# If unreachable, reconnect via Tailscale
tailscale up
```

### kubectl Contexts

You need access to the production Kubernetes cluster. Context should be set up during general onboarding, but confirm:

```shell
# List available contexts
kubectl config get-contexts

# Switch to production
kubectl config use-context luminary-prod-us-east-1

# Confirm access
kubectl get pods -n production
```

If you get an access denied error, your IAM role may not be in the `eks:prod-read-only` group. File an access request in the `#platform-access` Slack channel.

### Bookmarked Dashboards

Bookmark these in your browser before you need them:

| Dashboard | URL | When to Use |
| --- | --- | --- |
| Production overview | `https://app.datadoghq.com/dashboard/luminary-prod` | Starting point for any incident |
| API service metrics | `https://app.datadoghq.com/dashboard/api-service` | API errors, latency |
| Query service metrics | `https://app.datadoghq.com/dashboard/query-service` | Slow queries, ClickHouse health |
| Ingestion pipeline | `https://app.datadoghq.com/dashboard/ingestion` | Kafka lag, event throughput |
| Auth service metrics | `https://app.datadoghq.com/dashboard/auth-service` | Token errors, session issues |
| AWS console (prod) | `https://console.aws.amazon.com` | RDS, ElastiCache, MSK status |
| ArgoCD | `https://argocd.internal.luminary.io` | Deployment status, sync health |

All of these require either VPN or an active browser session authenticated with your Luminary SSO credentials.

## Your First Page

Your first real page will happen. Here's what to do:

**1. Acknowledge within 5 minutes.** Open PagerDuty and acknowledge the incident. This stops the escalation timer. You don't need to have a plan yet — just acknowledge.

**2. Open an incident channel.** Go to Slack and run `/incident open <brief description>`. This creates a dedicated channel (e.g. `#incident-20260224-api-errors`) and notifies the on-call secondary (which was you during shadowing; now it's someone else).

**3. Look before you act.** Resist the urge to immediately restart things or roll back deployments. Spend the first 3–5 minutes understanding what's actually happening:

- Check the production overview dashboard — what's red?
- Check ArgoCD — was there a recent deployment?
- Check Datadog APM — which service is generating errors?
- Check the relevant runbook for that service

**4. Communicate often, even without updates.** Post in the incident channel every 5–10 minutes. "Still investigating, looking at ClickHouse query latency" is a useful update. Silence is not.

**5. Escalate early rather than late.** If you've been investigating for 10 minutes and you're not converging on a cause, page your secondary or post in `#on-call-help`. There is no shame in this. The cost of a slow recovery is much higher than the social cost of asking for help.

**6. Document as you go.** Keep a running log in the incident channel: what you checked, what you found, what you tried. This becomes the incident timeline for the post-incident review.

## Common False Alarms

Not every page is a real incident. These are the most common false alarms and how to handle them:

### Synthetic Monitor Flap (single location failure)

**Symptom**: PagerDuty page from `[Datadog] Public API Health Check - us-east-1 ALERT`. The EU and AP monitors are still green.

**Cause**: Transient network issue at one Datadog synthetic location.

**Action**: Check the Datadog synthetic monitor — if only one location failed and the re-check passed, this is a false alarm. Resolve the PagerDuty incident with note "Single-location synthetic flap, no action required." If all three locations fail, this is real.

### High p99 Latency Spike (30-60 seconds)

**Symptom**: Query service p99 latency alert fires, then recovers before you acknowledge.

**Cause**: Often a single expensive query from a large workspace hitting the ClickHouse cluster. The query completes and the metric normalizes.

**Action**: Check ClickHouse query logs for the spike window. If no single query was responsible and latency remains elevated, follow the [Query Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-query-service.md). If it recovered cleanly, resolve with a note and check if the workspace needs query optimization.

### Worker Queue Depth Spike

**Symptom**: `Worker queue depth > 500` alert fires during 9am–10am UTC.

**Cause**: Morning usage surge — workspaces running scheduled reports at 9am UTC causes a predictable queue spike that drains within 15 minutes.

**Action**: Check the queue depth trend in Datadog. If it's draining, it's the morning spike. If queue depth is growing or stagnant for >20 minutes, follow the [Worker Service](https://placeholder.invalid/page/services%2Fworker-service.md) runbook.

### Silence Procedure

For confirmed false alarms, don't just close the incident — silence the monitor for 30 minutes to prevent re-trigger while you verify, then un-silence. Never silence indefinitely without filing a ticket to investigate the root cause of the false alarm.

## Escalation Is Not Failure

At Luminary, escalating during an incident is the right thing to do. The on-call engineer is not expected to be omniscient. You are expected to:

- Acknowledge quickly
- Investigate methodically
- Communicate clearly
- Ask for help when you need it

The people you can call during an incident:

- **Secondary on-call**: Always available. Page them if you need another brain, even before the formal escalation timer fires.
- **Service owners**: In `#on-call-help`, ping the relevant team. At night, be proportionate — wake people up for customer-facing outages, not single-service flaps.
- **Engineering manager**: For customer impact lasting >30 minutes, for anything requiring production database changes, or if you're completely stuck.

After your first incident, your manager will debrief with you. This is not a review of your performance — it's a chance to capture what you learned and what could make the runbooks better.

## Related

- [Writing Runbooks](https://placeholder.invalid/page/developer-guide%2Fwriting-runbooks.md) — how to improve the runbooks you use
- [Auth Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-auth-service.md)
- [Query Service Runbook](https://placeholder.invalid/page/operations%2Frunbook-query-service.md)
- [Monitoring Stack](https://placeholder.invalid/page/infrastructure%2Fmonitoring-stack.md) — understanding your dashboards
- Incident Response Playbook (SD space)
