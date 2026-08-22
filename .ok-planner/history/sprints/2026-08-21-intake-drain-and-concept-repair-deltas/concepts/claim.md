---
concept: claim
---

# Claim

## What it is

A claim is a node's request to hold a producer-managed resource while the node runs: a row in a data store, a path in a filesystem, the head of a queue, a snapshot of versioned data. A template declares a claim on a node. At dispatch the claim producer either grants the claim and returns what it acquired — an address, a payload, a claim scope, and the write semantics it realized — or reports the resource unavailable (see `concept:claim-producer`). The claim-producer protocol calls the granted thing a claim, and rimsky's own ledger calls the same thing a claim handle; the two names differ only in layer. Rimsky persists the resolved claim scope on that ledger (see `concept:claim-handle`, `concept:claim-scope`).

## Purpose

A claim lets a graph node state what it needs access to. It also lets rimsky decide whether two nodes are asking for the same thing: the producer knows its own domain and emits canonical claim scope bytes, and rimsky compares those bytes to detect a conflict. A producer whose overlap semantics are richer answers the overlap question itself (see `concept:claim-scope`, `decision:byte-equal-conflict-default`).

A claim also declares an intent: whether the holder only reads the resource or also writes it. Intent has one consumer, the coexistence decision. Whether a candidate may coexist with the holder of a conflicting scope follows from the two intents under the holder's realized write semantics (see `concept:write-semantics`). A producer does not branch on intent. Coexistence is rimsky's question, not the producer's.

A claim carries three extensions, each independent of the others:

- A lifetime decides whether the claim ends with the subgraph that holds it or outlives that subgraph (see `concept:claim-lifetime`, `concept:asset`).
- A claim's scope may split into sub-claims that hold sub-scopes. A sub-claim is itself a claim, and a parent claim resolves only after its sub-claims resolve (see `concept:fan-out`, `concept:claim-tree`).
- Co-holdership lets several node-runs hold one claim, which extends the claim's life to cover all of them (see `concept:claim-co-holdership`).

## Boundaries

A claim owns its declaration, what the producer returns when it grants the claim, and the terminal verb rimsky sends when the claim settles. It does not own the ledger of who holds what, which belongs to `claim-handle`; capacity counting, which belongs to `named-lock`; or the producer's own resource state, which belongs to `claim-producer`. Rimsky treats what a claim carries as opaque (see `concept:inertness`). See also: `claim-handle`, `claim-producer`, `claim-scope`, `claim-lifetime`, `claim-tree`, `claim-co-holdership`, `write-semantics`, `auto-terminal`, `inertness`.
