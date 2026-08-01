---
decision: metrics-prometheus-client
status: as-is
---

# Metrics export

## Choice

Metrics are exported with the standard Prometheus Go client library, exposing a scrapeable endpoint.

## Rationale

Prometheus exposition is the de-facto standard scrape format; the official client keeps the export surface consumable by any Prometheus-compatible collector without an adapter layer, and scrape-based export means an absent collector costs nothing rather than blocking or dropping.

## Alternatives

- OpenTelemetry metrics SDK — rejected: a heavier dependency plus a collector hop for a project whose observability need is a scrapeable endpoint.
- StatsD-style push metrics — rejected: push requires a running aggregator before anything is observable; scrape degrades gracefully when nothing collects.
