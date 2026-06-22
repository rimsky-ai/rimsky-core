---
decision: held-as-state-not-phase
status: as-is
aliases: []
---

# Held is a node-run state with cascade-defer until auto-terminal commit/abandon

## Choice

`held` is promoted from a node-run lifecycle phase to a first-class state in the seven-state machine (`pending`, `stale`, `running`, `held`, `parked`, `fresh`, `failed`). The trigger for `held` is uniform across acquirer and co-holders: a run transitions `running → held` at terminal time iff it participates in at least one active claim handle (as acquirer or as co-holder). The same rule applies to terminal/error outcomes inside a held subgraph — they abort to `held` (the run's co-holder participation marks failed), not to `failed`, so the poison rule can transition every participating holder uniformly when the claim resolves.

Cascade from a held node-run is filtered to subgraph-members only: receivers whose node type is in the union of holding-subgraph members for the subgraphs the sender participates in. Non-member receivers do NOT see cascade during the held period; their cascade fires when the holder transitions out of held.

When a claim resolves (commit or abandon), the resolution walker visits every co-holder participation for that claim handle:

- If a holder run has any OTHER active claim_handles, it stays held (re-evaluated when those resolve).
- Else, the poison rule evaluates the holder's full claim portfolio: all committed → `held → fresh` (cascade `terminal/success` to non-members); any abandoned → `held → failed` (cascade `terminal/error/abandoned` to non-members).

Each per-holder transition fires that holder's own deferred cascade — every member that executed broadcasts its own outcome to non-members of the union subgraph.

`held` joins the set of in-flight states (`pending`, `stale`, `running`, `held`, `parked`) for which the cascade walker treats the receiver as sealed: cascade events targeting a held node-run queue a new cascade-driven pending per the standard walker rule, never mutate the held run. The gate evaluator's "any subscribed upstream in-flight" check skips held upstreams that share a subgraph with the receiver — co-members must not gate each other's dispatch.

## Rationale

The held mechanism exists to express "this work is provisional pending claim commit." If the cascade fires `terminal/success` downstream at the moment the executor returns held=true, downstream subscribers act on provisional data — and there is no retract mechanism if auto-terminal later abandons. The cascade has already happened; downstream may have spawned its own cascades; the rollback has no way to chase them.

Held as a first-class state with the cascade walk deferred until auto-terminal resolution makes the wire-level cascade match the conceptual semantic: downstream sees only committed (or abandoned) signals, never provisional. The held interval is observable as a real state at the run row, not a transient phase that wraps the terminal.

The seven-state machine already needs `pending` and `held` as distinct values (to express the sealed-against-mutation invariant uniformly across the in-flight set). Adding `held` here is the same shape as adding `pending` — both are existing implicit states being made explicit at the state column.

## Alternatives

Keep held as a phase, add an explicit "is the cascade allowed to fire?" boolean — rejected because it duplicates information already encoded by the state machine. The state machine is the right surface for "is this run still in-flight?" — adding a parallel boolean splits the source of truth.

Keep cascade firing at the held terminal, add a retract-cascade mechanism — rejected because retract-cascade has to chase downstream cascades that already fanned out (downstream may have triggered its own cascades, parked itself, dispatched messages). The transitive closure of "what to retract" is unbounded; defer-until-commit is bounded.

Make cascade-defer-on-held opt-in per node-type — rejected because the held semantic is universal: "provisional pending commit" means the same thing for every held node, and downstream subscribers cannot reasonably opt out of the rollback semantic without breaking the held contract. The per-template cascade-mode surface is the right place for cascade-behavior knobs that legitimately differ per node (the four cascade-mode values); held-defer is not one of those.
