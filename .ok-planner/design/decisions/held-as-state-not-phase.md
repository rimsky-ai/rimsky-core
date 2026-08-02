---
decision: held-as-state-not-phase
---

# Held is a node-run state with cascade-defer until auto-terminal commit/abandon

## Choice

`held` is a first-class state in the node-run state machine, not a lifecycle phase wrapping the terminal. A run whose terminal participates in at least one active claim handle transitions to `held` instead of settling — uniformly for acquirer and co-holders, and for success and error outcomes alike. While held, the run's cascade to receivers outside its holding subgraph is deferred: it fires only when claim resolution (the auto-terminal commit/abandon machinery) transitions the run out of `held` — all claims committed settles it as success, any abandoned settles it as failure, and each holder's own deferred cascade fires with its transition. `held` is a member of the in-flight state set, sealed against mutation like the rest of it.

## Rationale

The held mechanism exists to express "this work is provisional pending claim commit." If the cascade fired the moment the executor returned held, downstream subscribers would act on provisional data with no retract path if auto-terminal later abandons — downstream may already have spawned its own cascades. Deferring the cascade until resolution makes the wire-level cascade match the semantic: downstream sees only committed (or abandoned) signals, never provisional. And the state machine is the right surface for "is this run still in-flight?" — held as a state expresses the sealed-against-mutation invariant the same way the other in-flight states do, rather than through a parallel mechanism.

## Alternatives

- Keep held as a phase and add an explicit "may the cascade fire?" boolean — rejected: duplicates information the state machine already encodes; a parallel boolean splits the source of truth.
- Fire the cascade at the held terminal and add a retract-cascade mechanism — rejected: retraction has to chase downstream cascades that already fanned out; the transitive closure of "what to retract" is unbounded, while defer-until-commit is bounded.
- Make cascade-defer-on-held opt-in per node type — rejected: "provisional pending commit" means the same thing for every held node, and downstream subscribers cannot opt out of the rollback semantic without breaking the held contract; per-node cascade knobs belong to the cascade-mode surface, and held-defer is not one of them.
