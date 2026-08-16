---
experiment: held-abandon-cascades-abandoned
commit: d977250c
---

# A downstream subscriber hears the abandoned signal when held work rolls back

## What it ran against

`run.py` boots a `rimsky-all-in-one` container from this tree's image with the
bundled filesystem claim producer configured over a bind-mounted workspace, and
drives it through the control API. The template declares an acquirer that opens
a claim on that producer, a co-holder that holds the same claim and calls an
unreachable URL so its work fails, and three non-member subscribers on the
acquirer: one subscribed to `terminal/error/abandoned`, one to the error-family
pattern `terminal/error/*`, and one to `terminal/success`.

## What was observed

The co-holder's failure rolled the claim back with a single
`claim_resolution.abandon`, and the acquirer emitted exactly one terminal
signal, `terminal/error/abandoned`. Both the exact-signal subscriber and the
error-family subscriber ran, each with one `work_started` at a sequence number
after the abandon, so each learned of the rollback at the moment the held work
was abandoned. The success subscriber never ran.

Seven checks, none failing.
