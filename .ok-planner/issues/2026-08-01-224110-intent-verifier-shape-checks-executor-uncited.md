---
issue: intent-verifier-shape-checks-executor-uncited
kind: sprint
category: intent-ledger
artifacts:
  - concept:service
  - concept:executor
status: open
opened: 2026-08-01T22:41:10Z
---

# The shipped verifier-shape-checks executor appears nowhere in the design corpus

## Problem

The bundled `verifier-shape-checks` executor (`lib/services/executors/verifier-shape-checks/`) ships today and advertises both executor and validator roles, but zero live corpus files mention it. The validation dossier flagged it 'awaiting promotion to a durable decision' on 2026-06-15; the promotion never happened.

Evidence tier: artifact.

## Candidates

- Add a decision (or a bundled-services mention) documenting the dual-role executor, so the next corpus audit doesn't read it as unsupported-by-omission.
- Rule it below corpus altitude — code plus its conformance coverage is the record — and retire the promotion question.
