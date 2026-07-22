# 2026-06-18 — Intent is a cascade-layer contract; document it as such

## Finding

While rewriting the `STORY-fanout-intent-inheritance` demo (cycle-2 review of
`plan:2026-06-18-fan-out`), the original STORY's `Acceptance` was discovered to
have been incorrectly framed. It asked for **producer-side** intent gating on
`Commit` ("the producer's Commit handler treats sub-claim Commits as read-only
… exhibits write-back"), but that is not where the architecture handles intent.

Architecturally:

- **Intent is a cascade-layer contract.** `code:lib/foundation/locks/conflict.go::ModeCoexists`
  takes the intents (and write semantics) of two would-be co-holders and
  decides whether they may coexist. It is the SOLE site that consults intent
  for any operational decision.
- **Producers MUST NOT branch on intent post-Open.** Both bundled stores
  currently read `intent` exactly once at Open — for shape validation — and
  ignore it everywhere else. `code:lib/services/stores/postgres/server/server.go::Open`
  and `code:lib/services/stores/filesystem/server/server.go::Open` are the
  only intent reads in either store. `Commit` / `Abandon` paths run the
  operator-configured pick policy without consulting intent. This is
  correct: producers don't see other claim-holders and have no basis to
  make co-holdership decisions.
- **The runtime's job** is to propagate parent intent into the persisted
  sub-claim row (`code:lib/runtime/runner_subclaim.go::AcquireSubClaims`), so
  the cascade's coexistence check has the right data to consult.

So intent has a clean three-layer separation:

| Layer    | Responsibility                                                |
| -------- | -------------------------------------------------------------- |
| Operator | declares intent on each `stores:` entry in a node             |
| Runtime  | propagates parent intent into every sub-claim row              |
| Cascade  | consults intent at coexistence-decision time (ModeCoexists)   |

The producer is intentionally absent from this table.

## Why the STORY's framing slipped

`concept:claim-producer` and `concept:claim-handle` do not document this
three-layer separation. A STORY author trying to figure out where to assert
intent's effect naturally looked at the most consumer-visible surface — the
producer's `Commit` outcome on the items table — and wrote an acceptance
clause asserting producer-side behavior. The acceptance was unsatisfiable in
the architecture as built, but nothing in the design docs steered the author
away from it.

## Proposed follow-up

Add an `## Intent` section to `concept:claim-handle` (and a cross-reference
from `concept:claim-producer`) that states the three-layer separation
explicitly and names `code:lib/foundation/locks/conflict.go::ModeCoexists` as
the contract site. Whatever language we use should make it obvious to a
future STORY author that:

- Asserting on "what the producer does at Commit when intent=r" is the wrong
  observable; the producer is intent-blind.
- The right observable is the persisted intent column on the sub-claim row
  (precondition) plus the cascade's coexistence decision (operational effect).

## Operator-surface gap surfaced en passant

The new demo had to query `rimsky_claim_handles` via direct `psql` because no
HTTP control-API surface exposes the persisted `intent` column on a claim
handle. The `code:lib/control/controlapi/lineage.go` and `code:lib/control/controlapi/claims.go`
routes carry claim_handle_id but not the intent. Worth considering an admin
or diagnostic endpoint that exposes per-claim intent + write_semantics,
purely for operator visibility and conformance probing. Out of scope for this
sketch, mentioned only because the demo exposed the gap.

## Status

Pending. To be picked up by a future spec that touches the concept catalog.
