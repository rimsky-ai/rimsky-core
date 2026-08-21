---
decision: lineage-identity-hashed-not-raw
---

# Lineage records carry hashed references, not payload bytes

## Choice

A lineage record references identity and substrate by content hash. A held-claim reference carries the hash of the claim-scope data rather than the data itself (see `concept:lineage-record`, `concept:inertness`).

## Rationale

A lineage projection is a broad, long-lived, operator-readable surface, and the bytes it would otherwise carry are inert streams rimsky never reads. A hash keeps the correlation the projection exists for — two records naming the same substrate join on it — while keeping opaque bytes out of a place many readers reach. A hash is also fixed in size, so a claim scope of any length produces the same record size.

## Alternatives

- Carry the raw bytes on the record — rejected: it copies an inert stream into a widely readable projection, and a record's size then follows a producer's selector length.
- Carry no reference at all — rejected: two records could no longer be joined on the substrate they touched, which is the question lineage answers.
