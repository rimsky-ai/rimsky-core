---
audit: sensor-cron
artifact: story:sensor-cron
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:35:00Z
---

# A declared schedule fires work with no external scheduler, and keeps its place across a restart

Supported. Nine checks against a deployment carrying the bundled cron sensor and
its own state store. Creating an instance mounted one live subscription for the
declared message type. With no operator message ever posted — the instance's
senders list holds the publisher and nothing else — a message arrived carrying
the expression the operator declared, a firing time on a whole minute, and no
missed windows, and the subscribed node ran once on it. Durability was measured
rather than assumed: with the sensor stopped before a window the subscription
had already recorded and started again once that window had passed, it sent a
message for the recorded window rather than the one that was next when it came
back, and the message's arrival time falls after the restart, so the revived
process sent it rather than anything salvaged from before the stop. That firing
drove the subscribed node too.
