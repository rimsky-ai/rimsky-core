---
decision: scratch-column
status: as-is
aliases: []
---

# Node-run scratch persistence column triple

## Choice

Add an inline-bytes column, a handle column, and a handle-backend column for executor scratch to the node-run row, mirroring the existing parked-payload triple. Spill follows `concept:blob-backend` per the existing pattern. Default value is empty.

## Rationale

Reuses the inert-payload column pattern (parked payload, named-event payloads); persistence-layer code stays uniform; the same blob-backend abstracts inline vs. spilled handle.
