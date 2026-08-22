---
decision: no-cross-frame-attribute-caching
---

# No runtime path reads an attribute row from a prior frame

## Choice

Every substitution, diff-gate, and cascade decision reads only attribute rows produced inside the running frame. A read of a node-run from an earlier frame returns a missing-source error. State that must travel across frames rides the message payload, instance params, claim payloads, or threaded sub-graph inputs (see `concept:attribute`, `concept:frame`).

## Rationale

A frame is the unit of isolation. Confining reads to the frame makes a frame's behavior a function of its own trigger and its own work, so an operator reasons about one frame without knowing what ran before it. The per-run attribute rows are the durable record of what each run produced, and they stay readable for audit. They are not a store the runtime reads forward from. Cross-frame state remains available, but an author declares it on an explicit carrier rather than acquiring it through an implicit read.

## Alternatives

- Let a directive resolve against the most recent run of a node in any frame — rejected: a frame's result would depend on unrelated earlier frames, and an author would inherit carried state without asking for it.
- Hydrate a new frame's root scope from the previous frame's bag — rejected: a frame's starting state would then follow retention and sweep timing, and a fresh run scope would stop starting from schema defaults.
