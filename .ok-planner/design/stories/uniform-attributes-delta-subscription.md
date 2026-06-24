---
story: uniform-attributes-delta-subscription
status: as-is
aliases: []
---

# Subscriber predicates on attributes_delta across every run-terminating signal

## Role and capability

As a template author, I can write a subscription whose CEL `when:` predicate reads `payload.attributes_delta.<key>` and have it fire on the producer's verdict-time attribute writeback uniformly across the two run-terminating signal kinds — `terminal/success` and `terminal/error/<class>` — without writing a separate subscription per kind.

## Business value

Authors express "fire when the producer's verdict carries this attribute value" once, and the subscription matches whether the producer succeeded or errored. Without this uniformity, an author who wants to react to a discriminator the producer rides in its attributes (a routing flag, an intent label, a structured outcome marker) would have to enumerate per-kind subscriptions or accept that the handler is blind to the same attribute when the producer errors.

## Acceptance

I declare two nodes: a producer whose executor settles with `attributes_delta` carrying a specific key/value on its run-terminating verdict, and a handler subscribing to a terminal-family pattern with a CEL predicate over that key. Cascade fires the producer; the handler dispatches when the producer's verdict carries the predicated value, uniformly across `terminal/success` and `terminal/error/<class>`. The same handler subscription matches a success that carries the value and an error that carries the value.

## Falsifier

The subscription fires when the producer succeeds with the predicated attribute but is silent when the producer errors with the same attribute. OR: the CEL predicate compiles but at evaluation time `payload.attributes_delta` is absent on error signals while present on success.

## Boundary

This story is about subscribers predicating on the executor's verdict-time `attributes_delta` — what the producer wrote with its verdict — on **run-terminating** signals (`terminal/success` and `terminal/error/<class>`). Park signals (`transient/park/*`) are audit-only, never fire cascade, and do not carry `attributes_delta` at all — park is dispatch-internal and writes no attributes (per `decision:uniform-attributes-delta`). Subscriptions targeting park paths are rejected at template registration (see `concept:signal`). An executor that needs to thread state across a park-and-resume boundary uses scratch (per `concept:parked-state`); its next run-terminating verdict after the wake is the next opportunity to mutate attributes that this story's subscribers can predicate on.

This story is also distinct from the `attribute/<key>/changed` signal family (see `concept:signal`), which fires per attribute key whose persisted value differs from the prior run's value at settlement. The verdict-time `payload.attributes_delta` is what the producer wrote with its verdict; the `attribute/<key>/changed` signal is the framework-detected diff against prior state. The two are different subscription surfaces.

## Proof

Executable proof — scenario tests driving a producer through both run-terminating kinds (success and error), each carrying the same attributes-delta key/value, and observing the handler subscription dispatches on every one.
