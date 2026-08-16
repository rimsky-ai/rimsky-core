---
experiment: breakpoint-debugger
commit: d977250c
---

# Debugging a live instance through breakpoints

## What it ran against

`way-debug-session.py` boots a `rimsky-all-in-one` container at
`RIMSKY_IMAGE_TAG` and drives it through the control API. The template declares
a counter node and a worker node that subscribes to it and reads its count. The
script installs one pause-mode pre-dispatch breakpoint on the worker, then walks
the whole debugging session: read the hit off the ledger, read the same hit off
the unified event log, resume it with an attribute overlay, and delete the
breakpoint.

## What was observed

Fourteen checks, none failing. The breakpoint installed against the worker's
before-dispatch checkpoint and read back off the instance. The worker's first
dispatch stopped there: the hits ledger carried one hit naming the worker's node
and the sealed dispatch bag (`seen=1`, `note=unmodified`), and the run it was
holding read `running` on the node-runs view. The unified event log carried the
same hit as one `breakpoint.hit` record naming the breakpoint and the node.

Resuming that hit with an overlay of `note=overlaid-by-operator` returned
`first_resume: true`, and the re-fired dispatch settled with
`{"note": "overlaid-by-operator", "seen": 1}` — the overlay applied, and nothing
it did not name changed.

Deleting the breakpoint answered 204, removed it from the instance's breakpoint
list, and emptied the hits ledger; the event-log record of the hit survived the
deletion.

RESULT: PASS
