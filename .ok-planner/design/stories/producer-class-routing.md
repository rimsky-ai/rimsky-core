---
story: producer-class-routing
status: as-is
---

# Template author routes producer-declared error classes

## Role

As a template author, I can route a producer-declared acquisition error class in `error_types:` — and rely on `acquire/*` keys as a documented fallback — so the error my producer takes care to classify is the error I can configure a response to.

## Capability

Claim producers may declare an error-class vocabulary in their capabilities handshake; the template validator range-checks `error_types:` keys against the union of executor-declared classes, producer-declared classes, and the `acquire/*` synthetic family, with unattributable keys registering as advisory warnings rather than hard rejections. At runtime, acquisition-failure policy lookup falls back from the exact producer-declared class to the `acquire/*` family before the unknown-class default (see `concept:error-policy`, `decision:producer-declared-classes-capability`, `decision:validator-learns-producer-classes`, `decision:acquire-prefix-fallback`).

## Business value

The classification work a producer does is usable: operators configure responses to the producer's own error vocabulary instead of being locked out by a validator that rejects what the runtime routes — and operators declaring only the generic family do not silently lose coverage when a producer starts naming classes.

## Acceptance

A template with `error_types: { pg/claim_unavailable: retry }` on a node whose executor declares its own vocabulary registers successfully, and at runtime an acquisition failure carrying that producer class routes to the declared action. A template declaring only `acquire/unavailable:` still matches a producer-classified acquisition failure via the documented prefix fallback. An `error_types:` key the validator can attribute to no declared vocabulary registers with an advisory warning rather than a hard rejection.

## Falsifier

Registration rejecting a producer-declared class that the runtime would route; or an `acquire/*` key that registers but never matches a producer-classified acquisition failure.

## Proof

Executable proof — a scenario registers both template shapes and drives a producer-classified acquisition failure through each, asserting the configured action fires.
