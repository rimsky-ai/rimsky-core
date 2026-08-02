---
issue: intent-role-template-grant-enumeration-stale
kind: sprint
category: intent-ledger
artifacts:
  - concept:role-template
status: open
opened: 2026-08-01T22:40:30Z
---

# Ledger enumerates agent-supervisor grants including the retired node:invalidate

## Problem

The role-template dossier enumerates the agent-supervisor bundled role's grants as `*:read` + `node:invalidate` + `node:reset` + `message:send`. `node:invalidate` and its whole surface were retired 2026-06-15; the compiled-in role file today carries `*:read` + `node:reset` + `message:send`, and `concept:role-template` deliberately declines to enumerate grants. Only the ledger is stale.

Evidence tier: artifact.

## Candidates

- Retire the ledger's grant enumeration as historical.
- Re-add node:invalidate (contradicts the recorded retirement ruling).
