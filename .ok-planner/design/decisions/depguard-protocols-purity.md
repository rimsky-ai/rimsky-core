---
decision: depguard-protocols-purity
status: as-is
---

# Protocols module import surface

## Choice

Stdlib plus the official gRPC and protobuf libraries, the chosen UUID library, and the chosen YAML library only.

## Rationale

The protocols module is the public implementer contract — minimal, independent.
