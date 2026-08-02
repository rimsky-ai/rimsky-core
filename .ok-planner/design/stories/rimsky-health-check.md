---
story: rimsky-health-check
---

# Health probe surface for LBs and k8s

## Story

As an operator running rimsky behind a load balancer or container-orchestrator liveness/readiness probe, I can query the deployment-health probe surface (or the health CLI verb) and get back the control-api's deployment health status, so that infrastructure operators have a probe surface to gate traffic on.

Unauthenticated deployment-health probe surface: returns success while the deployment is healthy, non-success when a critical dependency is down; fast, probe-suitable.

Infrastructure operators gate traffic on a real health signal — a critical-dependency outage produces a non-success response rather than a silently degraded happy-path.
