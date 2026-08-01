---
story: multi-hard-dep-rendezvous
status: as-is
---

# Template author relies on multi-hard-dep rendezvous

## Role

As a template author declaring two or more subscriptions with `force_upstream_refresh: true` on one node, I can rely on each upstream running once and the receiver dispatching once with all force-refreshed upstreams settled — so the shape rendezvouses instead of livelocking.

## Capability

The upstream-refresh pull carries a settled-this-frame guard: when a later-settling upstream's cascade walk re-visits the receiver, an upstream that already settled in the frame (a run row in the frame but no in-flight row) is not re-affirmed — its value is already in the receiver's drained wait-set, so there is nothing to gate on and nothing to re-run. A still-running or just-woken upstream falls through to the normal gate-insert path, so the guard protects frame termination without weakening the rendezvous (see `concept:cascade`, `decision:hard-dep-settled-guard`).

## Business value

Multi-hard-dep shapes are a checked contract, not a hazard: independent upstream settlements rendezvous on the receiver exactly once per frame, and the frame terminates.

