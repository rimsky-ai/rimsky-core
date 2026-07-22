---
tension: substitution-introspection-site-count
category: inconsistent
status: resolved
affects:
  - inertness
  - attribute
---

# "Single sanctioned introspection site" claim vs three actual sites

## What is muddy

`graph/attribute/substitution.go` and a casual reading of the claim-inertness rule can suggest there is exactly one sanctioned site where rimsky reads opaque bytes. The actual count is three:

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

- `concept:inertness` now carries the authoritative enumeration directly: it names the substitution engine's leaf extraction, the blob-spill movement between the inline column and the backend, and the shared matcher evaluator's primitive-equality `attrs.<path>` read as sanctioned sites, and states explicitly that "sanctioned read sites are precisely enumerated by the per-stream owning concepts." The three original sites this tension named (`walkPath`, `stringifyRaw`, `makeClaimHandle`) still exist, but the matcher-evaluator site is now also documented as sanctioned — a fourth site beyond the tension's original count.

## Resolution

`concept:inertness` was rewritten to carry the authoritative enumeration itself rather than leaving it implicit in a single package's comment: it names the substitution engine's leaf extraction, the blob-spill boundary, and the shared matcher evaluator's `attrs.<path>` read as the sanctioned sites, and states that each owning concept enumerates its own stream's exact sites (2026-05-04 through 2026-05-21, modeling-layer-contract / platform-extensions / attribute-overrides-matcher-overlay). This is exactly the resolution candidate the tension asked for — a single authoritative, cross-referenced enumeration owned by `concept:inertness` — so the "single site" framing this tension flagged as misleading is corrected at the source.
