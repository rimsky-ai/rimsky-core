---
story: sensor-http
status: as-is
---

# Operator wires poll-driven HTTP message

## Role

As an operator wiring a poll-driven message into a workflow, I can use the bundled HTTP sensor to poll a URL at a fixed interval, send a message when the upstream returns success and the response body has changed since the last poll (optionally filtered by response body), and persist the last-seen body across restart so an unchanged body doesn't re-send, so that I poll an external HTTP source without writing a custom publisher.

## Capability

Bundled HTTP sensor publisher: fixed-interval URL polling with per-poll body-change detection (dedup on unchanged content); optional response-body filter; durable body state across restart.

## Business value

Operators poll an external HTTP source without writing a custom publisher; polling state survives restart so windows don't get skipped or doubled.

## Acceptance

An HTTP-sensor instance polling a real upstream at a configured interval sends a message when the upstream returns 200 and the response body differs from the last-sent body; an unchanged body across repeated polls produces no repeat message; with a body-filter declared, only responses matching the filter are eligible to produce messages. Body state persists across restart, so a restart does not re-send an unchanged body.

## Falsifier

An unchanged body produces a repeat message, OR a genuinely changed body produces no message, OR the body filter is declared but unused, OR a process restart drops the persisted body state and re-sends an unchanged body.

## Proof

Executable proof.
