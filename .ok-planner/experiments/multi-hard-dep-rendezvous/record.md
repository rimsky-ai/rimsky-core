---
experiment: multi-hard-dep-rendezvous
commit: d977250c
---

# Two force-refreshed upstreams rendezvous on one receiver

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The template declares a receiver with three subscriptions: one on a trigger node
that is a structural root, and two carrying `force_upstream_refresh: true` on
`left` and `right`. Both `left` and `right` subscribe only to a message type
nothing ever sends, so the force-refresh declarations are the only thing that can
run them.

## What was observed

The operator message woke the trigger. The receiver's invalidation pulled both
`left` and `right` into the frame. Each ran exactly once. The receiver dispatched
exactly once, with both upstream values in its outcome. The instance reached rest
with no live runs, so neither upstream re-ran and the shape did not livelock.

Four checks, none failing.
