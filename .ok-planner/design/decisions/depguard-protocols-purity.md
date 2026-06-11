---
decision: depguard-protocols-purity
status: as-is
---

# Protocols module import surface

## Choice

Stdlib + grpc + protobuf + uuid + yaml.v3 only.

## Rationale

The protocols module is the public implementer contract — minimal, independent.
