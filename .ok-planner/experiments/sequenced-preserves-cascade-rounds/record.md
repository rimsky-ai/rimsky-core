---
experiment: sequenced-preserves-cascade-rounds
commit: PENDING
---

# Sequenced cascade mode across several rounds

A sequenced receiver can fall behind its sender in two shapes, so this directory
holds two runnable ways. Both boot a `rimsky-all-in-one` container at
`RIMSKY_IMAGE_TAG` (or reuse the control API named by `RIMSKY_CONTROL_API_URL`)
and drive it through the control API. Both templates declare a counter node and
an observer node with `cascade_mode: sequenced` that subscribes to the counter
and reads its count, so each observer dispatch names the round it ran on.

## way-receiver-held.py

### What it runs against

The counter emits one round per invalidation. The script installs a pause-mode
pre-dispatch breakpoint on the observer, so the observer's round 1 is dispatched
and then held. While it is held, the script invalidates the counter twice through
the debug-override channel, queuing rounds 2 and 3. Deleting the breakpoint
releases everything.

### What was observed

The counter produced rounds 1, 2, 3. The observer dispatched three times, seeing
1, then 2, then 3. The dispatches ran in arrival order.

Five checks, none failing.

## way-back-to-back-rounds.py

### What it runs against

The counter self-subscribes on its `loop` tag with `max: 4`, so it emits four
rounds back to back inside one frame. The observer is gated by the counter's own
in-flight run for the whole burst, so all four rounds queue before any observer
dispatch happens. Nothing pauses or invalidates anything.

### What was observed

The counter produced rounds 1, 2, 3, 4. The observer dispatched four times, and
each dispatch saw the count of its own round, so no round was coalesced away. The
dispatch order was 4, 1, 2, 3: the newest round ran first, then the earlier rounds
in order. This is reproducible and scales with the round count — with `max: 3` the
order is 3, 1, 2, and with `max: 5` it is 5, 1, 2, 3, 4.

Four checks, one failing: the arrival-order check.
