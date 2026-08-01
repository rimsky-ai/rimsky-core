---
decision: depguard-protocols-purity
status: as-is
---

# Protocols module import surface

## Choice

The protocols module imports only the stdlib, the official gRPC and protobuf libraries, the chosen UUID library, and the chosen YAML library — no rimsky-internal modules or layers and no test infrastructure — enforced by dependency lint.

## Rationale

The protocols module is the public implementer contract: every dependency in its graph lands in every external implementation's build, so the budget stays minimal and rimsky-internal code stays out entirely.

## Alternatives

- Letting the protocols module reach into the foundation module for shared helpers — rejected: external implementers would build rimsky-internal code just to speak the contract.
- No enforced budget — rejected: dependency creep on a public contract surface is one-way; the lint keeps every addition deliberate.
