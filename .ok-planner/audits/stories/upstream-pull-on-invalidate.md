---
audit: upstream-pull-on-invalidate
artifact: story:upstream-pull-on-invalidate
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# Template author pulls an upstream fresh when the receiver is invalidated

Supported. A `subscribes:` entry carries `force_upstream_refresh: true`, which (per `concept:node-subscription`) drags the named sender into the same frame before the receiver dispatches. `test/scenarios/per_run_attributes/hard_dep_test.go` carries two cases under this story's citation: `TestPerRunAttributes_HardDepPullsUpstream` re-invalidates receiver `c`'s upstream cascade-driven trigger and asserts `c` observes both the pulled-upstream `b`'s and the natural-upstream `a`'s second-fire values; `TestPerRunAttributes_HardDepPullsUpstream_DirectInvalidateOfReceiver` invalidates `c` directly (a message wake, not a cascade from `a`) and asserts the `force_upstream_refresh: true` edge to `b` still pulls `b` fresh into the same frame, with `c` observing `b`'s new value. Both cases assert on the persisted attribute bag actually produced, not just that a dispatch occurred.
