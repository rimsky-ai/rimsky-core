---
decision: validator-learns-producer-classes
---

# Error-type policy accepts producer vocabularies

## Choice

The template validator range-checks the node's error-type policy keys against the union of the executor's declared classes, the synthetic acquire-side error family, a broader set of runtime-synthesized classes covering schema and resolution failures the runtime itself can raise, and the declared error classes (see `decision:producer-declared-classes-capability`) of every producer reachable from the node's claims. A key attributable to no declared vocabulary becomes an advisory warning surfaced in the registration and validation responses (see `story:validation-warnings-surfaced`), not a hard rejection (see `concept:error-policy`).

## Rationale

The runtime already routes acquisition failures by producer-declared class; a validator rejecting what the runtime routes locks operators out of the system's own classification — and producers that declare nothing must not lock their operators out either.

## Alternatives

- Range-checking only against the executor's declared classes and the runtime-synthesized families, ignoring producer vocabularies — rejected: the validator would flag exactly the keys the runtime's acquisition-failure routing honors.
- Hard-rejecting keys attributable to no declared vocabulary — rejected: producers that declare no classes would lock their operators out of writing acquisition policy at all; an advisory warning preserves typo detection without the lockout.
