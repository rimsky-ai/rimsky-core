---
story: subscription-mounting
status: as-is
---

# Operator observes publisher subscriptions mount

## Role

As an operator deploying instances whose templates declare publishers, I can observe each publisher subscription progress from the mounting state to the active state, so that I know when my sensors are actually feeding the instance — instead of trusting a create response that can silently mean "failed."

## Capability

Instance creation inserts each declared publisher subscription as a row in the mounting state and returns immediately; the instance-detail surface exposes per-subscription state. A reconciliation worker drives the publisher Subscribe handshake for mounting rows with backoff and no attempt cap, flipping each row to the active state on success; the failed state is reserved for non-retryable errors (e.g. a publisher name not present in the registry). The startup resync pass is the durable safety net (see `concept:publisher-subscription`, `decision:subscription-mounting-state`, `decision:subscription-reconciler`).

## Business value

The operator knows when sensors are actually feeding an instance instead of trusting a create response that can silently mean "failed." Publisher slowness or load becomes a visible, self-recovering `mounting` state rather than a silent mount failure.

## Acceptance

The operator creates the instance and gets a fast success response; inspecting the instance, they see each declared subscription with its state; under publisher slowness or load the state reads as mounting and later flips to active without operator action, after which the publisher's messages flow; a genuinely non-retryable problem (e.g. a publisher name that is not registered) shows the failed state with a reason.

## Falsifier

A subscription that ends up unmounted with the operator unable to see that from the instance surface — the silent-success-create-response behavior is present; or the mounting state is observable but never reconciles without operator intervention under conditions that should recover (publisher merely slow or briefly down).

## Proof

Demo — against a running stack, create an instance whose publisher is deliberately slow to respond; show the create returning immediately, the subscription visibly in the mounting state, the flip to active once the publisher wakes, and the sensor's messages arriving.
