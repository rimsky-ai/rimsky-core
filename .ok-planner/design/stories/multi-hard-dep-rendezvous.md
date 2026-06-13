---
story: multi-hard-dep-rendezvous
status: as-is
---

# Template author relies on multi-hard-dep rendezvous

## Role

As a template author declaring two or more `hard_dep: true` upstream attribute sources on one node, I can rely on each upstream running once and the receiver dispatching once with all hard-dep'd upstreams settled — so the shape rendezvouses instead of livelocking.

## Capability

The hard-dep pull carries a settled-this-frame guard: when a later-settling upstream's cascade walk re-visits the receiver, an upstream that already settled in the frame (a run row in the frame but no in-flight row) is not re-affirmed — its value is already in the receiver's drained wait-set, so there is nothing to gate on and nothing to re-run. A still-running or just-woken upstream falls through to the normal gate-insert path, so the guard protects frame termination without weakening the rendezvous (see `concept:cascade`, `decision:hard-dep-settled-guard`).

## Business value

Multi-hard-dep shapes are a checked contract, not a hazard: independent upstream settlements rendezvous on the receiver exactly once per frame, and the frame terminates.

## Acceptance

A node with two hard-dep upstreams that settle independently in the same frame: each upstream runs once; the receiver runs once, after both; the frame terminates.

## Falsifier

Upstreams re-running each other after settling in the frame (mutual re-seeding), the frame never terminating, or the receiver dispatching more than once for one frame.

## Proof

Executable proof — a deterministic scenario test for the two-hard-dep shape pins the exact-once rendezvous: each upstream runs once, the receiver runs once after both, and the frame terminates within the deadline. It stands as the regression pin against the mutual re-seeding livelock.
