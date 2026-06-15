---
story: rimsky-health-check
status: as-is
---

# Health probe surface for LBs and k8s

## Role

As an operator running rimsky behind a load balancer or container-orchestrator liveness/readiness probe, I can query the deployment-health probe surface (or the health CLI verb) and get back the control-api's deployment health status, so that infrastructure operators have a probe surface to gate traffic on.

## Capability

Unauthenticated deployment-health probe surface: returns success while the deployment is healthy, non-success when a critical dependency is down; fast, probe-suitable.

## Business value

Infrastructure operators gate traffic on a real health signal — a critical-dependency outage produces a non-success response rather than a silently degraded happy-path.

## Acceptance

Against a running control-API, a request to the health surface returns a successful response while the deployment is healthy and a non-success response when a critical dependency (persistence reachable, etc.) is down. The surface requires no authentication (probes don't carry bearer tokens) and is fast (probe-suitable).

## Falsifier

The health probe surface returns success while a critical dependency is down (false-positive), OR requires auth (incompatible with anonymous probes).

## Proof

Executable proof.
