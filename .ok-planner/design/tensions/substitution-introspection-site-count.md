---
tension: substitution-introspection-site-count
category: inconsistent
status: open
affects:
  - opacity
  - attribute
---

# "Single sanctioned introspection site" claim vs three actual sites

## What is muddy

`graph/attribute/substitution.go` and the casual reading of `@blessed-invariant 20` can suggest there is exactly one sanctioned site where rimsky reads opaque bytes. The actual count is three:

- `walkPath` (`graph/attribute/substitution.go`) — substitution leaf.
- `stringifyRaw` (`graph/attribute/substitution.go`) — top-level address/scope flattening.
- `makeStoreHandle` (`foundation/integration/runner_dispatch.go`) — wire-encoding into `google.protobuf.Struct`.

The third site is in a different package and is the most easily missed. `graph/attribute/substitution.go` does call it out as "one additional sanctioned exception" but the headline "single introspection site" framing is misleading.

## Why it matters

A code-review discipline that grep-checks for "introspection-adjacent calls" needs to know the three sites. A future fourth site needs to be either added to the sanctioned list (with a comment) or refused.

## Resolution candidates (do NOT pick)

- Update the substitution.go docstring to lead with "three sanctioned sites" rather than "single."
- Move all three sites under a uniformly-annotated helper package.
- Add a per-site annotation block listing all three for cross-reference.

## Evidence

- `_discover/2026-05-10-attribute-substitution-grammar.md` Observations bullet 3.
- `_discover/2026-05-10-opacity-of-userdata-claim-blob.md` Observations bullet 2.

