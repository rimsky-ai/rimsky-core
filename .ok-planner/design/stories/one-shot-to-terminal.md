---
story: one-shot-to-terminal
status: as-is
---

# Operator drives a compose manifest to terminal in one invocation

## Role

As an operator with a compose manifest, I can drive its declared instances to terminal state with one invocation, so that I can run orchestrations on a machine without standing up rimsky infrastructure.

## Capability

A single CLI invocation against a compose manifest spins up an embedded rimsky runtime stack in-process, deploys the manifest's templates, creates its declared instances, drives them through their nodes, and returns when every declared instance has reached an instance-terminal state. There is no separate stack to start, no second process to tear down, and no follow-up command to read the outcome — the invocation that starts the run is the one that finishes it.

## Business value

Operators can execute compose-shaped orchestrations on a developer machine, a CI runner, or a bare server without provisioning a deployed rimsky stack — the same manifest format works in both modes, and the one-shot mode removes the standing-infrastructure precondition for casual or scripted use.

## Acceptance

The operator invokes the one-shot orchestrator against a compose manifest whose declared instances target reachable executors. Every declared instance reaches terminal state (success or failure), the orchestrator stops on its own, and the operator can observe the per-instance outcomes when it returns.

## Falsifier

The orchestrator exits before every declared instance reaches terminal; OR it stalls after the work is finished and has to be killed; OR it requires a separate teardown step before its results can be read.

## Proof

Demo + executable proof — drive a two-instance manifest where one succeeds and one fails; the run completes on its own and reports both outcomes.
