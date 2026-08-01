---
decision: cascade-inside-settlement
status: as-is
---

# The parent-settlement cascade fires inside settlement

## Choice

The parent-settlement cascade bridge fires within whichever settle primitive is closing the child execution context — carry or aggregate — not alongside it at call sites (see `concept:child-execution`).

## Rationale

No caller can settle without cascading; firing the bridge alongside the primitive at call sites would be exactly the class of defect this excludes.

## Alternatives

- Fire the cascade bridge at each settle call site alongside the primitive — rejected: any call site that forgets is exactly the defect class the choice excludes.
