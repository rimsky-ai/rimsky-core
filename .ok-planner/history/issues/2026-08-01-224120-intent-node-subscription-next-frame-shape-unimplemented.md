---
issue: intent-node-subscription-next-frame-shape-unimplemented
kind: sprint
category: intent-ledger
artifacts:
  - concept:node-subscription
  - concept:frame
status: repaired
opened: 2026-08-01T22:41:20Z
---

# The live node-subscription concept asserted a next-frame shape no code implements

Question: does self-subscription have a valid "next-frame" spelling (a fresh frame opened for the same node-instance on every matching commit) alongside the same-frame spelling?

No. `concept:frame`'s own invariants ("Every frame row carries a non-null triggering-message reference. There is no path that creates a frame without a triggering message." and "A frame begins only when a message sits pending in the instance's message queue and the frame engine picks it up on a tick") already foreclose any commit-triggered frame open, and the schema enforces it (`rimsky_frames.triggering_message_id NOT NULL`, migration `001-initial.sql`). No code anywhere implements a next-frame identifier for self-subscription. `concept:node-subscription`'s Invariants section carried a stale "next-frame shape" clause that contradicted its own counterpart concept and the schema both already agree on — a corpus-side repair under `{{MECHANICAL-VS-JUDGMENT-RULE}}` (aligning a stale sentence to the commitment the code and the counterpart artifact already agree on), not a commitment change: no real capability was removed since the next-frame shape never existed.

Repaired `.ok-planner/design/concepts/node-subscription.md`'s Invariants section: struck the "next-frame shape" clause, leaving self-subscription documented as strictly same-frame (one long-running frame, cascade walker's insert-then-drain-in-same-tx pattern). Verified: no other live corpus file references a next-frame shape (`grep -rn "next-frame" .ok-planner/design/` now empty); no code, test, or citation referenced the retired clause.
