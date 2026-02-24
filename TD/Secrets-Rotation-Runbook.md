---
title: Secrets Rotation Runbook
id: "5111974"
space: TD
version: 2
labels:
    - infrastructure
    - security
    - runbook
    - secrets
author: Robert Gonek
created_at: "2026-02-24T14:56:50Z"
last_modified_at: "2026-02-24T14:56:51Z"
last_modified_by: Robert Gonek
---
# Secrets Rotation Runbook

This runbook documents the procedure for rotating every category of secret and credential in Luminary's production environment. Follow these procedures exactly — improvising secret rotation has historically caused outages.

**Access required**: `engineering-ops` IAM group, Vault `admin` policy, production Kubernetes access.\
**Emergency contact**: If rotation causes an outage, page the on-call infra engineer immediately and do not attempt to fix it alone.

---

## Secret Inventory

| Secret | Storage | Rotation Frequency | Automated? |
| --- | --- | --- | --- |
| Postgres `app_user` password | Vault (dynamic credential) | Every 24 hours | Yes (Vault + RDS) |
| Postgres `readonly_user` password | Vault (dynamic credential) | Every 24 hours | Yes |
| Postgres `admin_user` password | Vault (static secret) | Quarterly | Manual |
| Redis password | AWS Secrets Manager | Quarterly | Semi-automated |
| ClickHouse `query_service` user password | Vault (static secret) | Quarterly | Manual |
| Stripe API keys (live) | Vault | Annually or on compromise | Manual |
| SendGrid API key | Vault | Annually or on compromise | Manual |
| Datadog API key | Vault | Annually | Manual |
| JWT signing key (ECDSA P-256) | Vault (transit engine) | Every 6 months | Zero-downtime procedure |
| Internal service-to-service tokens | Vault (AppRole) | Every 90 days | Automated (Vault TTL) |
| GitHub Actions OIDC | AWS IAM (trust policy) | N/A (OIDC; no static key) | N/A |
| ArgoCD admin password | Vault | Quarterly | Manual |

---

## Postgres Dynamic Credentials (Automated)

Postgres credentials for `app_user` and `readonly_user` are managed by Vault's database secrets engine. Vault creates short-lived credentials on demand and rotates them automatically.

**How it works**: Each application pod requests a fresh Postgres credential from Vault at startup via the Vault Agent injector. The credential is valid for 24 hours and is renewed automatically by the Vault Agent while the pod is running. When a pod is terminated, the lease is revoked and the credential is immediately invalidated.

**No manual action is needed for routine rotation** — Vault handles it automatically.

**To verify the integration is healthy:**

```shell
# Check Vault database secret engine status
vault read database/config/luminary-postgres

# List active leases for the app_user role
vault list sys/leases/lookup/database/creds/app-user-role

# Test credential generation
vault read database/creds/app-user-role
```

**If credentials stop rotating** (e.g., Vault-to-RDS connectivity issue):

1. Check Vault audit logs: `vault audit list` and inspect CloudWatch Logs group `/vault/audit`
2. Verify the Vault IAM role can reach the RDS endpoint: test from a pod in the isolated subnet
3. Re-initialize the connection if needed: `vault write -f database/config/luminary-postgres/rotate-root`

---

## Redis Password Rotation

Redis password rotation is semi-automated. ElastiCache does not support Vault dynamic credentials natively, so the procedure is:

1. **Generate a new password** (minimum 32 characters, alphanumeric):

   ```shell
   openssl rand -base64 40 | tr -d '+/=' | head -c 48
   ```
2. **Add the new password to ElastiCache** (ElastiCache supports dual-password during rotation):

   ```shell
   aws elasticache modify-replication-group \
     --replication-group-id luminary-redis-prod \
     --auth-token "NEW_PASSWORD_HERE" \
     --auth-token-update-strategy SET \
     --apply-immediately
   ```

   This adds the new password alongside the old one. Both are valid during the transition window.
3. **Update the secret in Vault:**

   ```shell
   vault kv put secret/production/redis password="NEW_PASSWORD_HERE"
   ```
4. **Restart application pods** to pick up the new Vault secret:

   ```shell
   kubectl rollout restart deployment -n production -l app.kubernetes.io/part-of=luminary
   # Wait for rollout to complete
   kubectl rollout status deployment/api-service -n production
   kubectl rollout status deployment/stream-processor -n production
   ```
5. **Remove the old password** from ElastiCache after all pods are confirmed healthy (wait at least 10 minutes):

   ```shell
   aws elasticache modify-replication-group \
     --replication-group-id luminary-redis-prod \
     --auth-token "NEW_PASSWORD_HERE" \
     --auth-token-update-strategy DELETE \
     --apply-immediately
   ```
6. **Verify** no connection errors in Datadog for 10 minutes post-cutover.

---

## Third-Party API Keys (Stripe, SendGrid, Datadog)

### Stripe Live API Key

> Do not confuse live keys (`sk_live_...`) with test keys. Stripe test keys are not in Vault — they are in the `.env.test` file.

