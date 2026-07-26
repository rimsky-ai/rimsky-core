---
issue: graph-runtime-scheduler-import-exception
kind: human
category: layering
artifacts:
  - concept:module-layout
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:37:13Z
---

# One layering rule has a permanent-looking exception that still calls itself temporary

Rimsky's code is organized in ordered layers, and a lint check enforces the ordering: the "graph" layer (templates, instances, deciding what runs next) must not import the "runtime" layer (actually executing a node). Exactly one exception exists — two scheduler files are whitelisted wholesale — and the exception's own comment calls itself a "documented residual, flagged for separate follow-up." Nobody has ever followed up. The question is whether this is the permanent shape of the boundary or a debt to pay down, because right now the code says one thing and its comment promises another.

Why the exception exists at all: the scheduler's periodic tick — the heartbeat that finds work ready to run — lives in the graph layer, but the work it drives (sweeps that resume parked jobs, expire deadlines, and so on) is runtime-layer machinery. The tick has to call across the line somehow. The whitelist lets it import runtime directly.

Two facts sharpen the choice. The whitelist is file-level, not function-level — any unrelated runtime import added to either file later would pass the lint silently. And one of the two files has quietly outgrown the original rationale: besides the once-per-tick sweep calls, it now calls runtime functions that fire per-node on every cascade (a downstream re-run triggered by an upstream change) — a slice the "sweeps need this" justification never covered. The comment has also rotted: it names two functions that no longer exist. No design document commits to either future; the docs only record that the exception exists.

## Options

- **Bless it as permanent.** Drop the "flagged for follow-up" language from the lint comment and the decision doc (`decision:depguard-graph-purity-with-scheduler-exception`); state the scheduler→runtime edge as accepted architecture. Cost: this stays the one boundary in the codebase looser than the others, at file-level grain.
- **Close it by relocation.** Move the runtime-dependent orchestration to the runtime side, and have the graph layer trigger it through a small interface it doesn't need imports for. Restores unconditional purity; costs a real refactor and a call on whether the moved code is still "graph layer" behavior at all.
- **Split the scope.** Bless the sweep calls but treat the newer per-cascade calls separately (or vice versa) — they were never part of the same rationale.

The ruling decides: permanent or debt; if permanent, whether to tighten the whitelist's grain; if debt, whether both slices move or only one.

## Ruling

> Close the exception by relocation (owner ruling, 2026-07-25). The
> tick loop and its orchestration (Start / runLoop / tick, and the
> pure-cascade settlement pass) move from graph/scheduler up to the
> runtime layer; graph's scheduler package keeps only its step
> functions (frame pass, pure-cascade readiness, and the like),
> exported for runtime's loop to call downward. Runtime's tick calls
> its own sweeps locally and graph's steps in the legal direction.
> Then graph-purity becomes unconditional: drop the two-file exemption
> from .golangci.yml, and amend
> decision:depguard-graph-purity-with-scheduler-exception and
> concept:module-layout (its Exceptions line and the per-site-
> exemption invariant) to state the clean boundary.
>
> Rationale (owner, live): the loop was in the wrong layer, not the
> work — the usual shape is a loop in the higher layer calling step
> functions exported from the lower one. Relocation dissolves the
> exception with no interface machinery, exactly the "scheduler
> split" the original lint comment anticipated.
