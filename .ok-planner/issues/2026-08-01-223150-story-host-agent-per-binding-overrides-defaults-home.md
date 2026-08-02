---
issue: story-host-agent-per-binding-overrides-defaults-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:host-agent-per-binding-overrides
  - concept:host-agent
status: open
opened: 2026-08-01T22:31:50Z
---

# Per-binding override defaults are stated only in story prose

## Problem

`story:host-agent-per-binding-overrides`' prose commits that bindings with no overrides spawn with inherited env, global cwd, and global timeout; `concept:host-agent` documents only the env inheritance — the argv override and cwd/timeout fallbacks have no corpus home.

## Candidates

- Extend concept:host-agent's invariants to cover argv/cwd/timeout overrides and their defaults, then reduce the story.
- Rule the defaults below corpus altitude and reduce.