1. Log into the Stripe Dashboard as an admin user
2. Navigate to **Developers → API keys**
3. Click **Create restricted key** (use restricted key scopes, not the secret key)
4. Configure scopes: for the Billing Service, required scopes are: `charges:read`, `customers:read/write`, `subscriptions:read/write`, `payment_methods:read`, `webhooks:read`
5. Copy the new key (shown once)
6. Update Vault: `vault kv put secret/production/stripe api_key="sk_live_NEW_KEY"`
7. Restart the Billing Service pods: `kubectl rollout restart deployment/billing-service -n production`
8. Monitor billing operations for 30 minutes:

   - Check Stripe Dashboard webhook delivery success rate
   - Check Datadog for `billing.stripe_api_error` metric
9. Revoke the old Stripe key from the Stripe Dashboard only after confirming the new key is working

### SendGrid API Key

1. Log into SendGrid as admin
2. Navigate to **Settings → API Keys → Create API Key**
3. Set permissions: **Mail Send** only (no other permissions needed for the notification service)
4. Copy the new key
5. `vault kv put secret/production/sendgrid api_key="SG.NEW_KEY"`
6. `kubectl rollout restart deployment/notification-service -n production`
7. Send a test email via the internal `/admin/send-test-email` endpoint
8. Revoke old key from SendGrid

### Datadog API Key

Datadog key rotation affects metrics, logs, and traces from all services simultaneously. Schedule this during low-traffic hours.

1. In Datadog, navigate to **Organization Settings → API Keys → New Key**
2. `vault kv put secret/production/datadog api_key="NEW_KEY"`
3. **Rolling restart all pods** (do not restart all at once — stagger to maintain observability):

   ```shell
   for deploy in $(kubectl get deployments -n production -o name); do
     kubectl rollout restart $deploy -n production
     kubectl rollout status $deploy -n production --timeout=120s
     sleep 30
   done
   ```
4. Verify metrics are flowing in Datadog within 5 minutes of each pod restart
5. Revoke old key after all pods are confirmed healthy

---

## JWT Signing Key (Zero-Downtime Rotation)

JWT signing key rotation is the most sensitive procedure. A naive rotation (replace old key → restart auth service) would immediately invalidate all active user sessions, logging out every user.

The correct procedure uses a **dual-validation window**: the Auth Service accepts tokens signed by either the current or the previous key for a configurable window (default: 2 hours — matches max JWT expiry).

### Procedure

1. **Generate a new ECDSA P-256 key** in Vault Transit:

   ```shell
   # Rotate the key version in Vault (does not activate it yet)
   vault write -f transit/keys/jwt-signing/rotate

   # Check current key versions
   vault read transit/keys/jwt-signing
   # Note the new 'latest_version' and existing 'min_decryption_version'
   ```
2. **Update Auth Service configuration** to enable dual-validation:

   ```shell
   # Set the new key version as the signing key and keep old version for verification
   kubectl set env deployment/auth-service -n production \
     JWT_SIGNING_KEY_VERSION="NEW_VERSION" \
     JWT_VERIFY_PREVIOUS_VERSION="true" \
     JWT_PREVIOUS_VERSION_SUNSET="$(date -d '+2 hours' --utc +%s)"

   kubectl rollout status deployment/auth-service -n production
   ```

   After this deploy, new tokens are signed with the new key. Old tokens (signed with the previous key) are still validated.
3. **Wait for the sunset period** (2 hours). During this window, users' existing sessions remain valid.
4. **Disable the old key version** for verification:

   ```shell
   kubectl set env deployment/auth-service -n production \
     JWT_VERIFY_PREVIOUS_VERSION="false"

   kubectl rollout restart deployment/auth-service -n production
   kubectl rollout status deployment/auth-service -n production
   ```
5. **Update Vault Transit min decryption version** to prevent future use of old key material:

   ```shell
   vault write transit/keys/jwt-signing/config \
     min_decryption_version=NEW_VERSION \
     min_encryption_version=NEW_VERSION
   ```
6. **Verify**: Log out and log back in to confirm new tokens work correctly. Check Datadog for `auth.jwt_validation_error` metric — it should remain at zero throughout.

---

## Internal Service-to-Service Tokens (AppRole)

Service-to-service authentication uses Vault AppRole. Each service has a `role_id` (static, committed to the Helm chart) and a `secret_id` (dynamic, 90-day TTL).

Vault automatically expires `secret_id` values after 90 days. Each service pod uses the Vault Agent to automatically renew its `secret_id` before expiry. **No manual rotation is needed in normal operation.**

If a `secret_id` is compromised:

```shell
# Immediately revoke all secret_ids for a role
vault write -f auth/approle/role/ROLE_NAME/secret-id-accessor/destroy \
  secret_id_accessor=ACCESSOR_ID

# Or revoke all secret_ids for the role (forces re-issue on pod restart)
vault write -f auth/approle/role/ROLE_NAME/secret-id/destroy \
  secret_id=COMPROMISED_SECRET_ID

# Trigger pod restart to force re-authentication
kubectl rollout restart deployment/AFFECTED_SERVICE -n production
```
