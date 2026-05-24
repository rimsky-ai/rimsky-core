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

A node-subscription declares `type:` (a canonical signal type-path, exact or trailing-`*` prefix per `concept:signal`) plus an optional `when:` CEL predicate over the signal payload. Sender-side filters (`node:` selects a specific upstream node-type, `instance: true` is cross-cutting) and the frame modifier (`frame: in | next`) carry forward unchanged. Subscriptions are declared per node under `subscribes:` in the template DSL.

The auto-subscribe rule from substitution refs in a node's attribute schema (`{{nodes.X.attribute.Y}}` or `{{nodes.X.event.Y}}`) carries forward — no orphan reads. The implicit subscriptions become `type: attribute/Y/changed` (or `type: attribute/*` for the bare `{{nodes.X.attribute}}` whole-pull form) and `type: event/Y` respectively.

## Purpose

Decouple reactive coupling from compound `dependencies:` declarations. Read access, cascade subscription, and eligibility gating become independent primitives:

- Read access lives in the substitution grammar (`{{...}}`).
- Cascade coupling lives in subscriptions (explicit + implicit).
- Eligibility gating lives in `concept:wait-set`.

This retires the overloaded `dependencies:` bundle and the send-side `invalidate.targets` slot on lifecycle handlers + error-policy actions; cascade flow is impactee-declared.

## Boundaries

Owns:
- The per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)`; a per-sender radix tree / prefix-bucket structure computed at registration by `code:graph/node/subscription_edges.go::BuildSubscriptionEdges`).
- The auto-subscribe rule from substitution refs.
- The consumer-side mapping from signal type-path to receiver wait-set rows.

Does NOT own:
- The signal taxonomy itself or payload schemas (those live in `concept:signal`).
- The cascade walk itself (lives in `concept:cascade`).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- The eligibility predicate evaluated by `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch`.

## Invariants

- Subscription `type:` and `when:` are validated at registration against the canonical taxonomy (`concept:signal`) and the resolved payload schema.
- Substitution refs auto-subscribe — no orphan reads.
- `Frame` defaults to `in` for per-node subscriptions and `next` for cross-cutting (`instance: true`).
- **Self-subscription is first-class in both `frame: in` and `frame: next` shapes** — the "drain my own queue" idiom has two equally-valid spellings. `frame: next` opens a fresh frame for the same node-instance on every matching commit (one frame per queue item, clean `frame.start` / `frame.end` markers per iteration). `frame: in` keeps iteration inside the current frame (one long-running frame, supervisor picks up each new pending run as it lands). The cascade walker's insert-then-drain-in-same-tx pattern makes `frame: in` safe: the new pending self-run's wait-set blocker (keyed on the just-committed run) is drained by `drainWaitSetOnSettled` at the end of `applyTerminalComplete` in the same tx, before the supervisor sees it. `MarkStaleForCascade` does not touch `rimsky_nodes.state` — it only inserts a new run row and re-stamps `frame_id` — so the just-committed `state=fresh` survives intact. Both shapes are the receiver-side replacement for the retired send-side `on_executor_complete: { invalidate: { targets: [self] } }` pattern; the canonical form is `{ node: <self-type>, type: terminal/success, when: payload.changed, frame: <in|next> }`.

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
- 2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. SubscriptionEntry's structured filter fields (When/Outcome/ErrorClass/Reason/Name/Kind/Sender/SenderKind/Target) retire; replaced by canonical signal `type:` (exact or trailing-`*` prefix from `concept:signal`) + CEL `when:` predicate over payload. Inverse-edge map shape changes from exact-key (sender → flat list) to prefix-keyed (per-sender radix tree). Auto-subscribe rule preserved; substitution refs map to `attribute/<key>/changed` / `event/<name>` patterns. Self-subscription invariant preserved (restated in new vocabulary). `validateOnEvent`-style validator carry-forwards to a `terminal/error/<class>` × executor-declared-error-classes range check (`graph/node/template_validator.go::validateSubscribes`; the proto wiring lands in Pass 6 of the signal-taxonomy plan, until then silent-skip).
