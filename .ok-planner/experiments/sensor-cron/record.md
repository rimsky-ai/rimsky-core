---
experiment: sensor-cron
commit: d977250c
---

# A cron expression declared in a template fires work, and keeps its place across a restart

## What it ran against

A stack of three containers on a private docker network from this tree's own
image tag: a `rimsky-all-in-one` orchestrator whose mounted config names one
publisher endpoint, a `rimsky-sensor-cron` container holding its firing state in
a Postgres container. The template declares one message type, one node
subscribed to it, and one publisher of kind `cron` whose config is the
expression `* * * * *`. Everything is driven over the control API; `run.py`
creates the network and containers and removes them. Re-run unchanged at this tree.

## What was observed

Creating an instance mounted one live `cron` subscription for the declared
message type. With no operator message ever posted to the instance, a message
arrived from the publisher carrying the declared expression, a `fire_at` on a
whole minute, and no missed windows, and the subscribed node ran once on it.

For the durability way the run waits for the top of a minute, creates a second
instance, and stops the sensor container before the window the subscription had
just recorded. Once that window is past, the sensor is started again. It sent a
message for the recorded window — not the window that was next when it came
back — and the message's arrival time is after the restart, so it was sent by
the revived process rather than salvaged from before the stop. That firing drove
the subscribed node as well.

The run also shows what the template file cannot carry: a publisher's `config`
is a raw JSON field, and the CLI's YAML template path cannot express it (a
mapping and a JSON string are both rejected, and a `!!binary` tag is dropped by
the CLI's own YAML round trip), so this experiment registers templates through
the control API's template route instead.
