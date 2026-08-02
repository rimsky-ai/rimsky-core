---
issue: intent-cascade-two-boundary-opacity-uncarried
kind: sprint
category: intent-ledger
artifacts:
  - concept:cascade
  - concept:run-scope
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:41:50Z
---

# A ratified cascade-boundary invariant exists only in a session transcript

Cascade is rimsky's signal propagation: when a node settles, its subscribers fire. Run scopes partition a graph into sub-graphs (delegated child graphs, fan-out partitions), and the question of where cascade may cross a scope boundary was settled by an explicit owner ruling in July, at transcript tier: cascade crosses at exactly two places — a sub-graph invocation's entry (caller success seeds the internals) and a fan-out parent's settlement — and nowhere else; partition-internal cascades never propagate outward, and outside cascade fires the calling node without ever descending into sub-graph internals.

The corpus carries the two trigger points piecemeal — the delegation concept describes entry-success seeding, the child-execution concept's invariant pins the parent-settlement bridge — but the closure half ("and nowhere else") and the opacity half (sub-graphs externally opaque) appear in no live artifact. A ratified invariant that exists only in a transcript is exactly what the corpus is for; carrying it in is an invariant addition only a sprint may make. The run-scope concept's own boundaries section already defers cascade-edge semantics to the cascade concept (`concept:run-scope`), which settles the only open sub-question — which file owns it.

## Options

- Add the two-boundary + opacity invariant to the cascade concept — the home its sibling concept already points at.
- Add it to the run-scope concept — fights that concept's own deferral of cascade-edge semantics.
- Rule the closure below corpus altitude with scenario tests as the record — hard to square with a ruling the owner explicitly elevated to transcript tier and never walked back.

The ruling confirms carrying the ratified invariant into the cascade concept. (The cascade concept's overview table was separately repaired this run — the addition lands in a freshly-corrected file.)

## Ruling

> Generated ruling (/verify-issues): carry the ratified invariant into the cascade concept as stated — cascade crosses a run-scope boundary at exactly two places, sub-graph entry-success and fan-out parent settlement, and nowhere else; sub-graphs are externally opaque to cascade. The 2026-07-14 owner ruling already decided the substance; the run-scope concept's deferral decides the home; all that remains is the sprint act of writing it in.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
