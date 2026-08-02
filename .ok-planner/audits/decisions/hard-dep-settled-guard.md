---
audit: hard-dep-settled-guard
artifact: decision:hard-dep-settled-guard
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# The hard-dep pull carries a settled-this-frame guard

Supported. `pullForceRefreshUpstreams` (tagged `@decision: hard-dep-settled-guard`) probes each named upstream for an in-flight run; only when none exists does it call `HasRunForNodeInFrame` and skip re-affirmation (`continue`, no new pending, no wait-set insert) if the upstream already settled in the current frame — still-running or freshly re-created upstreams are unaffected, matching the decision exactly. `TestMultiHardDepRendezvous` is the regression pin the rationale names: it asserts each of two force-refreshed upstreams and their shared receiver dispatch exactly once per frame across two full wake cycles, with a bounded-quiesce dispatch-count check (`awaitStableDispatchCounts`) that would fail the test outright on a mutual re-seeding tail.
