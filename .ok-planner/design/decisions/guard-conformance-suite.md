---
decision: guard-conformance-suite
status: as-is
---

# Wrong-claimant is a provable no-op

## Choice

The driver-parity suite (the cross-driver test library run against both persistence drivers) carries a guard suite: for every mutating claim-handle and node-run ownership operation, acting as the wrong supervisor must change nothing — asserted identically against both drivers.

## Rationale

The claimant-guard helper (see `decision:claimant-guard-helper`) cannot catch a future function that bypasses it; the behavioral proof can. Together they close each other's blind spot.

## Alternatives

- Rely on the claimant-guard helper alone — rejected: a helper enforces nothing on a mutation path that bypasses it.
- Duplicate wrong-claimant tests per persistence driver — rejected: the copies drift, and a driver-specific gap hides until it bites.
