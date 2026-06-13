---
decision: tls-mode-validation
status: as-is
---

# The tls value is a validated enum

## Choice

The `tls` config field is parse-time validated, accepting exactly `off | required` (empty defaults to `off`); `optional` and any other value are config errors.

## Rationale

Opportunistic TLS is not a real gRPC client mode; a documented third value with no honest semantics is surface noise. Pre-v1, deletion over deprecation.
