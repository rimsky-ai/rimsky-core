---
story: rimsky-health-check
---

# Health probe surface for LBs and k8s

## Story

As an operator running rimsky behind a load balancer or container-orchestrator probe, I can query the unauthenticated deployment-health probe (or the health CLI verb) and get success while persistence is available and non-success when it is not, so that I gate traffic on a real health signal rather than a silently degraded happy path.
