---
issue: peer-readiness-gate-is-generic
kind: sprint
category: unspecified
artifacts:
  - concept:lifecycle-subscriber
  - concept:service
  - concept:discovery-cache
  - concept:instance
status: open
opened: 2026-08-21T12:00:00Z
---

# Nothing waits for a peer to be ready before an instance runs against it

No peer kind has a readiness gate, and `concept:lifecycle-subscriber` advertises provisioning archetypes that need one. A claim producer that sets up per-template substrate at deploy, or an executor that warms a cache at instance creation, makes an instance work; nothing downstream waits for that work to finish. The same gap holds for every peer kind at the reachability level: the discovery cache records reachable or unreachable from the startup handshake and a refresh loop, and neither instance creation nor first dispatch reads it; an unreachable executor surfaces as an infra-class fault retried under the supervisor's cap. Validators are the one kind with an explicit unreachable policy, at registration only.

The owner ruled on 2026-08-21 that the gate is a generic peer-protocol question, not a subscriber-only mechanism, and that the lifecycle-subscriber concept narrows until one exists. Subscribers stay unique in one respect: they are the one peer kind notified at instance creation. The 2026-08-08 sketch on subscriber readiness gating records the per-event table (template deployed blocks instance creation; instance created blocks first dispatch; teardown and run-scope terminal block nothing) and three open questions: where blocked work waits, what a permanently failing peer does to a deployment, and whether an operator gets an override.

## Candidates

- A readiness question in the peer protocol every kind answers, per object where the kind provisions per object, and instance creation and first dispatch wait on every declared peer's answer.
- Reachability only: instance creation and first dispatch consult the discovery cache's reachability and wait or refuse on unreachable, with no per-object readiness.
- No gate: the concepts stop promising provisioning-shaped archetypes, and a peer that provisions does so before it is declared to the deployment.
