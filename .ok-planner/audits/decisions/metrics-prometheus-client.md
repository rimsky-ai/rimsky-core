---
audit: metrics-prometheus-client
artifact: decision:metrics-prometheus-client
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether metrics are exported through the official Prometheus client on a scrapeable endpoint

Supported. The root manifest pins the official client library and a manifest fitness test fails if the pin disappears. One package owns the export surface: it builds a registry, declares 43 collector registrations, and serves the standard exposition handler at the conventional scrape path, mounted both on the control API's router and on a dedicated per-role metrics listener the launch path starts for the scheduler and its siblings. Nothing competes with it — no source file imports an OpenTelemetry metrics SDK or a push-style client, so the scrape endpoint is the only export path, which is what the rationale's graceful-degradation argument rests on.
