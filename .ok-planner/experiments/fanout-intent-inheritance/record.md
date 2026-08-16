---
experiment: fanout-intent-inheritance
commit: d977250c
---

# Sub-claims opened by a fan-out carry the intent the template declared

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with the
bundled filesystem claim producer configured over a throwaway workspace, and
drives it through the control API. One template declares a node holding a claim
with `intent: r` and fanning that claim into three partitions; a second is the
same template with `intent: rw`. The claim-handle read surface reports each
handle's intent and its parent handle, and the event log records the intent of
every acquisition the run made.

## What was observed

Under `intent: r` the run opened one parent handle with intent `r` and three
sub-handles, every one of them pointing at that parent and every one of them
carrying intent `r`. All four acquisitions the run recorded named intent `r` and
no other value.

Under `intent: rw` the same template shape produced one parent and three
sub-handles all carrying `rw`. The intent the sub-claims carry therefore tracks
what the template declared rather than a fixed producer default.

Eight checks, none failing.
