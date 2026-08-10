---
experiment: lenient-marker
commit: PENDING
---

# A lenient directive resolves to empty where the unmarked one fails the dispatch

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` and drives
it through the control API. Two templates are identical except for the lenient
marker on one substitution directive. Each declares a trigger node that always
runs, an optional upstream node whose only subscription is to a message type
nothing ever sends, and a receiver that reads the optional upstream's attribute
and also carries a property of its own. Nothing can give the optional upstream a
value, so the receiver's read is a missing source at dispatch time.

## What was observed

In both templates the optional upstream never ran. Without the marker the
receiver settled `terminal/error/template_resolution_failed`, and the error
named the directive it could not resolve. With the marker the same receiver
dispatched and settled successfully; its resolved bag carried the marked
property as the empty string and its own unrelated property at its declared
value.

Seven checks, none failing.
