---
decision: synthetic-envelope-mechanism-retired
---

# No synthetic-envelope wake mechanism

## Choice

Receivers wake exclusively via the subscriber-side cascade walker consulting the augmented inverse-edge map. There is no synthetic-envelope chokepoint: no runtime-internal type-paths, no wake-node-id or wait-set-pair payload fields on the wire, no receipt-side reserved-field or registration-side reserved-property checks guarding them, and no frame-engine wake-node-id reader at promotion.

## Rationale

A synthetic-envelope chokepoint exists only to serve callers that bypass the subscriber-side cascade, and no such caller exists: instance creation is idle, there is no asset-materialize endpoint (`decision:asset-materialize-endpoint-retired`), node reset does not wake, and the test harness drives wakes through legitimate triggers. A chokepoint kept anyway would be dead code with structural surface, dragging along the reserved-field and reserved-property checks as standing obligations.

## Alternatives

- Keep the chokepoint for future callers — rejected: dead code with structural surface and two reserved-name checks nobody exercises.
- Keep it solely for asset materialization — rejected per `decision:asset-materialize-endpoint-retired`.
