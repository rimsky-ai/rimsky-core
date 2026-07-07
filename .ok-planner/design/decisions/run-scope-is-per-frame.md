---
decision: run-scope-is-per-frame
status: as-is
aliases: []
---

# RunScope lives inside exactly one frame; rejected the per-instance main RunScope alternative

## Choice

A RunScope is created inside a frame and lives for the lifetime of that frame. Three kinds (per `concept:run-scope`): **Root** (per-frame, no parent, created at frame start in the same tx as the frame row insert), **Sub-graph** (parent = whatever RunScope called the delegating node), **Fan-out partition** (parent = the fan-out node's RunScope, with a non-empty partition key). The frame row carries a non-null reference to its root RunScope; cascade walks and message delivery within the frame read the root from the frame, not from the instance.

## Rationale

Every property that scopes by RunScope — claim conflict, carry-forward, sequence monotonicity, the dispatcher's serialization gate, the cascade walker's in-flight-sealed invariant — is bounded by the frame and cannot leak across frame boundaries when the RunScope is nested inside its frame. The structural property does the work; no per-call frame qualifier is needed on RunScope-scoped queries because the qualifier is implicit in the RunScope identity.

With a per-instance main RunScope, those same properties would all bleed across frames: carry-forward would carry state from one frame's run into the next frame's gate evaluation, the dispatcher's serialization gate would refuse to claim a row in one frame while a sibling frame had a same-(node, scope) run mid-flight, and the per-(node, scope) sequence numbering would mean "the latest run in this scope" pointed at a run from a possibly-unrelated frame. Re-introducing a frame qualifier on every query would re-establish isolation at the call site but expose the surface to every missing-filter bug; making the RunScope itself per-frame is the strictly safer structural form (per `decision:frame-isolation-is-structural`).

## Alternatives

Main RunScope: one per instance, created at instance creation, frames and RunScopes orthogonal — rejected. With a shared instance-lifetime scope, RunScope-bounded carry-forward can read state from a sibling frame's mid-flight run before that frame's executor writeback commits, making a frame's inputs depend on cross-frame timing. The orthogonality framing is incompatible with the frame-isolation property the cascade model requires (per `decision:frame-isolation-is-structural`); a per-instance singleton is a persistence shape that imposes a model the runtime cannot honor.

RunScope created lazily on first cascade event within a frame — rejected. Lazy creation would make "the root RunScope of this frame" a derived quantity rather than a fact at frame start, complicating the carry-forward path (which needs the root identity at the first gate-eval) and the dispatcher's serialization gate (which needs to refuse claims against a non-existent scope deterministically). Eager creation at frame start is a single extra row insert in the same tx as the frame insert, costing nothing meaningful and removing a class of "did the scope exist yet?" predicates from the runtime.
