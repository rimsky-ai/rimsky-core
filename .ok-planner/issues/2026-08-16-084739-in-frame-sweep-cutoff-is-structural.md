---
issue: in-frame-sweep-cutoff-is-structural
kind: audit
category: conflicting
artifacts:
  - concept:orphan-reaper
status: verified
opened: 2026-08-16T08:47:39Z
---

# The in-frame orphan sweep releases claims by structure, not by the deadlines the concept names

The orphan reaper runs two sweeps. The node-run sweep asks whether a dispatch has gone quiet past its configured deadlines. The in-frame sweep asks a different question: did this claim outlive the frame that authorized it. It reads no timestamp and no deadline. It selects every claimed run whose frame has ended and releases all of them. The concept says both sweeps key on per-dispatch quiet-period and runtime deadlines. A run dispatched moments before its frame ended, well inside every deadline, is released anyway. The ruling decides whether that eager release is the design or a latent defect, and fixes the concept to it.

## Options

- Rewrite the invariant to give the in-frame sweep its own structural cutoff (frame ended → claim released) and say why; cost: leaves the eagerness unexamined.
- Add deadline checks so a claim inside its deadlines survives an ended frame; cost: a behaviour change with its own question. May a claim be held past frame end on purpose?
- Restate the invariant around what each cutoff protects (one sweep a stalled dispatch, the other an authorization that no longer exists) and record the absent timestamp as deliberate; cost: same behaviour, better words.

The ruling decides whether a fresh claim under an ended frame should be released.

## Ruling

> Retire: the concept-catalog repair dissolves this issue. Its prior ruling was words-only: rewrite the concept's deadline claim. That claim is an Invariants entry, and the repair (issue `concept-catalog-carries-non-definitional-content`) deletes the Invariants sections — a concept holds no prescription. The code owns the cutoff behavior, and no one disputes it. Ruled live by the owner, 2026-08-20.
