---
issue: loop-counter-readme-retired-subscription-model
kind: human
category: doc-drift
artifacts:
  - concept:node-subscription
  - story:loop-counter-cap
  - decision:loop-counter-shape
  - decision:executor-unary-rpc
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:27Z
github: https://github.com/rimsky-ai/rimsky-core/issues/103
---

# The loop-counter example's README describes a subscription model rimsky doesn't have

The loop-counter example is where the design corpus sends readers to learn how a
node subscribes to itself — the pattern behind every converging workflow. Its
README describes that self-subscription using a signal family and a frame key
that don't exist in rimsky's taxonomy.

The real mechanism is a tag-filtered subscription on the node's own terminal
success: the handler tags its success `loop` while counting and `done` when it
finishes, and the subscription fires only on the `loop` tag. The example's own
template file, in the same directory, already narrates this correctly in its
inline comments. So a reader gets two contradicting accounts of the same
mechanism from two files sitting side by side — which is worse than one wrong
account, because it costs the reader confidence in both.

Five further claims in the same README were re-verified and are false:

- It describes four stream-close outcomes. Executor dispatch is a unary call
  (`decision:executor-unary-rpc`); there are four outcome variants and no stream.
- Relatedly, it says dispatch "streams the handler's events".
- It calls the loop counter the first bundled utility kind. There are three.
- It says the supervisor and control API both seed the kind-alias map. Only the
  control API does; no supervisor-side caller exists.
- It says the test waits for a `done` event on the events feed. The test waits
  for the node to reach a fresh state and asserts the persisted count.

## Ruling

> Retired: the examples module is being removed in full, so the README this issue
> reports on ceases to exist and the drift it documents is dissolved rather than
> corrected. The documentation project maintains the cookbook that replaces it.
>
> Findings underneath these issues that concern rimsky rather than the examples
> were pulled out as their own issues before retirement — an unenforced publisher
> kind, an unproven ordering guarantee on terminal events, and a lifecycle
> callback that cannot refuse — so nothing about the platform is lost with the
> module.
