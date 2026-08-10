---
audit: rimsky-health-check
artifact: story:rimsky-health-check
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# An unauthenticated probe whose answer tracks persistence

Supported. Both halves were measured. On a deployment moved off anonymous
access, an ordinary route refused an unauthenticated caller with 401 while the
probe still answered success to a caller carrying nothing, so the probe is
genuinely credential-free rather than merely unguarded by default. On a
deployment running against its own postgres container, the probe answered success
while postgres was up, answered a 500 naming the failed transaction once the
postgres container was stopped, and answered success again once it was started —
and the health verb tracked it, exiting 0 and then 1 on the same two states. The
non-success case is the whole point of the story, and it is a real signal rather
than a happy path that stays green.

## Compliance

The body names the delivery surface twice — "query the unauthenticated
deployment-health probe" and "(or the health CLI verb)" — which the story rules
place in `decisions/` rather than in a story. Compliant text: "As an operator
running rimsky behind a load balancer or container-orchestrator probe, I can ask a
deployment whether it is healthy without presenting credentials and get success
while its persistence is available and non-success when it is not, so that I gate
traffic on a real health signal rather than a silently degraded happy path."
