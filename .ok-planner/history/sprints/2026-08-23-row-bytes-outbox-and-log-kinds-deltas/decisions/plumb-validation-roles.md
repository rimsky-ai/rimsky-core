---
decision: plumb-validation-roles
---

# The validation mix-in works for every service kind

## Choice

The validation-registry dial walks every service kind — stores, executors, and publishers alongside claim producers — and honors the declared validation-supported roles identically for each: publishers declare them on their capability surface, executors on the executor-observability capabilities message (see `story:validation-mixin-uniform`).

## Rationale

The wire contract implies all service kinds; two of three silently ignoring the field is a gap, not a design. The executor-side declaration is a compatible proto extension of the same shape as `decision:producer-declared-classes-capability`.

## Alternatives

- Validation roles honored only for claim-producer services, with the field ignored on the other kinds — rejected: a declared capability two service kinds silently drop advertises a contract the runtime does not honor.
