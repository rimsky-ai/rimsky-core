# Per-run attribute keying

**Date:** 2026-05-20
**Status:** design (minimalist revision)
**Sketch:** `.ok-planner/sketches/2026-05-20-substitution-fallback-and-invalidate-payload.md` (Feature 1 only; Feature 2 is a separate concern with a different shape — separate spec)

## Context

The 2026-05-15 data-platform-extensions cycle introduced the run-tree on `rimsky_node_runs`, lifting state columns off `rimsky_nodes` (`state`, `last_outcome`, `phase`, parked metadata, fan-out fields, parent_run_id, child_key). Per `concept:node-run`, the run-tree carries "all state-bearing columns." Each invalidate creates a new run; fan-out creates sibling runs per partition; subgraph invocations create children under the calling parent's run.

The attribute table did not move with that lift. `table:rimsky_node_attributes` remains `node_id UUID PRIMARY KEY REFERENCES rimsky_nodes(id)` (`code:foundation/persistence/postgres/migrations/001-baseline.sql#296`). The substitution context built at every dispatch (`code:runtime/runner_dispatch.go::loadSubscribedNodeAttributesByID#618`, `code:runtime/runner_locks.go::loadSubscribedNodeAttributes#379`) reads via `NodeAttributes().Get(ctx, depNode.ID, tx)` — keyed by node, not run.

This is a latent gap with a real failure surface:

- **Concurrent fan-out leaves race.** Multiple leaves of one fanning parent share `node_id`; each leaf's `upsertFinalAttributesTx` writes to the same row. One leaf's writeback overwrites another's.
- **Concurrent subgraph invocations collide.** A subgraph invoked twice from different parent contexts has internal nodes that share `node_id` across both invocations. Both invocations' internal writebacks hit the same row.
- **Cross-frame reads are intrinsically sticky.** Receiver R reads `{{nodes.X.attribute.Y}}` in frame F1 (X just transitioned). R reads it again in frame F2 (a different upstream transitioned, X did not). Both reads hit X's single `node_id` row; R sees whatever X most-recently wrote, regardless of frame. The "this is the value X emitted in this frame" semantic is unrecoverable from the data shape.

The first two are correctness bugs that the platform's structural primitives (fan-out, subgraph) lean on attributes to compose with cleanly. The third is the temporal-coupling problem that has surfaced repeatedly in substitution-grammar discussions — most recently in the declined multi-source substitution sketch (`history/sketches/2026-05-19-multi-source-attribute-substitution.md`).

This spec completes the lift. Attributes get re-keyed per-run, the substitution context at dispatch reads from this-frame's senders only, and the cascade walker gains a parallel edge map for an opt-in "ensure-upstream-exists" flag. None of this introduces new architecture — it makes explicit a separation that was already implicit in the design.

## Substitution semantics under per-run keying

The model is deliberately minimalist:

- A `{{nodes.X.attribute.Y}}` directive at receiver R's dispatch resolves to the value at field path `Y` in X's `rimsky_node_attributes.data` row for the X-run that contributed to R's dispatch in the current frame.
- If no X-run contributed to R's dispatch in this frame (R wasn't gated on X), the directive returns `ErrMissingSource`.
- The fallback operator `{{<directive> | <literal>}}` lets the author supply a literal default for missing reads.

**There is no scope-walk, no caching across frames, no freshness model.** Each dispatch reads from this-frame's runs only.

`rimsky_node_attributes` rows are **not a cache**. They are the persistent record of what each node-run produced. Reads across frames return `ErrMissingSource` even if the upstream has a settled row from an earlier frame — that prior row belongs to a prior run-context, not to this dispatch.

State that needs to be available across frames belongs in one of the other source kinds the substitution grammar already supports:

- **`params.<key>`** — instance-scoped, always available. The right home for stable configuration.
- **Claim payloads** — durable artifacts via `holds:` / `stores:`; read via `{{claim.<alias>.payload.<key>}}`.
- **Threaded inputs** — values explicitly passed via the calling node into a subgraph; the subgraph's entry node absorbs them per `concept:delegation`.

Attributes are strictly reactive: they capture this-frame's outputs and nothing else.

## hard_dep flag — the one new knob

The cascade is the mechanism that gets upstreams into the current frame. By default, an upstream that isn't invalidated by some cascade walk has no run in this frame, and reads of its attributes return `ErrMissingSource`. The author has three options for handling that:

1. **Explicit subscription chain.** Arrange the template so the upstream is invalidated whenever the receiver is. E.g., subscribe both to a common trigger Z.
2. **Accept absent reads and use the fallback operator.** `{{nodes.X.attribute.Y | "default"}}`.
3. **Declare the read as a hard dependency.** `hard_dep: true` on the schema property; the cascade walker proactively invalidates the upstream.

The hard-dep flag:

```yaml
attributes:
  schema:
    properties:
      config_blob:
        type: object
        source: "{{nodes.generate-config.attribute.config_blob}}"
        hard_dep: true
```

