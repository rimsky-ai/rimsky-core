---
story: sensor-http
---

# Operator wires poll-driven HTTP message

## Story

As an operator wiring a poll-driven message into a workflow, I can use the bundled HTTP sensor to poll a URL at a fixed interval, send a message when the upstream returns success and the response body has changed since the last poll (optionally filtered by response body), and persist the last-seen body across restart so an unchanged body doesn't re-send, so that I poll an external HTTP source without writing a custom publisher.
