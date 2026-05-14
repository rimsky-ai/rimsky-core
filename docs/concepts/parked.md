---
concept: parked
definition: |
  A non-terminal node state representing "work paused; will resume on time, signal, or watchdog." Distinct from `failed` (no auto-recovery) and `running` (work in flight). Held claims persist across the park boundary; cascade does not propagate from parked nodes.
proto_symbol: Park in protocols/proto/v1/executor.proto
config_field: rimsky.yml:nodes
api_surface: POST /admin/instances/{i}/nodes/{n}/invalidate
related: [node-state, executor, frame, holding-subgraph, claim-handle]
deprecated_terms: []
---

# Parked

## Definition

A non-terminal node state representing "work paused; will resume on
time, signal, or watchdog." A node enters `parked` when its executor
emits the `Park` terminal event; it exits via one of three
paths:

1. **Time-based wake** — when `Park.resume_at` is set, the
   `SweepParkedNodes` sweep transitions the node back to `stale`
   (and the node_run row from `parked` to `pending`) when
   `resume_at` has passed; the next supervisor tick re-dispatches the
   executor with a `ResumeContext` carrying back the original
   `payload` and `session_token` plus `resume_reason:
   "deadline_elapsed"`.
2. **Signal-based wake** — an in-graph or admin invalidate against
   the parked node transitions it back to `stale` and re-dispatches
   on the next tick with `resume_reason: "external_invalidate"`.
   Both paths run through the unified invalidate handler.
3. **Watchdog timeout** — when the node's `max_park_duration` is set
   and `parked_at + max_park_duration < now()`, the watchdog forces
   the node to `failed` with `error_class: "park_timeout"`.

## Why it exists

Some work is genuinely paused, not failed. A rate-limit retry that
will succeed in 60 seconds is wasted as a retry-loop; an awaiting-
human-decision step is blocked, but not erroneous. Modeling these as
`failed` (which requires `give_up`-driven retry policy or
operator-driven invalidate) loses the distinction between "stuck on
something legitimate" and "broke." Parked is the orthogonal axis.

## Held claims and the park boundary

Claims acquired before the park stay live across the park boundary:

- The orphan-claim reaper (foundation invariant 6) skips
  `phase='parked'` rows because heartbeating is paused during park.
- The held-claim auto-terminal mechanism still fires correctly: a
  parked node remains an `active` member of the holding subgraph, and
  resolution waits for it to complete or fail just like any other
  holding-subgraph member.
- On resume, the node re-acquires its dispatch slot but does not
  re-Open the claims — it still owns them.

## ResumeContext

When the runner re-dispatches a parked node, `ExecuteRequest.resume_context`
is populated:

```protobuf
message ResumeContext {
  bytes  payload        = 1;  // verbatim from Park.payload
  string session_token  = 2;  // verbatim from Park.session_token
  string resume_reason  = 3;  // "deadline_elapsed" | "external_invalidate"
}
```

Executors use these to resume external work — for example, the
`claude-agent` reference impl uses `session_token` as the Claude CLI's
`--resume <session_id>` argument.

## "Human review = indefinite park"

A common pattern: an executor produces a tentative output, then parks
indefinitely (no `resume_at`) with `reason: "human_review"`. Operators
inspect the parked node via the diagnostics endpoints, take whatever
action is needed externally, and call `POST /admin/instances/{i}/nodes/{n}/invalidate`
to wake the node. The resumed dispatch sees `resume_reason:
"external_invalidate"` and can read context from any side-channel the
operator wrote (e.g. an attribute set by another node in the same
graph).

## Antipattern: mid-frame human review

Parking a frame on review serializes parallel work in the same frame
and creates long-lived held frames. The recommended idiom is
**post-frame review**: the producing frame runs to completion; review
happens externally; a follow-on graph or instance kicks off the
post-review work. Frame-blocking review is supported and works
correctly, but should be reserved for cases where downstream genuinely
cannot proceed safely without approval (e.g. cross-system commit
where the alternative is to reverse-cascade after the fact).

## Diagnostics

- `GET /admin/diagnostics/parked-nodes` — every currently-parked
  node, optional `?reason=<name>` filter.
- `GET /admin/diagnostics/held-frames` — frames with at least one
  parked node, grouped by `frame_id`.
- `POST /admin/instances/{instance}/nodes/{node_id}/invalidate` —
  signal-based wake; returns 409 if the node is in `running` or
  `failed`.