Semantic: *"ensure the upstream named by this read is invalidated in this frame so it exists when I dispatch."*

The flag is syntactic sugar — authors can express the same coupling via explicit subscription chains, but `hard_dep: true` is concise at the declaration site and makes the contract visible. The cascade does the work.

### Mechanism

At template registration, the validator computes a hard-dep edge map (`BuildHardDepEdges`) from each node's attribute-schema fields with `hard_dep: true` whose `source:` references `nodes.<X>.attribute.<Y>`. Other source kinds (claim, params, etc.) reject the flag at registration — those kinds are intrinsically per-frame or instance-scoped and the flag is meaningless.

Hard-dep cycles are rejected at template registration with a `ValidationError` naming the cyclic node-types. Soft-dep cycles (no `hard_dep` flag) remain allowed — the cascade has its own gating.

At runtime, when the cascade walker invalidates a receiver R, it consults the hard-dep edge map. For each (R, X) hard-dep edge:

- If X is already in the cascade for this frame (already invalidated, in-flight, or settled), the walker inserts a wait-set row on R pointing at X's run. R waits for X as it would for any other subscribed sender.
- If X is not in the cascade, the walker inline-invalidates X (stale-mark + recursive cascade walk in the same transaction) and inserts the wait-set row.

By the time R becomes dispatch-eligible, every X named in a hard-dep edge has a run in this frame for R.

The flag composes with the fallback operator: `hard_dep: true` ensures the upstream runs, but if it produces null or doesn't write the field, the fallback's literal default still fires.

## Sealed subgraphs — no closure semantics

Subgraph internal nodes can read attributes from:

- **Their siblings** — other internal nodes of the same subgraph invocation, identified by sharing a `parent_run_id` ancestor that names this invocation.
- **The calling node's attributes** — via the delegation contract; the calling node's entry-absorption brings its attributes into scope for the subgraph's entry node, which can forward them via its own writeback to non-entry internals.
- **`params`, claims, trigger messages, child partition_key** — the always-available source kinds.

Subgraph internals **cannot read** from upstream nodes in the calling graph by free reference. The calling graph's namespace is not visible inside the subgraph. Authors who need to pass calling-graph state into a subgraph thread it through the calling node explicitly.

This is a clarification, not a behavior change — under per-run keying with this-frame-only substitution context, cross-scope reads naturally don't resolve (the calling-graph node didn't fire in the subgraph internal's wait-set). The rule is made explicit so authors and reviewers don't expect closure-like behavior.

Rationale: sealed subgraphs are reusable (the dependency contract is explicit at the delegation boundary), debuggable (no implicit cross-scope state), and forward-compatible with future memoization (cache key is the explicit input set).

## Data model — re-key `rimsky_node_attributes`

### New schema

```sql
CREATE TABLE rimsky_node_attributes (
    node_run_id          UUID PRIMARY KEY REFERENCES rimsky_node_runs(id) ON DELETE CASCADE,
    node_id              UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    data                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    value_handle         TEXT,
    value_handle_backend TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX rimsky_node_attributes_node_idx
    ON rimsky_node_attributes (node_id, updated_at DESC);
```

Changes from current:

- `node_run_id` is the new PRIMARY KEY (was `node_id`).
- `node_id` becomes a denormalized FK for forensic / observability queries ("most-recent run for this node, regardless of context"). The `(node_id, updated_at DESC)` index makes that lookup one btree seek. Forensic reads are NOT substitution reads — they exist for control-api endpoints (`/observability/*`), lineage projections, and operator dashboards that need to surface a node's last-known output across runs.
- `run_attempt` column is removed. Pre-per-run, it tracked "which attempt wrote this data"; post-per-run, every row IS a distinct run, so the field is redundant.
- Cascade delete from `rimsky_node_runs` instead of `rimsky_nodes`. Attribute rows live exactly as long as their run rows; reaping a run reaps its attributes for free.

### Pre-v1 migration discipline

Per `.claude/rules/rules.md` "Pre-v1 — break freely": this is a destructive migration. New migration drops the old table and creates the new one. No data preserved (no production data exists). Migration filename follows the numbered-append-only convention; both `code:foundation/persistence/postgres/migrations/` and `code:foundation/persistence/sqlite/migrations/` get the new migration.

### `NodeAttributesRow` and table accessor

`code:foundation/persistence/node_attributes.go::NodeAttributesRow` updates:

```go
type NodeAttributesRow struct {
    NodeRunID shared.UUID
    NodeID    shared.UUID  // denormalized for forensic queries
    Data      map[string]any
    UpdatedAt time.Time
}
```

`NodeAttributeTable` interface gains the per-run key and adds a forensic lookup:

```go
type NodeAttributeTable interface {
    GetByRun(ctx context.Context, runID shared.UUID, tx Tx) (*NodeAttributesRow, error)
    GetLatestByNode(ctx context.Context, nodeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
    Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any, tx Tx) error
    MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any, tx Tx) error
}
```

