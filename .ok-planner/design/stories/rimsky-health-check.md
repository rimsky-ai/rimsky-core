---
story: rimsky-health-check
status: as-is
---

# Health probe surface for LBs and k8s

## Role

As an operator running rimsky behind a load balancer or k8s liveness/readiness probe, I can query `GET /health` (or `rimsky health` CLI) and get back the control-api's deployment health status, so that infrastructure operators have a probe surface to gate traffic on.

## Capability

`GET /health` unauthenticated probe surface: returns success while the deployment is healthy, non-success when a critical dependency is down; fast, probe-suitable.

## Business value

Infrastructure operators gate traffic on a real health signal — a critical-dependency outage produces a non-success response rather than a silently degraded happy-path.

## Acceptance

Against a running control-api, a request to the health surface returns a successful response while the deployment is healthy and a non-success response when a critical dependency (persistence reachable, etc.) is down. The route requires no authentication (probes don't carry bearer tokens) and is fast (probe-suitable).

## Falsifier

Health route returns success while a critical dependency is down (false-positive), OR requires auth (incompatible with anonymous probes).

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
