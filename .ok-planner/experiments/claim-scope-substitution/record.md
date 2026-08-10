---
experiment: claim-scope-substitution
commit: PENDING
---

# One spelling for claim-scope substitution, end to end

## What it ran against

`run.py` boots a `rimsky-all-in-one` container from this tree's image with the
bundled filesystem claim producer configured over a bind-mounted workspace, and
drives it through the control API. The template is one node that acquires a
claim on that producer and declares a single attribute whose source is the
claim-scope directive; the node's executor is the built-in attribute
passthrough, so whatever the directive resolved to comes back on the node's
terminal event. The same template shape is registered twice more — once with
the abbreviated spelling, once with a deliberately non-canonical selector.

## What was observed

With the canonical spelling `{{claim.<alias>.claim_scope}}` the node acquired,
dispatched and settled carrying `data/inbox` — byte for byte the
`claim_scope_data` the claim-handle ledger recorded for that live claim. With
the selector written non-canonically as `./data/inbox/`, the directive still
resolved to `data/inbox`, so the value follows the producer's canonical claim
scope rather than the template's selector text. Registering the same template
with the abbreviated `{{claim.<alias>.scope}}` was refused with HTTP 400 and a
validation error naming the offending directive, the attribute path it sits on,
and the three second segments the grammar admits; re-registering the canonical
form on the identical shape succeeded.

Six checks, none failing.
