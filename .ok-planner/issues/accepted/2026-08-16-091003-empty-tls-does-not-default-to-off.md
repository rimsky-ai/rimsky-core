---
issue: empty-tls-does-not-default-to-off
kind: audit
category: conflicting
artifacts:
  - decision:tls-mode-validation
status: verified
opened: 2026-08-16T09:10:03Z
---

# An empty peer TLS setting does not default to "off" — it follows the deployment's peer-auth mode

Each peer service entry in the deployment configuration carries a TLS mode that is either off or required, and the decision that fixed that two-value grammar also says an empty value defaults to off. It does not: the default handed to the parser is derived from the deployment-wide peer-auth switch, so an empty field resolves to required the moment peer auth is set to mutual TLS. The enum half of the decision holds exactly (two values, one parser at all five peer blocks, a typo fails the load); only the default sentence is false. The ruling decides how the decision states the default.

This matters because an operator turning on mutual TLS gets every peer with an empty TLS field hardened at once with no per-block edit — the decision as written tells them the opposite (plaintext) for exactly that field.

## Options

- State the conditional default in the decision — off unless deployment peer auth is mutual TLS, then required — and cite the peer-auth decision that owns the derivation; cost: none beyond the edit.
- Strip the default from this decision entirely and point at the peer-auth decision for it; cost: a reader of this decision must follow one more link.

The ruling decides which of two compliant renderings the decision carries.

## Ruling

> Generated ruling (/verify-issues): Replace "empty defaults to off" with the actual rule — an empty TLS field inherits the deployment's peer-auth posture: off while peer auth is unset or none, required once it is mutual TLS — and cite the peer-auth decision that derives it. The current-state-only rule forces the change; the code's behaviour is deliberate and tested, so the text moves. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
