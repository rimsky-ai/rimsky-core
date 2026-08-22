---
concept: cascade
aliases:
  - reactive-cascade
---

# Cascade

## What it is

Cascade is the engine that turns one node's state transition into the set of downstream node-state transitions. Three words name its parts. The **walk** is the mechanism: a traversal of the graph, fired inside the transaction that settles the triggering signal rather than run as a separate topology-ordered pass. **Propagation** is what the walk does where a subscription edge matches the settling signal the sender emitted and the subscriber's predicate holds: it marks the dependent stale, records what that dependent waits for, and recurses. The gate evaluator then advances the receiver once its wait set drains (see `concept:wait-set`). **Fallthrough** is what the walk does for a node that has no executor: the node's state rolls forward fresh without anything running, carried by the scheduler's periodic pure-cascade sweep, which records its own transition reason (see `concept:transition-reason`). One walk, two node-level behaviours. The walk fires on the settling terminal's own transaction; only fallthrough's advance of the node waits for a scheduler tick.

## Purpose

A reactive graph orchestrator earns its keep only if one executor's "I changed" signal recomputes the right downstream nodes and no others. Cascade turns a single terminal outcome into that set of downstream transitions.

## Boundaries

Cascade owns the firing gate that decides whether a settling signal starts a walk at all, the downstream walk, and the two node-level behaviours the walk performs. Emitting an invalidation belongs to `concept:message`. Creating a frame belongs to `concept:frame`. Resolving a terminal handler belongs to `concept:terminal-resolution`. The queue-shaping rule applied once a receiver's gate clears belongs to `concept:cascade-mode`.

The walk looks downstream from a transitioning sender along subscription edges, and upstream from a freshly invalidated receiver to proactively invalidate the upstreams that receiver names. The implicit empty message wakes every structural root of the template.

The fresh-or-failed colour a node-run's terminal outcome carries is informational, and cascade does not gate on it. A receiver that wants to ignore a failed sender says so in its subscription predicate, or does not subscribe to the sender's error signals at all (see `concept:node-subscription`).

see also: `message`, `signal`, `transition-reason`, `frame`, `terminal-resolution`, `cascade-mode`, `wait-set`, `node-subscription`, `node-run`

## Aliases

- reactive-cascade
