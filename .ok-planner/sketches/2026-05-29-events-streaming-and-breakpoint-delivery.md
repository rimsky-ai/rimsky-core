# Events streaming (SSE) + breakpoint-hit delivery

**Date:** 2026-05-29
**Type:** Pre-spec sketch (deferred work).
**Deferred from:** `spec:2026-05-29-console-upstream-auth-audit-and-fixes` (the
operator-console upstream brainstorm). Co-owned by
`sketch:2026-05-27-readonly-runscope-spine`; brainstorm the two together.

## Why deferred

This is the live-observation half of the operator-console asks. It was held out of
the console-upstream spec for two reasons: it is the heaviest single piece, and it is
**co-owned by the read-only/run-scope spine sketch** — designing it blind to that
sketch's live-tail requirements would invite rework. The console degrades to
poll/refresh on `route:GET /events` until this lands (already accepted as the fallback).

## F8 — Events SSE

**Need.** An SSE variant of the events feed (`text/event-stream`) so consumers see live
transitions without polling. Route shape open — `?follow=true` on `/events`, or a
sibling `/events/stream`. Reconnect carries a last-event-id for gap-fill.

**Verified state (as of 2026-05-29).** `route:GET /events`
(`code:lib/control/controlapi/events.go`) is poll-only (`since` / `until` / `cursor`).
No streaming variant anywhere. The QoL `rimsky watch` is a client-side poll loop.

**Design crux.**
- **Collides with `concept:cascade-graph`'s invariant** "all handlers run inside a short
  fresh transaction." A long-lived SSE connection is not a short transaction. The
  streaming handler likely lives *outside* the per-request short-tx model — it tails the
  event log and pushes, rather than serving one short read.
- **Shared poller, not N pollers.** A cluster-wide tail with many subscribers must not
  spawn one table-poll loop per connection. Design a single poller (per replica) that
  fans out new rows to connected subscribers.
- **Gap-fill = the existing cursor.** `last-event-id` maps to the `/events` cursor;
  on reconnect, replay from the cursor then resume the live tail. This reuses the
  poll-mode pagination as the catch-up path.
- **Cardinality / backpressure.** Bound per-connection buffers; decide drop vs.
  disconnect-slow-consumer policy. (Note: the *event log itself* is now durable per the
  console-upstream spec — this is about delivery backpressure, not about dropping
  persisted rows.)

**Concept touched:** `concept:cascade-graph` (the short-tx invariant gets a streaming
carve-out), `concept:observability`.

## F9 — Breakpoint-hit delivery

**Need.** Real-time delivery of breakpoint hits to the debugger UX, instead of polling
`route:GET /instances/{idOrKey}/breakpoint-hits`.

**Verified state.** Hits are HTTP-pollable today; there is no push and no
`breakpoint.hit` row on the events stream. `concept:breakpoint` owns the hit *ledger*;
only the supervisor writes hit rows; hit *delivery* is `concept:control-api`'s concern.

**Lean.** Have the supervisor emit a `breakpoint.hit` event-log row when it records a
hit, so F8's SSE delivers hits **uniformly** through the same stream. This is cheap and
**decouples cleanly from F8**: emit the `breakpoint.hit` row now and it is immediately
pollable on `/events`; push comes free once F8 lands. Lower priority — the debugger
already accepts polling as the fallback.

**Concept touched:** `concept:breakpoint` (delivery boundary), `concept:event-log`
(new `breakpoint.hit` kind).

## Open questions for the joint brainstorm

- Route shape (`?follow=true` vs `/events/stream`) and how it composes with the
  spine sketch's live-tail surface.
- Exact reconnect / gap-fill protocol and cursor semantics under streaming.
- Backpressure policy for slow/cluster-wide consumers.
- Whether `breakpoint.hit` emission ships ahead of F8 (pollable interim) or together.

## Relation to other work

- Deferred from `spec:2026-05-29-console-upstream-auth-audit-and-fixes`; that spec made
  the event log durable (synchronous writes), which this streaming layer reads from.
- Brainstorm with `sketch:2026-05-27-readonly-runscope-spine`, the co-owner of the
  live-tail surface.
