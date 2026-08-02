---
issue: intent-inertness-payload-predicate-supersession
kind: sprint
category: intent-ledger
artifacts:
  - concept:inertness
  - concept:node-subscription
status: open
opened: 2026-08-01T22:40:20Z
---

# Ledger's "filters never read payload bytes" claim predates the CEL payload predicate

## Problem

The inertness dossier (artifact tier) claims subscription topic filters operate only on metadata, never payload bytes. The live `concept:inertness` sanctions a specific later-added read site: node-subscription payload predicates evaluating a CEL expression over the emitted signal payload. The corpus is internally coherent; only the ledger claim is stale, and no supersession is recorded in the ledger.

Evidence tier: artifact.

## Candidates

- Retire the stale ledger claim; the CEL-predicate site is the ratified current model.
- Rule the payload-blindness the intent and retire the CEL predicate (a capability removal).
