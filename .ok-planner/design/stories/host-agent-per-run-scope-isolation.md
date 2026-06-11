---
story: host-agent-per-run-scope-isolation
status: as-is
---

# Concurrent run-scopes get isolated children

## Role

As a template author running a fan-out workflow whose run-scopes concurrently dispatch the same late-bound executor, I can trust that each run-scope spawns its own isolated child process — they never share executor state — and the child is reaped when its run-scope terminates, so that concurrent run-scopes don't corrupt each other's state.

## Capability

Per-run-scope process isolation in the host agent: concurrent run-scopes spawn distinct children for the same binding; child reap is run-scope-scoped, not binding-scoped.

## Business value

Template authors get isolated process state across concurrent run-scopes — no surprise cross-talk between fan-out partitions — and clean reaping when a run-scope terminates.

## Acceptance

With a stateful late-bound executor whose binary records which run-scope dispatched to it, an instance whose fan-out produces two concurrent run-scopes both dispatching the same binding: the agent spawns two distinct child processes (one per run-scope, not one shared); each child sees only its own run-scope's dispatches; terminating one run-scope reaps only that run-scope's child while the other keeps serving.

## Falsifier

The two run-scopes share a single child, OR terminating one run-scope reaps both children, OR a terminated run-scope's child survives.

## Proof

Executable proof.
