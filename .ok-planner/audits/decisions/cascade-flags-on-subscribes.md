---
audit: cascade-flags-on-subscribes
artifact: decision:cascade-flags-on-subscribes
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# Cascade-behavior flags live on subscription entries

Supported. `lib/foundation/spec/subscription.go::SubscriptionEntry` carries `ForceUpstreamRefresh *bool` as a field of the subscription-entry shape; a repo-wide search finds no `cascade_deps` block anywhere in the codebase and no `ForceUpstreamRefresh`-shaped field on the attribute/substitution-ref grammar (`lib/graph/attribute`) — both rejected alternatives are genuinely absent, not merely undocumented. `test/scenarios/per_run_attributes/hard_dep_test.go` and `test/scenarios/all_upstream_gating_test.go` both exercise multiple substitution reads riding one subscription edge, consistent with the rationale that the flag belongs to the edge, not the per-read reference.
