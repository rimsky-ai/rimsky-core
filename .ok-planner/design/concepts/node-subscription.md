---
concept: node-subscription
status: as-is
aliases: [subscription]
references:
  - ../../specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
  - ../../specs/2026-05-17-sensor-messaging-unification-design.md
---

# Node-subscription

This concept describes the **receiver-side** template-DSL subscription declared in a node's `subscribes:` block — a node's wait-set on a sibling's terminal-changed signal. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal.

## What it is

A node-subscription is an impactee-side declaration of "fire me when this upstream topic transitions." Four topic kinds: `state` (any state-machine transition), `attribute` (an upstream node's attribute write), `event` (a named-event emission), `message` (boundary-crossing message arrival, post-2026-05-15). Scope: per-node (`node: <upstream>`) or cross-cutting (`instance: true`, matches every node in the instance). Optional filters narrow the match: `when` / `outcome` / `error_class` / `reason` for state topics, `name` for attribute or event topics, `kind` / `sender` / `sender_kind` / `target` for message topics. Frame modifier (`in` | `next`) controls whether the subscriber fires inside the current cascade frame or in a follow-up frame.

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
- **Self-subscription is first-class in both `frame: in` and `frame: next` shapes** — the "drain my own queue" idiom has two equally-valid spellings. `frame: next` opens a fresh frame for the same node-instance on every matching commit (one frame per queue item, clean `frame.start` / `frame.end` markers per iteration). `frame: in` keeps iteration inside the current frame (one long-running frame, supervisor picks up each new pending run as it lands). The cascade walker's insert-then-drain-in-same-tx pattern makes `frame: in` safe: the new pending self-run's wait-set blocker (keyed on the just-committed run) is drained by `drainWaitSetOnSettled` at the end of `applyTerminalComplete` in the same tx, before the supervisor sees it. `MarkStaleForCascade` does not touch `rimsky_nodes.state` — it only inserts a new run row and re-stamps `frame_id` — so the just-committed `state=fresh, last_outcome=fresh_changed` survives intact. The BFS `visited` set blocks cycle re-walk. Both shapes are the receiver-side replacement for the retired send-side `on_executor_complete: { invalidate: { targets: [self] } }` pattern; the canonical form for either is `{ node: <self-type>, on: state, when: fresh, outcome: fresh_changed, frame: <in|next> }`.

## Aliases and historical names

None. The pre-2026-05-14 vocabulary used `dependencies:` (compound), `on_event:` (send-side, retired), and `invalidate.targets:` (send-side, retired). All three retire in favor of subscriptions.

## Open within this concept

None at present.

## Notes

- 2026-05-14: concept introduced by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. `dependencies:`, `on_event:`, and send-side `invalidate.targets` retire.
- 2026-05-15: **fourth topic kind `message`** added. Filter fields: `kind`, `sender`, `sender_kind`, `target`. Receivers can combine; `target: self` is the common pattern. Substitution context for dispatched executors: `{{trigger.message.payload.X}}` reads payload fields (via the sanctioned `walkPath` introspection site per `@blessed-invariant 24`). See `concept:message`, `concept:sensor`, `concept:backfill`.
- 2026-05-17: renamed `concept:subscription` → `concept:node-subscription` to disambiguate from the new `concept:publisher-subscription` (the publisher-side rimsky↔publisher binding). The receiver-side template-DSL slug becomes node-subscription; the publisher-side slug is publisher-subscription. The `sender_kind` filter values update from `(operator | sensor | instance)` to `(operator | publisher | instance)`.
- 2026-05-19: **self-subscription is first-class in both `frame: in` and `frame: next` shapes** as the "drain my own queue" idiom. The cascade walker's prior over-broad receiver-id check at `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` skipped *all* self-edges; removed entirely (the BFS `visited` set already blocks cycle re-walk). Both branches now handle self-edges normally: FrameNext via `EnqueueOrCoalesce` + `MarkSourceNodeStale` against the next-frame source set; FrameIn via the insert-then-drain-in-same-tx pattern documented at lines 235–238 of the function. The architectural change that makes FrameIn work: `applyTerminalComplete` now flips the dispatch row's phase to terminal inside its own tx (via `Queue.RemoveForNodeInTx`) BEFORE invoking `cascadeSubscribersStaleInTx`. Without this, `MarkStaleForCascade`'s `NOT EXISTS (phase IN active set)` guard would reject the self-edge's insert because the sender's runOld was still in `phase='active'` during the walk. Mirrors the in-tx phase flip the sibling terminals already do (`applyTerminalPass`, `applyErrorPolicy`). Restores the convergence-loop primitive that the 2026-05-14 retirement of send-side `invalidate: { targets: [self] }` left without a receiver-side equivalent. Spelling is a design choice (per-iteration frame markers vs. one long-running frame), not a constraint imposed by the platform.
- 2026-05-20 — The arity split between node-subscriptions (many-to-many over upstreams) and per-field attribute substitution (1:1) is load-bearing, not an inconsistency. Subscriptions sum signals; per-field `source:` names a single value. See `concepts/attribute.md` (per-field-arity invariant + Boundaries clarification) for the rationale; companion to the declined multi-source-substitution sketch (`.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`).
- 2026-05-20 — Minimalist substitution model under per-run attribute keying. Subscriptions remain push: an upstream transition causes the receiver to fire via the cascade. Attribute reads at dispatch are scoped to this-frame's contributing senders only (no scope-walk, no cross-frame caching). The auto-subscribe rule (substitution refs imply subscriptions) stays as the default and is not opt-out-able. See `concept:attribute` for the per-run keying details and the `hard_dep: true` opt-in for proactive upstream invalidation. See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md`.
