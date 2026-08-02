---
decision: kind-sugar-resolver
---

# Kind-field sugar at template registration

## Choice

An optional kind field on the template node definition, distinct from the required routing-key field. At registration, a resolver consults a static kind-alias map (populated alongside the in-process registry) to map the declared kind to a pre-registered in-process executor entry. The kind field and an explicit executor declaration are mutually exclusive, unknown kinds are rejected at registration like unknown executors, and a node declaring neither follows the ordinary executor-resolution path.

## Rationale

Ergonomic shorthand — template authors writing a kind value like loop-counter skip the executor-identity vocabulary. The kind field name avoids collision with the existing required routing-key field. A static map keeps registration deterministic; same authoring surface as the existing executor system.

## Alternatives

Auto-register utility executors as ordinary executor entries in the config layer; templates use the long form. Avoids a new template field but every utility-node reference is longer; the sugar form is a small schema change for a real ergonomic win. Overload the existing routing-key field with reserved utility-name values — would be magical and silently collide with template-author-chosen routing-key strings.
