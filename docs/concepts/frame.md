---
concept: frame
definition: |
  The unit of cascade resolution. A frame begins when a node receives an invalidate and ends when no node remains in `stale` or `running` for the instance. The template's `frame_resolution:` field decides how concurrent invalidates are handled — `serial_queue` (each invalidate produces its own frame; frames run one at a time) or `coalesce` (new invalidates merge into a single pending row).
proto_symbol: (none)
config_field: (none)
api_surface: (none)
related: [cascade, instance, node-state, invalidate]
deprecated_terms: []
---

# Frame

## Definition

The unit of cascade resolution. A frame begins when a node receives an invalidate and ends when no node remains in `stale` or `running` for the instance. The template's `frame_resolution:` field decides how concurrent invalidates are handled — `serial_queue` (each invalidate produces its own frame; frames run one at a time) or `coalesce` (new invalidates merge into a single pending row).

## Why it exists

A reactive cascade can be triggered while a previous cascade is still working. Without a notion of "what work belongs to which trigger," operators cannot answer "is this instance settled?" or "did my invalidate finish propagating?" Frames make these questions answerable: every dispatched run carries a frame ID, and once no node remains in `stale` or `running` for the instance, the frame is resolved.

Frames also gate concurrency at the instance level: at most one frame is `running` per instance. New invalidates that arrive while a frame is in flight are handled by the template's `frame_resolution:` policy.

The frame ID is a correlation token, not a structural lock. Workers within a frame run concurrently when their claim and lock acquisitions don't conflict.

## Frame resolution: `serial_queue` vs `coalesce`

`frame_resolution:` is a required scalar field at the top of every template spec. It picks between two policies for handling new invalidates that arrive while a frame is in flight:

- **`serial_queue`** preserves ordering. Each invalidate produces its own frame; frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B").
- **`coalesce`** preserves the latest input. While a frame is in flight, new invalidates merge into a single pending row; when the in-flight frame ends, that one merged row dispatches. Right answer when only the latest input matters (e.g. "recompute the dashboard from the current data").

The two modes never mix within an instance — the policy is template-level.

## How you encounter it

- **Templates**: the `frame_resolution:` field at the template's top level. Required; one of `serial_queue` or `coalesce`.
- **Observability**: every event in the event log carries the `frame_id` of the frame that produced it. Dashboards group activity by frame.
- **Frame end**: computed at every scheduler tick by checking whether any node in this instance is still in `stale` or `running`. When none remain, the active frame transitions to `resolved`.

## Consumer-visible guarantees

- Frame end is monotonic: once a frame is resolved, no in-flight work from it remains.
- A new invalidate arriving when a frame is `running` does not start a second concurrent frame for the same instance — it queues or coalesces per the template's frame-resolution policy.
- Operator-originated invalidates do not preempt running work in either mode. The current frame always runs to completion.
- Under `serial_queue`, frames execute in arrival order. Under `coalesce`, the merged frame represents the most recent invalidate at the time the in-flight frame ended.
- Frames are never backfilled: a missed schedule fire does not retroactively create a frame; the schedule advances from the recorded next-fire-at, not from the wall clock.

## Common mistakes

- **Rimsky's frame ≠ stack frame, video frame, UI frame.** A Rimsky frame is the unit of cascade resolution for an instance; nothing to do with call stacks, animation, or screen rendering.
- Treating frame ID as a sequence number with strong ordering. It's a UUID; ordering across frames is captured by the timestamps of frame-start events, not by ID arithmetic.
- Assuming frames span instances. A frame is per-instance; two instances of the same template have entirely separate frame populations.
- Treating `coalesce` as a debouncer. Coalesce merges all pending invalidates into one frame regardless of timing; it does not delay dispatch waiting for a quiet period.
- Expecting `serial_queue` to give strong ordering across instances. The ordering guarantee is per-instance; two instances of the same template execute independently.

## See also

- [`cascade.md`](cascade.md)
- [`instance.md`](instance.md)
- [`node-state.md`](node-state.md)
