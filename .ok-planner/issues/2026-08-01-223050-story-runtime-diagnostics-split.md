---
issue: story-runtime-diagnostics-split
kind: sprint
category: stories-splits
artifacts:
  - story:runtime-diagnostics
status: verified
opened: 2026-08-01T22:30:50Z
---

# Should the runtime-diagnostics story split per surface?

The runtime-diagnostics story bundles four read-only inspection surfaces — parked nodes, pending wake dependencies, held sub-graph frames, and claim holders — under one benefit: see why the runtime is wedged without ad-hoc database spelunking. The filed worry is that each surface is independently queryable, so the story fuses four outcomes.

Re-verification found a single well-formed story sentence, no prescriptive tail. The four surfaces are distinct queries, but the promise being made is the workflow across them: when a graph stalls, the operator can find the reason from the product's own surfaces. No corpus rule sets a per-surface story bar, and the bundling matches the catalog's one-persona-one-workflow pattern (the node-admin story is the nearest precedent).

## Options

- Split per surface — four files for one diagnostic workflow, and the "one wedge-diagnosis pass" framing is lost.
- Keep bundled — no identified cost.

The ruling decides whether a diagnostic workflow spanning several read surfaces is one story. Siblings `issue:story-node-admin-split`, `issue:story-http-node-split`, and `issue:story-instance-lifecycle-split` pose the same granularity question and should be ruled consistently.

## Ruling

> Recommended ruling (/verify-issues): keep the story bundled. "Why is my graph wedged?" is the user question; the four surfaces are parts of the answer, and none of them is a deliverable a user would accept alone.
>
> Rationale: a per-surface split optimizes for enumerability at the cost of the checkable promise — an owner can verify "I can diagnose a wedge end-to-end," but has no independent acceptance test for "I can list claim holders" divorced from that workflow. Flip case: if one surface acquires its own persona — say claim-holder listing becomes a capacity-planning tool rather than a wedge-diagnosis step — that surface then earns its own story.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
