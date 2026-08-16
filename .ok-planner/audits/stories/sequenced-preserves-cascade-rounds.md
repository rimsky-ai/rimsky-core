---
audit: sequenced-preserves-cascade-rounds
artifact: story:sequenced-preserves-cascade-rounds
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:43:46Z
---

# Sequenced mode dispatches every cascade round, but not always in arrival order

Unsupported: the ordering half of the promise fails in one of the two shapes a
sequenced receiver can fall behind in, driven through the control API of an
all-in-one deployment. Where the receiver's own round is held at a pause-mode
breakpoint while two further rounds queue behind it, three rounds produced three
dispatches seeing one, two, then three, in arrival order. Where the sender
instead bursts several rounds back to back inside one frame while the receiver
is gated by the sender's in-flight run, every round still dispatched and every
round's own inputs still reached the executor — nothing was coalesced — but the
executor observed the newest round first and the earlier rounds after it, in the
sequence four, one, two, three. Run rows read through the observability surface
show why from the user's side: the earlier rounds sit queued while the last
round's row is created ready to dispatch and overtakes them. The behavior is
deterministic and scales with the burst, giving three-one-two at three rounds
and five-one-two-three-four at five. A workload the story names as its reason —
an audit trail, an accumulator, rapid-flip detection — reads a sequence that
never happened.
