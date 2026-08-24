---
story: host-daemon-per-run-scope-isolation
---

# Sibling run-scopes within one frame get isolated children

## Story

As a template author running a fan-out workflow whose sibling run-scopes (e.g. fan-out partitions within one frame, per `concept:run-scope`) concurrently dispatch the same late-bound executor, I can trust that each run-scope spawns its own isolated child process — they never share executor state — and the child is reaped when its run-scope closes, so that concurrent sibling run-scopes don't corrupt each other's state.
