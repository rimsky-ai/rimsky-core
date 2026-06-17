---
story: instance-create-is-idle
status: as-is
---

# Operator creates an idle instance

## Role

As an operator, I can create an instance of a deployed template and have nothing happen as a side effect, so that "creating an instance" and "invoking work on the instance" are separate operator actions I drive independently.

## Capability

Operator-driven instance creation that is strictly idempotent on instance state — no frames open, no messages land, no node-runs begin — until the operator separately posts a message against the instance.

## Business value

Operators control when work begins. Creating an instance is a setup action (allocate the row, mint the per-instance node rows, notify lifecycle subscribers); waking the work is a separate, deliberate action (post a message). Conflating them prevents operators from creating instances ahead of the moment they want work to run.

## Acceptance

I `POST /instances` with `{template, instance_key?, params, attribute_overrides?}` against a deployed template. The instance row appears in `GET /instances/{id}` with `paused: false` and no terminal timestamp; the instance's frame collection (returned by `GET /instances/{id}/frames`) is empty; the instance's message ledger (returned by `GET /instances/{id}/messages`) is empty; no node-runs exist yet; the lifecycle-subscriber's `OnInstanceCreated` callback fires once. The supervisor does not dispatch anything for this instance until a sender posts a message.

## Falsifier

The create call returns success but a frame row exists for the instance with no operator-posted triggering message; OR a synthetic envelope appears in the message ledger immediately after create; OR a node-run row exists with no operator emission having occurred.

## Proof

Executable proof — `POST /instances` followed by `GET /instances/{id}/frames` and `GET /instances/{id}/messages` returning empty collections; one lifecycle-subscriber callback recorded.
