---
decision: peer-auth-mtls
status: as-is
aliases:
  - peer-auth-switch
---

# Internal service auth is optional mutual TLS, default off

## Choice

A two-mode deployment-level peer-auth switch, default off, governs the internal service↔service boundary (scheduler↔claim-producers, supervisor↔executors, and their return legs). Off, the internal dials are plaintext against the trusted-private-subnet assumption — zero-config, unchanged for local dev and testcontainers. On, the mode is mutual TLS: a per-deployment CA lives in the control plane, its private key encrypted at rest with authenticated symmetric encryption under an operator-supplied environment key (startup fails closed when mtls is on and the key is missing or malformed); every operator-deployed standing service enrolls with its api-key to obtain a short-lived leaf certificate, its key, and the CA root; and both peers of every internal connection present and verify CA-signed certificates. Coverage is uniform across both dispatch transports (gRPC and the HTTP dispatch bridge), the supervisor async-callback return leg, and services' publish-back-to-control-API calls (see `concept:peer-auth`).

## Rationale

Production wants the internal call paths authenticated rather than resting on network isolation alone; local dev and the single-process all-in-one must stay zero-config. A default-off switch delivers both — the secured posture is one config flip and its absence costs nothing.

## Alternatives

- **More api-keys everywhere (api-key on every internal call)** — rejected. Api-keys are a server-inbound bearer pattern, but rimsky's internal peers are mostly OUTBOUND clients (the scheduler dials producers, the supervisor dials executors). Only a certificate lets the SERVER side of a connection be authenticated too, and only a certificate keeps the standing secret off the wire — the api-key transits once at enroll, then the cert's private key proves possession by signature per handshake. An api-key on every call would put a reusable secret on every internal hop.
- **Default on** — rejected: breaks zero-config local use and testcontainers, which have no CA and no operator config at all.
- **Terminating external identity (OIDC/SAML/mTLS) at rimsky's edge** — out of scope; that is a deployment-edge concern (see `concept:api-key`).
