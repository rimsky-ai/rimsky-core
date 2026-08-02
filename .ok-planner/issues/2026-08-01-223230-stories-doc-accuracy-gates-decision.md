---
issue: stories-doc-accuracy-gates-decision
kind: sprint
category: stories-prescriptive
artifacts:
  - story:rules-doc-accuracy
  - story:substitution-doc-accuracy
status: open
opened: 2026-08-01T22:32:30Z
---

# Two doc-accuracy fitness gates have no decision home

## Problem

`story:rules-doc-accuracy` and `story:substitution-doc-accuracy` each describe a build-time fitness gate (rules-doc citation resolution; substitution source-kind list vs the runtime resolver's dispatch set) whose mechanism is stated nowhere but in the story prose — both are real, code-verified gates.

## Candidates

- One decision documenting the doc-accuracy-gate pattern (both gates as instances), then reduce both stories.
- One decision per gate.
- Rule the mechanisms below corpus altitude and reduce.
