---
story: fanout-intent-inheritance
status: as-is
---

# Template author trusts fan-out claim intent applies to sub-claims

## Role

As a template author,

## Capability

I can declare a fan-out claim with `intent: r` and trust that every sub-claim inherits that intent, honored by the coexistence rule that decides which holders may hold a scope concurrently — not by the producer branching its own behavior on intent,

## Business value

so my read-only declarations are honored end-to-end without depending on producer-specific behavior.

## Acceptance

I author a fan-out template with `claims: [{name: <X>, intent: r}]`; I deploy and trigger; each sub-claim's persisted intent matches the parent claim's declared intent, and the coexistence rule evaluated at acquisition treats a read-only sub-claim's scope as shareable with another concurrent read-only holder of the same scope. I author a second template with `intent: rw`; its sub-claims are exclusive under the same rule. A producer's behavior at Commit or Abandon is uniform regardless of the sub-claim's intent.

## Falsifier

A fan-out sub-claim's persisted intent diverges from its parent claim's declared intent; OR the coexistence rule fails to honor a read-only sub-claim's shareability, or fails to keep a `rw` sub-claim exclusive; OR a producer branches its Commit or Abandon behavior on claim intent (intent enforcement belongs to the coexistence rule, not the producer).

## Proof

Executable proof. Side-by-side runnable templates (one `intent: r`, one `intent: rw`) with the persisted sub-claim intent asserted against the parent's declaration and observable coexistence behavior differing per declared intent.
