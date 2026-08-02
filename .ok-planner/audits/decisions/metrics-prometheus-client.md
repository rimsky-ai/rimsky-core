---
audit: metrics-prometheus-client
artifact: decision:metrics-prometheus-client
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:34Z
---

# Metrics export

Supported. `lib/control/observability/metrics.go` builds a
`prometheus.Registry` via `github.com/prometheus/client_golang`, registers
13 counters/gauges/histograms, and serves them through
`promhttp.HandlerFor` mounted at `GET /metrics` by `MountMetrics`, which is
wired into the scheduler's HTTP server
(`lib/control/launch/scheduler.go`). No OpenTelemetry SDK or push-based
(StatsD) client is present anywhere in `go.mod`. `TestMetricsHandler_Smoke`
exercises the mounted handler end-to-end.
