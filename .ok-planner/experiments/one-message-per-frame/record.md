---
experiment: one-message-per-frame
commit: PENDING
---

# One message per frame under a burst

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`). The template declares one
message type and one node substituting that type's field. Two instances of it
run the same burst, one in `backlog` mode and one in `coalesce` mode: a
pause-mode `before_dispatch` breakpoint holds the first frame open while three
distinctly-labelled messages arrive, then the run resumes every hit until the
queue drains. It then reads the frame list, the message history and the event
log.

## What was observed

The backlog instance delivered all three messages across three frames, each
delivered message naming a distinct frame and each frame's triggering message
being one of the three. Its node settled three times, resolving one label per
run in arrival order, and no run recorded a template-resolution failure. Every
frame the burst opened names the one declared message type.

The coalescing instance delivered two messages across two frames, again one
message per frame, and its node resolved one label per run. Neither mode put two
bodies in one frame, so no run had to choose between them.

Eight checks, none failing.

RESULT: PASS
