---
decision: tls-mode-validation
status: as-is
---

# The tls value is a validated enum

## Choice

The peer TLS config field is parse-time validated, accepting exactly off-or-required; empty defaults to off; opportunistic and any other value are config errors.

## Rationale

Opportunistic TLS is not a real gRPC client mode; a documented third value with no honest semantics is surface noise. Pre-v1, deletion over deprecation.

## Alternatives

- Keep `opportunistic` as an accepted third value — rejected: no honest client-mode semantics stand behind it; it would document a behavior the transport cannot deliver.
- Lenient parsing that maps unknown values to a default — rejected: a typo would silently downgrade the intended security posture instead of failing at parse time.
