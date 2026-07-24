---
spec: cascade-and-claim-handoff
date: 2026-06-10
---

# Cascade and claim-handoff design corpus extension

## Framing

This spec extends the design corpus with four user-outcome stories — three filling the claim-handoff family (single-frame, multi-frame, durable) and one pinning the cascade engine's signal-blind property — and sharpens the wording of the `serial_queue` description in `concept:frame`. The stories' executable proofs close three GitHub-surfaced symptoms: a co-holder's `{{claim.<alias>.address}}` substitution failing at dispatch (issue #16); a per-sender `terminal/error/<class>` subscription silently skipped on v0.6.0 (issue #15 — likely already fixed by the post-v0.6.0 signal-emit refactor at commit `6088bb0`; the proof either confirms or surfaces the residual bug for the implementer to close per the necessity rule); and a misreadable wording in `concept:frame` that contributed to a downstream misdiagnosis (issue #17).

The spec carries no new technical decisions. The work brings the code and the prose back to existing intent — the concept catalog and the code-level invariants already say what should happen.

## User outcomes

### STORY-claim-handoff

**Role.** As a template author building a multi-node atomic-staging workflow, I can declare an upstream acquirer node that opens a claim and downstream co-holder nodes that share the same claim via `holds:` — reading the live claim's address, payload fields, and scope bytes via `{{claim.<alias>.address|payload.<f>|claim_scope}}` to do work against the staged location — then have the runtime fire Commit (all-success) or Abandon (any-failed) atomically across the holding subgraph, so that I compose stage-then-write-then-verify-then-commit pipelines (and similar all-or-nothing patterns) without re-acquiring the same claim from every node.

**Capability.** A downstream node declaring `holds: { <alias>: { from: <upstream-type> } }` co-holds the upstream's claim by alias; at dispatch the runtime resolves `{{claim.<alias>.address}}`, `{{claim.<alias>.payload.<key>}}`, and `{{claim.<alias>.claim_scope}}` substitutions against the held claim's actual bytes — the same acquired result the original acquirer received. Auto-terminal fires once every node in the holding subgraph settles non-active: Commit on all-success, Abandon on any-failed.

**Business value.** Multi-node atomic-staging composes naturally from existing template-DSL primitives. The author writes one acquirer plus N co-holders; rimsky enforces the all-or-nothing guarantee without bespoke rollback logic in template-land.

