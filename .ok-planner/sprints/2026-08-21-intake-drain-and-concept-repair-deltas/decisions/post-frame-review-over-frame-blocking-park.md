---
decision: post-frame-review-over-frame-blocking-park
---

# Human review happens after a frame, not inside one

## Choice

Rimsky offers no indefinite park. Every park carries a resume-at, so a node waiting on a human decision re-checks on a deadline instead of holding its frame open until someone acts. Rimsky's supported idiom for human review is post-frame review: the producing frame runs to completion, review happens outside it, and a follow-on graph or instance does the post-review work (see `concept:parked-state`, `concept:frame`).

## Rationale

A frame is a unit of work with a settlement. A park that waits for a human keeps its frame open for as long as the human takes, serializes the frame's other parallel work behind it, and turns operator response time into a runtime property. A mandatory resume-at keeps every parked node observable and bounded, and moving review out of the frame lets the produced work settle where an operator can inspect it. A re-checking park still blocks its frame, and that shape is right only where downstream cannot proceed without approval.

## Alternatives

- A no-deadline park that resumes only on an operator action — rejected: a frame's lifetime becomes a human's response time, and a forgotten review holds the frame open with nothing scheduled to notice.
