---
audit: idempotent-mode-dedupes
artifact: story:idempotent-mode-dedupes
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# An idempotent cascade mode drops re-runs whose inputs equal a predecessor

Supported. Against an all-in-one deployment driven through the control API, a
sender cascaded to a receiver four times. When the receiver's resolved bag was
byte-identical across all four rounds, a non-idempotent mode dispatched the
receiver's executor four times, while both idempotent modes the story names —
comparing against the queued predecessor, and also against the most recent
settled run — dispatched it exactly once. When the receiver read a value that
changed every round, both idempotent modes dispatched all four rounds and the
four dispatches carried four distinct bags, so the drop follows from input
equality rather than from cascade rounds being coalesced.
