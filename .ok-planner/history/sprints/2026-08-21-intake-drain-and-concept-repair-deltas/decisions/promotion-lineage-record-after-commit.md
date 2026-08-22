---
decision: promotion-lineage-record-after-commit
---

# A data-processing promotion's lineage record waits for the commit response

## Choice

For a claim whose producer speaks the data-processing protocol, rimsky writes the claim-terminal lineage record once, after the producer's commit response lands, carrying the version identifier that response returns (see `concept:lineage-record`, `concept:data-processing`). The record is not written at settlement and updated later.

## Rationale

Lineage records are append-only, and the version is the fact a lineage consumer of a promotion wants. Writing early costs the fact; writing late costs an ordering change at the settlement path, which nothing else depends on.

## Alternatives

- Write the record at settlement without the version, and narrow the lineage promise to the claim handle — rejected: a dashboard reading what happened before the producer answers is the only consumer it serves, and it leaves the ledger without the promotion's identity.
- Write at settlement and update the row when the version arrives — rejected: lineage rows are append-only.
