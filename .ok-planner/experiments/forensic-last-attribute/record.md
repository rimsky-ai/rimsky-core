---
experiment: forensic-last-attribute
commit: d977250c
---

# The node's most recent resolved attribute bag, read directly

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` and drives
it through the control API. The template's first node cascades to itself and
dispatches three times, so it has a history of bags rather than one; the second
node reads the first node's attribute. Two read surfaces are asked for the
latest bag: the node route and the observability node route.

## What was observed

Six checks, none failing. The looping node dispatched three times, emitting the
deltas `{count: 1}`, `{count: 2}`, `{count: 3}`. Both read surfaces answered
with the same resolved bag, `{count: 3, max: 3}` — the most recent one, not an
earlier dispatch's, and including the input value `max` that no delta ever
carried. No single event in the log carries that bag, so an operator working
from the event log alone would have to fold the deltas together and add the
resolved inputs. The second node's latest bag came back the same way and
carried its own resolved values, `{note: "latest", seen_count: 3}`.
