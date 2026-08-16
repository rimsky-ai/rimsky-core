---
issue: structural-root-edges-are-derived-on-demand-not-injected-at-registration
kind: audit
category: conflicting
artifacts:
  - decision:structural-root-edge-injection-at-registration
  - decision:subscription-edges-only-from-explicit-block
status: verified
opened: 2026-08-16T09:10:01Z
---

# The structural-root edge decision describes a registration-time step the code never takes

A decision in the corpus says that when a template is registered, the runtime's inverse-edge map (the table that answers "who is downstream of this node") gains one synthetic entry per structural root, and that computing this later — at message receipt or during a cascade walk — was the rejected alternative. The code does the rejected thing: the map is derived on demand from the stored template and memoized per template hash, at three independent sites (the graph builder, a CLI helper, and the test harness), and no registration-time hook exists. The same decision also says, in a parenthetical, that substitution references and message-body reads are "sugar-form subscriptions that derive real edges"; they do not — they disqualify a node from being a structural root but add no edges, which the sibling decision on subscription edges states outright. A third detail is a real fork: the code restricts structural roots to the main graph, so a node inside a delegated sub-graph that has no upstream of any kind gets no synthetic entry, though the decision's own definition of a structural root would include it. The ruling settles the two text corrections and the sub-graph question.

Why it matters: a reader deciding where to hook a change to the injection rule, or how many caches must be invalidated when a template changes, is told "one place, one moment" when there are three derivation sites and no registration moment at all; and the parenthetical contradicts a sibling decision on the very question of what contributes an edge.

## Options

- Correct the timing claim and the parenthetical to match the code, and name the main-graph restriction as deliberate (the sub-graph decision already glosses it as such); cost: a sub-graph-internal node with no upstream stays unwakeable by the empty message, and that stays a documented boundary rather than a fixed gap.
- Correct the two text claims and remove the main-graph restriction from the code so sub-graph-internal no-upstream nodes also get the synthetic entry; cost: a behavioural change to how delegated sub-graphs wake, needing its own scenario coverage.

The ruling decides how the decision's text is repaired and whether the sub-graph restriction is intent or a gap.

## Ruling

> Generated ruling (/verify-issues): Rewrite the decision so it says what the code does — the inverse-edge map is derived on demand from the stored spec and memoized per template hash, and the structural-root entry is added by the builder at that derivation, with deferred computation no longer listed as the rejected alternative; and drop the parenthetical that calls substitution refs and message-body reads edge-deriving, since the sibling decision on subscription edges and the code both say they contribute no edges. Both are forced by the current-state-only rule: a decision may not record as its choice a mechanism the code does not have. Verified against the tree as it stands; nothing was applied.
>
> Recommended ruling (/verify-issues) for the remaining fork: keep the main-graph restriction and name it in the decision as deliberate — the sub-graph decision already treats structural-root injection as main-graph territory, and delegated sub-graphs are entered through their entry node, not woken from outside.
>
> Rationale: the two text repairs are rules-forced; the restriction is a design boundary two artifacts already agree on, so stating it beats widening the wake surface. Flip case: a story that needs a sub-graph-internal node to wake on an empty message would make the second option the right one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
