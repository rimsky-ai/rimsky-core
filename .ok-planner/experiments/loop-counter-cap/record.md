---
experiment: loop-counter-cap
commit: PENDING
---

# Bounded iteration with the bundled loop-counter node kind

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The template declares one node of kind `loop_counter` with a `max` input
attribute and a self-subscription filtered on `"loop" in payload.tags`. The
script runs the template twice, once with `max: 4` and once with `max: 1`, and
reads the tags and counts off the event log.

## What was observed

With `max: 4` the node dispatched four times, emitting counts 1, 2, 3, 4 tagged
`loop`, `loop`, `loop`, `done`. With `max: 1` the node dispatched once, emitting
count 1 tagged `done`. Both instances reached rest with no live runs, so
iteration stopped at the cap in each case. The template names no executor of its
own.

Six checks, none failing.
