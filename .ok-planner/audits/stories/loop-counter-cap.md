---
audit: loop-counter-cap
artifact: story:loop-counter-cap
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# The bundled loop-counter node bounds iteration by its max attribute

Supported. A template whose single node names the bundled `loop_counter` kind and
supplies a `max` input attribute was driven through the control API twice. With
`max: 4` the node dispatched four times, emitting counts 1, 2, 3, 4 tagged
`loop`, `loop`, `loop`, `done`. With `max: 1` it dispatched once, emitting count 1
tagged `done`. Both instances then reached rest with no live runs, so the cap
stopped the iteration in each case. The template declares no executor of its own,
so the bounded loop was expressed without authoring one.
