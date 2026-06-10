---
story: host-agent-per-binding-overrides
status: as-is
---

# Per-binding env/args/cwd/timeout honored

## Role

As a template author declaring late-bind bindings for varied local binaries, I can specify per-binding env vars, command-line args, working directory, and ready/spawn timeout — the agent applies them when exec'ing the child — so that I run different binaries with different configuration through the same agent without global config soup.

## Capability

Per-binding exec overrides (env vars, argv, cwd, spawn timeout) honored by the host agent at child-process spawn; bindings with no overrides spawn with inherited env, global cwd, and global timeout.

## Business value

Template authors run different binaries with different configuration through the same agent — without global config soup or environment leakage between bindings.

## Acceptance

With a late-bind binding declared with non-default args (e.g., a mode flag), an env var, a per-binding cwd, and a per-binding timeout, an instance dispatching against the binding produces a spawned child that actually runs with those args / env / cwd in effect (the binary echoes argv / env / cwd back through the real dispatch response); the per-binding timeout (shorter than global) actually bounds the spawn wait. A binding with no overrides spawns with inherited env, global cwd, and global timeout (backward-compatible).

## Falsifier

An override is declared but ignored, OR the per-binding timeout has no effect.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
