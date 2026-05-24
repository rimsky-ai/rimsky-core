---
concept: claim-co-holdership
status: as-is
aliases:
  - inherits (legacy singular directive; superseded by `holds:`)
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Claim co-holdership

## Definition

Multiple node-runs holding the same `claim_handle` via the `holds:` template directive. Distinct from acquiring a claim (`claims:`): `holds:` adds a row in `rimsky_claim_holders` against an existing handle rather than opening a new one. The co-holdership extends the holding subgraph — auto-terminal fires only after all `rimsky_claim_holders` rows for the handle are non-active.

Template shape:

```yaml
nodes:
  - type: verify-staging
    executor: verifier-shape-checks
    subscribes:
      - { node: load-data, type: terminal/success }
    holds:
      staging-data: { from: load-data }
    userdata: { ... }
```

Co-holdership enables two distinct propagation patterns to coexist in a template:

- **Value-pass.** A source node extracts captured fields into its own attributes; downstream nodes consume via `{{nodes.<source>.attribute.<field>}}`. Lifetime-independent — works after the source's claim has closed. No `holds:` declaration needed.
- **Claim-pass.** A downstream node co-holds the live claim via `holds:` and uses `{{claim.<alias>.address | payload.<f> | scope}}`. Requires the claim to remain open; every co-holder's existence widens the holding subgraph and extends the claim's lifetime.

Without claim-pass, every downstream consumer would need to re-acquire the same scope, risking a different snapshot or a different queue item. The "from an upstream dependency" rule (see Invariants) is the deliberate constraint that keeps claim lifetimes legible: reading a template, you can immediately see which runs hold a given claim. There is no transitive auto-holdership through subscription chains; if you need a chain, declare `holds:` at every link.

## Boundaries

Owns: the `holds:` template directive, the per-co-holder `rimsky_claim_holders` row insertion at the co-holder's own acquire-tx, the holding-subgraph extension over co-holders. Does NOT own: claim acquisition (see `concept:claim`), state aggregation in the parent run (see `concept:node-run` aggregation table), the verifier pattern documentation. Adjacent: `concept:claim`, `concept:claim-handle`, `concept:auto-terminal`, `concept:node-run`.

## Invariants

- A co-holdership `from:` pointer MUST reference an upstream dependency. The co-holdership graph is a subset of the cell graph and naturally acyclic.
- At dispatch, the co-holder's `ExecuteRequest` carries the inherited claim's address (the same `ClaimResult` the original acquirer received) — same wire shape as `claims:`. Per `@blessed-invariant 20` the bytes are inert in rimsky.
- Persistence: `rimsky_claim_holders` row is INSERTed in the co-holder's own acquire-tx (`runner_acquire.go::insertCoHolderClaimHoldersAtAcquire`) keyed by `holder_run_id` (post-2026-05-15 column name; legacy was `holder_node`).
- Auto-terminal fires when all `rimsky_claim_holders` rows for the claim_handle are non-active. The holding-subgraph extension includes the acquirer plus every co-holder.
- Multiple co-holders are supported — the `holds:` block can list many; multiple nodes can co-hold the same claim independently. `strict.cancel_siblings: true` (the default error policy) walks the co-holder set when one fails; the walk is supervisor-scoped — see `concept:cancel-siblings` for the multi-supervisor consequence.

## Annotation sites

- `code:runtime/runner_acquire.go::insertCoHolderClaimHoldersAtAcquire` — the acquire-time INSERT.
- `code:foundation/spec/template.go` — `TemplateNodeDef.Holds` field.
- `code:foundation/persistence/claim_holders.go` — the table-shape interface.
- `code:test/scenarios/verifier/` — co-holder dispatch scenarios.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The verifier pattern (`executors/verifier-shape-checks/`, `executors/verifier-http/`) is the canonical use case: a verifier executor co-holds an upstream staging claim, runs checks, and its terminal contributes to the parent's aggregation; the aggregation outcome drives `Commit` (atomic swap) vs `Abandon` (drop staging).

- [2026-05-18] Folded content from former `docs/concepts/inheritance.md` (now retired). The retired doc framed co-holdership under the legacy `inherits:` directive name; examples rewritten to use the modern `holds:` directive. Added value-pass-vs-claim-pass distinction + lifetime-extension authoring story to Definition. `inherits:` recorded in Aliases as a legacy singular synonym.
