---
story: claim-handoff-durable
status: as-is
---

# Template author wires a durable held claim that survives across instance dispatches

## Role

As a template author wiring an asset-producing topology — or any workflow whose claim must outlive a single instance dispatch — I can declare `lifetime: durable` on the acquirer's claim, optionally have co-holders share it via `holds:` within the producing dispatch, and trust that the claim handle row persists past auto-terminal (promoted to committed rather than reaped), so that future dispatches in the same instance can co-hold the same durable row by alias, the producer still occupies the scope, and release happens only on explicit operator action or instance termination.

## Capability

`lifetime: durable` on a claim causes the claim handle row to be promoted to state committed at holding-subgraph completion AND to be exempted from the retention sweep. The row stays present past the dispatch that produced it; conflict detection includes it (the producer still occupies the scope); future dispatches can `holds:` the same alias against the upstream durable claim and read `{{claim.<alias>.address|payload.<f>|claim_scope}}` from the persisted handle. Released only by the asset Release endpoint or by instance termination's held-durable-release path.

## Business value

Workflows whose data outputs are consumed by future dispatches — assets, re-materializable artifacts, "build once, co-hold many times" patterns — compose naturally. The author chooses lifetime in the template; rimsky's persistence layer enforces survival without per-template bookkeeping. Paired with `story:asset-management`, the durable claim becomes the writable counterpart to the operator's readable asset surface.

## Acceptance

A template whose acquirer declares a claim with `lifetime: durable`. In the producing dispatch, the acquirer settles `terminal/success`; auto-terminal fires Commit; the claim handle row reaches state committed with the held flag set. After the producing dispatch terminates, the claim handle row is still present (the retention sweep does not reap it). A second dispatch on the same instance — with a node declaring `holds:` against the same upstream alias — finds the row, the co-holder's `{{claim.<alias>.address}}` substitution resolves to bytes equal to the persisted handle's address, and the co-holder settles fresh without re-acquiring. While committed-durable, an unrelated competing acquirer attempting to open the same scope hits a conflict (committed-durable rows participate in conflict detection). Triggering the asset Release endpoint transitions the row out of the active-scope set; a subsequent acquirer succeeds.

## Falsifier

The claim handle row is reaped after the producing dispatch's terminal despite `lifetime: durable`, OR a later dispatch's `holds:` against the upstream alias returns missing-source for `{{claim.<alias>.address}}`, OR a competing acquirer against the same scope succeeds while the row is committed-durable (conflict detection didn't include it), OR the asset Release endpoint doesn't actually release the row, OR instance termination doesn't fire the held-durable-release path.

## Proof

Executable proof — cross-dispatch persistence (open a `lifetime: durable` claim in a dispatch; settle; force a retention sweep tick; assert the row is still present with state committed); cross-dispatch `holds:` (a later dispatch with a co-holder declaring `holds:` against the original upstream's alias; assert dispatch succeeds and substitution resolves to the persisted bytes); conflict detection includes committed-durable (a separate template's acquirer against the same scope hits `terminal/error/acquire/unavailable` while the row is committed-durable); release-path (operator hits the asset Release endpoint; row leaves the active-scope set; a subsequent acquirer against the same scope succeeds); instance-termination release (terminate the instance while a held-durable row exists; the held-durable-release path fires; the row exits). Pins `@blessed-invariant 22` on `concept:claim-handle` and the `concept:claim-lifetime` invariant "Conflict detection includes committed-durable rows."
