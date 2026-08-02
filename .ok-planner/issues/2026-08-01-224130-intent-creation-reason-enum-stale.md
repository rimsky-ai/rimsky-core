---
issue: intent-creation-reason-enum-stale
kind: sprint
category: intent-ledger
artifacts:
  - concept:persistence-database
status: open
opened: 2026-08-01T22:41:30Z
---

# Ledger's creation_reason vocabulary doesn't match the shipped schema

## Problem

The persistence-database dossier records `creation_reason IN (cascade, operator_invalidate, policy_retry, infra_reenqueue)`. The shipped schema's CHECK is `(cascade, operator_invalidate, recalculate, message_delivery)`; `policy_retry` and `infra_reenqueue` appear nowhere in code, while `recalculate` and `message_delivery` are real and used. The live concept rightly declines to enumerate the values.

Evidence tier: artifact.

## Candidates

- Retire the ledger's enum claim; the schema is the record.
- Rule the enum corpus-altitude and add the current vocabulary to the concept.
