---
issue: intent-role-template-grant-enumeration-stale
kind: sprint
category: intent-ledger
artifacts:
  - concept:role-template
status: answered
opened: 2026-08-01T22:40:30Z
---

# Ledger enumerates agent-supervisor grants including the retired node:invalidate

## Question

Does the live corpus enumerate the agent-supervisor bundled role's grants, and if so does it include the retired `node:invalidate`?

## Answer

`concept:role-template`'s Invariants state plainly that grant strings are "owned by the compiled-in role files, not enumerated here" — the concept carries no grant enumeration that could go stale. The compiled-in role file (`cmd/rimsky/cli/roles/agent-supervisor.json`) lists exactly `*:read`, `node:reset`, `message:send`; `node:invalidate` has zero hits anywhere in the codebase. There is nothing to repair in the corpus — only the historical intent ledger's enumeration is stale, which is expected per the project's records rule.
