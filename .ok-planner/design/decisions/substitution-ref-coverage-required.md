---
decision: substitution-ref-coverage-required
---

# Every substitution ref must be covered by a subscription entry

## Choice

Every substitution ref in a node's attribute schema must be matched by at least one subscription entry whose sender and type would deliver the corresponding signal; registration rejects templates with uncovered refs. Applies to per-field attribute reads and whole-pull reads.

## Rationale

The "no orphan reads" guarantee survives by static validation rather than by silent edge generation; cascade edges become exactly what the author wrote.

## Alternatives

- Silently generate a cascade edge for each uncovered ref — rejected: the edge set no longer matches what the author wrote, and a typoed ref manufactures an unintended edge instead of a registration error.
- Leave uncovered refs to fail at dispatch time — rejected: turns a statically detectable authoring error into a per-dispatch runtime surprise.
