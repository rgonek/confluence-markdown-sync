---
title: Segment Integration
id: "5800113"
space: TD
version: 2
labels:
    - data-source
    - integrations
    - segment
author: Robert Gonek
created_at: "2026-02-24T14:57:01Z"
last_modified_at: "2026-02-24T14:57:02Z"
last_modified_by: Robert Gonek
---
# Segment Integration (Luminary as Destination)

You can configure Luminary as a destination in Segment to forward your existing Segment event stream into Luminary without instrumenting the Luminary SDK directly. This is the recommended migration path for teams already running Segment.

## Table of Contents

- [Adding Luminary as a Destination](#adding-luminary-as-a-destination)
- [Field Mapping](#field-mapping)
- [Supported Event Types](#supported-event-types)
- [Configuration Options](#configuration-options)
- [Data Volume Considerations](#data-volume-considerations)

---

## Adding Luminary as a Destination

1. In the Segment app, go to **Connections → Destinations → Add Destination**.
2. Search for **Luminary** in the destination catalog.
3. Select the Segment source(s) you want to connect to Luminary.
4. In the destination settings, enter your **Luminary Write Key** (found in Luminary under **Settings → Workspace → API Keys → Write Keys**).
5. Optionally configure the **Host** override if you are routing through a proxy (see [Configuration Options](#configuration-options)).
6. Click **Save** and then **Enable Destination**.

Events will begin flowing within 2–3 minutes. Verify delivery in **Luminary → Settings → Integrations → Segment → Event Log**.

> The Luminary Segment destination is built on top of the Segment [Actions framework](https://segment.com/docs/connections/destinations/actions/). Legacy destination mode (non-Actions) is not supported.

---

## Field Mapping

Segment uses a spec-based model where certain fields are standardized across all events. The table below shows how Segment fields map to Luminary event fields.

### Top-Level Fields

| Segment Field | Luminary Field | Notes |
| --- | --- | --- |
| `messageId` | `messageId` | Direct pass-through. Used for idempotency. |
| `type` | `type` | `track`, `identify`, `group`, `page`, `screen`. |
| `userId` | `userId` | Direct pass-through. |
| `anonymousId` | `anonymousId` | Direct pass-through. |
| `timestamp` | `timestamp` | Direct pass-through (ISO 8601). |
| `sentAt` | `sentAt` | Direct pass-through. |
| `context` | `context` | Merged; see context mapping below. |
| `integrations` | *(not forwarded)* | Segment integration routing metadata. Not included in Luminary payload. |

### Context Fields

| Segment Context Field | Luminary Context Field | Notes |
| --- | --- | --- |
| `context.app` | `context.app` | Direct pass-through. |
| `context.campaign` | `context.campaign` | Direct pass-through. |
| `context.device` | `context.device` | Direct pass-through. |
| `context.ip` | `context.ip` | Used for geolocation in the Ingestion Service. |
| `context.library` | `context.library` | Overridden: Luminary sets `name: "segment-destination"` and `version: <destination version>` to indicate the forwarding path. |
| `context.locale` | `context.locale` | Direct pass-through. |
| `context.os` | `context.os` | Direct pass-through. |
| `context.page` | `context.page` | Direct pass-through. |
| `context.screen` | `context.screen` | Direct pass-through. |
| `context.timezone` | `context.timezone` | Direct pass-through. |
| `context.userAgent` | `context.userAgent` | Direct pass-through. |
| `context.traits` | *(merged into traits)* | For `identify` calls, `context.traits` is merged into the top-level `traits` object. |

### Event-Type-Specific Mapping

#### `track`

| Segment Field | Luminary Field |
| --- | --- |
| `event` | `event` |
| `properties` | `properties` |

#### `identify`

| Segment Field | Luminary Field |
| --- | --- |
| `traits` | `traits` |

#### `group`

| Segment Field | Luminary Field |
| --- | --- |
| `groupId` | `groupId` |
| `traits` | `traits` |

#### `page`

| Segment Field | Luminary Field |
| --- | --- |
| `name` | `name` |
| `properties` | `properties` |
| `properties.url` | `properties.url` |
| `properties.path` | `properties.path` |
| `properties.referrer` | `properties.referrer` |
| `properties.title` | `properties.title` |
| `properties.search` | `properties.search` |

#### `screen`

Luminary does not have a native `screen` event type. `screen` events from Segment are forwarded as `track` events with event name `Screen Viewed` and the screen name as `properties.screen_name`. The original `context.screen` object is preserved.

---

## Supported Event Types

| Segment Event Type | Supported | Notes |
| --- | --- | --- |
| `track` | Yes | Full support. |
| `identify` | Yes | Full support. |
| `group` | Yes | Full support. |
| `page` | Yes | Full support. |
| `screen` | Yes (remapped) | Forwarded as `track` with event name `Screen Viewed`. |
| `alias` | No | Luminary does not support user aliasing via the destination. Use the Luminary API directly for identity merges. |

---

## Configuration Options

These options are set in the Segment destination settings UI.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| **Write Key** | `string` | — | Required. Your Luminary workspace write key. |
| **Host** | `string` | `https://ingest.luminaryapp.io` | Override the ingest endpoint. Use this if you are proxying Luminary events through your own domain for ad blocker bypass. |
| **Batch Size** | `int` | `100` | Number of events per batch sent to Luminary. Segment batches events internally before forwarding. |
| **Enable Compression** | `bool` | `true` | Compress batch payloads with gzip before sending. |
| **Forward Screen as Track** | `bool` | `true` | Convert `screen` events to `track` events as described above. Disable only if you have a custom schema that uses `screen` natively (not recommended). |

---

## Data Volume Considerations

Segment charges per event on the source side, and Luminary charges per event on the destination side. Forwarding all events from Segment to Luminary means you pay for each event twice. Consider the following strategies to reduce volume:

**Event filtering in Segment:** Use Segment's Destination Filters to exclude low-value events (e.g., `Heartbeat Ping`, `Scroll Depth`) from being forwarded to Luminary.

**Sampling:** For extremely high-volume `track` events (e.g., every mouse move or video playback progress), add a sampling middleware to the Segment source that forwards only a fraction of events.

**Schema enforcement:** Enable schema enforcement in Luminary to reject unexpected event types early in the pipeline rather than storing and processing them.

**Separate write keys per environment:** Use different write keys for production and staging/development environments. Do not route staging events through the production Luminary workspace; they inflate metrics and dashboards.

Once event volume from the Segment destination exceeds **500 million events/month**, contact Luminary support to discuss dedicated ingest capacity and volume pricing. The default shared ingest tier enforces a soft rate limit of 5,000 events/second per workspace.
