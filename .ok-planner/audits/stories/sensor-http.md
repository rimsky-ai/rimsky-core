---
audit: sensor-http
artifact: story:sensor-http
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:35:00Z
---

# Polling an external source, sending only what changed, and still not re-sending after a restart

Supported. Eleven checks against a deployment carrying the bundled HTTP sensor,
its own state store and a document the run rewrites from outside. All four
declared subscriptions mounted live. The unfiltered watch sent a message
carrying the status the upstream returned, the location polled, the decoded body
and a hash of it, and the subscribed node ran on it; rewriting the document
produced a second message with the new body and a different hash. Leaving the
document alone showed no re-send: a second instance's own first message is what
proves the poller kept polling, and across that the first instance stayed at two
messages — and the same observation repeats after the sensor process was
restarted, a third instance sending its first message while the first still held
exactly two. A subscription narrowed by a body match sent only the body
satisfying it, while the unfiltered watch on the same location had by then sent
all three. A watch on a location that never answers with success sent nothing
across the whole run, and so did a watch routed to a sensor with no egress
allowlist, whose log records the refused dial — a private-range target stays
unreachable until the operator opens it.

## Compliance

The body prescribes storage — "persist the last-seen body across restart" — which belongs to a decision; the outcome clause that follows it already carries the need, so compliant text says the sensor does not re-send an unchanged body even after a restart.
