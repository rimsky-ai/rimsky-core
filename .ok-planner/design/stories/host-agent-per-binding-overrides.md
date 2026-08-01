---
story: host-agent-per-binding-overrides
status: as-is
---

# Per-binding env/args/cwd/timeout honored

## Story

As a template author declaring late-bind bindings for varied local binaries, I can specify per-binding env vars, command-line args, working directory, and ready/spawn timeout — the agent applies them when exec'ing the child — so that I run different binaries with different configuration through the same agent without global config soup.

Per-binding exec overrides (env vars, argv, cwd, spawn timeout) honored by the host agent at child-process spawn; bindings with no overrides spawn with inherited env, global cwd, and global timeout.

Template authors run different binaries with different configuration through the same agent — without global config soup or environment leakage between bindings.
