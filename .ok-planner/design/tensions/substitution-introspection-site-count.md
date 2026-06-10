---
tension: substitution-introspection-site-count
category: inconsistent
status: open
affects:
  - inertness
  - attribute
---

# "Single sanctioned introspection site" claim vs three actual sites

## What is muddy

`graph/attribute/substitution.go` and the casual reading of `@blessed-invariant 20` can suggest there is exactly one sanctioned site where rimsky reads opaque bytes. The actual count is three:

- `walkPath` (`graph/attribute/substitution.go`) — substitution leaf.
- `stringifyRaw` (`graph/attribute/substitution.go`) — top-level address/scope flattening.
- `code:runtime/runner_dispatch.go::makeClaimHandle` — wire-encoding into `google.protobuf.Struct`.

The third site is in a different package and is the most easily missed. `graph/attribute/substitution.go` does call it out as "one additional sanctioned exception" but the headline "single introspection site" framing is misleading.

## Why it matters

A code-review discipline that grep-checks for "introspection-adjacent calls" needs to know the three sites. A future fourth site needs to be either added to the sanctioned list (with a comment) or refused.

## Resolution candidates (do NOT pick)

- State in `concept:inertness` (and cross-reference from `concept:attribute`) that there are three sanctioned introspection sites, not one, so the documented count matches reality.
- Record the three-site enumeration as a single authoritative list owned by `concept:inertness`, so the sanctioned set is stated once rather than scattered.
- Have `concept:inertness` require each sanctioned site to carry a cross-referencing annotation that names all three, so the set stays mutually discoverable.

## Evidence

- `_discover/2026-05-10-attribute-substitution-grammar.md` Observations bullet 3.
- `_discover/2026-05-10-opacity-of-userdata-claim-blob.md` Observations bullet 2.
