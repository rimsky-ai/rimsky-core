---
story: sensor-object-store
status: as-is
---

# Workflow reacts to deposited content

## Story

As an operator, I want my workflow to react when new content is deposited into a location I designate, so that upstream producers can hand work to the graph by simply dropping it there — without anyone writing custom integration code.

## Acceptance

A producer deposits new content into the watched location → the workflow reacts to that deposit, observing the deposit's real identity and metadata. Each deposit is reacted to at least once, and a deposit already reacted to is not re-triggered by a restart of the watching component. A deposit still being written is never treated as work — the workflow sees it only once it is complete. The component delivering the reaction is real (not stubbed); the technology of the location and of the detection is a technical decision, not part of this story.

## Falsifier

A restart re-triggers deposits already reacted to, OR a still-being-written deposit is consumed as work, OR the reaction's payload is canned rather than the deposit's real identity and metadata, OR content deposited while the watcher was down is silently never reacted to.

## Proof

Executable proof.
