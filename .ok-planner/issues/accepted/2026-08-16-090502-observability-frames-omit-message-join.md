---
issue: observability-frames-omit-message-join
kind: audit
category: conflicting
artifacts:
  - concept:cascade-graph
status: verified
opened: 2026-08-16T09:05:02Z
---

# The dashboard's frame routes return frames without their triggering message

Every frame (one wave of work through an instance) opens because of a message, and the cascade-graph concept says the frames-read routes join each frame to its triggering message so "why did this frame open" is answerable directly — a story on frame-origin audit promises the same. Four routes read frames; the two under the instance path use the joined store read and return message type, sender and sender kind; the two under the dashboard's observability path call the unjoined read and return only the message id, so a dashboard reader must make a second call. The store already offers both shapes. The ruling points the two dashboard handlers at the joined read.

## Options

- Point the observability frame handlers at the joined store reads and add the three message fields to their response; cost: none beyond the change.
- Narrow the concept and the story to the instance-scoped routes; cost: a real commitment change on the surface the story names.

The ruling makes the join universal, as the concept already says.

## Ruling

> Generated ruling (/verify-issues): Have the observability frame list and get handlers use the joined store reads and carry the triggering message's type, sender and sender kind, matching the instance-scoped routes; the concept's invariant is already the compliant text. Forced by the concept and the frame-origin-audit story, both of which quantify over the frames-read routes without exception. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
