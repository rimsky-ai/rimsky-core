---
audit: sequenced-preserves-cascade-rounds
artifact: story:sequenced-preserves-cascade-rounds
determination: unsupported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# Sequenced mode delivers every round, but not always in arrival order

Unsupported. The story promises that M cascade rounds produce M dispatches in
arrival order. Two ways a sequenced receiver can fall behind its sender were
driven through the control API, and the promise holds in one and fails in the
other. Where the receiver's first round dispatched before the later rounds
arrived — the receiver held at a pause-mode breakpoint while the sender was
invalidated twice — the receiver dispatched three times seeing 1, then 2, then 3.
Where the sender emitted its rounds back to back inside one frame, which is what
a self-subscribing sender does and which gates the receiver for the whole burst,
the receiver dispatched four times seeing 4, then 1, then 2, then 3: the newest
round ran first and the earlier rounds followed in order. The count is right and
each dispatch sees the inputs of its own round in both ways, so nothing is
coalesced away; the ordering clause is the part a run contradicts. The
out-of-order result is reproducible and scales with the round count — 3, 1, 2 at
three rounds and 5, 1, 2, 3, 4 at five. An accumulator or a rapid-flip detector,
the workloads the story names, consumes the rounds in the wrong order in that
shape.
