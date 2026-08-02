---
issue: intent-node-subscription-next-frame-shape-unimplemented
kind: sprint
category: intent-ledger
artifacts:
  - concept:node-subscription
  - concept:frame
status: open
opened: 2026-08-01T22:41:20Z
---

# The live node-subscription concept asserts a next-frame shape no code implements

## Problem

`concept:node-subscription` (in text added by a 2026-07-17 doc-only adjudication commit) asserts a 'next-frame shape' of self-subscription that opens a fresh frame for the same node-instance on every matching commit. No code implements it (zero repo-wide hits for any next-frame identifier), and it contradicts the triggering-message-only frame-open invariant proven at schema level (`rimsky_frames.triggering_message_id NOT NULL`). The ledger's transcript-tier record says `frame: next` was retired and frames open only on message arrival.

Evidence tier: mixed.

## Candidates

- Strike the next-frame clause from the concept, restoring same-frame-only self-subscription (matches transcript-tier intent and the schema invariant).
- Implement fresh-frame-per-commit self-subscription atop a real message-triggered frame open (makes the doc true).
