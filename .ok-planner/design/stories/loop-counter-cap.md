---
story: loop-counter-cap
status: as-is
aliases: []
---

# Bounded iteration via the loop-counter utility node

## Role and capability

As a template author, I can use the bundled loop-counter utility node kind with a maximum-count input attribute, and observe it emit a `loop` tag on each dispatch while count is below max and a `done` tag when count reaches max, so I can express bounded iteration without authoring a custom executor.

## Acceptance

I declare a node of the loop-counter kind with a maximum count of three; cascade re-fires the node three times within one frame via a subscription self-edge on the `loop` tag; on the third dispatch the node emits the `done` tag instead of the `loop` tag; a downstream subscriber on the `done` tag fires.

## Falsifier

The loop-counter carries the `loop` tag after reaching the maximum count. OR: it carries the `done` tag before reaching the maximum count. OR: count does not carry across dispatches within the same frame's RunScope and the `done` tag never fires. (Cross-frame counting is out of scope; it requires message-borne propagation per `concept:message`.)

## Proof

Demo — scenario test wiring a loop-counter node (maximum count of three) to a sink subscriber via `subscribes: [{node: <emitter>, type: terminal/success, when: "loop" in payload.tags}]` and a different sink subscriber on `"done" in payload.tags`; observes the `loop` tag fires three times then the `done` tag fires once.
