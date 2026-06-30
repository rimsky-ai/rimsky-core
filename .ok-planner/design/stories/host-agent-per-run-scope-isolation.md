---
story: host-agent-per-run-scope-isolation
status: as-is
---

# Sibling run-scopes within one frame get isolated children

## Role

As a template author running a fan-out workflow whose sibling run-scopes (e.g. fan-out partitions within one frame, per `concept:run-scope`) concurrently dispatch the same late-bound executor, I can trust that each run-scope spawns its own isolated child process — they never share executor state — and the child is reaped when its run-scope closes, so that concurrent sibling run-scopes don't corrupt each other's state.

## Capability

Per-run-scope process isolation in the host agent: concurrent sibling run-scopes (always within one frame, since run-scopes never span frames per `decision:run-scope-is-per-frame`) spawn distinct children for the same binding; child reap is run-scope-scoped, not binding-scoped.

## Business value

Template authors get isolated process state across concurrent sibling run-scopes — no surprise cross-talk between fan-out partitions — and clean reaping when a run-scope closes (sub-graph carry-rule, fan-out aggregation, or the owning frame ending).

## Acceptance

With a stateful late-bound executor whose binary records which run-scope dispatched to it, an instance whose fan-out produces two concurrent partition run-scopes both dispatching the same binding within one frame: the agent spawns two distinct child processes (one per run-scope, not one shared); each child sees only its own run-scope's dispatches; closing one run-scope reaps only that run-scope's child while the other keeps serving.

## Falsifier

The two sibling run-scopes share a single child, OR closing one run-scope reaps both children, OR a closed run-scope's child survives.

## Proof

Executable proof.
