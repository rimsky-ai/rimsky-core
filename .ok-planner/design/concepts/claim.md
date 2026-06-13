---
concept: claim
status: as-is
aliases: []
---

# Claim

## What it is

`claim` is the protocol-layer noun returned by a claim producer's open verb; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer — `@blessed-invariant 20` (claim content inert) gates content; `@blessed-invariant 4` (claimant-guarded release) gates the persistence row.

A claim is a node's request to access a producer-managed resource: an items-table row, a filesystem path, a queue head, an MVCC snapshot. Declared in templates as a claim spec (fields: producer name, selector, intent, alias). At runtime, the producer's open verb returns an acquired result (address, payload, claim scope, realized write semantics) or an unavailable response. The resolved claim scope bytes are persisted as claim-scope-kinded rows in the claim-handle ledger (see `concept:claim-handle`).

## Purpose

Claims are how a graph node says "I need exclusive (or coexisting) access to this thing while I run." The producer parses the selector from its own DSL and emits canonical claim scope bytes; rimsky enforces the conflict matrix byte-equally.

Claims carry three orthogonal extensions:

- **Lifetime** (`subgraph | durable`; default `subgraph`): governs auto-terminal behavior. A `durable` claim's handle row persists past holding-subgraph completion in a committed-durable state, released only by explicit operator action or instance termination. See `concept:claim-lifetime`, `concept:asset`.
- **Sub-claim chains**: a claim's claim scope may be partitioned via the producer's split-scope verb into sub-claims that hold sub-scopes. Persisted via a self-referential parent pointer on the claim-handle ledger. Auto-terminal walks bottom-up: a parent claim resolves only after all sub-claims have terminal. See `concept:fan-out`, `concept:claim-handle`.
- **Co-holdership**: multiple node-runs may hold the same claim handle via the `holds:` template directive. Each co-holder gets a row in a per-claim co-holder ledger keyed by holder run. The holding subgraph extends to all co-holders; auto-terminal fires only after every co-holder reaches a non-active state. See `concept:claim-co-holdership`.

## Boundaries

Owns: the claim declaration, the address/payload/claim-scope returned at open, the post-terminal verb (commit, abandon, or release). Does NOT own: lock state ledger (lives in `claim-handle`), capacity counting (that's `named-lock`), producer-internal state (lives in the producer). Adjacent: `claim-handle` (including its Held variant subsection), `claim-producer`, `claim-scope`, `write-semantics`, `auto-terminal`, `inertness`.

## Invariants

- Claim content (payload, address, claim scope) is inert in rimsky: read only at the sanctioned substitution sites and one wire-encoding site. Never logged, formatted into strings, attached to traces, validated beyond schema gates, or included in error messages (`@blessed-invariant 20`).
- Producers do not persist lock state internally; the claim-handle ledger is the sole authority (`@blessed-invariant 9a`).
- Producers must not internally serialize on lock-shaped predicates (reader-lease serialization is forbidden for the staged-async write semantics) — `@blessed-invariant 9b`.
