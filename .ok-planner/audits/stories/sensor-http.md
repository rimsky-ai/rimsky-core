---
audit: sensor-http
artifact: story:sensor-http
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Polling an HTTP source sends only what changed, and remembers it across a restart

Supported. Four poll subscriptions against one static JSON document behaved as
the story promises. The unfiltered watch sent a message carrying the response
status, the URL, the decoded body and a hash of it, and its node ran; rewriting
the document sent a second message with the new body and a different hash. With
the document then left alone, a newly created instance's own first poll message
is what shows the poller still running, and across it the first instance stayed
at two messages — and it stayed at two across a restart of the sensor container
as well, measured the same way. The watch narrowed by a JSON-path match sent
exactly one message, the body satisfying the match, while the unfiltered watch on
the same URL had by then sent all three bodies. The watch on a URL that never
answers with success sent nothing, and so did a watch routed through a sensor
with no egress allowlist, whose log records the refused dial — reaching a
private-network target requires the operator to allowlist the range.

## Compliance

The capability clause prescribes the mechanism — "persist the last-seen body
across restart" names storage and the comparison that drives the decision, which
the story rules place in decision territory; the compliant text states the
promise instead: "and not re-send a body that has not changed, across a restart
of the sensor".
