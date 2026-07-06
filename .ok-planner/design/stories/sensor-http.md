---
story: sensor-http
status: as-is
---

# Operator wires poll-driven HTTP message

## Role

As an operator wiring a poll-driven message into a workflow, I can use the bundled HTTP sensor to poll a URL at a fixed interval, send a message when the upstream returns success (optionally filtered by response body), and persist polling state so a restart preserves the schedule, so that I poll an external HTTP source without writing a custom publisher.

## Capability

Bundled HTTP sensor publisher: fixed-interval URL polling; optional response-body filter; durable polling state across restart.

## Business value

Operators poll an external HTTP source without writing a custom publisher; polling state survives restart so windows don't get skipped or doubled.

## Acceptance

An HTTP-sensor instance polling a real upstream at a configured interval sends exactly one message per interval-tick when the upstream returns 200; downstream nodes consume the message; with a body-filter declared, only responses matching the filter produce messages. State persists across restart.

## Falsifier

Polling skips a window, OR the body filter is declared but unused, OR a process restart drops the polling watermark.

## Proof

Executable proof.
