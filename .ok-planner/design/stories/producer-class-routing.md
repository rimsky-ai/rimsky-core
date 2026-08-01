---
story: producer-class-routing
status: as-is
---

# Template author routes producer-declared error classes

## Role

As a template author, I can route a producer-declared acquisition error class in the template's error-types declaration — and rely on the generic acquire-family keys as a documented fallback — so the error my producer takes care to classify is the error I can configure a response to.

## Capability

Claim producers may declare an error-class vocabulary in their capabilities handshake; the template validator range-checks error-types keys against the union of executor-declared classes, producer-declared classes, and the synthetic acquire-family vocabulary, with unattributable keys registering as advisory warnings rather than hard rejections. At runtime, acquisition-failure policy lookup falls back from the exact producer-declared class to the acquire-family before the unknown-class default (see `concept:error-policy`, `decision:producer-declared-classes-capability`, `decision:validator-learns-producer-classes`, `decision:acquire-prefix-fallback`).

## Business value

The classification work a producer does is usable: operators configure responses to the producer's own error vocabulary instead of being locked out by a validator that rejects what the runtime routes — and operators declaring only the generic family do not silently lose coverage when a producer starts naming classes.

