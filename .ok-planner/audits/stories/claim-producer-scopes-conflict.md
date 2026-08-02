---
audit: claim-producer-scopes-conflict
artifact: story:claim-producer-scopes-conflict
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# The scopes-conflict capability's overlap rule gates both top-level and fan-out sub-claim acquisition

Supported. `evaluateClaimScopeConflict` (`lib/runtime/runner_acquire_claims.go`) calls the producer's `ScopesConflict` for every existing claim holder on the producer when the producer advertises `supports_scopes_conflict` (falling back to byte-equal otherwise), and the fan-out sub-claim path (`lib/runtime/runner_subclaim.go`) makes the same call when opening sub-claims, so both acquisition paths the story names are covered. An end-to-end scenario test (`lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go`) exercises this against a real gRPC producer that implements prefix-containment overlap (`lib/services/test/overlapproducer`): a top-level case where scopes `"tenant/a"` and `"tenant/a/x"` are not byte-equal but overlap under the producer's rule, asserting exactly one of the two writers acquires and the other's dispatch fails on the rejection; and a fan-out case where sibling sub-claim partition keys `"a"` and `"a/x"` overlap, asserting the whole sub-claim acquisition transaction is rejected (zero committed sub-claim rows). This directly demonstrates the no-overlapping-writers rule being enforced under the producer's own (non-byte-equal) overlap definition.
