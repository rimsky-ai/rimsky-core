---
audit: rimsky-health-check
artifact: story:rimsky-health-check
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:50:41Z
---

# Unauthenticated deployment-health signal that tracks persistence

Supported: a caller carrying no credentials gets a success answer while
persistence is available and a non-success answer when it is not. Both halves
were driven against running deployments. On the credentials half, the probe
answered success to an unauthenticated caller in the deployment's default
anonymous mode, and still answered success after the deployment was moved off
anonymous access and an ordinary route began refusing the same unauthenticated
caller — so the probe is genuinely outside the authenticated surface, not merely
riding an open deployment. On the persistence half, a deployment backed by a
postgres container was driven through all three states of that dependency:
success while it was up, non-success naming the failed transaction once the
container was stopped, and success again after it was started. Both answers
were taken twice over, through the route and through the health CLI verb, whose
exit code tracked them. Ten checks across two runs, none failing.

## Compliance

- The body names two delivery surfaces — "the unauthenticated deployment-health
  probe (or the health CLI verb)" — which the story rules place in decisions; the
  compliant text names the outcome only, e.g. "I can ask a deployment whether it
  is healthy without presenting credentials, and get success only while its
  persistence is available, so that I gate traffic on a real health signal rather
  than a silently degraded happy path."
