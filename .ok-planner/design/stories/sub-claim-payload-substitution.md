---
story: sub-claim-payload-substitution
status: as-is
---

# Template author reads per-sub-claim payload through the standard claim directive

## Story

As a template author,

I can read producer-supplied per-sub-claim data via `{{claim.<alias>.payload[.<field>]}}` in a fan-out child's substitution context, and the path resolves identically to how it resolves on a regular Open'd claim,

so there is no second mechanism to learn for sub-claims.
