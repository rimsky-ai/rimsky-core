---
concept: node-subscription
status: as-is
aliases: [subscription]
---

# Node-subscription

This concept describes the **receiver-side** template-DSL subscription declared in a node's `subscribes:` block — a node's wait-set on a sibling's terminal-changed signal. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal.

## What it is

A node-subscription declares `type:` (a canonical signal type-path, exact or trailing-`*` prefix per `concept:signal`) plus an optional `when:` CEL predicate over the signal payload, plus two required cascade-shape booleans: `wake_on_change` and `force_upstream_refresh`. Sender-side filters (`node:` selects a specific upstream node-type, `instance: true` is cross-cutting) and the frame modifier (`frame: in | next`) apply. Subscriptions are declared per node under `subscribes:` in the template DSL.

`wake_on_change` governs whether a matching emission from the sender dispatches the receiver: `true` stale-marks the receiver and inserts a wait-set row gating its dispatch on the sender; `false` inserts only the wait-set row so the receiver's substitution context can read the sender's data if it dispatches via another edge, without firing the receiver itself.

`force_upstream_refresh` governs whether the receiver's invalidation drags the sender into the same frame: `true` invalidates the sender so it re-runs in the same frame before the receiver dispatches; `false` leaves the sender wherever it is. A cross-cutting subscription (`instance: true`) cannot carry `force_upstream_refresh: true` — the combination is incoherent and rejected at registration.

Every substitution ref in a node's attribute schema (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Y}}`, or the whole-pull `{{nodes.X.attribute}}`) is matched at registration against the receiver's `subscribes:` block; a ref with no covering entry is rejected.

## Purpose

Decouple reactive coupling from compound `dependencies:` declarations. Read access, cascade subscription, and eligibility gating become independent primitives:

- Read access lives in the substitution grammar (`{{...}}`).
- Cascade coupling lives in subscriptions.
- Eligibility gating lives in `concept:wait-set`.

Cascade flow is impactee-declared.

## Boundaries

Owns:
- The per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)`; a per-sender radix tree / prefix-bucket structure computed at template registration), populated from `subscribes:` entries plus runtime-injected structural-root edges. The runtime-injected edges are keyed by sender=`""`, one per structural root receiver (a node whose author-declared `subscribes:` block is empty or absent), with `wake_on_change: true`, `force_upstream_refresh: false`, type-pattern matching `terminal/success`, and `sender_bound_to_empty: true`. The augmentation is template-determinable and lives on the runtime's derived in-memory map; the canonical template hash is over the spec bytes only and is unaffected by the derived view.
- The two-flag cascade-shape contract (`wake_on_change`, `force_upstream_refresh`) on every subscription entry.
- The registration-time coverage check that matches every substitution ref against the receiver's `subscribes:` block.
- The consumer-side mapping from signal type-path to receiver wait-set rows.

Does NOT own:
- The signal taxonomy itself or payload schemas (those live in `concept:signal`).
- The cascade walk itself (lives in `concept:cascade`).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- The dispatch-eligibility predicate that selects ready node-runs (a persistence-layer query).

## Invariants

- Subscription `type:` and `when:` are validated at registration against the canonical taxonomy (`concept:signal`) and the resolved payload schema.
- Every substitution ref in a node's attribute schema is matched by at least one `subscribes:` entry whose sender and type would deliver the corresponding signal. Templates with uncovered refs are rejected at registration.
- Every `subscribes:` entry carries the `wake_on_change` and `force_upstream_refresh` boolean fields; entries missing either field are rejected at registration. A cross-cutting subscription (`instance: true`) cannot carry `force_upstream_refresh: true`.
- Every subscription edge carries a `sender_bound_to_empty` flag. Cross-cutting (`instance: true`) edges set it false (consulted on every settled sender, matched against the sender's signal). Runtime-injected structural-root edges set it true (consulted only when the actual settling sender's type is `""`). Author-declared `subscribes:` entries cannot set the flag; the runtime owns it.
- The `frame:` modifier defaults to `in` for per-node subscriptions and `next` for cross-cutting (`instance: true`).
- **Self-subscription is first-class in both `frame: in` and `frame: next` shapes** — the "drain my own queue" idiom has two equally-valid spellings. `frame: next` opens a fresh frame for the same node-instance on every matching commit (one frame per queue item, clean `frame.start` / `frame.end` markers per iteration). `frame: in` keeps iteration inside the current frame (one long-running frame, supervisor picks up each new pending run as it lands). The cascade walker's insert-then-drain-in-same-tx pattern makes `frame: in` safe: the new pending self-run's wait-set blocker (keyed on the just-committed run) is drained at the end of the terminal-complete handler in the same transaction, before the supervisor sees it. The cascade stale-mark does not touch the per-instance node row's `state` — it only inserts a new run row and re-stamps `frame_id` — so the just-committed `state=fresh` survives intact. The canonical form is `{ node: <self-type>, type: terminal/success, when: payload.changed, frame: <in|next> }`.
