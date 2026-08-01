---
decision: protocol-version-v1-namespaced
status: as-is
---

# Protocol versioning

## Choice

A single v1 protocol namespace for the entire contract surface: every proto package and every control-API HTTP route — the executor async-callback surface and the observability sub-router included — sits under one v1 namespace, with no bare-path or version-omitted carve-outs (see `concept:control-api`, `concept:supervisor`).

## Rationale

Consistent versioned contract surface across the whole control-API; aligns the URL layer with the already-versioned protocol namespace (see `decision:pre-v1-break-freely`).

## Alternatives

- Bare (version-omitted) HTTP paths on part of the surface — rejected: splits the contract into versioned and unversioned halves that must be reconciled at the first breaking change.
- Version discovery plus client-side negotiation — rejected: a new mechanism not justified by current scope.
