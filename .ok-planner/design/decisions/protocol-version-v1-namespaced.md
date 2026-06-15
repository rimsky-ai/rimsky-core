---
decision: protocol-version-v1-namespaced
status: as-is
---

# Protocol versioning

## Choice

A single v1 protocol namespace for the entire contract surface: every proto package and every control-API HTTP route — including the executor async-callback surface and the observability sub-router — sits under one v1 namespace. No bare-path or version-omitted carve-outs (see `concept:control-api`, `concept:supervisor`).

## Rationale

Consistent versioned contract surface across the whole control-API; aligns the URL layer with the already-versioned protocol namespace (see `decision:pre-v1-break-freely`).

## Alternatives

Committing to bare paths indefinitely (leaves the existing versioned carve-outs as permanent exceptions); adding version-discovery + client-side negotiation (introduces a new mechanism not justified by current scope).
