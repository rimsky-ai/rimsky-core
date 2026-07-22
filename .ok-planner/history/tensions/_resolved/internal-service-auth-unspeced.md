---
tension: internal-service-auth-unspeced
category: unspecified
status: resolved
affects:
  - supervisor
  - control-api
  - host-agent-proxy
---

# No mechanism for rimsky-process-to-rimsky-process authentication

## What is muddy

Rimsky has no mechanism for one rimsky process to authenticate to another. Supervisor → control-api coordination is DB-only today. The host-agent-proxy → control-api lifecycle subscription introduces a service-to-service call path (proxy subscribing to lifecycle events, proxy POSTing publisher messages to `/instances/{id}/messages`) that relies on deployment-level network isolation rather than explicit auth.

## Why it matters

Production deployments may want mTLS or service-tokens between rimsky processes. Today's posture is implicit.

## Resolution candidates (do NOT pick)

- Internal-service api-key kind with a system-permission grant.
- mTLS via per-process certificates.
- A service-mesh handoff.

## Evidence

- No internal-auth surface is declared on `concept:supervisor`, `concept:control-api`, or `concept:host-agent-proxy`; the cross-process call paths rely on deployment-level network isolation.

## Resolution

Resolved by the peer-auth posture (`concept:peer-auth`). The internal service↔service boundary now has an optional deployment-level switch `peer_auth: none|mtls` (default `none`, keeping the trusted-private-subnet model for local dev and testcontainers; see `decision:peer-auth-mtls`). Under `mtls` a per-deployment CA in the control plane issues short-lived certificates and both peers of every internal dial mutually authenticate, covering both dispatch transports, the async-callback return leg, and services' publish-back-to-control-API calls. Rather than a new credential kind, a service enrolls with an api-key carrying the `service:enroll` grant and exchanges it for a cert identity (`decision:enroll-token-is-api-key`) — so the api-key ledger stays the single principal registry. The former per-call run-token and the `async_ack_id`-as-credential are swept in favor of the peer identity (`decision:run-token-swept`). The host-agent→proxy hop (dev-machine→deployment) is secured by pinned-root TLS with the user's api-key inside the channel (`decision:host-agent-proxy-tls`). The resolution candidates above were reconciled: mTLS via per-process certificates was chosen, but the certificate is DERIVED from an api-key rather than a new internal-service credential kind, and it is optional rather than mandatory.
