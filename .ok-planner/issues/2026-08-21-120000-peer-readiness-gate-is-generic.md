---
issue: peer-readiness-gate-is-generic
kind: sprint
category: unspecified
artifacts:
  - concept:lifecycle-subscriber
  - concept:service
  - concept:discovery-cache
  - concept:instance
status: verified
opened: 2026-08-21T12:00:00Z
---

# Should rimsky gate work on peer readiness?

Rimsky dispatches work to peers — executors, claim producers, sensors, publishers, lifecycle subscribers — and nothing orders a peer's own setup against the work that depends on it. The discovery cache records whether a peer answered its last handshake, but neither instance creation nor first dispatch reads it. The validation service is the one peer with an unreachable policy, and only at registration. A peer that is offline, or reachable but not yet finished its own per-template setup, is discovered only when a dispatch to it fails.

Lifecycle subscribers made this concrete. Their concept once promised provisioning archetypes — a subscriber that provisions substrate at template deploy, one that warms a cache at instance creation — which implied dependent work should wait for them. The catalog repair narrowed the concept: a subscriber now only "learns of" control-plane transitions, after commit, with no ordering promise. That narrowing removed the promise; it did not answer whether a readiness mechanism should exist. A sketch (2026-08-08, subscriber readiness gating) works the mechanism — a readiness question per peer kind, blocking without vetoing — and leaves open where blocked work waits, what a permanently unready peer does to a deployment, and whether an operator can override.

Subscribers stay distinctive in one respect: they are invoked at instance creation as well as at deploy, so a gate for them must cover both moments.

## Options

- **A readiness question in the peer protocol.** Every peer kind answers "am I ready for this scope"; instance creation and first dispatch wait on the answer. The most general shape and the one the sketch develops; it needs a wait-and-surface story for a peer that never becomes ready.
- **Reachability-only gating.** Instance creation and first dispatch read the discovery cache and refuse or wait when a declared peer is unreachable. Cheap, but blind to a peer that answers the handshake while its per-scope setup is unfinished.
- **No gate.** A peer declares itself only when ready; ordering is the operator's discipline. Zero mechanism, and the failure stays where it is today — surfaced by the first failed dispatch.

The ruling decides whether rimsky owns peer-readiness ordering, and at what granularity.

## Ruling

> Recommended ruling (/verify-issues): add a readiness question to the
> peer protocol and gate instance creation and first dispatch on it,
> taking the 2026-08-08 sketch as the sprint's starting point; its
> three open questions (where blocked work waits, the permanently
> unready peer, the operator override) are settled in that sprint's
> planning, not left to execution.
>
> Rationale: the project's grain is explicit gates that fail loudly —
> validation refuses an unreachable service at registration, the
> expected-attributes contract refuses an undeclared key at
> registration — and reachability-only gating reproduces the known
> blind spot (a reachable peer mid-setup) that motivated the issue.
> Flip case: if deployments show peers reliably self-ordering (declare
> only when ready), the no-gate option suffices and this becomes an
> operational-guidance paragraph instead of mechanism.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
