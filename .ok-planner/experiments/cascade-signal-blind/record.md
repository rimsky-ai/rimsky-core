---
experiment: cascade-signal-blind
commit: PENDING
---

# Subscribing to every cascade-firing signal kind

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The template declares two senders and four receivers. A counter node emits
`terminal/success` and `attribute/count/changed`. An `http-node` node aimed at an
unreachable address emits `terminal/error/http/network_error`. Each receiver
declares one `subscribes` entry: one on the success terminal, one on the
attribute-changed signal, one on the error wildcard, and one on the error
wildcard with a CEL `when` predicate.

The script then registers a second template whose only difference is a
subscription on `transient/park`.

## What was observed

All three cascade-firing signal kinds were emitted, and all four receivers
dispatched exactly once each. Every subscription entry used the same three keys
plus the optional predicate key. The second template was rejected at registration
with an error naming `transient/park` and stating that transient signals are
audit-only.

Seven checks, none failing.
