---
concept: node
aliases:
  - graph-node
---

# Node

## What it is

A node is one declarative unit of work in a template's graph. A node declares a name and a type; the template author chooses the type, and rimsky routes the dispatch by it. A node may also declare the subscriptions that form its reactive surface, the claims and locks it requires, the upstream claims it co-holds, a schema for its attributes, operator-facing tags, and a policy chain per error class. A node that does more than hold claims also names the executor it targets. Rimsky materializes a node once per instance, keyed by the instance and the type, and the materialized node carries its identity, its operator-facing tags, and its cascade mode. The materialized node carries no runtime state: every piece of execution state belongs to a node-run (see `concept:node-run`). Operator-facing surfaces summarize a node's runs as categorical counts.

## Purpose

The node is the smallest reactive unit rimsky orchestrates. Cascade resolution propagates from node to node. Rimsky acquires claims per node and dispatches per node. A template composes work by declaring node-to-node dependencies and per-node policy.

## Boundaries

A node owns its dispatch and terminal lifecycle, the claims and locks it declares, its per-error-class policy chains, its attribute writeback, and its operator-facing tags. It does not own cascade scheduling, which belongs to the frame (see `frame`). It does not own claim conflict resolution (see `claim-handle`), and it does not own the shape of the durable record (see `event-log`). Whether a node may dispatch is read from the state of its run (see `node-run`), and propagation between nodes runs on subscriptions and signals (see `node-subscription`, `signal`). See also: `error-policy`, `cascade`, `attribute`, `claim`, `named-lock`, `message-sender-node`.

## Aliases

- graph-node
