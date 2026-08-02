---
audit: named-lock-metric
artifact: story:named-lock-metric
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:43:58Z
---

# Operator can graph and alert on named-lock acquisitions alongside claim acquisitions

Supported. `acquireNamedLock` increments the named-lock acquisition counter with `intent="unavailable"` when a name is over capacity and the acquisition batch increments it again with `intent="acquired"` once a named-lock acquisition commits, so both the saturation signal and the success signal are counted. `TestMetricsHandler_Smoke` in `lib/control/observability/metrics_test.go` exercises the `/metrics` HTTP endpoint and asserts `rimsky_named_lock_acquisitions_total` is present in the scrape output alongside `rimsky_claim_acquisitions_total`, giving an operator a scrapeable counter to graph or alert on rather than reconstructing saturation from events. The story does not commit to any particular metric-family shape (that's `decision:named-lock-metric`'s concern), only that the capability exists — which it does.
