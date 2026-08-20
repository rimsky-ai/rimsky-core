---
issue: structural-root-edges-are-derived-on-demand-not-injected-at-registration
kind: audit
category: conflicting
artifacts:
  - decision:structural-root-edge-injection-at-registration
  - decision:subscription-edges-only-from-explicit-block
status: promoted
opened: 2026-08-16T09:10:01Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# The structural-root edge decision describes a registration-time step the code never takes

A decision in the corpus says that registering a template adds one synthetic entry per structural root to the runtime's inverse-edge map, the table that answers "who is downstream of this node". The decision names computing that map later, at message receipt or during a cascade walk, as the rejected alternative. The code does the rejected thing. It derives the map on demand from the stored template and memoizes it per template hash. Three independent sites derive it: the graph builder, a CLI helper, and the test harness. No registration-time hook exists. The same decision also says, in a parenthetical, that substitution references and message-body reads are "sugar-form subscriptions that derive real edges". They are not. They disqualify a node from being a structural root but add no edges, which the sibling decision on subscription edges states outright. A third detail is a real fork. The code restricts structural roots to the main graph, so a node inside a delegated sub-graph that has no upstream of any kind gets no synthetic entry. The decision's own definition of a structural root would include that node. The ruling settles the two text corrections and the sub-graph question.

Why it matters: the decision tells a reader "one place, one moment" when three derivation sites exist and no registration moment does. That misleads a reader deciding where to hook a change to the injection rule, or how many caches a template change must invalidate. The parenthetical also contradicts a sibling decision on the very question of what contributes an edge.

## Options

- Correct the timing claim and the parenthetical to match the code, and name the main-graph restriction as deliberate, which the sub-graph decision already does; cost: a sub-graph-internal node with no upstream stays unwakeable by the empty message, and that boundary stays documented rather than fixed.
- Correct the two text claims and remove the main-graph restriction from the code, so sub-graph-internal nodes with no upstream also get the synthetic entry; cost: a behavioural change to how delegated sub-graphs wake, needing its own scenario coverage.

The ruling decides how the decision's text is repaired and whether the sub-graph restriction is intent or a gap.

## Ruling

> Generated ruling (/verify-issues): Rewrite the decision so it says what the code does. The runtime derives the inverse-edge map on demand from the stored spec and memoizes it per template hash, and the builder adds the structural-root entry at that derivation. The decision no longer lists deferred computation as the rejected alternative. Drop the parenthetical that calls substitution refs and message-body reads edge-deriving, since the sibling decision on subscription edges and the code both say they contribute no edges. The current-state-only rule forces both repairs: a decision may not record as its choice a mechanism the code does not have. Verified against the tree as it stands; nothing was applied.
>
> Recommended ruling (/verify-issues) for the remaining fork: keep the main-graph restriction and name it in the decision as deliberate. The sub-graph decision already treats structural-root injection as main-graph territory, and a caller enters a delegated sub-graph through its entry node rather than waking it from outside.
>
> Rationale: the rules force the two text repairs. The restriction is a design boundary two artifacts already agree on, so stating it beats widening the wake surface. Flip case: a story that needs a sub-graph-internal node to wake on an empty message would make the second option the right one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
