---
audit: producer-class-routing
artifact: story:producer-class-routing
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# A template's error_types keys a producer-declared class exactly, with the acquire/* family as fallback

Supported. The template validator (`validateErrorTypes` in `lib/graph/node/template_validator.go`, annotated `@decision: validator-learns-producer-classes`) accepts an `error_types` key that exactly matches (or prefix-matches a `*`-suffixed pattern in) any claim producer reachable from the node's `claim_producers:` block, enumerated via `RequiredClaimProducers`. At runtime, `handleAcquireUnavailable`/`handleAcquireProducerError` (`lib/runtime/runner_acquire_error_policy.go`) look up the policy keyed on the producer-declared class first (`acq.UnavailableClass`/`acq.ProducerErrorClass`, sourced from the producer's `Unavailable.error_class` or gRPC `ErrorInfo.Reason`), falling back to the synthetic `acquire/unavailable`/`acquire/producer_error` family when the producer names nothing. This exact chain is exercised end-to-end by `test/scenarios/producer_class_routing_test.go`: `TestProducerClassRouting_ExactMatch` configures a producer that declares `pg/claim_unavailable` and a template that keys `error_types` on that exact class, asserting registration raises no vocabulary warning and the retry policy fires on it; `TestProducerClassRouting_PrefixFallback` runs the same producer against a template keyed only on the generic `acquire/unavailable` fallback and asserts the same routing succeeds.
