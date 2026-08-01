---
concept: claim
status: as-is
aliases: []
---

# Claim

## What it is

`claim` is the protocol-layer noun returned by a claim producer's open verb; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer — invariant 20 (claim content inert) gates content; invariant 4 (claimant-guarded release) gates the persistence row.

A claim is a node's request to access a producer-managed resource: an items-table row, a filesystem path, a queue head, an MVCC snapshot. Declared in templates as a claim spec. At runtime, the producer's open verb returns an acquired result (address, payload, claim scope, realized write semantics) or an unavailable response. The resolved claim scope bytes are persisted on the claim-handle ledger (see `concept:claim-handle`).

## Purpose

Claims are how a graph node says "I need exclusive (or coexisting) access to this thing while I run." The producer parses the selector from its own DSL and emits canonical claim scope bytes; rimsky's default conflict predicate is byte-equal comparison of those bytes, and a producer that advertises the scopes-conflict capability supplies its own overlap predicate instead, consulted at acquisition and in the fan-out sub-claim path (see `concept:claim-scope`).

A claim also declares an intent — read or read-write. Intent's only runtime consumer is the coexistence predicate: whether a candidate may coexist with a conflicting-scope holder is decided from the two intents under the holder's realized write semantics (see `concept:write-semantics`). Producers do not branch their own behavior on intent; coexistence is rimsky's layer, not the producer's.

Claims carry three orthogonal extensions:

- **Lifetime** (subgraph or durable, default subgraph): governs auto-terminal behavior. A durable claim's handle row persists past holding-subgraph completion in a committed-durable state, released only by explicit operator action or instance termination. See `concept:claim-lifetime`, `concept:asset`.
- **Sub-claim chains**: a claim's claim scope may be partitioned via the producer's split-scope verb into sub-claims that hold sub-scopes. Persisted via a self-referential parent pointer on the claim-handle ledger. Auto-terminal walks bottom-up: a parent claim resolves only after all sub-claims have terminal. See `concept:fan-out`, `concept:claim-handle`, `concept:claim-tree`.
- **Co-holdership**: multiple node-runs may hold the same claim handle by declaring co-holdership in the template. Each co-holder gets a row in a per-claim co-holder ledger keyed by holder run. The holding subgraph extends to all co-holders; auto-terminal fires only after every co-holder reaches a non-active state. See `concept:claim-co-holdership`.

## Boundaries

Owns: the claim declaration, the address/payload/claim-scope returned at open, the terminal verb (commit, abandon, or release). Does NOT own: lock state ledger (lives in `claim-handle`), capacity counting (that's `named-lock`), producer-internal state (lives in the producer). Adjacent: `claim-handle` (including its Held variant subsection), `claim-producer`, `claim-scope`, `claim-tree`, `write-semantics`, `auto-terminal`, `inertness`.

## Invariants

- Claim content (payload, address, claim scope) is inert in rimsky: read only at the sanctioned substitution sites, the conflict-check comparison, claim-scope lock keying, and the terminal-verb wire encoding back to the producer. Never logged, formatted into strings, attached to traces, validated beyond schema gates, or included in error messages (invariant 20).
- Producers do not persist lock state internally; the claim-handle ledger is the sole authority (invariant 9a).
- Producers must not internally serialize on lock-shaped predicates (reader-lease serialization is forbidden for the staged-async write semantics) — invariant 9b.
