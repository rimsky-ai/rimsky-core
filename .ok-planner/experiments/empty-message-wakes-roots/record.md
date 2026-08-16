---
experiment: empty-message-wakes-roots
commit: PENDING
---

# One empty message wakes every structural root

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The template declares three structural roots, one node downstream of the first
root, and one node whose only subscription is to a message type nothing ever
sends. The script posts a message whose request body is `{}`, so the caller names
no type and supplies no envelope fields.

## What was observed

The send returned 201. All three structural roots dispatched exactly once. The
node with a declared upstream never dispatched. The node downstream of a root
dispatched by cascade. The message is a row in the same ledger the operator uses
for typed sends, carries the empty type, and opened a frame whose triggering
message is that row.

Nine checks, none failing.

RESULT: PASS
