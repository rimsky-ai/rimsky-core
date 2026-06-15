---
story: loop-counter-cap
status: as-is
aliases: []
---

# Bounded iteration via the loop-counter utility node

## Role and capability

As a template author, I can use the bundled loop-counter utility node kind with a maximum-count input attribute, and observe it emit a step-iteration named event on each dispatch while count is below max and a done named event when count reaches max, so I can express bounded iteration without authoring a custom executor.

## Acceptance

I declare a node of the loop-counter kind with a maximum count of three; cascade re-fires the node three times via a subscription on its step-iteration event; on the third dispatch the node emits the done event instead of the step-iteration event; a downstream subscriber on the done event fires.

## Falsifier

The loop-counter emits the step-iteration event after reaching the maximum count. OR: it emits the done event before reaching the maximum count. OR: count does not carry across dispatches and the done event never fires.

## Proof

Demo — scenario test wiring a loop-counter node (maximum count of three) to a sink subscriber on the step-iteration event and a different sink on the done event; observes the step-iteration event fires three times then the done event fires once.
