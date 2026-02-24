---
title: Networking and DNS
id: "7405718"
space: TD
version: 2
labels:
    - infrastructure
    - networking
    - dns
    - certificates
author: Robert Gonek
created_at: "2026-02-24T14:56:48Z"
last_modified_at: "2026-02-24T14:56:49Z"
last_modified_by: Robert Gonek
---
# Networking and DNS

This page covers DNS management, CDN and WAF configuration, internal service discovery, and TLS certificate management for Luminary's infrastructure.

## DNS: Route 53

Luminary's authoritative DNS is managed in AWS Route 53. The primary hosted zone is `luminary.io` (public) with a separate `internal.luminary.io` zone for internal service names.

### Hosted Zones

| Zone | Type | Purpose |
| --- | --- | --- |
| `luminary.io` | Public | Customer-facing domains |
| `internal.luminary.io` | Private (VPC) | Internal service-to-service |
| `luminaryanalytics.io` | Public | Redirect alias to `luminary.io` |

The Route 53 zones are managed via Terraform in `infrastructure/terraform/dns/`. Manual changes in the AWS console will be overwritten on the next Terraform apply — always make changes in Terraform.

### Delegation

DNS for `luminary.io` is delegated from the registrar (Namecheap) to the Route 53 nameservers. The Cloudflare proxy sits in front for `app.luminary.io`, `api.luminary.io`, and `ingest.luminary.io` — these records use Cloudflare's nameservers as a proxy origin, not Route 53 nameservers directly.

The delegation chain for proxied subdomains:

```
Browser → Cloudflare DNS → Cloudflare Edge → Route 53 (ALB origin IP)
```

Non-proxied (DNS-only) subdomains like `auth.luminary.io` and `status.luminary.io` resolve directly through Route 53.

## Cloudflare (WAF, CDN, Edge Workers)

Cloudflare is used in front of the primary customer-facing services. Configuration is managed via Terraform using the Cloudflare provider (`infrastructure/terraform/cloudflare/`).

### WAF Rules

Cloudflare WAF is enabled on all orange-clouded (proxied) zones. Custom WAF rules in priority order:

| Priority | Rule | Action |
| --- | --- | --- |
| 1 | Block requests with `X-Luminary-Internal` header from outside Cloudflare IP ranges | Block |
| 2 | Rate limit: >1000 req/min per IP to `/v1/ingest` | Challenge |
| 3 | Managed ruleset: Cloudflare OWASP Core | Managed Challenge |
| 4 | Managed ruleset: Cloudflare Bot Management | JS Challenge |
| 5 | Allow verified bots (Googlebot, etc.) | Skip WAF |

### CDN

Static frontend assets (JS, CSS, fonts, images) are served from `cdn.luminary.io`, a Cloudflare-proxied CNAME to our S3 CloudFront distribution. Cloudflare's edge caches assets with `Cache-Control: public, max-age=31536000, immutable` (assets are content-hash-named by Vite, so cache busting is automatic).

### Edge Workers (Edge Auth)

A Cloudflare Worker runs on `app.luminary.io` to handle lightweight auth edge cases before traffic reaches the origin:

- Validates the presence (not the signature) of the session cookie
- Redirects unauthenticated requests to `auth.luminary.io/login?return=...`
- Handles the maintenance mode banner injection

The Worker code lives in `infrastructure/cloudfront/edge-auth/worker.ts` and is deployed via `wrangler deploy` in CI.

## Internal Service Discovery

### CoreDNS in Kubernetes

Internal service-to-service communication within the Kubernetes cluster uses standard Kubernetes DNS (`*.svc.cluster.local`). CoreDNS is the cluster DNS provider, configured with the default Kubernetes plugin stack plus a `forward` plugin for resolving `internal.luminary.io` names:

```
# CoreDNS ConfigMap
luminary.io:53 {
    errors
    cache 30
    forward . 10.0.0.2  # Route53 Resolver endpoint
}
```

### external-dns

`external-dns` runs in the cluster and automatically creates Route 53 records for Kubernetes `Service` and `Ingress` resources annotated with `external-dns.alpha.kubernetes.io/hostname`. This means adding a new public-facing service only requires annotating the Kubernetes resource — no manual DNS changes needed.

```yaml
# Example Service annotation
metadata:
  annotations:
    external-dns.alpha.kubernetes.io/hostname: "new-service.luminary.io"
    external-dns.alpha.kubernetes.io/ttl: "60"
```

`external-dns` is configured with an IAM role (IRSA) that can only modify records in the `luminary.io` hosted zone.

## Certificate Management

### Public Certificates (cert-manager + Let's Encrypt)

Public TLS certificates are managed by `cert-manager` running in the cluster. It uses the Let's Encrypt ACME protocol with DNS-01 challenge (via the Route 53 solver) to obtain and auto-renew certificates.

```yaml
# ClusterIssuer for production certs
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-production
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: platform-team@luminary.io
    privateKeySecretRef:
      name: letsencrypt-production-account-key
    solvers:
    - dns01:
        route53:
          region: us-east-1
          hostedZoneID: Z1234567890
```

Certificates are requested by creating a `Certificate` resource. cert-manager handles the ACME challenge, issuance, and renewal (renews at 2/3 of the certificate's lifetime, so ~60 days for Let's Encrypt 90-day certs).

### Wildcard Certificate Strategy

We maintain wildcard certificates for `*.luminary.io` and `*.internal.luminary.io`. The wildcard cert is stored as a Kubernetes Secret and shared across namespaces using `cert-manager`'s `Secret` synchronisation (via a Helm chart that copies the secret into each namespace that needs it).

New subdomains automatically get TLS coverage from the wildcard cert — no new certificate request needed.

### Internal Certificates (Vault PKI)

Internal service-to-service mTLS uses certificates issued by a private CA hosted in HashiCorp Vault's PKI secrets engine.

```shell
# Issue a short-lived internal cert (valid 24h)
vault write pki_int/issue/luminary-internal \
    common_name="query-service.internal.luminary.io" \
    ttl="24h"
```

Services that require mTLS (currently: query service → ClickHouse, ingestion service → Kafka) request certificates at startup via Vault Agent Injector. The Vault Agent sidecar handles certificate renewal automatically before expiry.

## Related

- [Backup and Restore](Backup-and-Restore-Procedures.md)
- [Monitoring Stack](Monitoring-Stack.md)
- [Security: Vulnerability Management](https://placeholder.invalid/page/security%2Fvulnerability-management.md)
- [Frontend Architecture](https://rgonek.atlassian.net/wiki/pages/viewpage.action?pageId=4882794) — CSP headers, CDN usage
