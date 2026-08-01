---
story: sensor-http
status: as-is
---

# Operator wires poll-driven HTTP message

## Story

As an operator wiring a poll-driven message into a workflow, I can use the bundled HTTP sensor to poll a URL at a fixed interval, send a message when the upstream returns success and the response body has changed since the last poll (optionally filtered by response body), and persist the last-seen body across restart so an unchanged body doesn't re-send, so that I poll an external HTTP source without writing a custom publisher.

Bundled HTTP sensor publisher: fixed-interval URL polling with per-poll body-change detection (dedup on unchanged content); optional response-body filter; durable body state across restart.

Operators poll an external HTTP source without writing a custom publisher; polling state survives restart so windows don't get skipped or doubled.
