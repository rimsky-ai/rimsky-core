---
story: sensor-http
status: as-is
---

# Operator wires poll-driven HTTP message

## Role

As an operator wiring a poll-driven message into a workflow, I can use the bundled `sensor-http` to poll a URL at a fixed interval, emit a message when the upstream returns success (optionally filtered by response body), and persist polling state so a restart preserves the schedule, so that I poll an external HTTP source without writing a custom publisher.

## Capability

Bundled `sensor-http` publisher: fixed-interval URL polling; optional response-body filter; durable polling state across restart.

## Business value

Operators poll an external HTTP source without writing a custom publisher; polling state survives restart so windows don't get skipped or doubled.

## Acceptance

A `sensor-http` instance polling a real upstream at a configured interval emits exactly one message per interval-tick when the upstream returns 200; downstream nodes consume the message; with a body-filter declared, only responses matching the filter produce messages. State persists across restart.

## Falsifier

Polling skips a window, OR the body filter is declared but unused, OR a process restart drops the polling watermark.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
