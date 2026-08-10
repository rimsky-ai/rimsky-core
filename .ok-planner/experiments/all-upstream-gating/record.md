---
experiment: all-upstream-gating
commit: PENDING
---

# Fan-in receiver waits for every in-flight upstream

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The template declares a fan-in receiver over two upstreams whose staleness
arrives by different routes. `slow_root` is a structural root woken by the
operator message. `cascaded` is woken by a cascade from a third node, and the
script later restales it a second time with an operator invalidation through the
debug-override channel.

The script installs a pause-mode pre-dispatch breakpoint on `slow_root`, so that
upstream stays in flight while the other upstream settles twice. Deleting the
breakpoint releases it.

## What was observed

While `slow_root` sat at the breakpoint, `cascaded` settled once by cascade and
once more by operator invalidation, and the receiver never dispatched. After the
breakpoint was deleted and `slow_root` settled, the receiver dispatched, and no
receiver dispatch preceded the last upstream settlement. The receiver's outcome
carried both upstream values.

Seven checks, none failing.
