---
concept: claim-producer
---

# Claim producer

## What it is

A claim producer is an implementation of the claim-producer protocol. It opens a claim against a resource it manages, settles that claim when rimsky tells it to, and advertises what it can do. A producer runs either as its own service, which advertises its capabilities when it starts, or as a bundled handler inside the single rimsky process, which declares them when it registers. Rimsky dispatches to both shapes through the same protocol surface (see `concept:service`).

A producer may advertise two further abilities on the same protocol. The first splits a claim's scope into sub-scopes, which is what lets rimsky fan one claim out into sub-claims; each sub-scope carries the same substrate-meaningful content a plain acquisition returns, plus what distinguishes it inside its parent (see `concept:fan-out`, `concept:claim-tree`). Where a producer splits a scope by picking from a shared pool, each pick policy declares what happens to the picked item at each terminal outcome, so rimsky's settlement rather than the producer's own timing decides when the producer consumes an item and when it returns one. The second ability answers the overlap question over two claim scopes; a producer that does not advertise it leaves rimsky's own predicate in force (see `concept:claim-scope`, `decision:byte-equal-conflict-default`).

A producer may also advertise two sibling protocols on the same service. Validation checks a template's claim bindings against the producer's domain when the template registers (see `concept:validation`). Data-processing is the control-plane surface for the version life of typed data; its data motion stays substrate-direct through the acquired address (see `concept:data-processing`).

## Purpose

A claim producer is how rimsky stays project-agnostic. The producer knows what "the same data" means in its own domain — how a path canonicalizes, how a snapshot is identified, how a queue key is formed — and folds that knowledge into the claim scope bytes it emits. Rimsky compares those bytes without understanding them. A producer may be written in any language, because wire compatibility with the protocol is the only requirement.

## Boundaries

A claim producer owns its own resource state, the canonical claim scope bytes it emits, and the write semantics it realizes for each claim. It does not own the ledger of who holds what, which belongs to `claim-handle`, or the conflict decision, which rimsky makes. Both shapes of producer serve the same protocol surface (see `decision:parallel-inproc-claim-producer-registry`). A producer may serve the executor protocol on the same service, which is how it verifies content it has staged (see `concept:executor`). See also: `claim`, `claim-handle`, `claim-scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber`, `service`, `validation`, `data-processing`.
