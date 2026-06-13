---
decision: plumb-validation-roles
status: as-is
---

# The validation mix-in works for every peer kind

## Choice

The all-peer-kind validation-registry dial (which walks stores, executors, and publishers) plumbs the declared validation-supported roles from the publisher capability surface (where the field already existed) and from the executor side via a validation-supported-roles field on the executor-observability capabilities message, wiring both identically to claim-producer peers (see `story:validation-mixin-uniform`).

## Rationale

The wire contract implies all peer kinds; two of three silently ignoring the field is a gap, not a design. The executor-side proto addition follows the same compatible-extension pattern as `decision:producer-declared-classes-capability`.
