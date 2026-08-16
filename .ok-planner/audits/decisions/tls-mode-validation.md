---
audit: tls-mode-validation
artifact: decision:tls-mode-validation
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:38:10Z
---

# The peer tls field as a parse-time validated two-value enum defaulting to off

Unsupported, on the default clause. The enum half is exactly as described: one parser accepts only the off and required values, returns a config error naming the field, the block, the peer and the offending value for anything else, and is called at all five peer-configuration blocks that carry the field — claim producers, executors, publishers, validators and data processors — so no block parses leniently and no third value survives anywhere. There is no opportunistic value in the accepted set, and a typo fails the load rather than silently downgrading, which is what both alternatives were rejected to prevent. What is false is that an empty value defaults to off. The default handed to the parser is not a constant: it is derived from the deployment-level peer-auth switch, so an empty tls field resolves to off only while peer auth is unset or none, and resolves to required as soon as peer auth is mutual TLS. That derivation is deliberate and is what makes the one-flip mutual-TLS posture work, so the code is not wrong — the decision's sentence is, because it states an unconditional default the configuration loader does not have.
