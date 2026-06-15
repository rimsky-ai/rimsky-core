---
decision: force-upstream-refresh-via-receiver-keyed-map
status: as-is
---

# Force-upstream-refresh reuses the receiver-keyed edge map with subscription-flag input

## Choice

A receiver-keyed map of upstream node-types is built at registration from every `subscribes:` entry carrying `force_upstream_refresh: true`. The cascade walker consumes the map on receiver invalidation to proactively invalidate the named upstream so it re-runs in the same frame before the receiver dispatches. Cycle detection runs at registration; fan-out targets are rejected; same-receiver-to-same-sender pairs de-duplicate.

## Rationale

Keeping the upstream-refresh edge map separate from the per-edge subscription map keeps the cascade walker's consumption path linear in receivers and lets cycle detection run once at registration rather than per dispatch.
