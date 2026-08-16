---
assumption: metrics-on-bundled-services
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# because the three core roles each get a metrics port, the eleven bundled services also expose Prometheus metrics on a port, at `/metrics`.

As operator building dashboards, I would take it that because the three core roles each get a metrics port, the eleven bundled services also expose Prometheus metrics on a port, at `/metrics`.

## Source

sibling-symmetry — `RIMSKY_METRICS_PORT_SCHEDULER` / `_SUPERVISOR` / `_CONTROL_API` with no sensor or executor equivalent

## What a run would observe

scrape each bundled service container for a `/metrics` endpoint on any exposed port.

## Measured

Experiment `assumption-metrics-on-bundled-services` (eight checks, none failing)
scraped a `rimsky-all-in-one` container with its metrics listener opened, and
all eleven bundled service images given the same `RIMSKY_METRICS_HOST` and
`RIMSKY_METRICS_PORT` variables an operator would carry over. The prior does not
hold. The three core roles each served prometheus text at `/metrics` on their
own port, and the control API's own port answered 404 there — the metrics
listener is a separate port, not a route on the API. No bundled service served
metrics anywhere: across every published port of the ten services with
listeners, `/metrics` returned prometheus text nowhere; the port the variables
named was never opened by any of them, every probe refused; the openlineage
subscriber opens no port at all; and no service image declares a metrics port in
its `EXPOSE` set. An operator building dashboards gets the three core roles and
nothing from the sensors, executors, producers or the subscriber — and the
variables that open the core's listener are silently inert on every one of them.
