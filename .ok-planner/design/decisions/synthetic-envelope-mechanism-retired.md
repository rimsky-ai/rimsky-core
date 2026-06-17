---
decision: synthetic-envelope-mechanism-retired
status: as-is
aliases: []
---

# Synthetic-envelope mechanism retired

## Choice

The four runtime-internal type-paths and the chokepoint helper that synthesized them all retire. The wake-node-id and wait-set-pair payload fields disappear from the wire. The receipt-side reserved-field check and the registration-side reserved-property check both retire. The frame engine's wake-node-id reader at promotion retires. Receivers wake exclusively via the subscriber-side cascade walker consulting the augmented inverse-edge map.

## Rationale

The chokepoint exists only to serve callers that bypass the subscriber-side cascade. With all four callers retired (instance-create becomes idle; the asset-materialize endpoint retires; the node-reset endpoint trims to no-wake; the test-harness invalidate helper migrates to debug-channel), the chokepoint has no remaining role. Keeping it would be dead code with structural surface, including the load-bearing reserved-field and reserved-property checks.

## Alternatives considered

Keep the chokepoint for future use — dead code; keep it specifically for asset-materialize — rejected per `decision:asset-materialize-endpoint-retired`.
