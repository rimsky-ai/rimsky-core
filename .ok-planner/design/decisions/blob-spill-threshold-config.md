---
decision: blob-spill-threshold-config
status: as-is
---

# Spill threshold control

## Choice

Whether an oversized payload spills to the blob backend is governed by a per-deployment configurable byte threshold; the default configuration stores all payloads inline with no spill backend.

## Rationale

Payload profiles and blob-backend costs differ per deployment; a tunable threshold lets each deployment pick its own inline-vs-spill point without a code change.

## Alternatives

- A fixed built-in threshold — rejected: no single cutoff suits both tiny-payload and large-artifact deployments.
- Spill every payload to the blob backend — rejected: forces a blob round trip for small values that fit inline.
