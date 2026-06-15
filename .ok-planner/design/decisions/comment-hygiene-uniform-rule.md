---
decision: comment-hygiene-uniform-rule
status: as-is
---

# Comment-hygiene uniform tag-or-delete rule

## Choice

Comment-hygiene violations are resolved by tag-or-delete on a per-site basis. A comment is tagged with `@constraint:`, `@deliberate:`, `@agent-contract`, or one of the project-extended design-citation tags (`@concept:`, `@story:`, `@decision:`) when it encodes a load-bearing why an agent or contributor would otherwise lose. A comment is deleted as residue otherwise. The doc-residue cluster overrides this rule with a reshape-first evaluation per `decision:doc-residue-reshape-pass`.

## Rationale

Plumbline's thesis is that load-bearing prose must be mechanically distinguishable from generation residue, and the structured-tag vocabulary is the project's existing surface for that distinction. Uniform per-site application keeps the rule simple and the lint enforceable; the cluster taxonomy the lint surfaces is for sampling and prioritization, not for parallel rules.

## Alternatives

Per-cluster bespoke rules (different action vocabulary for divider vs commented-out-code vs prose) — rejected because the cluster heuristic is for grouping by shape, not for licensing different categorical actions; the per-site decision is the same tag-or-delete in every case.
