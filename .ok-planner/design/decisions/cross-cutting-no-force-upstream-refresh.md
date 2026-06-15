---
decision: cross-cutting-no-force-upstream-refresh
status: as-is
---

# Cross-cutting subscriptions cannot carry force_upstream_refresh

## Choice

A cross-cutting subscription (`instance: true`) carrying `force_upstream_refresh: true` is rejected at registration.

## Rationale

Cross-cutting subscriptions are sender-agnostic; there is no specific upstream for the refresh to invalidate.
