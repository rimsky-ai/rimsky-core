---
decision: tls-mode-validation
---

# The tls value is a validated enum

## Choice

The service TLS config field is parse-time validated, accepting exactly off-or-required; opportunistic and any other value are config errors. An empty field inherits the deployment's service-auth posture: off while service auth is unset or none, required once service auth is mutual TLS (see `decision:service-auth-mtls`).

## Rationale

Opportunistic TLS is not a real gRPC client mode; a documented third value with no honest semantics is surface noise. Pre-v1, deletion over deprecation. Deriving the empty field from the deployment posture lets an operator enable mutual TLS once and harden every service that never named a TLS mode, instead of editing each service block.

## Alternatives

- Keep `opportunistic` as an accepted third value — rejected: no honest client-mode semantics stand behind it; it would document a behavior the transport cannot deliver.
- Lenient parsing that maps unknown values to a default — rejected: a typo would silently downgrade the intended security posture instead of failing at parse time.
- A fixed empty-means-off default independent of the deployment posture — rejected: enabling mutual TLS would then leave every unedited service block plaintext, which is the opposite of what enabling it asks for.
