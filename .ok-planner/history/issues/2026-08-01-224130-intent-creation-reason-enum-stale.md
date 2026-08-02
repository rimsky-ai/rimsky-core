---
issue: intent-creation-reason-enum-stale
kind: sprint
category: intent-ledger
artifacts:
  - concept:persistence-database
  - concept:node-run
status: answered
opened: 2026-08-01T22:41:30Z
---

# Ledger's creation_reason vocabulary doesn't match the shipped schema

Question: does any live corpus artifact carry a stale `creation_reason` enum (the filed `policy_retry` / `infra_reenqueue` values, which the shipped schema's CHECK never had)?

No. The stale enum lives only in `.ok-planner/history/intent/persistence-database.md` — an out-of-context-by-default historical dossier, not a live corpus artifact. `concept:node-run` already states the current, correct vocabulary: "A creation-reason field — `cascade | operator_invalidate | recalculate | message_delivery`" (Invariants), matching the shipped schema's CHECK exactly (`creation_reason IN ('cascade','operator_invalidate','recalculate','message_delivery')`, `lib/foundation/persistence/postgres/migrations/001-initial.sql`). `concept:cascade-mode` and `concept:wait-set` both cite the same four-value vocabulary consistently. `concept:persistence-database` itself declines to enumerate the values at all (correctly, per its Boundaries — schema content lives in migration files) so it carries nothing to correct. Nothing in the live corpus needs changing.
