---
decision: producer-declared-classes-capability
---

# Producers can declare an error-class vocabulary

## Choice

The claim-producer capabilities response carries a declared-error-classes field, mirroring the executor-observability declaration; the capabilities handshake stores it in the discovery cache alongside the executor vocabularies (see `concept:discovery-cache`). Producers MAY declare; declaring nothing remains legal.

## Rationale

The validator can only range-check vocabularies that are declared somewhere; the runtime routes producer-declared classes, so the declaration surface is the other half of that contract.

## Alternatives

- Executor-only vocabulary declaration — rejected: producer-class policy keys go silently unvalidated because the validator has nothing to range-check them against.
- Mandatory declaration — rejected: breaks producers with no error vocabulary; declaring nothing must stay legal.
