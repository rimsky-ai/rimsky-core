---
concept: node-subscription
aliases:
  - subscription
---

# Node-subscription

## What it is

A node-subscription is the receiver-side reactive declaration a node makes in a template. Each subscription names the signal it waits for, exactly or with a wildcard (see `concept:signal`), and filters by the upstream node type it reacts to. A subscription may also carry a payload predicate, and it always carries the force-upstream-refresh flag. A subscription targets only the settling signals that fire cascade (see `concept:cascade`). A subscription that cares about one terminal tag pairs a wildcarded terminal target with a payload predicate over the signal's tags, so the discriminator sits in the payload rather than at the leaf of the type path.

A subscription is also a wake declaration. A matching emission from the sender stale-marks the receiver and gates the receiver's dispatch on that sender. No subscription gathers a sender's data into the receiver's substitution context without also waking the receiver.

The force-upstream-refresh flag says what the receiver's invalidation does to the sender. When the flag is set, the receiver invalidates the sender, and the sender re-runs in the same frame before the receiver dispatches. When the flag is unset, the sender stays where it is.

## Purpose

A node-subscription keeps reactive coupling separate from the other couplings between two nodes. Read access lives in the substitution grammar, cascade coupling lives in the subscription, and eligibility gating lives in the wait-set (see `concept:wait-set`). Because the receiver declares the subscription, the node a change affects is the node that declares the flow.

## Boundaries

A node-subscription owns the per-template map of inverse edges, the force-upstream-refresh contract every entry carries, the registration-time coverage check, and the mapping from a signal's type path to the receiver wait-set rows it fills. The edges in that map come from the subscriptions a template declares, plus the edges rimsky derives for a receiver with no upstream at all (see `decision:subscription-edges-only-from-explicit-block`, `decision:structural-root-edges-derived-on-demand`).

A node-subscription does not own the signal taxonomy or the payload schemas, which belong to the signal (see `signal`). It does not own the cascade walk (see `cascade`), the wait-set ledger that drives dispatch eligibility (see `wait-set`), or the query that selects the runs ready to dispatch. A separate concept covers the publisher-side binding between a publisher peer and an instance (see `publisher-subscription`). See also: `node`, `node-run`, `frame`, `attribute`.

## Aliases

- subscription
