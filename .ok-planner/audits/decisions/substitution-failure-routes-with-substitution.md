---
audit: substitution-failure-routes-with-substitution
artifact: decision:substitution-failure-routes-with-substitution
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:32:02Z
---

# Substitution failures are classified and routed to the receiver's error-class policy inline, in the transaction where substitution ran

Supported. At gate-eval, `evaluateOneGate` calls `routeSubstitutionFailureAtGate` on a classifiable substitution error (`ErrMissingSource` or an attribute-schema validation error) within the same `tx` the gate evaluator itself runs in, which classifies the error into `template_resolution_failed` or `template_validation_failed`, appends the event, transitions the row, and calls `applyErrorPolicy` inline — no propagation to a later phase. For non-cascade rows, the equivalent failure surfaces at claim-substitution/acquire time (`handleAcquireLockSpecSubstitutionFailed` in `lib/runtime/runner_acquire_error_policy.go`), opened in its own dispatch-time transaction, classified to the same `template_resolution_failed` class and routed through the same `applyErrorPolicy` machinery. A scenario test (`idempotent_mode_substitution_failure_routes_first_test.go`) asserts the failure surfaces as the named event and drives the node to its declared `give_up` policy rather than leaving it stuck pending.
