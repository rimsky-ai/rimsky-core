---
audit: tag-based-subscription
artifact: decision:tag-based-subscription
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:40Z
---

# Named-event subscription is terminal/* plus a CEL tag filter

Supported. `lib/foundation/signal/taxonomy.go` carries no `event/<name>` leaf anywhere in its canonical emit or subscription pattern lists (`canonicalEmitPatterns` covers only `terminal/`, `transient/`, and `attribute/*/changed`), and `ValidateSubscriptionType` rejects any subscription type-path outside that taxonomy, confirmed by `TestValidateSubscriptionType_RejectsMessageTypePath` and the taxonomy tests generally. `lib/foundation/signal/payloads.go` puts a `Tags []string` field on the terminal payload structs, so `payload.tags` is available to a subscription's `when:` CEL expression for a `terminal/*` subscription; the earlier `event/<name>` parsing form is explicitly retired, confirmed by `parseSubstitutionDirective`'s rejection of the `nodes.<n>.emit.event.<name>.field` shape in `lib/graph/node/subscription_edges_test.go`. `lib/graph/node/subscription_tag_gate.go::validateSubscriptionDeclaredTags` (carrying the decision's own citation) further validates at template-registration time that every tag literal referenced in a subscription's `payload.tags` filter is one the sender's executor actually declares, closing the loop between the tag-based filter and the terminal-tags mechanism.
