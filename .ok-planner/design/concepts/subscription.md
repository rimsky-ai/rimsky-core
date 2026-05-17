---
concept: subscription
status: as-is
aliases: []
references:
  - ../../specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
---

# Subscription

## What it is

A subscription is an impactee-side declaration of "fire me when this upstream topic transitions." Four topic kinds: `state` (any state-machine transition), `attribute` (an upstream node's attribute write), `event` (a named-event emission), `message` (boundary-crossing message arrival, post-2026-05-15). Scope: per-node (`node: <upstream>`) or cross-cutting (`instance: true`, matches every node in the instance). Optional filters narrow the match: `when` / `outcome` / `error_class` / `reason` for state topics, `name` for attribute or event topics, `kind` / `sender` / `sender_kind` / `target` for message topics. Frame modifier (`in` | `next`) controls whether the subscriber fires inside the current cascade frame or in a follow-up frame.

Subscriptions are declared per node under `subscribes:` in the template DSL. Substitution refs in a node's attribute schema (`{{nodes.X.attribute.Y}}` or `{{nodes.X.event.Y}}`) auto-subscribe the receiver to the named upstream topic — no orphan reads.

## Purpose

Decouple reactive coupling from compound `dependencies:` declarations. Read access, cascade subscription, and eligibility gating become independent primitives:

- Read access lives in the substitution grammar (`{{...}}`).
- Cascade coupling lives in subscriptions (explicit + implicit).
- Eligibility gating lives in `concept:wait-set`.

This retires the overloaded `dependencies:` bundle and the send-side `invalidate.targets` slot on lifecycle handlers + error-policy actions; cascade flow is impactee-declared.

## Boundaries

Owns:
- The per-template inverse-edge map (computed at registration by `code:graph/node/subscription_edges.go::BuildSubscriptionEdges`).
- The topic taxonomy (`state` / `attribute` / `event`).
- The auto-subscribe rule from substitution refs.

Does NOT own:
- The cascade walk itself (lives in `concept:cascade`).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- The eligibility predicate evaluated by `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch`.

## Invariants

- Subscriptions validate against the upstream's declared output topology at registration when the upstream executor is reachable via the observability handshake (silent-skip otherwise, mirroring today's `validateOnEvent` semantics).
- Substitution refs auto-subscribe — no orphan reads.
- `Frame` defaults to `in` for per-node subscriptions and `next` for cross-cutting (`instance: true`).
- `name` is required for `on: event`; optional for `on: attribute`; unused for `on: state`.
- State-only filters (`when`, `outcome`, `error_class`, `reason`) only apply when `on: state`.

## Aliases and historical names

None. The pre-2026-05-14 vocabulary used `dependencies:` (compound), `on_event:` (send-side, retired), and `invalidate.targets:` (send-side, retired). All three retire in favor of subscriptions.

## Open within this concept

None at present.

## Notes

- 2026-05-14: concept introduced by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. `dependencies:`, `on_event:`, and send-side `invalidate.targets` retire.
- 2026-05-15: **fourth topic kind `message`** added. Filter fields: `kind`, `sender`, `sender_kind`, `target`. Receivers can combine; `target: self` is the common pattern. Substitution context for dispatched executors: `{{trigger.message.payload.X}}` reads payload fields (via the sanctioned `walkPath` introspection site per `@blessed-invariant 24`). See `concept:message`, `concept:sensor`, `concept:backfill`.
