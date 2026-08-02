---
issue: intent-inertness-payload-predicate-supersession
kind: sprint
category: intent-ledger
artifacts:
  - concept:inertness
  - concept:node-subscription
status: answered
opened: 2026-08-01T22:40:20Z
---

# Ledger's "filters never read payload bytes" claim predates the CEL payload predicate

## Question

Does the live corpus still claim subscription filters read only metadata, never payload bytes?

## Answer

No — `concept:inertness`'s Structural-inertness sub-discipline explicitly sanctions the read site in question: "node-subscription payload predicates evaluate a CEL expression over the emitted signal payload, spanning all three structurally-inert streams." `concept:node-subscription` corroborates: a subscription "declares a target signal... plus an optional payload predicate" and "pairs a wildcarded terminal target with a payload predicate over the signal's tags." The CEL-predicate read site is already the corpus's ratified current model; only the historical intent ledger's payload-blindness claim predates it.
