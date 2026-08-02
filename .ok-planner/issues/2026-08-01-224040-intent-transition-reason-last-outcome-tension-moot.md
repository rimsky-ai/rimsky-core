---
issue: intent-transition-reason-last-outcome-tension-moot
kind: sprint
category: intent-ledger
artifacts:
  - concept:transition-reason
  - concept:signal
status: open
opened: 2026-08-01T22:40:40Z
---

# Ledger carries an open tension against a vocabulary that no longer exists

## Problem

The transition-reason dossier records an open tension about reconciling `TransitionReason` with `last_outcome`. `last_outcome` no longer exists anywhere in code or the live corpus (retired 2026-05-23 per the wait-set dossier's own record). One side of the tension has been gone for months.

Evidence tier: artifact.

## Candidates

- Retire the tension claim; nothing is left to reconcile.
- Keep it open pending a broader audit-vocabulary review.
