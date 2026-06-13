---
decision: parity-expansion
status: as-is
---

# The driver-parity suite covers what the runtime depends on

## Choice

The driver-parity suite (the cross-driver test library run against both persistence drivers) covers every queue, claim-handle, and frame behavior the runtime depends on, executed against both drivers; the wrong-claimant guard suite (see `decision:guard-conformance-suite`) is one slice of it.

## Rationale

Two large hand-mirrored drivers drift; parity by test beats parity by review.