- The today's keyed-by-node `Get` is removed. Every callsite re-threads to `GetByRun` (substitution path) or `GetLatestByNode` (forensic path).
- `Upsert` takes both `runID` (the PK) and `nodeID` (for the denorm column).
- `MergeDelta` keys by `runID`.

## Wait-set — mark-don't-delete on drain

`rimsky_wait_set` today deletes rows on sender settle. To preserve trigger context for the substitution-context builder, drain marks rather than deletes:

```sql
ALTER TABLE rimsky_wait_set ADD COLUMN drained_at TIMESTAMPTZ;
```

Semantics:

- Cascade walks insert rows with `drained_at IS NULL` (today's behavior).
- When sender S settles, the drain rule sets `drained_at = NOW()` on rows where `sender_run_id = S` (rather than deleting them). Idempotent: rows already drained are not re-touched (the UPDATE includes `AND drained_at IS NULL`).
- Eligibility predicate updates: a stale run is dispatch-eligible iff no `drained_at IS NULL` rows exist for it in the current frame. Today's predicate is "no rows at all"; the new predicate is "no undrained rows."
- Drained rows are visible to the substitution-context builder for trigger-context queries.
- Cleanup happens via `frame_id ON DELETE CASCADE` when the frame is reaped. No separate sweep needed.

The drain primitive `code:foundation/persistence/postgres/wait_set.go::DeleteBySender#53` (and SQLite counterpart at `code:foundation/persistence/sqlite/wait_set.go#51`) renames to `MarkDrainedBySender` and changes from `DELETE` to `UPDATE ... SET drained_at = NOW() WHERE ... AND drained_at IS NULL`.

Note on `WaitSetRow.TopicKind`: the doc-comment at `code:foundation/persistence/wait_set.go#28` lists `"state" | "attribute" | "event"` — this is **correct for the wait-set** specifically. The wait-set's `topic_kind` column has a CHECK constraint admitting only those three kinds (`code:foundation/persistence/postgres/migrations/001-baseline.sql#235` and `code:foundation/persistence/sqlite/migrations/001-baseline.sql#202`). The 2026-05-15 unified-message-layer changes added `"message"` as a fourth topic kind at the **subscription-edge** layer (per `concept:node-subscription`), but message-topic subscriptions don't materialize as wait-set rows. No edit to the wait-set doc-comment is needed; the comment is accurate for the wait-set's role.

## Substitution context builder

At every dispatch, the substitution context's `Deps` map for `nodes.X.attribute.Y` directives is built by:

1. **Query drained wait-set rows for this receiver in this frame, filtered to attribute-topic.** Filter to `receiver_run_id = <this dispatch>, frame_id = <current frame>, drained_at IS NOT NULL, topic_kind = 'attribute'`. Each row's `sender_run_id` identifies a run that contributed to this dispatch via an attribute-topic edge.
2. **For each contributing sender_run_id, check sender state, then fetch the attribute row via `GetByRun`.** The sender's `last_outcome` must be in the settled-success set (`fresh_changed`, `fresh_unchanged`, `passed`, `pure_cascade`). Failed senders (`last_outcome = 'failed'`) do not populate the attribute Deps map; their attributes are treated as absent for the receiver. Map by the sender's node-type for keying. (Parked senders are filtered out by the same `last_outcome` check — parked terminals drain the wait-set (call site at `code:runtime/runner_terminal_park.go#164`; the drain primitive itself is defined at `code:runtime/runner_terminal.go::drainWaitSetOnSettled#533`), but per the park-has-no-outcome convention they leave `last_outcome` empty, so they fail the settled-success-set membership check. `parked` is a value of `phase`, not `last_outcome`; the filter is by outcome only and works uniformly for failed and parked senders.)
3. **Senders not in the drained set are absent.** A `{{nodes.X.attribute.Y}}` directive where X didn't contribute to this frame's wait-set for this receiver resolves to `ErrMissingSource`. The receiver's schema controls whether this is fatal (`required: true`) or silently dropped (default), and the fallback operator (below) provides explicit defaults.

No scope-walk. No cross-frame lookups. The substitution context is exactly "what fired this frame for this receiver."

### Substitution grammar surface

The grammar surface seen by template authors does not change. `{{nodes.X.attribute.Y}}` still references X by node-type. The runtime maps node-type to run_id via the wait-set lookup. Authors never type run IDs.

### Lock-substitution timing

`code:runtime/runner_locks.go::loadSubscribedNodeAttributes#379` runs at the acquisition phase, before the dispatch tx. The wait-set is settled (rows drained) by the time acquisition begins — that's what made the receiver eligible. The lock-substitution path uses the same builder logic. No behavioral split between acquisition-time and dispatch-time substitution context.

## hard-dep cascade extension

At template registration, a parallel edge map is computed alongside `BuildSubscriptionEdges`:

`BuildHardDepEdges(tmpl TemplateSpec) (HardDepEdgeMap, error)` — for each node, enumerate its attribute schema fields with `hard_dep: true` and `source:` referencing `{{nodes.X.attribute.Y}}` directives. Produce a map from receiver node-type to the set of sender node-types that must be invalidated in the same frame as the receiver.

Note the key-direction difference from `SubscriptionEdgeMap`: subscription edges are keyed by **sender** node-type (`map[string][]SubscriptionEdge` — the cascade walker holds a transitioning sender and looks up downstream receivers in O(1)). Hard-dep edges are keyed by **receiver** node-type (`map[string][]string` — the walker holds a freshly-invalidated receiver and looks up its hard-dep upstreams). This is correct given how each is consulted: subscription edges walk downstream from a transitioning sender; hard-dep edges walk upstream from a freshly-invalidated receiver. The direction divergence is intentional, not a pattern slip.

The validator runs cycle detection on the resulting graph and rejects templates whose hard-dep edges form cycles, returning a `ValidationError` naming the cyclic edges. Soft-dep cycles are not in this graph and remain permitted.

At runtime, when the cascade walker invalidates a receiver R (because some upstream A transitioned and R is in A's subscription downstream):

1. R is marked stale and a wait-set row is inserted for (R, A) — existing behavior.
2. **(new)** For each hard-dep edge (R, X) in the registry: check whether X has an in-flight run in this frame via `queue.GetInFlightRunForNode`. If yes, insert a wait-set row on R pointing at X's existing run. If no, inline-invalidate X (stale-mark + recursive cascade walk in the same transaction via a new `stalemarkAndEnqueueInFrame` helper) and insert the wait-set row.

The cascade walker thus consults two edge maps: subscription edges (existing) and hard-dep edges (new). Both feed the wait-set with the same row shape (`topic_kind='attribute'`, `subscription_scope='direct'`). The dispatcher's eligibility predicate is unchanged — gates on "no undrained wait-set rows."

The hard-dep walk lives inside `cascadeSubscribersStaleInTx` (the existing subscriber-walk site in `runtime/runner_terminal.go`), on the FrameIn branch only. FrameNext receivers don't need hard-dep gating because they're not dispatching in the current frame.

## Fallback operator in substitution

The fallback operator lets a `{{...}}` directive supply a literal default for missing reads.

### Grammar

`{{<directive> | <literal>}}` — a directive followed by a literal default.

Operand rules:

- Left operand: any current source-kind directive (`nodes.X.attribute.Y`, `claim.X.payload.Y`, `params.X`, `trigger.message.payload.X`, `nodes.X.event.Y`, `child.partition_key`).
- Right operand: a JSON literal — `null`, `true`, `false`, a number (`42`, `3.14`), or a quoted string (`"text"`).

### Semantics

At substitution:

1. Resolve the left operand.
2. If it resolves to a present value (anything except `ErrMissingSource`), use it.
3. If `ErrMissingSource`, return the literal.

Note that `walkPath` (`code:graph/attribute/substitution.go#558-585`) already treats JSON `null` along the resolution path as missing — so an upstream that wrote `null` to the field path is uniformly absent for the fallback's purposes, alongside upstreams that didn't write the field at all and upstreams that didn't run.

### What this is not

- **Not chains.** `{{X | Y | Z}}` with multiple directives is not admitted. The use case (multi-source aggregation) is explicitly deferred — see Out of scope.
- **Not falsy fallthrough.** Boolean `false`, the number `0`, and the empty string `""` are present values; the fallback fires only on `ErrMissingSource`.
- **Not composite literals.** `{}`, `[]`, and other complex JSON shapes are not admitted as literals; the grammar admits only `null`, booleans, numbers, and quoted strings.

### Implementation site

`code:graph/attribute/substitution.go::resolveDirectiveValue#268` gains parser support for the `|` infix. The directive body parser (currently `directiveBodyRe` in `code:graph/node/template_validator.go#58`) extends to recognize `<directive> | <literal>` shape. Validation rejects malformed forms (chains, missing operands, non-literal right operand).

## Write-site rewiring

All `NodeAttributes()` callsites change from `(nodeID, run_attempt, data)` to `(runID, nodeID, data)`. The dispatching run's ID is available throughout the supervisor as `acq.DispatchID` (which IS the new `rimsky_node_runs.id` for the dispatch).

**Write sites:**

- `code:runtime/runner.go::upsertAttributesPreDispatch#523` — was: read prior, bump `run_attempt`, upsert by nodeID. Now: insert by the dispatching runID. The prior-read transaction block is removed entirely — each run is a fresh row by PK, no read needed.
- `code:runtime/runner_terminal.go::upsertFinalAttributesTx#609` — was: read prior (within-run state), merge with executor writeback, upsert by nodeID. Now: same merge logic; keyed by runID.
- `code:runtime/callback.go#485-515` — `attributesStoreAdapter` methods (`Get` at #485, `Upsert` at #505, `MergeDelta` at #511) that bridge `persistence.NodeAttributeTable` to the narrower `attributes.NodeAttributeTable` consumed by the callback handler. The adapter methods rekey by `runID` (resolved from the cancel_token's parsed dispatch identity); no incremental-call site walking needed — fix the adapter itself.

**Read sites that consumed `run_attempt`:**

- `code:runtime/runner_dispatch.go::buildExecuteRequest#714-724` — today reads the prior attribute row purely to populate `proto:executor.proto::ExecuteRequest.run_attempt` (field 11). With `run_attempt` removed from both the persistence row and the wire (see "Proto wire impact" below), this entire `Persist.Transaction` block is removed.

### Parallel `graph/attribute/callback.go` interface

The HTTP callback endpoint backed by `code:graph/attribute/callback.go` exposes the narrow `attributes.NodeAttributeTable` interface (separate from `persistence.NodeAttributeTable`) used by the callback handler for the incremental writeback endpoint. This interface (and the route shape it serves) updates in lock-step:

- **Wire shape** (today): `POST {callback_url}/v1/attributes/{node_id}`. Pre-v1, the URL path keyed on `node_id` is incompatible with per-run keying. Change to:

  `POST {callback_url}/v1/runs/{run_id}/attributes`

- **`Row` struct** (`code:graph/attribute/callback.go#37`) updates: `NodeID` → `RunID` as the primary identifier; `RunAttempt` field removed; `NodeID` retained as a denormalized field for forensic context.
- **`NodeAttributeTable` interface** (`code:graph/attribute/callback.go#49`) updates: methods take `runID` instead of `nodeID`.
- **`AuthLookup`** (`code:graph/attribute/callback.go#59`) signature changes from `func(token string, nodeID shared.UUID) error` to `func(token string, runID shared.UUID) error`.

The cancel_token itself encodes the dispatch identity (`<supervisorID>:<dispatchID>` per `code:runtime/runner_dispatch.go#713`, parsed inline by `code:runtime/callback.go::attributesAuth#392`). There is no in-memory registry mapping tokens to nodes today; the parsed token directly identifies the run.

### Proto wire impact

`run_attempt` is removed from two proto surfaces, pre-v1 break:

- `proto:executor.proto::ExecuteRequest.run_attempt` (field 11) — deleted. Executors that depend on this for idempotency/visibility migrate to the already-present `proto:executor.proto::ExecuteRequest.dispatch_id` (field 12), which is documented as "The supervisor-side rimsky_node_runs.id for this dispatch."
- `proto:events.proto::AttributesSubstitutedPayload.run_attempt` (field 3) — deleted. Consumers reading `run_attempt` for retry-counting migrate to the `consecutive_retries_no_progress` column on `rimsky_node_runs`.

Proto field-number reuse rules: per the codebase's existing convention (e.g. `code:protocols/proto/v1/executor.proto#68-69`, which carries both `reserved 10;` and `reserved "resumed";` for the retired `resumed` field), the deleted fields gain both numeric and named reservations. The proto files gain `reserved 11; reserved "run_attempt";` on `ExecuteRequest` and `reserved 3; reserved "run_attempt";` on `AttributesSubstitutedPayload`.

### Eligibility-predicate SQL sites

The wait-set eligibility predicate ("no undrained rows") replaces today's "no rows at all" check:

- `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch#222` — Postgres: `NOT EXISTS` subquery gains `AND w.drained_at IS NULL`.
- `code:foundation/persistence/postgres/nodes.go::ListPureCascadeReady#249` — same predicate update.
- `code:foundation/persistence/sqlite/nodes.go::ListReadyForDispatch#185` and its `ListPureCascadeReady` sibling — SQLite counterparts.

## Subgraph carry-rule

The subgraph carry-rule is the mechanism that makes a subgraph's exit writeback observable through the calling node's attribute row. Per `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphExit#606` and its blessed-invariant docstring at #605 (*"exit-node-writeback flows to parent run writeback"*), when a subgraph's exit node completes successfully, its writeback bytes become the calling node's attribute row — making the subgraph's output available to downstream consumers subscribed to the calling node.

**Today the carry-rule is incomplete.** `CarryExitWriteback` at `code:runtime/subgraph_dispatch.go#170-239` validates the writeback bytes (decodable JSON object) and emits an audit log, but it does NOT persist them. `applyTerminalCompleteSubgraphExit` invokes `CarryExitWriteback` then emits a `subgraph.exit_carry` audit event but also does not persist the writeback to the parent's attribute row. The blessed-invariant is aspirational, not implemented — exit writebacks are dropped on the floor after validation.

This spec completes the carry-rule. Under per-run attribute keying, downstream consumers reading `{{nodes.SC.attribute.foo}}` (where SC is the calling node) need SC's attribute row to actually contain the subgraph's output for the read to resolve. Without the carry, those reads return `ErrMissingSource` and subgraphs become leaky abstractions — the cascade can still fire subscribers via SC's terminal transition, but they have nothing to substitute from.

### Fix

In `applyTerminalCompleteSubgraphExit`, after `CarryExitWriteback` validates, the caller resolves the parent run + node and persists the exit's writeback to the parent's attribute row in the same transaction:

```go
return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
    if err := CarryExitWriteback(ctx, ..., tx, acq.DispatchID, wb); err != nil {
        return err
    }
    exit, err := args.Persist.RunTree().GetByID(ctx, tx, acq.DispatchID)
    if err != nil || exit == nil || exit.ParentRunID == nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: load exit: %w", err)
    }
    parent, err := args.Persist.RunTree().GetByID(ctx, tx, *exit.ParentRunID)
    if err != nil || parent == nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: load parent: %w", err)
    }
    if err := args.Persist.NodeAttributes().Upsert(
        ctx, parent.RunID, parent.NodeID, merged, tx,
    ); err != nil {
        return fmt.Errorf("applyTerminalCompleteSubgraphExit: upsert parent: %w", err)
    }
    // Existing subgraph.exit_carry audit event continues here.
})
```

The exit's own attribute row stays empty — the exit is internal to the subgraph and not externally addressable. The parent's row carries the exit's writeback, observable through the calling node's name. The fix is ~10 lines and turns the blessed-invariant from aspirational to enforced.

`CarryExitWriteback` itself remains a validate-and-log helper; no signature or behavioral change inside that function. The Upsert lives in the caller because the caller already has access to the `NodeAttributeTable` via `args.Persist` (the propagation args passed into `CarryExitWriteback` don't carry it).

### Why this lands in this spec

The carry-rule's incompleteness is pre-existing, but under per-run + this-frame-only substitution, the failure mode becomes sharper. Previously, SC's attribute row might have lingering values from an earlier run that consumers accidentally saw; under per-run keying with this-frame reads, an unimplemented carry means consumers see nothing from a subgraph. The fix is on the critical path for subgraph composition under the new model and is small enough to ride along with the write-site rewiring.

Scenario test in `## Testing strategy` extends to assert that under concurrent subgraph invocations, each invocation's parent (the calling node's run) sees its own subgraph's exit writeback, isolated by per-run keying.

## Fan-out terminal coordination

The current race condition (multiple leaves of one fanning parent writing to the same `node_id`-keyed attribute row) disappears for free under per-run keying. Each leaf has its own `node_run_id`; each leaf's `upsertFinalAttributesTx` writes to its own row.

Per-leaf attribute reads from outside the fan-out are out of scope (see Out of scope); aggregating consumers route fan-out outputs through claim payloads or aggregator nodes as today.

## Other source kinds — unchanged

The per-run keying and the new substitution-context model apply only to `nodes.<X>.attribute.<Y>` reads. Other source kinds retain their existing behavior:

- **`params.<key>`** — instance-scoped, populated at instance creation, immutable across the instance's lifetime. The canonical home for stable configuration under the minimalist model.
- **`claim.<alias>.{address|scope|payload}`** — reads from the live `rimsky_claim_handles` row bound to the dispatch context at acquisition. Hard-dep by construction: the `holds:`/`stores:` directive enforces that the claim handle is in `state='active'` before the receiver dispatches. The `hard_dep:` flag is not admitted on claim reads (the validator rejects it) because the holding-subgraph machinery already enforces the requirement.
- **`trigger.message.payload.<...>`** — bound to the frame's trigger message, per-frame by construction.
- **`nodes.<X>.event.<name>.<...>`** — append-only event stream semantics; "most recent emission from any X-run" is the right shape because events are historical, not state-bearing. The minimalist substitution model does not extend to events; this spec keeps current behavior.
- **`child.partition_key`** — per-leaf-dispatch, intrinsically per-run.

## Design changes

This spec mutates the design docs under `.ok-planner/design/`. The following entries are applied by execute-plan as part of carrying out the implementation plan.

- Concept: mutate `.ok-planner/design/concepts/attribute.md`.
  - Replace the closed-grammar invariant to admit the fallback operator (`<directive> | <literal>`) as a permitted shape on top of the existing source-kind enumeration. The closed-source-kind list is unchanged.
  - Append to the Invariants list: *"Attribute storage is per-run, keyed by `node_run_id` (foreign key to `rimsky_node_runs` with `ON DELETE CASCADE`). A denormalized `node_id` column supports forensic / observability lookups via `GetLatestByNode`; the dispatch-time substitution path uses `GetByRun` against wait-set sender_run_ids that contributed to this dispatch in this frame."*
  - Append to the Invariants list: *"Per-field `source:` admits an opt-in `hard_dep: true` flag on `nodes.<X>.attribute.<Y>` reads. When set, the cascade walker proactively invalidates the upstream so its value is available in the current frame. Hard-dep cycles are rejected at template registration via `BuildHardDepEdges`."*
  - Append to the Invariants list: *"Substitution reads are scoped to the current frame. A `{{nodes.X.attribute.Y}}` directive resolves to the X-run that contributed to this dispatch via the frame's wait-set; reads of X-runs from earlier frames return `ErrMissingSource`. `rimsky_node_attributes` rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs."*
  - Append a Boundaries paragraph after the existing per-field-arity clarification: *"Subgraphs are sealed: internal nodes can read from siblings of the same invocation, the calling node's attributes, and the always-available source kinds (`params`, claims, trigger messages, `child.partition_key`) — but not from upstream nodes in the calling graph by free reference. The calling graph's namespace is not visible inside the subgraph. Authors thread calling-graph state through the calling node explicitly."*
  - Insert a new `## Non-goals` section between `## Boundaries` and `## Invariants`. The section captures load-bearing design decisions about what the attribute concept deliberately does NOT support — durable non-goals carried forward from the brainstorm that produced this spec. Section content (exact text):

    ```
    ## Non-goals

    Patterns considered carefully during platform design and **decided against**. These are positions, not deferrals — future agents reaching for these patterns should argue against this section's rationale rather than treating them as open backlog.

    - **Cross-frame attribute caching.** A `{{nodes.X.attribute.Y}}` read at receiver R's dispatch resolves only against the X-run that contributed to R's dispatch via this frame's wait-set. Reads of X-runs from earlier frames return `ErrMissingSource`. `rimsky_node_attributes` rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs.
    - **Function-form substitution grammar.** No `{{coalesce(X, Y)}}`, `{{newest(X, Y)}}`, `{{merge(X, Y)}}`, or other in-grammar functions. The grammar stays a closed enumeration of source-kind directives plus an optional literal fallback. Aggregation and transformation logic lives in receiver executors, not in the substitution layer.
    - **Multi-directive fallback chains.** The fallback operator `{{<directive> | <literal>}}` admits exactly one directive on the left and exactly one JSON literal (`null`, boolean, number, or quoted string) on the right. Multi-directive chains (`{{X | Y | Z}}`) and composite literals (`{}`, `[]`) are not admitted.
    - **Closure semantics for subgraphs.** Subgraph internal nodes cannot read attributes from upstream nodes in the calling graph by free reference (see Boundaries above). Calling-graph state threads through the calling node explicitly.
    - **`force_fresh: true` (always-re-execute), `pull_only: true` (suppress auto-subscribe), `trigger_if_missing: true` (lazy upstream initialization).** None of these flags exist. The configuration surface is exactly `hard_dep: true` on attribute schema properties whose source is `{{nodes.<X>.attribute.<Y>}}`.

    See `.ok-planner/history/specs/2026-05-20-attribute-pull-resolution-design.md` for the brainstorm rationale per item.
    ```

  - Append a Notes entry dated 2026-05-20.

- Concept: mutate `.ok-planner/design/concepts/node-run.md`.
  - Update "all state-bearing columns" framing to explicitly include `rimsky_node_attributes` via the per-run FK.
  - Append a Notes entry dated 2026-05-20.

- Concept: mutate `.ok-planner/design/concepts/wait-set.md`.
  - Update Invariants: drain marks rather than deletes (`drained_at` column); eligibility predicate is "no undrained rows for this receiver in this frame." Cleanup via frame cascade-delete.
  - Update the PK enumeration to the actual schema shape (`receiver_run_id`/`sender_run_id`, per-run identity since 2026-05-15) — the current concept doc's PK text is stale.
  - Append to Boundaries: *"Drained rows are queryable by the substitution-context builder to enumerate sender_run_ids that contributed to this receiver's dispatch in this frame (filtered to topic_kind='attribute' and settled-success outcomes). The wait-set is the durable record of trigger context per dispatch."*
  - Append a Notes entry dated 2026-05-20.

- Concept: mutate `.ok-planner/design/concepts/cascade.md`.
  - Update to reflect that the cascade walker consults two edge maps: the subscription edge map (existing) and the hard-dep edge map (new). Both feed the wait-set with the same row shape; the walker invalidates upstream proactively for hard-dep edges when the upstream has no current-frame run.
  - Append a Notes entry dated 2026-05-20.

- Concept: mutate `.ok-planner/design/concepts/node-subscription.md`.
  - Append a brief Notes entry dated 2026-05-20 noting that the auto-subscribe rule remains the default under the per-run keying / minimalist substitution model. No behavior change.

No new tension entries. This spec completes a latent design intent (per-run state-bearing columns including attributes) that was implicit in `concept:node-run` since 2026-05-15.

## Testing strategy

- **Conformance suite.** The existing `code:foundation/persistence/conformance/node_attributes_merge_delta.go` is updated to use the per-run keying, and a new file (e.g., `node_attributes_per_run.go`) is added under the same directory to cover per-run-specific behaviors: insert by run, get by run, get latest by node, cascade-delete with run row, denormalized node_id correctness, blob-spill preservation across the rekeying.
- **Wait-set drain tests** verify the `drained_at` mark-don't-delete semantic, the updated eligibility predicate, and the queryability of drained rows post-dispatch.
- **Substitution-context tests** at `code:graph/attribute/substitution_test.go` cover:
  - This-frame resolution: contributing senders resolve, non-contributing senders return missing.
  - Cross-frame reads return `ErrMissingSource` (no scope-walk).
  - Failed/parked senders excluded from Deps even when they drained the wait-set.
  - Fallback operator: literal returns when directive is missing; left operand wins when present.
  - hard_dep cycle detection at template registration.
- **End-to-end scenario tests** under `test/scenarios/`:
  - Concurrent fan-out leaves writing distinct attribute rows without collision.
  - Concurrent subgraph invocations with isolated internal-node attribute rows.
  - Z-pattern (producer reads consumer's failure attribute via fallback on first dispatch; gets the value on subsequent dispatches once verify has fired in this frame).
  - hard_dep cascade: receiver declares `hard_dep: true` on an upstream that wasn't otherwise invalidated; cascade walker proactively pulls the upstream into the frame; receiver dispatches with both fresh.

## Non-goals

Features considered carefully during the brainstorm that produced this spec and **decided against**. These are not deferrals — they are positions. Future agents reaching for these patterns should treat the rationale as load-bearing and argue against the concept-doc Boundaries (which carry these forward durably; see `## Design changes`), not just propose the feature as a new idea.

- **Scope-walk-cache (cross-frame attribute reads).** Reading a prior frame's settled X-run as a cached value when X didn't fire this frame. Decided against: the minimalist this-frame-only model is the position; `rimsky_node_attributes` rows are the persistent record of what each node-run produced, not a cache. Cross-frame state belongs in `params`, claim payloads, or threaded subgraph inputs. Adding cross-frame caching would re-introduce the temporal-coupling problem the per-run keying explicitly resolves.
- **Function-form grammar in substitution.** `{{coalesce(X, Y, Z)}}`, `{{newest(X, Y)}}`, `{{merge(X, Y)}}`, conditional / arithmetic / string functions. Decided against: the grammar stays a closed enumeration of source-kind directives plus an optional literal fallback. Functions would import a small DSL into the substitution surface; consumers needing aggregation or transformation use executor logic in the receiver (which has full programming-language access) rather than template-level functions.
- **Multi-directive fallback chains.** `{{X | Y | Z}}` with multiple directive operands. Decided against: re-imports the temporal-aggregation question the per-run keying explicitly closes. The fallback operator's right operand is always a literal, never another directive.
- **Composite literals in the fallback operator.** `{{X | {}}}`, `{{X | [1, 2]}}`. Decided against: the YAML/JSON literal-embedding inside `{{...}}` is fiddly, and no consumer pattern needs it. The fallback admits only `null`, booleans, numbers, and quoted strings.
- **`force_fresh: true` (always-re-execute variant).** A flag that would invalidate the upstream on every dispatch of the receiver, regardless of whether the upstream already has a current-frame run. Decided against: `hard_dep: true` covers the documented use cases; "ignore cache and re-fetch" has no consumer pattern; the cascade is the right mechanism for "this should re-run."
- **`pull_only: true` (suppress auto-subscribe).** A per-read flag that would let the receiver read X's attribute without auto-subscribing to X. Decided against: reading X without subscribing to X is an orphan-read footgun; if you depend on X's value you want to fire when X changes.
- **`trigger_if_missing: true` (lazy upstream initialization).** A per-read flag that would invalidate the upstream when no run exists anywhere. Decided against: the Z-pattern's first-dispatch case is handled cleanly by the fallback operator; implicit upstream execution from a read site is a footgun (Z-pattern collision, cycle risk, surprise re-execution).
- **Closure semantics for subgraphs (parent-graph reads from subgraph internals).** Subgraph internal nodes reading attributes from upstream nodes in the calling graph by free reference. Decided against: subgraphs are sealed. The calling graph's namespace is not visible inside the subgraph; authors thread state through the calling node explicitly. Sealed subgraphs are reusable (explicit dependency contract at the delegation boundary), debuggable (no cross-scope implicit state), and forward-compatible with future memoization.

## Out of scope (separate concerns)

Real future work that doesn't belong in this spec. Each has its own design surface and will get its own spec / sketch when relevant.

- **Feature 2 of the sketch (invalidate-with-payload).** Operator-injectable context delivered with an invalidate event. Different shape from this spec's pull-resolution concern; the brainstorm explored alternative approaches (publisher messages, admin endpoints) and the design hasn't converged. Separate spec.
- **Per-leaf fan-out addressing in attribute substitution.** Reading a specific child_key's attribute from outside the fan-out. Consumers route fan-out outputs through claim payloads or aggregator nodes today; if a grammar for per-leaf addressing becomes warranted, that's a fan-out-grammar spec.
- **Memoization of subgraph / recursive invocations.** Reusing a prior invocation's results when inputs are identical. Future feature; cleanly composes with sealed-subgraph semantics (cache key is the explicit input set).
- **Event scope-walk.** Applying any scope-walk semantics to `{{nodes.X.event.<name>}}` reads. Events have an append-only stream model that this spec's per-run keying doesn't affect; any future change to event resolution semantics is its own spec.

## Open questions for the spec phase

None. The minimalist scoping resolved the architectural questions surfaced during brainstorm.
