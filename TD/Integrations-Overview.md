---
title: Integrations Overview
id: "5046486"
space: TD
version: 2
labels:
    - overview
    - integrations
author: Robert Gonek
created_at: "2026-02-24T14:56:59Z"
last_modified_at: "2026-02-24T14:57:00Z"
last_modified_by: Robert Gonek
---
# Integrations Overview

Luminary supports a growing set of integrations that connect your existing tools to the analytics pipeline — either to bring data in, push it out, or route notifications.

## Available Integrations

| Integration | Type | Status | Documentation |
| --- | --- | --- | --- |
| [Slack](https://placeholder.invalid/page/integrations%2Fslack-integration.md) | Notification | GA | [Slack Integration](https://placeholder.invalid/page/integrations%2Fslack-integration.md) |
| [Segment](https://placeholder.invalid/page/integrations%2Fsegment-integration.md) | Data Source | GA | [Segment Integration](https://placeholder.invalid/page/integrations%2Fsegment-integration.md) |
| [AWS S3 Export](AWS-S3-Export.md) | Data Destination | GA | [AWS S3 Export](AWS-S3-Export.md) |
| [Google Tag Manager](Google-Tag-Manager-Integration.md) | SDK Delivery | GA | [Google Tag Manager](Google-Tag-Manager-Integration.md) |
| Snowflake Export | Data Destination | Beta | Snowflake Export *(coming soon)* |
| HubSpot | Data Destination | Beta | HubSpot Integration *(coming soon)* |
| PagerDuty | Notification | Beta | PagerDuty Integration *(coming soon)* |
| Fivetran | Data Source | GA | Fivetran Integration *(coming soon)* |
| dbt Cloud | Data Source | Beta | dbt Cloud Integration *(coming soon)* |
| Zapier | Notification / Destination | GA | Zapier Integration *(coming soon)* |
| Google BigQuery Export | Data Destination | Beta | BigQuery Export *(coming soon)* |
| Intercom | Destination | GA | Intercom Integration *(coming soon)* |

## Integration Types

**Data Source** — sends event or entity data *into* Luminary, supplementing or replacing direct SDK instrumentation.

**Data Destination** — exports Luminary event or aggregated data *out* to an external system for storage, transformation, or activation.

**Notification** — routes Luminary alerts (threshold breaches, anomaly detections, scheduled reports) to an external channel.

**SDK Delivery** — provides an alternative mechanism to deploy the Luminary JavaScript SDK without modifying application code.

## Requesting a New Integration

To request a new integration or report a problem with an existing one, open a ticket in the `INTEGRATIONS` Jira project. GA integrations have a two-business-day SLA for critical bugs. Beta integrations are best-effort.
