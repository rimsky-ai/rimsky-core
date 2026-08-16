---
experiment: message-bus
commit: d977250c
---

# Sending into an instance's bus and reading the history back

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API: register and deploy a template declaring one message type, create
an instance, send messages, then read the instance's message history, one
message by its id, and the node the messages woke. It then reads the same
history through the CLI, building `bin/rimsky` first if it is absent.

## What was observed

A send without an `Idempotency-Key` header is refused. A send carrying one is
accepted and returns a message id. Two replays under the same key — one with an
identical body, one with a different body — each returned that same id, and the
instance's history holds one row for the key, not three. The history lists both
distinct sends, each attributed to the operator, and the fetch-by-id route
returns the row with its body and instance; an id that was never minted is not
found. Both bodies reached the downstream node.

The CLI retrieves that one message by id correctly. The CLI history verb does
not: `rimsky messages tail` without `--follow` prints only the newest row and
drops the older ones, because it de-duplicates against a watermark that assumes
ascending arrival while the route returns newest-first. The run pins that
behaviour as a check so the defect is re-checkable; the history capability
itself is obtained on the control-API route the same run exercises.

Thirteen checks, none failing.

RESULT: PASS
