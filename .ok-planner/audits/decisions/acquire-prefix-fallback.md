---
audit: acquire-prefix-fallback
artifact: decision:acquire-prefix-fallback
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:26Z
---

# Generic acquire-family error_types policy still catches a producer's classified acquisition failures

Supported. `lib/runtime/runner_error_policy.go::lookupPolicyForNodeWithFallback` (carrying the decision's own citation tag) tries the producer-declared exact class first and, only if absent, the single synthetic family class (`acquire/unavailable` for handler `handleAcquireUnavailable`, `acquire/producer_error` for `handleAcquireProducerError`, both in `lib/runtime/runner_acquire_error_policy.go`); when neither resolves, `node.Evaluate` (`lib/graph/node/policy.go`) falls through to the unknown-error-class default (`give_up`). `test/scenarios/producer_class_routing_test.go` proves this end to end with two scenarios against a real producer that declares its own class (`pg/claim_unavailable`): `TestProducerClassRouting_ExactMatch` (operator declares the exact class) and `TestProducerClassRouting_PrefixFallback` (operator declares only the generic `acquire/unavailable` key) both retry correctly rather than giving up, driven through a live worker node against the stub producer.
