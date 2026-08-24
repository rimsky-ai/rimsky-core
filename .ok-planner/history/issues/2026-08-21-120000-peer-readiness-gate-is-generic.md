---
issue: peer-readiness-gate-is-generic
kind: sprint
category: unspecified
artifacts:
  - concept:lifecycle-subscriber
  - concept:service
  - concept:discovery-cache
  - concept:instance
status: retired
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

Retired. No gate. The provisioning promise the gate would have protected is gone: the lifecycle-subscriber concept now commits that rimsky orders no work against a subscriber's reaction, and no corpus artifact promises that a peer provisions substrate at deploy or at instance creation. No peer in the tree does per-scope setup: the host-agent proxy reacts (cache fill, spawn reap) and the bundled claim producers' subscriber callbacks are no-ops. Executors, claim producers, and publishers have no setup moment; a dispatch to a peer that never answered fails that dispatch, and the error policy routes it. A gate would only move that failure to instance creation. The owner retired it live on 2026-08-23 and archived the 2026-08-08 readiness sketch as abandoned.
