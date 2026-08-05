---
issue: closed-taxonomy-enumeration-convention-split
kind: human
category: design-convention
artifacts:
  - concept:signal
  - concept:transition-reason
status: answered
opened: 2026-07-22T10:18:33Z
github: https://github.com/rimsky-ai/rimsky-core/issues/32
---

# Two concepts document a code-owned closed set two different ways

## Question

Does `concept:signal` still hand-enumerate its code-owned closed sets in
prose where `concept:transition-reason` declines to, or have the two
concepts already converged on one convention?

## Answer

They already converged. `concept:signal`'s "`transient/*` — dispatch-internal"
section states: "Membership of the `transient/*` leaf set is owned by the
signal-taxonomy code, not enumerated here; the taxonomy validator is the
closed set's enforcement point" — and its "Payload schemas" section states:
"per-type field membership is owned by the emission code and the CEL
environment construction, not enumerated here." Both match
`concept:transition-reason`'s convention verbatim ("Membership of the set is
owned by the state-machine code, not enumerated here"). This text landed in
commit `07c02d5d` (2026-06-24), four weeks before this issue was filed
(2026-07-22) — the filed Problem describes a pre-`07c02d5d` snapshot of the
doc (consistent with the issue's "found during a rimsky-docs reconcile
against v0.11.0" provenance) and had already rotted at filing time.

The one enumeration `concept:signal` still states in prose — "The
`terminal/*` leaves are exactly two kinds: `terminal/success` and
`terminal/error/<error_class>`" — is not the same kind of case: it is a
structural invariant of the concept itself (a run-terminating outcome is
definitionally either success or an error), restated as an Invariant ("Only
`terminal/*` signals end a run... at exactly one of `terminal/success` or
`terminal/error/<class>` and at no other type-path"), not a mirror of an
open, code-owned set that could grow independently of the concept (as
`transient/*` leaves and per-type payload fields both can). The two concepts
do not disagree on convention.
