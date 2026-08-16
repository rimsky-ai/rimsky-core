---
experiment: message-schema
commit: d977250c
---

# Declared message types and what happens to undeclared ones

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`). The template declares two
message types, each with a body schema, and one node per type reading that
type's field. The run sends conforming messages, undeclared ones, and
non-conforming bodies, then reads the instance's message history and event log.

## What was observed

Both declared types are accepted with conforming bodies and each reaches the
node that subscribes to it. A message of an undeclared type is refused at the
send with HTTP 400; the response names the type it refused and lists the two
types the template declares. Three non-conforming bodies — wrong field type,
missing required field, undeclared extra field — are each refused against the
declared body schema.

The instance's history holds only the two accepted messages, so nothing refused
entered the bus, and the event log carries no dead-letter row. A template whose
node reads a message type it never declared is refused at registration, naming
the substitution reference and the undeclared type.

Eleven checks, none failing.

RESULT: PASS
