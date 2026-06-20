---
concept: node-subscription
status: as-is
aliases: [subscription]
---

# Node-subscription

This concept describes the **receiver-side** template-DSL subscription declared on a node — a node's wait-set on a sibling's terminal-changed signal. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal.

## What it is

A node-subscription declares a target signal (a canonical signal type-path, exact or wildcarded per `concept:signal`) plus an optional payload predicate, plus two required cascade-shape booleans (a wake-on-change flag and a force-upstream-refresh flag). Sender-side filters (selecting a specific upstream node-type, or a cross-cutting any-sender form) and a frame modifier (same-frame or next-frame) apply. Subscriptions are declared per node in the template.

The subscription's signal family is one of the families enumerated in `concept:signal` (terminal, transient, attribute, message). To express interest in a specific terminal-tag discriminator, a subscription pairs a wildcarded terminal target with a payload predicate over the signal's tags. The payload predicate carries the discriminator at the payload layer rather than at the type-path leaf.

The wake-on-change flag governs whether a matching emission from the sender dispatches the receiver: when set, it stale-marks the receiver and inserts a wait-set row gating its dispatch on the sender; when unset, it inserts only the wait-set row so the receiver's substitution context can read the sender's data if it dispatches via another edge, without firing the receiver itself.

The force-upstream-refresh flag governs whether the receiver's invalidation drags the sender into the same frame: when set, it invalidates the sender so it re-runs in the same frame before the receiver dispatches; when unset, it leaves the sender wherever it is. A cross-cutting subscription cannot set the force-upstream-refresh flag — the combination is incoherent and rejected at registration.

Every substitution ref in a node's attribute schema is matched at registration against the receiver's subscription declarations; a ref with no covering entry is rejected.

## Purpose

Decouple reactive coupling from compound dependency declarations. Read access, cascade subscription, and eligibility gating become independent primitives:

- Read access lives in the substitution grammar.
- Cascade coupling lives in subscriptions.
- Eligibility gating lives in `concept:wait-set`.

Cascade flow is impactee-declared.

## Boundaries

Owns:
- The per-template inverse-edge map computed at template registration from declared subscriptions plus runtime-injected structural-root edges. The runtime-injected edges originate from an empty-typed sender, one per structural root receiver (a node whose author-declared subscriptions are empty or absent), with the wake-on-change flag set, the force-upstream-refresh flag unset, the signal pattern matching a success terminal, and a sender-bound-to-empty marker. The augmentation is template-determinable and lives on the runtime's derived in-memory map; the canonical template hash is over the spec bytes only and is unaffected by the derived view.
- The two-flag cascade-shape contract on every subscription entry.
- The registration-time coverage check that matches every substitution ref against the receiver's subscription declarations.
- The consumer-side mapping from signal type-path to receiver wait-set rows.

Does NOT own:
- The signal taxonomy itself or payload schemas (those live in `concept:signal`).
- The cascade walk itself (lives in `concept:cascade`).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- The dispatch-eligibility predicate that selects ready node-runs (a persistence-layer query).

## Invariants

- The subscription target and payload predicate are validated at registration against the canonical taxonomy (`concept:signal`) and the resolved payload schema.
- Every substitution ref in a node's attribute schema is matched by at least one subscription entry whose sender and type would deliver the corresponding signal. Templates with uncovered refs are rejected at registration.
- Every subscription entry carries both cascade-shape flags; entries missing either are rejected at registration. A cross-cutting subscription cannot set the force-upstream-refresh flag.
- Every subscription edge carries a sender-bound-to-empty marker. Cross-cutting edges leave it unset (consulted on every settled sender, matched against the sender's signal). Runtime-injected structural-root edges set it (consulted only when the actual settling sender's type is the empty type). Author-declared subscription entries cannot set the marker; the runtime owns it.
- The frame modifier defaults to same-frame for per-node subscriptions and next-frame for cross-cutting ones.
- **Self-subscription is first-class in both same-frame and next-frame shapes** — the "drain my own queue" idiom has two equally-valid spellings. The next-frame shape opens a fresh frame for the same node-instance on every matching commit (one frame per queue item, clean frame boundaries per iteration). The same-frame shape keeps iteration inside the current frame (one long-running frame, supervisor picks up each new pending run as it lands). The cascade walker's insert-then-drain-in-same-tx pattern makes the same-frame shape safe: the new pending self-run's wait-set blocker (keyed on the just-committed run) is drained at the end of the terminal-complete handler in the same transaction, before the supervisor sees it. The cascade stale-mark does not touch the per-instance node row's state — it only inserts a new run row and re-stamps the frame reference — so the just-committed fresh state survives intact.
