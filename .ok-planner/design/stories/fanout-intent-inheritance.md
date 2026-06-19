---
story: fanout-intent-inheritance
status: as-is
---

# Template author trusts fan-out claim intent applies to sub-claims

## Role

As a template author,

## Capability

I can declare a fan-out claim with `intent: r` and trust that read-only applies to the sub-claims too,

## Business value

so my read-only declarations are honored end-to-end.

## Acceptance

I author a fan-out template with `claims: [{name: <X>, intent: r}]`; I deploy and trigger; the producer's Commit handler treats sub-claim Commits as read-only (the substrate-specific write-back does not fire). I author a second template with `intent: rw`; the producer's Commit handler exhibits write-back.

## Falsifier

A read-only fan-out template later causes the producer to perform write-back operations on sub-claim Commit (the inheritance regressed); OR the producer's behavior on a sub-claim Commit diverges from its behavior on a sibling regular claim of the same intent.

## Proof

Executable proof. Side-by-side runnable templates (one `intent: r`, one `intent: rw`) with observable producer-side write behavior differing per declared intent.
