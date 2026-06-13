---
decision: producer-declared-classes-capability
status: as-is
---

# Producers can declare an error-class vocabulary

## Choice

The claim-producer capabilities response carries a declared-error-classes field, mirroring the executor-observability declaration; the capabilities handshake stores it in the discovery cache alongside the executor vocabularies (see `concept:discovery-cache`). Producers MAY declare; declaring nothing remains legal.

## Rationale

The validator can only range-check vocabularies that are declared somewhere; the runtime already routes producer-declared classes, so the declaration surface is the missing half of an existing contract.
