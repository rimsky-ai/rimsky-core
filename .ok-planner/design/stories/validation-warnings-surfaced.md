---
story: validation-warnings-surfaced
status: as-is
---

# Template author sees validator advisories in responses

## Story

As a template author, I can see the static validator's advisory warnings in the registration and validation responses — and promote them to errors with the existing flag — so advice the validator already computes reaches me.

Both the register handler and the validate endpoint merge the static validator's warnings into the responses' validation-warnings field, and the warnings-as-errors flag trips on them when set (see `decision:merge-validator-warnings`).

Advice the validator already computes reaches the author who can act on it — and authors who want a strict gate can promote the same advisories to rejections with the warnings-as-errors flag.
