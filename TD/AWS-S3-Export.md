---
title: AWS S3 Export
id: "7536671"
space: TD
version: 2
labels:
    - integrations
    - aws
    - s3
    - export
    - data-destination
author: Robert Gonek
created_at: "2026-02-24T14:56:54Z"
last_modified_at: "2026-02-24T14:56:56Z"
last_modified_by: Robert Gonek
---
# AWS S3 Export

The AWS S3 export integration writes Luminary event data to your own S3 bucket on a continuous basis. Data is exported in Parquet format, partitioned by workspace, date, and hour, making it directly queryable with Athena, Spark, or any tool that reads Parquet from S3.

## Table of Contents

- [IAM Setup](#iam-setup)
- [Configuring the Export in Luminary](#configuring-the-export-in-luminary)
- [Export Format](#export-format)
- [Partitioning Scheme](#partitioning-scheme)
- [Querying with Athena](#querying-with-athena)

---

## IAM Setup

Luminary uses an IAM Role with cross-account trust to write to your S3 bucket. This is more secure than providing long-lived access keys — Luminary's export worker assumes your role using STS and rotates the temporary credentials automatically.

### Step 1: Create the IAM Role

Create an IAM role in your AWS account with the following trust policy. Replace `LUMINARY_AWS_ACCOUNT_ID` with the value shown in **Settings → Integrations → S3 Export → Setup Guide**.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::LUMINARY_AWS_ACCOUNT_ID:role/luminary-s3-exporter"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "YOUR_LUMINARY_WORKSPACE_ID"
        }
      }
    }
  ]
}
```

The `sts:ExternalId` condition is required. It binds the role to your specific Luminary workspace, preventing the [confused deputy problem](https://docs.aws.amazon.com/IAM/latest/UserGuide/confused-deputy.html).

### Step 2: Attach the Least-Privilege Policy

Attach the following inline policy to the role. Replace `YOUR_BUCKET_NAME` and `YOUR_PREFIX` with your values. If you do not use a prefix, omit it and use `YOUR_BUCKET_NAME/*` for the object ARN.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowParquetWrites",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:PutObjectAcl"
      ],
      "Resource": "arn:aws:s3:::YOUR_BUCKET_NAME/YOUR_PREFIX/*"
    },
    {
      "Sid": "AllowListBucket",
      "Action": [
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Effect": "Allow",
      "Resource": "arn:aws:s3:::YOUR_BUCKET_NAME",
      "Condition": {
        "StringLike": {
          "s3:prefix": ["YOUR_PREFIX/*"]
        }
      }
    },
    {
      "Sid": "AllowDeleteOwnObjects",
      "Effect": "Allow",
      "Action": "s3:DeleteObject",
      "Resource": "arn:aws:s3:::YOUR_BUCKET_NAME/YOUR_PREFIX/*",
      "Comment": "Required for atomic part-file replacement during compaction"
    }
  ]
}
```

> **Do not grant** `s3:*` **or** `s3:DeleteBucket`**.** The policy above is sufficient for all export operations. If Luminary support asks you to broaden permissions, treat it as a potential social engineering attempt and contact your security team.

### Step 3: Note the Role ARN

Copy the Role ARN (format: `arn:aws:iam::YOUR_ACCOUNT_ID:role/YOUR_ROLE_NAME`). You will need it in the next step.

---

## Configuring the Export in Luminary

1. In Luminary, go to **Settings → Integrations → S3 Export → New Export**.
2. Enter:

   - **S3 Bucket**: your bucket name (without `s3://` prefix).
   - **Prefix**: optional path prefix within the bucket (e.g., `luminary-exports/`).
   - **AWS Region**: the region your bucket is in.
   - **IAM Role ARN**: the role ARN from Step 3 above.
3. Click **Test Connection**. Luminary will attempt to assume the role and write a small test object (`<prefix>/.luminary-test`). If the test fails, the UI will show the specific IAM error.
4. Configure **Event Types to Export**: choose `All event types` or select specific types (`track`, `identify`, `group`, `page`).
5. Configure **Export Delay**: events are exported with a delay of 1 hour by default to allow late-arriving events from SDK offline queues to be included. The maximum configurable delay is 24 hours.
6. Click **Save and Enable**.

The first export run will start within 5 minutes and will include all historical data unless you configure a **Start Date** to limit the backfill.

---

## Export Format

Data is exported as **Apache Parquet** files. Parquet was chosen over CSV/JSON because it is column-oriented (efficient for analytics queries that touch a subset of columns), self-describing (schema is embedded), and significantly smaller on disk than equivalent gzip-compressed JSON.

### Schema

The Parquet files have the following column schema:

| Column | Parquet Type | Nullable | Description |
| --- | --- | --- | --- |
| `message_id` | `BYTE_ARRAY (STRING)` | No | Client-generated unique event ID. |
| `type` | `BYTE_ARRAY (STRING)` | No | Event type: `track`, `identify`, `group`, `page`. |
| `event` | `BYTE_ARRAY (STRING)` | Yes | Event name (for `track` events). Null for `identify`, `group`, `page`. |
| `user_id` | `BYTE_ARRAY (STRING)` | Yes | User ID. Null for anonymous events. |
| `anonymous_id` | `BYTE_ARRAY (STRING)` | Yes | Anonymous ID. |
| `workspace_id` | `BYTE_ARRAY (STRING)` | No | Luminary workspace ID. |
| `timestamp` | `INT64 (TIMESTAMP_MICROS)` | No | Client event timestamp in microseconds since epoch. |
| `received_at` | `INT64 (TIMESTAMP_MICROS)` | No | Server receipt timestamp. |
| `properties` | `BYTE_ARRAY (STRING)` | Yes | JSON-serialized properties object. |
| `traits` | `BYTE_ARRAY (STRING)` | Yes | JSON-serialized traits (for `identify`/`group`). |
| `context_ip` | `BYTE_ARRAY (STRING)` | Yes | Client IP address. |
| `context_user_agent` | `BYTE_ARRAY (STRING)` | Yes | User agent string. |
| `context_page_url` | `BYTE_ARRAY (STRING)` | Yes | Page URL at time of event. |
| `context_page_path` | `BYTE_ARRAY (STRING)` | Yes | Page path. |
| `context_page_referrer` | `BYTE_ARRAY (STRING)` | Yes | Referring URL. |
| `geo_country` | `BYTE_ARRAY (STRING)` | Yes | ISO 3166-1 alpha-2 country code resolved from IP. |
| `geo_region` | `BYTE_ARRAY (STRING)` | Yes | Region/state name. |
| `geo_city` | `BYTE_ARRAY (STRING)` | Yes | City name. |
| `library_name` | `BYTE_ARRAY (STRING)` | Yes | SDK name (e.g., `@luminary/analytics`). |
| `library_version` | `BYTE_ARRAY (STRING)` | Yes | SDK version string. |

`properties` and `traits` are stored as JSON strings rather than nested Parquet structs. This avoids schema evolution problems when new properties are added to events — the column schema stays stable. Use JSON functions in your query engine (`JSON_EXTRACT_SCALAR` in Athena, `json_value()` in BigQuery) to access individual property values.

---

## Partitioning Scheme

Files are written to S3 using Hive-compatible partition paths:

```
<prefix>/
  workspace_id=ws_889f/
    date=2025-01-14/
      hour=12/
        events-2025-01-14T12-00-00Z-part-0001.parquet
        events-2025-01-14T12-00-00Z-part-0002.parquet
      hour=13/
        events-2025-01-14T13-00-00Z-part-0001.parquet
```

Each hourly partition contains all events whose `received_at` timestamp falls within that hour. The export delay setting shifts which wall-clock hour an event is written to — an event received at 12:45 with a 1-hour export delay will appear in the `hour=13` partition.

Part files within a partition are typically 64–256 MB each. Partitions with very high event volume may produce multiple part files; low-volume partitions produce a single small file.

---

## Querying with Athena

### Create the External Table

Run this DDL in the Athena query editor once to register the table. Replace `YOUR_BUCKET` and `YOUR_PREFIX`:

```sql
CREATE EXTERNAL TABLE luminary_events (
  message_id     STRING,
  type           STRING,
  event          STRING,
  user_id        STRING,
  anonymous_id   STRING,
  workspace_id   STRING,
  timestamp      TIMESTAMP,
  received_at    TIMESTAMP,
  properties     STRING,
  traits         STRING,
  context_ip     STRING,
  context_user_agent STRING,
  context_page_url   STRING,
  context_page_path  STRING,
  context_page_referrer STRING,
  geo_country    STRING,
  geo_region     STRING,
  geo_city       STRING,
  library_name   STRING,
  library_version STRING
)
PARTITIONED BY (
  workspace_id_part STRING,
  date              STRING,
  hour              STRING
)
STORED AS PARQUET
LOCATION 's3://YOUR_BUCKET/YOUR_PREFIX/'
TBLPROPERTIES ('parquet.compress' = 'SNAPPY');
```

After creating the table, run `MSCK REPAIR TABLE luminary_events` to discover existing partitions, or add partitions manually:

```sql
ALTER TABLE luminary_events ADD PARTITION (
  workspace_id_part = 'ws_889f',
  date = '2025-01-14',
  hour = '12'
) LOCATION 's3://YOUR_BUCKET/YOUR_PREFIX/workspace_id=ws_889f/date=2025-01-14/hour=12/';
```

### Example Queries

**Count events by type for the last 7 days:**

```sql
SELECT
  type,
  COUNT(*) AS event_count
FROM luminary_events
WHERE date BETWEEN '2025-01-08' AND '2025-01-14'
  AND workspace_id_part = 'ws_889f'
GROUP BY type
ORDER BY event_count DESC;
```

**Extract a specific property from track events:**

```sql
SELECT
  user_id,
  JSON_EXTRACT_SCALAR(properties, '$.revenue') AS revenue,
  timestamp
FROM luminary_events
WHERE date = '2025-01-14'
  AND workspace_id_part = 'ws_889f'
  AND type = 'track'
  AND event = 'Order Placed'
  AND JSON_EXTRACT_SCALAR(properties, '$.revenue') IS NOT NULL
ORDER BY timestamp DESC
LIMIT 100;
```

**Daily unique users:**

```sql
SELECT
  date,
  COUNT(DISTINCT user_id) AS daily_active_users
FROM luminary_events
WHERE date BETWEEN '2025-01-01' AND '2025-01-14'
  AND workspace_id_part = 'ws_889f'
  AND type = 'track'
  AND user_id IS NOT NULL
GROUP BY date
ORDER BY date;
```

> Always include the `workspace_id_part` and `date` predicates in your queries to use partition pruning. Queries without date filters will scan all partitions and can be expensive.
