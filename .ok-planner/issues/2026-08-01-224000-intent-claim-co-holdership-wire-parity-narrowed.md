---
issue: intent-claim-co-holdership-wire-parity-narrowed
kind: sprint
category: intent-ledger
artifacts:
  - concept:claim-co-holdership
status: open
opened: 2026-08-01T22:40:00Z
---

# Intent ledger says co-holder wire handles are indistinguishable; corpus and code say narrower

## Problem

The intent ledger (claim-co-holdership dossier, artifact tier) records that a co-holder's executor-wire claim handle is indistinguishable from a fresh acquirer's. The live corpus and code both state the opposite: a co-holder's handle carries alias, address, and payload only — no producer kind, no intent, no candidate handle (`concept:claim-co-holdership` invariants; `code:lib/runtime/runner_dispatch.go::makeHeldClaimHandle`). No supersession record exists in the ledger for this reversal.

Evidence tier: artifact.

## Candidates

- Retire the ledger claim as superseded; owner confirms the narrowing was intentional.
- Treat the narrowing as accidental drift and restore wire parity (change code + corpus).