**Acceptance.** A template with (a) an acquirer node opening a claim with alias X via its `stores:` declaration, and (b) a co-holder declaring `holds: { X: { from: <acquirer-type> } }` AND reading `{{claim.X.address}}` (or `.payload.<f>` or `.claim_scope`) in its attribute schema. When the acquirer is invalidated and settles `terminal/success`, the co-holder dispatches with the substitution resolved to the held claim's actual bytes — the address bytes the co-holder receives equal the bytes the acquirer received. When the co-holder also settles `terminal/success`, the held claim's auto-terminal fires Commit (the holding subgraph promotes to committed; the producer's Commit verb fires). When either the acquirer or the co-holder settles failed, auto-terminal fires Abandon.

**Falsifier.** The co-holder's `{{claim.X.address}}`, `{{claim.X.payload.<f>}}`, or `{{claim.X.claim_scope}}` substitution fails at dispatch with `terminal/error/template_resolution_failed`, OR the co-holder dispatches but receives substituted bytes that don't equal the acquirer's bytes, OR auto-terminal fails to fire Commit when every holding-subgraph member settles fresh, OR fails to fire Abandon when any member settles failed.

**Proof.** Executable proof — table-driven scenario test covering the regression-close shape (acquirer + co-holder reading `{{claim.X.address}}` → Commit), per-field substitution kinds (`.address`, `.payload.<f>`, `.claim_scope` each resolve to the held claim's bytes), the Abandon path (co-holder forced to terminal-error via `error_types: give_up` → Abandon), the multi-co-holder Commit shape (two co-holders both reading; auto-terminal fires only after the slowest settles), and wire-payload parity (a co-holder receives a store-handle wire entry identical to what the acquirer receives — same handle bytes regardless of whether the receiver opened the claim or co-held it). Pins `concept:claim-co-holdership` invariant "At dispatch, the co-holder's execution request carries the co-held claim's address (the same acquired result the original acquirer received)."

### STORY-claim-handoff-across-frames

**Role.** As a template author wiring a multi-node atomic-staging workflow where the co-holder runs in a different frame from the acquirer — e.g. a `frame: next` per-node subscription, or a cross-cutting (`instance: true`) subscription — I can rely on the held claim staying active across the frame boundary until the holding subgraph completes, with `{{claim.<alias>.address|payload.<f>|claim_scope}}` resolving in the new frame to the same bytes it would in the acquirer's frame, so that I'm free to separate work into independent frames for clean per-iteration audit and distinct `frame.start`/`frame.end` markers without breaking the atomicity guarantee.

**Capability.** A held claim's lifetime is governed by the holding subgraph, not by any frame. When the co-holder's subscription opens a fresh frame (`frame: next`, or `instance: true` which defaults to `frame: next`), the claim handle row stays active until every holder settles, regardless of how many frames the holding subgraph spans. The substitution context's claim-alias lookup walks from the holding-subgraph's template `holds:` directive to the upstream's claim-handle row directly, so the alias resolves in any frame where the holding subgraph is still open.

**Business value.** Frame topology and claim lifetime are independent design knobs. Authors can choose per-iteration frames for audit-trail granularity or for distinct frame-timeout windows without losing the holding-subgraph atomicity. Conversely, an author who needs the entire holding subgraph in one frame (for shared in-frame substitution context or a single `frame.start`/`frame.end` pair) chooses `frame: in` and gets that — the claim doesn't care either way.

**Acceptance.** Same template shape as `story:claim-handoff` (acquirer + co-holder with `holds:` reading `{{claim.X.address}}`), but the co-holder's `subscribes:` block sets `frame: next` (or uses `instance: true`). When the acquirer is invalidated and settles `terminal/success` in one frame, the cascade walk opens a fresh frame for the co-holder; the co-holder dispatches in the new frame; the co-holder's `{{claim.X.address}}` resolves to bytes equal to the acquirer's claim handle's address; both settle fresh; auto-terminal fires Commit only after the co-holder's frame ends. The claim handle row stays active across the frame boundary; the acquirer's run and the co-holder's run carry distinct frame ids, both committed before the held claim resolves.

**Falsifier.** The held claim is released between the acquirer's frame end and the co-holder's frame start (auto-terminal fires prematurely on the acquirer's settlement alone), OR the co-holder's `{{claim.X.<field>}}` substitution returns missing-source in the new frame (alias context not threaded across the frame boundary), OR auto-terminal fires Commit before the co-holder's frame ends.

**Proof.** Executable proof — three scenario variants: a co-holder with `frame: next` on a per-node subscription (assert the co-holder's frame id differs from the acquirer's, the substitution resolves to identical bytes, the claim handle row stays active until the second frame ends, auto-terminal fires Commit once after the second frame); a co-holder with `instance: true` (cross-cutting; defaults to `frame: next`); and a three-frame chain (acquirer plus two co-holders each subscribed `frame: next`, each reading `{{claim.X.address}}`; assert three distinct frame ids, the claim handle row stays active until the third frame ends).

### STORY-claim-handoff-durable

**Role.** As a template author wiring an asset-producing topology — or any workflow whose claim must outlive a single instance dispatch — I can declare `lifetime: durable` on the acquirer's claim, optionally have co-holders share it via `holds:` within the producing dispatch, and trust that the claim handle row persists past auto-terminal (promoted to committed rather than reaped), so that future dispatches in the same instance can co-hold the same durable row by alias, the producer still occupies the scope, and release happens only on explicit operator action or instance termination.

**Capability.** `lifetime: durable` on a claim causes the claim handle row to be promoted to state committed at holding-subgraph completion AND to be exempted from the retention sweep. The row stays present past the dispatch that produced it; conflict detection includes it (the producer still occupies the scope); future dispatches can `holds:` the same alias against the upstream durable claim and read `{{claim.<alias>.address|payload.<f>|claim_scope}}` from the persisted handle. Released only by the asset Release endpoint or by instance termination's held-durable-release path.

**Business value.** Workflows whose data outputs are consumed by future dispatches — assets, re-materializable artifacts, "build once, co-hold many times" patterns — compose naturally. The author chooses lifetime in the template; rimsky's persistence layer enforces survival without per-template bookkeeping. Paired with `story:asset-management`, the durable claim becomes the writable counterpart to the operator's readable asset surface.

**Acceptance.** A template whose acquirer declares a claim with `lifetime: durable`. In the producing dispatch, the acquirer settles `terminal/success`; auto-terminal fires Commit; the claim handle row reaches state committed with the held flag set. After the producing dispatch terminates, the claim handle row is still present (the retention sweep does not reap it). A second dispatch on the same instance — with a node declaring `holds:` against the same upstream alias — finds the row, the co-holder's `{{claim.<alias>.address}}` substitution resolves to bytes equal to the persisted handle's address, and the co-holder settles fresh without re-acquiring. While committed-durable, an unrelated competing acquirer attempting to open the same scope hits a conflict (committed-durable rows participate in conflict detection). Triggering the asset Release endpoint transitions the row out of the active-scope set; a subsequent acquirer succeeds.

**Falsifier.** The claim handle row is reaped after the producing dispatch's terminal despite `lifetime: durable`, OR a later dispatch's `holds:` against the upstream alias returns missing-source for `{{claim.<alias>.address}}`, OR a competing acquirer against the same scope succeeds while the row is committed-durable (conflict detection didn't include it), OR the asset Release endpoint doesn't actually release the row, OR instance termination doesn't fire the held-durable-release path.

**Proof.** Executable proof — cross-dispatch persistence (open a `lifetime: durable` claim in a dispatch; settle; force a retention sweep tick; assert the row is still present with state committed); cross-dispatch `holds:` (a later dispatch with a co-holder declaring `holds:` against the original upstream's alias; assert dispatch succeeds and substitution resolves to the persisted bytes); conflict detection includes committed-durable (a separate template's acquirer against the same scope hits `terminal/error/acquire/unavailable` while the row is committed-durable); release-path (operator hits the asset Release endpoint; row leaves the active-scope set; a subsequent acquirer against the same scope succeeds); instance-termination release (terminate the instance while a held-durable row exists; the held-durable-release path fires; the row exits). Pins `@blessed-invariant 22` on `concept:claim-handle` and the `concept:claim-lifetime` invariant "Conflict detection includes committed-durable rows."

### STORY-cascade-signal-blind

**Role.** As a template author wiring reactive nodes, I can subscribe to any cascade-firing signal type the runtime emits — `terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`, `event/<name>` — and have my subscriber dispatched when a matching signal lands, regardless of which type it is, so that I write "react to X" topologies without learning which signal types are first-class and which are quietly second-class.

**Capability.** The cascade engine is signal-blind. Subscription firing is gated purely on `(edge type-path match) AND (CEL when: predicate)`; the engine never branches on the signal type itself. Equivalently: the same code path that delivers `terminal/success` to its subscribers delivers `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`, and `event/<name>` to theirs. A new cascade-firing signal type added to the canonical taxonomy automatically becomes observable.

**Business value.** Template authors write "react to X" without learning which signal types are first-class and which are quietly second-class. The "react to upstream error" topology — a deterministic-primary node `terminal/error`s on cache-miss, paired with a repair node subscribing to its `terminal/error/*` — composes the same way as the "react to upstream success" topology.

**Acceptance.** For each cascade-firing signal type in the canonical taxonomy, a node declaring a subscription with a matching type-path dispatches when the upstream emits the signal. Exact-type and trailing-`*` prefix subscription shapes both fire. Per-sender (`{ node: X, type: ... }`) and cross-cutting (`instance: true`) subscription shapes both fire. The signal's audit row lands in the event log on every emit, so an operator's event-log query and a subscriber's wait-set see the same signal. Concretely: a per-sender `{ node: X, type: terminal/error/* }` subscription fires when the sender settles `terminal/error/<class>` via either `error_types: give_up` (failed-color settlement) or `error_types: pass` (fresh-color settlement).

**Falsifier.** Any single cascade-firing signal type produces no subscriber dispatch when its subscription matches the type-path, OR the event-log audit row for that emit is missing, OR the per-sender `terminal/error/*` subscriber doesn't dispatch when the sender settles `terminal/error/<class>`.

**Proof.** Executable proof — table-driven scenario test that iterates over the cascade-firing signal types and asserts, for each: (a) a per-sender subscription on that type-path dispatches its subscriber when the upstream emits the signal; (b) a cross-cutting (`instance: true`) subscription on that type-path dispatches; (c) the audit row for the signal lands in the event log; (d) trailing-`*` prefix subscription shapes match every type-path with that prefix. Pins `concept:cascade` invariant "Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL when: predicate evaluates true." The non-cascading signal types — `terminal/park/<reason>` and `terminal/infra/<class>` — are explicitly out of the proof's scope per their design (those emit a bare audit row, no cascade, because the node resumes rather than settling).

## Design changes

- Story: create `design/stories/claim-handoff.md` capturing this spec's STORY-claim-handoff verbatim — Role, Capability, Business value, Acceptance, Falsifier, Proof. Frontmatter: `story: claim-handoff`, `status: as-is`.
- Story: create `design/stories/claim-handoff-across-frames.md` capturing this spec's STORY-claim-handoff-across-frames verbatim. Frontmatter: `story: claim-handoff-across-frames`, `status: as-is`.
- Story: create `design/stories/claim-handoff-durable.md` capturing this spec's STORY-claim-handoff-durable verbatim. Frontmatter: `story: claim-handoff-durable`, `status: as-is`.
- Story: create `design/stories/cascade-signal-blind.md` capturing this spec's STORY-cascade-signal-blind verbatim. Frontmatter: `story: cascade-signal-blind`, `status: as-is`.
- Concept: mutate `design/concepts/frame.md` in place. Replace the existing `serial_queue` bullet (currently reading "**`serial_queue`** preserves ordering. Each invalidate produces its own frame; frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. \"process item A, then process item B\").") with:

  > **`serial_queue`** preserves ordering. Each boundary-crossing invalidate (operator-API send or publisher-origin message) produces its own frame; cascade walks stay within the current frame. Frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B").

## Manifest

### Stories
- **STORY-claim-handoff** — co-holder reads `{{claim.<alias>.<field>}}` at dispatch; auto-terminal Commit/Abandon fires across the holding subgraph (Proof: executable proof)
- **STORY-claim-handoff-across-frames** — held claim survives the frame boundary; substitution resolves to the same bytes in the new frame (Proof: executable proof)
- **STORY-claim-handoff-durable** — `lifetime: durable` row survives past the dispatch terminal; future dispatches can co-hold it (Proof: executable proof)
- **STORY-cascade-signal-blind** — subscribers fire on every cascade-firing signal type (Proof: executable proof)

### Technical decisions

None. The spec delivers existing intent; no new architectural choices.

### Design changes
- 4 story creates in `design/stories/` (one per STORY above)
- 1 in-place sharpen of `design/concepts/frame.md` (the `serial_queue` bullet)
