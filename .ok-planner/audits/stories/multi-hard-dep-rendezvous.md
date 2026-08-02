---
audit: multi-hard-dep-rendezvous
artifact: story:multi-hard-dep-rendezvous
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# Template author relies on multi-hard-dep rendezvous

Supported. `TestMultiHardDepRendezvous` deploys a template with a receiver node subscribing to two force-refreshed (`force_upstream_refresh: true`) upstreams plus one ordinary trigger, fires a second wake, and asserts across a full second frame that: each of the two upstreams and the receiver dispatch exactly once (no mutual re-seeding tail), the receiver's dispatch reads both upstreams' fresh values in the same run, and the receiver dispatches only after both upstreams have settled. This end-to-end assertion is backed by the upstream-refresh pull in the runtime (`pullForceRefreshUpstreams`), which probes each named upstream for an in-flight run and, when none exists, checks a settled-this-frame guard before re-seeding — the mechanism that prevents the livelock the story disclaims.
