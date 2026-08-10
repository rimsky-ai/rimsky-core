---
audit: multi-hard-dep-rendezvous
artifact: story:multi-hard-dep-rendezvous
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# Two force-refreshed upstreams each run once and the receiver runs once

Supported. A template declaring a receiver with two `force_upstream_refresh`
subscriptions was driven through the control API, with both upstreams wired so
nothing else in the template could run them. The receiver's invalidation pulled
both into the frame, each dispatched exactly once, and the receiver dispatched
exactly once with both upstream values in its outcome. The instance then reached
rest with no live runs, so neither upstream re-ran and the shape did not
livelock — the two failure modes the story names.
