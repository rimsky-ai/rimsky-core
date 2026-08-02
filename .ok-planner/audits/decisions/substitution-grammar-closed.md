---
audit: substitution-grammar-closed
artifact: decision:substitution-grammar-closed
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:32:02Z
---

# The substitution grammar is a closed set of data-reference kinds; cascade shape is declared only on subscriptions

Supported. `resolveDirectiveValueRaw` in `lib/graph/attribute/substitution.go` switches over exactly six source kinds (`claim`, `params`, `nodes`, `messages`, `child`, `env`) with a default branch that rejects any other kind as an unknown source (`"unknown source kind " + parts[0]`), enforced identically at registration by the template validator's directive checker, which lists the same six kinds in its error message and rejects anything else. Cascade-shape information (`force_upstream_refresh`) exists only as a field on `subscribes:` entries (`SubscriptionEntry`/`SubscriptionEdge`), never as a directive-grammar token — no directive kind accepts or produces a cascade edge, confirmed by reading the full closed switch and the subscription-edge builder, which derives edges solely from declared `subscribes:` entries, not from substitution refs.
