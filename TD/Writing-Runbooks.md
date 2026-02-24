---
title: Writing Runbooks
id: "6455521"
space: TD
version: 2
labels:
    - developer-guide
    - runbooks
    - operations
author: Robert Gonek
created_at: "2026-02-24T14:56:28Z"
last_modified_at: "2026-02-24T14:56:30Z"
last_modified_by: Robert Gonek
---
# Writing Runbooks

A runbook is a documented procedure for handling a specific operational scenario. Good runbooks reduce incident duration and reduce the cognitive load on the on-call engineer, especially at 2am.

## When Is a Runbook Needed?

Write a runbook when:

- A failure mode has occurred (or is likely to occur) more than once
- The diagnostic or remediation steps aren't obvious from the alert or dashboard alone
- The fix requires specific commands, credentials, or context that an on-call engineer might not have memorized
- The scenario involves elevated risk (data operations, infra changes under load)

You do **not** need a runbook for every alert. A runbook for "disk usage > 80%" that just says "add more disk" is noise, not signal.

## Runbook Template

All runbooks should live in [operations/](https://placeholder.invalid/page/operations) and follow this structure:

```markdown
# Runbook: <Alert Name or Failure Scenario>

**Service**: <service name>
**Alert**: <exact Datadog/PagerDuty alert title, if applicable>
**Severity**: Critical / High / Medium

## Healthy State

What does normal look like? What metric values and behaviors indicate
the service is working correctly? This section lets the responder
quickly confirm whether they're looking at an actual problem.

## Symptoms

What the engineer will observe: error messages, metrics out of range,
customer impact description.

## Diagnostic Steps

Numbered, specific steps. Include exact commands. Don't say
"check the logs" — say:

    kubectl logs -n production deploy/auth-service --since=5m | grep ERROR

## Remediation

Numbered steps to resolve the issue. If there are multiple resolution
paths depending on root cause, label each clearly.

## Escalation

When to escalate, and who to escalate to.

## Post-Incident

What to check after the issue is resolved to confirm recovery.
Any cleanup steps.
```

## Good vs Bad Runbooks

**Bad:**

> Check if the auth service is having issues. If there are a lot of errors, it might be a database problem or a Redis problem. Look at the logs and see if anything stands out. If it's bad, consider restarting the pods or rolling back the deployment.

This is useless during an incident. It requires background knowledge the on-call may not have, gives no specific commands, and provides no decision criteria.

**Good:**

> **Step 1**: Check token rejection rate in Datadog: `sum:luminary.auth.token.rejected{*}.as_rate()`. If > 50/sec, proceed to Step 2.
> 
> **Step 2**: Check for recent deployments: `kubectl rollout history deployment/auth-service -n production`. If a deploy occurred in the last 30 minutes, proceed to Step 3a. Otherwise, proceed to Step 3b.
> 
> **Step 3a (post-deploy issue)**: Roll back the deployment: `kubectl rollout undo deployment/auth-service -n production`. Monitor for 5 minutes.

This is actionable. The on-call engineer can follow it at 2am without prior context.

## Keeping Runbooks Up to Date

A stale runbook is worse than no runbook — it sends the on-call in the wrong direction.

**Rule: update the runbook after every incident that uses it.** This is part of the post-incident review checklist. If the runbook was inaccurate, incomplete, or the fix was different from what it described, update it before closing the incident.

Runbooks are code. They belong in version control (this Confluence space is synced from Git), and changes should be reviewed by the service owner.

If a runbook hasn't been verified in use for more than 6 months, add a notice at the top:

```
> **Stale Warning**: This runbook has not been used or reviewed since <date>.
> Verify steps are still accurate before following.
```

## Related

- [Operations runbooks](https://placeholder.invalid/page/operations)
- [Onboarding to On-Call](Onboarding-to-On-Call.md)
- [Post-Incident Review Template](https://placeholder.invalid/page/operations%2Fpost-incident-review-template.md)
