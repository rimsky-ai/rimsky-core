---
decision: substitution-ref-coverage-required
status: as-is
---

# Every substitution ref must be covered by a subscription entry

## Choice

Every substitution ref in a node's attribute schema must be matched by at least one subscription entry whose sender and type would deliver the corresponding signal; registration rejects templates with uncovered refs. Applies to per-field attribute reads and whole-pull reads.

## Rationale

The "no orphan reads" guarantee survives by static validation rather than by silent edge generation; cascade edges become exactly what the author wrote.
