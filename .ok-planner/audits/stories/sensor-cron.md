---
audit: sensor-cron
artifact: story:sensor-cron
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# A declared cron expression fires work on schedule and keeps its place across a restart

Supported. A template declaring one publisher of kind `cron` with the expression
`* * * * *` mounted a live subscription on a fresh instance, and with no operator
message ever posted the sensor sent a message echoing that expression, timed to a
whole minute, with no missed windows; the subscribed node ran on it. Nothing but
the orchestrator, the bundled cron sensor and its state store was running, so no
external scheduler carried the schedule. Durability was measured by stopping the
sensor container before the window its subscription had recorded and starting it
again after that window had passed: it sent a message for the recorded window
rather than the next one, and the message arrived after the restart, so the
revived process fired it. That firing drove the subscribed node too. What the run
does not cover is the CLI's YAML template path, which cannot express a
publisher's raw-JSON `config` at all; the template was registered through the
control API's template route instead.
