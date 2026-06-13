---
concept: node-subscription
status: as-is
aliases: [subscription]
---

# Node-subscription

This concept describes the **receiver-side** template-DSL subscription declared in a node's `subscribes:` block — a node's wait-set on a sibling's terminal-changed signal. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal.

## What it is

A node-subscription declares `type:` (a canonical signal type-path, exact or trailing-`*` prefix per `concept:signal`) plus an optional `when:` CEL predicate over the signal payload. Sender-side filters (`node:` selects a specific upstream node-type, `instance: true` is cross-cutting) and the frame modifier (`frame: in | next`) apply. Subscriptions are declared per node under `subscribes:` in the template DSL.

The auto-subscribe rule from substitution refs in a node's attribute schema (`{{nodes.X.attribute.Y}}` or `{{nodes.X.event.Y}}`) applies — no orphan reads. The implicit subscriptions become `type: attribute/Y/changed` (or `type: attribute/*` for the bare `{{nodes.X.attribute}}` whole-pull form) and `type: event/Y` respectively.

## Purpose

Decouple reactive coupling from compound `dependencies:` declarations. Read access, cascade subscription, and eligibility gating become independent primitives:

- Read access lives in the substitution grammar (`{{...}}`).
- Cascade coupling lives in subscriptions (explicit + implicit).
- Eligibility gating lives in `concept:wait-set`.

Cascade flow is impactee-declared.

## Boundaries

Owns:
- The per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)`; a per-sender radix tree / prefix-bucket structure computed at template registration).
- The auto-subscribe rule from substitution refs.
- The consumer-side mapping from signal type-path to receiver wait-set rows.

Does NOT own:
- The signal taxonomy itself or payload schemas (those live in `concept:signal`).
- The cascade walk itself (lives in `concept:cascade`).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- The dispatch-eligibility predicate that selects ready node-runs (a persistence-layer query).

## Invariants

- Subscription `type:` and `when:` are validated at registration against the canonical taxonomy (`concept:signal`) and the resolved payload schema.
- Substitution refs auto-subscribe — no orphan reads.
- The `frame:` modifier defaults to `in` for per-node subscriptions and `next` for cross-cutting (`instance: true`).
- **Self-subscription is first-class in both `frame: in` and `frame: next` shapes** — the "drain my own queue" idiom has two equally-valid spellings. `frame: next` opens a fresh frame for the same node-instance on every matching commit (one frame per queue item, clean `frame.start` / `frame.end` markers per iteration). `frame: in` keeps iteration inside the current frame (one long-running frame, supervisor picks up each new pending run as it lands). The cascade walker's insert-then-drain-in-same-tx pattern makes `frame: in` safe: the new pending self-run's wait-set blocker (keyed on the just-committed run) is drained at the end of the terminal-complete handler in the same transaction, before the supervisor sees it. The cascade stale-mark does not touch the per-instance node row's `state` — it only inserts a new run row and re-stamps `frame_id` — so the just-committed `state=fresh` survives intact. The canonical form is `{ node: <self-type>, type: terminal/success, when: payload.changed, frame: <in|next> }`.
