---
audit: template-subscriptions
artifact: story:template-subscriptions
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:40Z
---

# Template author wires CEL-predicated subscriptions

Supported. `lib/foundation/signal/taxonomy.go::ValidateSubscriptionType` accepts an exact canonical type-path or a trailing-`*` prefix and rejects positional wildcards, explicit-park targets, and non-canonical paths; it is invoked from template validation (`lib/graph/node/template_validator.go`) and from the breakpoint API, so every subscription entry on every node is checked against the same rule. `lib/foundation/signal/cel.go::CompileWhenWithBodyFields` compiles an optional `when:` CEL expression against a `type`/`payload` environment, and `lib/graph/node/subscription_edges.go::edgeFromSubscription` wires the compiled predicate into the runtime's per-sender subscription-edge map that gates cascade dispatch. End-to-end coverage in `test/scenarios/subscription_cascade_test.go` (six scenarios) exercises both exact and trailing-wildcard type-paths (`terminal/*`, `terminal/error/*`) together with `when:` predicates, including a self-subscription loop that only continues while the predicate holds and quiesces once it flips false — demonstrating the promised "fire only when a matching signal's payload satisfies the predicate" behavior.
