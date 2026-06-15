---
decision: substitution-context-builder-reads-drained-rows
status: as-is
---

# Substitution-context builder reads drained wait-set rows

## Choice

The substitution-context builder reads drained wait-set rows per `concept:wait-set`; row presence is the key, irrespective of which subscription-flag combination caused the row to land.

## Rationale

Keying on row presence keeps the builder uniform across every cascade-flag combination — the two-flag distinction is captured implicitly by whether the row exists at all, so the builder does not need a per-flag branch.
