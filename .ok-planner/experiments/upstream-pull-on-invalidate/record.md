---
experiment: upstream-pull-on-invalidate
commit: d977250c
---

# Force-refresh declaration pulls the sender current

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The script registers the same template twice, differing only in the
`force_upstream_refresh` value on one subscription. The template declares a
trigger node that is a structural root, a `pulled` node whose only subscription
is to a message type nothing ever sends, and a receiver that subscribes to both
and reads an attribute of `pulled`. Nothing but the force-refresh declaration can
bring `pulled` into the frame.

## What was observed

With `force_upstream_refresh: true`, the operator message woke the trigger, the
receiver's invalidation pulled `pulled` into the same frame, `pulled` dispatched
exactly once, and the receiver dispatched afterwards carrying the value `pulled`
had just produced.

With `force_upstream_refresh: false`, `pulled` never dispatched, and the receiver
settled with the error class `template_resolution_failed` because its source had
no value.

Four checks, none failing.
