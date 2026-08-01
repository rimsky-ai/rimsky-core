---
story: uniform-attributes-delta-subscription
status: as-is
aliases: []
---

# Subscriber predicates on attributes_delta across every run-terminating signal

## Story

As a template author, I can write a subscription whose CEL `when:` predicate reads `payload.attributes_delta.<key>` and have it fire on the producer's verdict-time attribute writeback uniformly across the two run-terminating signal kinds — `terminal/success` and `terminal/error/<class>` — without writing a separate subscription per kind.

Authors express "fire when the producer's verdict carries this attribute value" once, and the subscription matches whether the producer succeeded or errored. Without this uniformity, an author who wants to react to a discriminator the producer rides in its attributes (a routing flag, an intent label, a structured outcome marker) would have to enumerate per-kind subscriptions or accept that the handler is blind to the same attribute when the producer errors.

This story is about subscribers predicating on the executor's verdict-time `attributes_delta` — what the producer wrote with its verdict — on **run-terminating** signals (`terminal/success` and `terminal/error/<class>`). Park signals (`transient/park/*`) are audit-only, never fire cascade, and do not carry `attributes_delta` at all — park is dispatch-internal and writes no attributes (per `decision:uniform-attributes-delta`). Subscriptions targeting park paths are rejected at template registration (see `concept:signal`). An executor that needs to thread state across a park-and-resume boundary uses scratch (per `concept:parked-state`); its next run-terminating verdict after the wake is the next opportunity to mutate attributes that this story's subscribers can predicate on.

This story is also distinct from the `attribute/<key>/changed` signal family (see `concept:signal`), which fires per attribute key whose persisted value differs from the prior run's value at settlement. The verdict-time `payload.attributes_delta` is what the producer wrote with its verdict; the `attribute/<key>/changed` signal is the framework-detected diff against prior state. The two are different subscription surfaces.
