---
decision: kind-sugar-resolver
status: as-is
aliases: []
---

# Kind-field sugar at template registration

## Choice

Add a new optional kind field on the template node definition (alongside the existing required routing-key field, which is unchanged). At registration, a resolver maps the declared kind value to a pre-registered executor entry whose transport is the in-process transport and whose endpoint identity is the canonical in-process executor URL for that kind. The resolver consults a static kind-alias map populated alongside the in-process registry. A node may declare the kind field OR an explicit executor field but not both — mixing is a registration error. A node with neither falls through to the existing executor-resolution path (some nodes have no executor today, that path stays). Unknown kind values are rejected at registration with the same error class as unknown executors.

## Rationale

Ergonomic shorthand — template authors writing a kind value like loop-counter skip the executor-identity vocabulary. Picked the kind field name to avoid collision with the existing required routing-key field. Static map keeps registration deterministic; same authoring surface as the existing executor system.

## Alternatives

Auto-register utility executors as ordinary executor entries in the config layer; templates use the long form. Avoids a new template field but every utility-node reference is longer; the sugar form is a small schema change for a real ergonomic win. Overload the existing routing-key field with reserved utility-name values — would be magical and silently collide with template-author-chosen routing-key strings.
