---
story: inproc-utility-executor
status: as-is
aliases: []
---

# Utility node types dispatched without external service deployment

## Role and capability

As a template author or operator, I can reference a utility node kind (e.g. a loop-counter) in a template and have it dispatched without registering an external executor service for it, so utility nodes don't require additional deployment to function.

## Acceptance

I deploy rimsky with no external executor configuration for the loop-counter kind; I register a template whose node declares the loop-counter kind; the template registers successfully; an instance of the template dispatches that node without any external IPC; the node terminates as expected.

## Falsifier

Registering a template that references the loop-counter utility kind requires the operator to also register an external executor service for it. OR: dispatch fails because no executor service is reachable for the utility node.

## Proof

Example — a minimal template referencing the loop-counter utility kind with no external executor configured for it; the template registers and runs to completion in a deployment with no utility-executor service.
