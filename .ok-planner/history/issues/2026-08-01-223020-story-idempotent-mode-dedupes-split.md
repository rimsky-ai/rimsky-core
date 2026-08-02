---
issue: story-idempotent-mode-dedupes-split
kind: sprint
category: stories-splits
artifacts:
  - story:idempotent-mode-dedupes
status: answered
opened: 2026-08-01T22:30:20Z
---

# idempotent-mode-dedupes covers two named modes as one story

Should `story:idempotent-mode-dedupes` be split into one story per mode (`idempotent-queue` and `idempotent-settled`)?

No — the corpus already squarely commits to the bundled shape. `concept:cascade-mode`'s Invariants section states: "Each of the sequenced and idempotent modes' behavior on the user-outcome surface has a dedicated story: `story:sequenced-preserves-cascade-rounds` and `story:idempotent-mode-dedupes` (covering both `idempotent-queue` and `idempotent-settled`)." The concept doc names the story and explicitly scopes it to cover both modes as one artifact — this is not an inference, it is the corpus's own assignment of story boundaries. The filed gap does not exist: the "two outcomes in one story" observation is accurate, but the corpus has already ruled that pairing intentional.
