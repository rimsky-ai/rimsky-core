---
story: host-agent-per-run-scope-isolation
status: as-is
---

# Sibling run-scopes within one frame get isolated children

## Story

As a template author running a fan-out workflow whose sibling run-scopes (e.g. fan-out partitions within one frame, per `concept:run-scope`) concurrently dispatch the same late-bound executor, I can trust that each run-scope spawns its own isolated child process — they never share executor state — and the child is reaped when its run-scope closes, so that concurrent sibling run-scopes don't corrupt each other's state.

Per-run-scope process isolation in the host agent: concurrent sibling run-scopes (always within one frame, since run-scopes never span frames per `decision:run-scope-is-per-frame`) spawn distinct children for the same binding; child reap is run-scope-scoped, not binding-scoped.

Template authors get isolated process state across concurrent sibling run-scopes — no surprise cross-talk between fan-out partitions — and clean reaping when a run-scope closes (sub-graph carry-rule, fan-out aggregation, or the owning frame ending).
