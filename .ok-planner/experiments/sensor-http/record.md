---
experiment: sensor-http
commit: PENDING
---

# Polling an HTTP source, sending only what changed, and remembering it across a restart

## What it ran against

A private docker network carrying a `rimsky-all-in-one` orchestrator, a
`rimsky-sensor-http` container with a Postgres state store and a private-range
egress allowlist, a second `rimsky-sensor-http` container with no allowlist, and
a static file server whose one JSON document the run rewrites from the host. The
template declares four poll subscriptions against the same sensor pair: an
unfiltered watch on the document, a watch on the same document narrowed by a
JSON-path match, a watch on a URL that does not exist, and a watch routed to the
un-allowlisted sensor. `run.py` builds and removes everything. Re-run unchanged at this tree.

## What was observed

All four subscriptions mounted live. The unfiltered watch sent a message
carrying the response status, the URL, the decoded body and a hash of it, and
the subscribed node ran on it. Rewriting the document produced a second message
with the new body and a different hash.

With the document then left alone, a second instance was created; its own first
poll message is what shows the poller kept running, and across that the first
instance stayed at two messages. Restarting the sensor container and creating a
third instance repeats the observation after a process restart: the third
instance's watch sent its first message while the first instance still held
exactly two.

Rewriting the document to satisfy the declared JSON-path match produced the
filtered subscription's only message, whose body is the matching one, while the
unfiltered watch on the same URL had by then sent all three bodies. The watch on
the URL that never answers with success sent nothing across the whole run, and
so did the watch routed to the sensor with no egress allowlist, whose log
records the dial failure — a private-network poll target is refused until the
operator allowlists the range.
