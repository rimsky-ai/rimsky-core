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

