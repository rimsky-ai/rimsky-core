---
assessment: rimsky-health-check--probe-unauthenticated
subject: story:rimsky-health-check
way: probe-unauthenticated
release: d977250c
outcome: held
warrant: experiment:rimsky-health-check
---
# Asking a deployment whether it is healthy without presenting credentials

The audit called `catalog:http-routes/GET /v1/health` with no credentials attached against a running deployment in its default anonymous mode, and it answered success; `catalog:cli-verbs/rimsky health` reported the same and exited zero. The claim that the probe is genuinely outside the authenticated surface was then tested where it could fail: the deployment was moved off anonymous access, after which an ordinary route refused the same unauthenticated caller, while the probe still answered success to a caller carrying nothing. The probe therefore works for a load balancer or container-orchestrator that holds no key, not merely for callers of an open deployment. Ten checks across this way and its sibling, none failing.

## Unverified remainder

Two deployment postures were driven — default anonymous access and anonymous access turned off. The way does not enumerate every authentication posture a deployment can be configured into.
