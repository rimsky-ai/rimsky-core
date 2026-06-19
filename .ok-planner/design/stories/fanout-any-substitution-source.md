---
story: fanout-any-substitution-source
status: as-is
---

# Template author substitutes a fan-out partition_request from any standard source

## Role

As a template author,

## Capability

I can write a fan-out `partition_request` that substitutes from any standard source — upstream node attribute, claim payload, instance param, or typed message — and the substitution engine resolves it uniformly,

## Business value

so the source I use is my choice and not the architecture's.

## Acceptance

I author one template with `partition_request: "{{nodes.<X>.attribute.items}}"` and a second with `partition_request: "{{messages.<T>.items}}"`, both targeting the same fan-out node and claim. Both templates register; both deploy; both produce N parallel child runs when their respective sources resolve.

## Falsifier

One source returns "not found" at substitution while the other resolves; OR one is rejected at registration while the other passes.

## Proof

Executable proof. Two runnable templates side-by-side, exercising the two sources.
