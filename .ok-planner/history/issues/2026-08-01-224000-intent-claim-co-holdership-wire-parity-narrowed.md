---
issue: intent-claim-co-holdership-wire-parity-narrowed
kind: sprint
category: intent-ledger
artifacts:
  - concept:claim-co-holdership
status: answered
opened: 2026-08-01T22:40:00Z
---

# Intent ledger says co-holder wire handles are indistinguishable; corpus and code say narrower

## Question

Does a co-holder's executor-wire claim handle carry the same field set as a fresh acquirer's handle, or a deliberately narrower one?

## Answer

`concept:claim-co-holdership`'s Invariants section squarely decides this: "the co-holder's execution request carries the co-held claim's address and payload... The populated field set is narrower than a fresh acquirer's handle: alias, address, and payload only — a co-holder's handle carries no producer kind, no intent, and no producer candidate handle." Current code matches exactly: `makeHeldClaimHandle` (`lib/runtime/runner_dispatch.go`) populates only `alias`, `address`, and `payload` fields. The narrowing is the corpus's own ratified, current-state commitment, not accidental drift. The disagreement was confined to the historical intent ledger (`.ok-planner/history/drift-remediation/`), which per the project-records rule in `.ok-planner/CLAUDE.md` is expected to drift from current state and is never reconciled.
