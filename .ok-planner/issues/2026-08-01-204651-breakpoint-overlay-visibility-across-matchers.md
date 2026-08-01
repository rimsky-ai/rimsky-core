---
issue: breakpoint-overlay-visibility-across-matchers
kind: human
category: conflicting
artifacts:
  - concept:breakpoint
status: open
opened: 2026-08-01T20:46:51Z
---

# When multiple pause breakpoints match one dispatch, should a later matcher observe an earlier hit's L6 resume overlay?

## Problem

The intent dossier (`.ok-planner/design/intent/breakpoint.md`, distilling the 2026-05-24 instance-debugger artifact) records a per-iteration block contract: hit N resumes before hit N+1 is written, and "matchers evaluate against a snapshot of the post-L5 bag captured at function entry so a later matcher never observes an earlier hit's L6 overlay."

The code deliberately does the opposite. Commit `2627ae3c` (2026-07-20, phase-3 step-5 misc-c defect sweep — after the dossier was distilled) changed `lib/runtime/breakpoint_eval.go` so that after each resume overlay merges, the matcher input and the hit-snapshot bag are updated to the merged result (`matcherInput = result`; `cc.MergedAttributes = result` before hit creation), and added `TestEvaluateBreakpoints_LaterBreakpointSeesEarlierResumeOverlay` locking the new behavior in: a later matching breakpoint's matcher and snapshot both see the earlier hit's overlay.

The sequential half of the contract (hit N resumes before hit N+1 is written) still holds in both versions. The live concept (`concepts/breakpoint.md`) is silent on which bag later matchers evaluate against, so the corpus currently commits to neither reading. The dossier's version is recorded design intent; the code's version was landed by a defect-ledger sweep that evidently judged the old isolation a bug. Either could be the intended commitment.

## Candidates

- Ratify the code: an L6 resume overlay is part of the dispatch's effective bag from the moment it is applied, so later matchers and later hit snapshots observe it (matcher results reflect what will actually dispatch); add the invariant to `concept:breakpoint`.
- Restore the dossier's contract: matchers and snapshots evaluate against the post-L5 bag captured at evaluation entry, so breakpoint matching is independent of resume-overlay contents; add the isolation invariant to `concept:breakpoint`, with the evaluation behavior and its locking test brought back to match.
