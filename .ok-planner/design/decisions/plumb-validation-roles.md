---
decision: plumb-validation-roles
---

# The validation mix-in works for every peer kind

## Choice

The validation-registry dial walks every peer kind — stores, executors, and publishers alongside claim producers — and honors the declared validation-supported roles identically for each: publishers declare them on their capability surface, executors on the executor-observability capabilities message (see `story:validation-mixin-uniform`).

## Rationale

The wire contract implies all peer kinds; two of three silently ignoring the field is a gap, not a design. The executor-side declaration is a compatible proto extension of the same shape as `decision:producer-declared-classes-capability`.

## Alternatives

- Validation roles honored only for claim-producer peers, with the field ignored on the other kinds — rejected: a declared capability two peer kinds silently drop advertises a contract the runtime does not honor.
