# Subscription-cascade resolution + quality-of-life cycle

**Date:** 2026-05-14
**Status:** spec / approved
**Supersedes:** `.ok-planner/sketches/2026-05-13-quality-of-life-cycle.md`
**Scope class:** foundation change + two bundled improvements

## What this is

A bundled cycle of three pieces. Two were planned in the original sketch (`parked_reason` typed enum, atomic-staging pattern doc + reference producer). The third — the subscription-cascade model resolution — emerged from brainstorm when the bundled `barrier` executor's design exposed a deeper conceptual debt: rimsky's reactive substrate had outgrown its DAG-flavored vocabulary, and the bundled-`barrier` executor was a workaround for a foundation-level gap. Resolving the model dissolves the barrier problem and a family of adjacent overloads.

The pieces:

1. **Subscription-cascade model resolution.** Retire `dependencies:` as a node-template construct. Retire send-side `invalidate.targets` clauses across the lifecycle-handler family and `error_types:` policy. Introduce `subscribes:` (impactee-side reactive declarations) with auto-subscribe from substitution refs in attribute schemas. Introduce `rimsky_wait_set` as a per-frame ledger that drives a new eligibility predicate. Retire `concept:on-event-handler`; mutate `concept:cascade`, `concept:invalidate`, `concept:node`, `concept:lifecycle-handler`, `concept:error-policy`, `concept:named-event`. Add new concepts `concept:subscription` and `concept:wait-set`.

2. **`parked_reason` typed.** Promote `Park.reason` from free-form `string` to typed `ParkReason` enum on the executor protocol; add a `reason_note` companion string for human annotation. Add `parked_reason_note` column. Add `rimsky-cli parked list` subcommand. Add dashboard treatment for distinct reasons. Add `report_park` MCP tool on `executors/claude-agent`. Migrate `executors/stub` to the enum.

3. **Atomic-staging pattern.** Pattern doc at `docs/agents/examples/atomic-staging.md` plus a reference filesystem producer at `examples/atomic-staging-fs-producer/` demonstrating stage-then-swap-on-Commit semantics. Example-only; not bundled.

Piece 1 is foundational and runs first. Pieces 2 and 3 are independent and run alongside it. Piece 2's subscription composition (`on: state, when: parked, reason: <kind>`) is one composition point with Piece 1; Piece 3's worked-example template is the other.

---

## Piece 1: Subscription-cascade model resolution

### Why this resolution

Today's `dependencies: [foo]` block bundles three independent capabilities into one declaration: (a) read access to `foo`'s attributes via substitution, (b) cascade subscription — when `foo` terminals, I go stale, (c) eligibility gate — don't dispatch me until `foo` is fresh. The three are conceptually distinct and the system already has separate non-`dependencies:` paths for each: substitution of `{{nodes.X.event.Y}}` reads without declaring a dep; named-event substitution makes data available across the graph without extending cascade; the eligibility check is an opaque strict-AND over the dependency list.

Meanwhile, the rimsky cascade is bidirectional in practice — nodes already send stale messages upstream via `invalidate.targets`, creating loops. The DAG mental model was always wrong for what the engine actually does. Rimsky is a reactive node graph with bidirectional message flow, a frame-scoped scheduler, and a ledger. "Dependency" is a particular compound bundle over more primitive operations.

This piece resolves that compound by decomposing it into named primitives: substitution refs for read access, subscriptions for reactive coupling, and a wait-set ledger for eligibility. Send-side `invalidate.targets` retire — they're equivalent to impactee-side subscriptions, which give locality of declaration to the impactee and make each node's reactive surface readable from its own template entry.

### Subscription syntax + topic taxonomy

A node declares `subscribes:` as a list of subscription entries. Each entry has a required `node:` (or `instance: true` for cross-cutting) and a required `on:` topic kind, plus optional filters and a `frame:` modifier.

Topic kinds:

| `on:` | Required filters | Optional filters | Fires when |
|---|---|---|---|
| `state` | — | `when: <node-state>`, `outcome: <last-outcome>`, `error_class: <class>`, `reason: <ParkReason>` | Subscribed node enters the filtered state. Filters compose conjunctively. Empty `when:` means any state transition. |
| `attribute` | — | `name: <attribute-key>` | Subscribed node terminals with a changed attribute. With `name:`, fires only when that key changed; without, fires on any attribute change. |
| `event` | `name: <event-name>` | — | Subscribed node emits the named event. The name must appear in the upstream executor's `Capabilities.declared_events` (validated at registration). |

Cross-cutting (`instance: true`) subscriptions fire on the topic match across every node in the instance. Useful for "clean up on any failure of class X" patterns previously expressed as `error_types: invalidate.targets` blocks spread across many node types.

Each subscription entry accepts an optional `frame: in | next` modifier. Default = `in` for per-node `state`/`attribute`/`event` topics (in-cascade by default); default = `next` for cross-cutting (`instance: true`) topics so cross-cutting reactions queue for the next frame and don't surprise the current frame's resolution.

Example:

```yaml
nodes:
  - type: finalize
    executor: http-node
    subscribes:
      - { node: spine_z, on: state, when: fresh, outcome: fresh_changed }
      - { node: optional_check_1, on: state, when: fresh }
      - { node: optional_check_2, on: state, when: fresh }
      - { node: intake, on: event, name: applicable_subgraphs_decided }
      - { node: foo, on: attribute, name: my_attr }
      - { instance: true, on: state, when: failed, error_class: rate_limited, frame: next }
    attributes:
      foo: { source: "{{nodes.intake.event.applicable_subgraphs_decided.foo}}" }
      bar: { source: "{{nodes.spine_z.attribute.bar}}" }
```

The `dependencies:` block does not exist on the new template shape.

### Substitution grammar rename

Today's substitution grammar names upstream-node access in two inconsistent ways: attribute substitution uses `{{deps.<node>.<field>}}`; event substitution uses `{{nodes.<emitter>.event.<event_name>.<path>}}`. The first form predates the second and was not updated when named-event substitution landed.

This cycle unifies the grammar under `{{nodes.<node>.<topic_kind>.<key>...}}`:

- `{{deps.<node>.<field>}}` → `{{nodes.<node>.attribute.<field>}}` (rename).
- `{{nodes.<node>.event.<event_name>.<path>}}` (unchanged).
- `{{claim.<alias>.<field>}}` and `{{params.<field>}}` are unchanged (claim and params are not graph nodes).

The substitution engine accepts only the new form post-cycle. Pre-v1: no compat alias. Template-validator rejects the old `deps.X.Y` form with an error that cites the new shape.

### Substitution refs auto-subscribe

Every substitution directive in a node's `attributes` schema that references another node implicitly adds a subscription:

- `{{nodes.X.attribute.Y}}` → `{node: X, on: attribute, name: Y}`.
- `{{nodes.X.event.Z.<path>}}` → `{node: X, on: event, name: Z}`.
- `{{claim.X.Y}}` and `{{params.X}}` add no subscription (not graph nodes).

The auto-subscribed set is unioned with the explicit `subscribes:` block. Explicit declarations exist for couplings that don't read upstream data — e.g. "fire me when X reaches `parked` even though I don't read any of X's attributes."

This rule eliminates the asymmetry where attribute substitution required an explicit `dependencies:` declaration while event substitution did not — both now auto-subscribe.

### Wait-set table + cascade walk semantics

A new persisted ledger drives eligibility:

```sql
CREATE TABLE rimsky_wait_set (
  frame_id           UUID NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
  receiver_node_id   UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
  sender_node_id     UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
  topic_kind         TEXT NOT NULL,    -- 'state' | 'attribute' | 'event'
  subscription_scope TEXT NOT NULL,    -- 'direct' | 'instance' — distinguishes per-node from cross-cutting
  topic_filter       JSONB,             -- nullable; the subscription's declared filter pattern (carried for observability, not re-evaluated at drain time)
  inserted_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope)
);
CREATE INDEX rimsky_wait_set_receiver ON rimsky_wait_set(frame_id, receiver_node_id);
CREATE INDEX rimsky_wait_set_sender   ON rimsky_wait_set(frame_id, sender_node_id);
```

Subscription edges are precomputed at template registration. The template validator walks each node's `subscribes:` block plus the parsed substitution refs in `attributes` source strings, then builds an inverse-edge map keyed by sender node-type → list of `(receiver node-type, topic_kind, topic_filter)`. The map is cached on the template row at registration time. Cross-cutting (`instance: true`) subscriptions live in a separate small per-template map keyed by topic.

Cascade walk discipline (within the cascade engine's transaction):

- **On a sender's invalidation** (state transition from a settled value — `fresh` / `failed` / `parked` — into `stale` or `running`), the engine reads the template's inverse-edge map for the sender's node-type, finds every subscription edge that could end up matching at the sender's eventual settled state (state subscriptions regardless of `when:` filter; attribute subscriptions; named-event subscriptions), marks each receiver `stale` (idempotent within the frame), and inserts a row into `rimsky_wait_set` carrying `(frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter)`. `subscription_scope = 'direct'` for per-node subscriptions; `'instance'` for cross-cutting subscriptions sourced from the per-template cross-cutting map. This is the "pessimistic" invalidation: any subscriber that might fire on the sender's cycle is invalidated up front and gated.
- **On a sender's named-event emission mid-cycle**, the engine evaluates `on: event` subscription filters; matching subscribers get an additional stale-mark + wait-set row.
- **On a sender's resolution to any settled state** (`fresh`, `failed`, `parked`), the engine bulk-deletes wait-set rows where `frame_id = F AND sender_node_id = S`. This single drain rule covers every topic kind — `when: fresh`, `when: failed`, `when: parked`, attribute, event — because every subscription resolves on the sender reaching a settled state. The drain is unconditional once the sender settles; receivers whose filter did not actually match at the settled state still re-dispatch (idempotent re-fire; produces equivalent output).
- **Multiple invalidators** are handled trivially — each sender contributes its own row; the receiver waits until all rows for `(frame, receiver)` are gone.

### Eligibility predicate

`SweepReady` (in the scheduler-adjacent code that finds dispatch-eligible nodes) replaces today's "all dependencies fresh" query with:

```sql
SELECT id FROM rimsky_nodes
 WHERE state = 'stale'
   AND id NOT IN (
     SELECT receiver_node_id FROM rimsky_wait_set
      WHERE frame_id = $current_frame
   );
```

A stale node is eligible iff its wait-set is empty for the current frame. A node with no upstream-graph invalidators this frame (e.g. operator-API direct invalidate, scheduled-node cron tick, freshly-created node with no in-graph triggers) has an empty wait-set and is immediately eligible — matching today's semantics for those cases.

### Frame interaction

Frames close as today: when no nodes are `stale` or `running` for the instance. Wait-set rows for the closed frame are deleted via `ON DELETE CASCADE` from `rimsky_frames`. Stale wait-set rows from prior frames cannot affect new frames.

The `frame: in | next` modifier on a subscription controls whether the cascade walk's stale-mark + wait-set insert lands in the current frame or queues for the next. `frame: next` deferrals follow the same mechanism as today's per-emit `frame:` discipline on `invalidate.targets`.

### System emitters

External signal sources mark nodes stale without inserting wait-set rows (no graph-upstream waiter exists):

- Operator-API `POST .../invalidate` and `force-fire`.
- Scheduled-node cron tick (advances `NextFireAt` and stale-marks the scheduled node).
- `SweepParkedNodes` time-wake (transitions `parked → stale` at `resume_at`).
- `SweepStaleHeartbeats` and watchdog `park_timeout` (terminal transitions).

These match today's "out-of-graph emitter" semantics. The wait-set is graph-internal.

### Cross-cutting topics

A subscription with `instance: true` and a topic kind fires when any node in the instance matches the topic filter. Example use cases:

- "Run cleanup on any failure of class X" — `{instance: true, on: state, when: failed, error_class: rate_limited, frame: next}`.
- "Alert on any human-attention park" — `{instance: true, on: state, when: parked, reason: awaiting_human, frame: next}`.

At each sender transition the engine evaluates the per-template cross-cutting subscription map (a smaller table than the per-sender inverse-edge map); matching receivers get a wait-set row with `sender_node_id = sender` and `subscription_scope = 'instance'`. Per-node subscriptions write rows with `subscription_scope = 'direct'`. A receiver that has both a direct subscription and a cross-cutting subscription matching the same sender on the same `topic_kind` gets two distinct rows (the PK includes `subscription_scope`); both must drain for eligibility.

**`frame: next` wait-set placement.** For both per-node and cross-cutting `frame: next` subscriptions, the cascade walk does not write a wait-set row into the current frame. Instead it queues the receiver's stale-mark + wait-set insert for the next frame, applied at next-frame-creation time (the scheduler's frame-opening step processes the queue). The wait-set row therefore lives in the next frame's `frame_id` and participates in the eligibility predicate only after the current frame closes. The mechanism follows the existing per-emit `frame: next` invalidate discipline (operator-API, error-types policy, lifecycle-handler today): the deferred-invalidate queue is reused as the carrier rather than introducing a new persistence shape. The queue is consumed at next-frame-creation and cleared once the next frame opens; entries that haven't been consumed (e.g. instance never opens another frame) remain until the instance's terminated lifecycle event drops them with the instance.

> **Erratum (2026-05-14, post-implementation).** The shipped implementation in `runtime/runner_terminal.go::cascadeSubscribersStaleInTx` (the `FrameNext` branch) does NOT insert any wait-set row for `frame: next` subscriptions — not in the current frame and not in the next frame. The cascade walk instead opens a new frame for the receiver via `frame.EnqueueOrCoalesce` (in-tx, no separate deferred-payload queue); the receiver becomes a frame source for the new frame and is stamped by `MarkSourceNodeStale` at frame-open. Parked receivers are woken in-tx via `wakeParkedReceiverInTx` so the next-frame open can pick them up as a source.
>
> **Rationale.** A wait-set row keyed on `(next_frame_id, receiver, current_sender)` would never drain, because `drainWaitSetOnSettled` fires per-(frame_id, sender) at the sender's terminal — and by the time the next frame opens, the current sender has already settled into the prior frame. The drain trigger has come and gone. Any wait-set row referencing the prior-frame sender would persist indefinitely (until frame-close cascade-delete on the next frame's close), gating the receiver forever. Deeper gating in the next frame is correctly handled by the new frame's own cascade walks: when nodes invalidate within the new frame, their senders emit `state` transitions and the wait-set machinery fires for those in-frame senders.
>
> **Soundness.** Under `frame_resolution_mode: serial_queue` (the default and currently the only supported mode) this matches the intended semantics: a `frame: next` edge fires the receiver in the next frame's source-set without a per-edge gate, leaving in-frame gating to subscriptions that the receiver itself declares with `frame: in`. If a future frame-resolution mode (e.g. interleaved-frames) requires honoring per-edge gates across frame boundaries, the deferred-invalidate-queue carrier described above is the path forward; for `serial_queue` it is unnecessary.
>
> **Where it lives.** `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` — see the in-code comment on the `case node.FrameNext` branch.

### Migration of existing send-side declarations

Every send-side `invalidate.targets` clause retires. Each retires by being rewritten as a receiver-side subscription:

| Today's declaration | Receiver-side replacement |
|---|---|
| `dependencies: [foo]` | Implicit from substitution refs; or explicit `subscribes: [{node: foo, on: state, when: fresh}]` for non-reading coupling |
| `on_executor_complete: invalidate: { targets: [B] }` on A | `subscribes: [{node: A, on: state, when: fresh, outcome: fresh_changed}]` on B |
| `on_executor_errored: invalidate: { targets: [B] }` on A | `subscribes: [{node: A, on: state, when: failed}]` on B (optionally with `error_class:` filter) |
| `on_acquire_unavailable: invalidate: { targets: [B] }` on A | `subscribes: [{node: A, on: state, when: failed, error_class: acquire_unavailable}]` on B |
| `on_event: { "X": invalidate: { targets: [B] } }` on A | `subscribes: [{node: A, on: event, name: X}]` on B |
| `error_types: { class: { policy: [{action: invalidate, targets: [B]}] } }` on A | `subscribes: [{node: A, on: state, when: failed, error_class: <class>}]` on B; or cross-cutting if it should fire for any node's error of that class |

What stays untouched on the lifecycle-handler family:

- `resolve: pass | retry | error` — pure state-transition verdict.
- `error_class: <class>` annotation on the `resolve: error` path.
- The retry-loop cap in `error_types`.
- `action: retry | give_up | pass` in `error_types` policy.

What is silently dropped (not load-bearing in any scenario today): the `on_event: { <name>: { resolve: ... } }` emitter-side resolve verdict. `concept:on-event-handler` shared the `resolve` vocabulary with the three lifecycle handlers, letting the emitter steer its own dispatch outcome based on its own emitted events. No live template, scenario, or bundled executor exercises this capability; the retirement is acknowledged here rather than carried forward without a use case. If a future need surfaces, the receiver-side subscription model can reintroduce it as a self-subscription with a `resolve` verdict, but that's not part of this cycle.

Self-invalidate retires. `invalidate: targets: [self]` had two uses today: the reflexive wake from `on_event` and self-invalidate from `on_executor_complete` for "always re-fire next frame" patterns. Both dissolve under subscription-only; the second becomes a scheduled-node node or an operator-API call if needed.

### Template-validator changes

`graph/node/template_validator.go` gains:

- **Rejection** of `dependencies:`, of `invalidate.targets:` on lifecycle handlers, and of `action: invalidate` in `error_types` policy actions. Error messages cite the new shape.
- **Validation** of `subscribes:` entries: subscriber syntax shape; each topic-kind's required and optional filters; `node:` references resolving to declared template nodes; `instance: true` requires a topic kind that supports cross-cutting; per-topic-kind cross-checks against upstream's declared output topology (attribute names declared in upstream's `attributes` schema; event names declared in upstream's `Capabilities.declared_events`; node states valid against the enum).
- **Substitution-ref parsing** in attribute `source:` strings to compute the implicit subscription set; union with the explicit `subscribes:` set.
- **Inverse-edge-map construction** keyed by sender node-type → list of (receiver node-type, topic_kind, topic_filter). Cached on the template row.

Cross-checks against upstream's executor `Capabilities.declared_events` follow the same silent-skip semantics as today's `on_event` validation: when the upstream executor is not reachable via the observability handshake at registration, the check is skipped silently (`validateOnEvent`'s pattern).

### Operator surface

- `GET /admin/diagnostics/wait-sets?frame=<frame_id>&node=<node_id>` — returns the wait-set for a given frame (and optionally narrowed to one receiver). Used for debugging stuck frames. Body: list of `(receiver_node_id, sender_node_id, topic_kind, topic_filter)` rows.

### Bundled-template / scenario-test migration

**Behavioral shift to acknowledge during migration:** receivers that previously gated on `dependencies: [A]` (waited until A reached `fresh`) now dispatch under the wait-set drain rule whenever A reaches *any* settled state, including `failed` or `parked`. The receiver re-dispatches idempotently with whatever upstream attribute data is persisted (A's last successful resolution, possibly from a prior frame). Templates that relied on the implicit "won't fire if upstream failed" gate must rewrite the coupling as an explicit filtered subscription (e.g. `{node: A, on: state, when: fresh, outcome: fresh_changed}`) and check executor-side whether the upstream attribute data is current for the receiver's logic. The template-validator's `dependencies:` rejection error includes a pointer to this section so the rewrite path is discoverable when an author hits the error.

Pre-v1: break freely. No compat shim, no deprecation window. The migration touches:

- `test/scenarios/*` — every scenario template rewritten.
- `executors/claude-agent/` template fragments in docs and examples — rewritten.
- `executors/stub/`'s template fixtures (including `executors/stub/stubtest/` if applicable) — rewritten.
- `docs/agents/examples/*.md` template snippets — rewritten.
- `docs/concepts/*` — rewritten where `dependencies:` or send-side `invalidate.targets` are illustrated.
- `deploy/` reference templates — rewritten.

### Concept-doc impacts

Mutate in place:

- `concept:cascade` — Definition unchanged; Boundaries gain the wait-set machinery; Invariants gain "eligibility = empty wait-set + state=stale". Notes entry.
- `concept:invalidate` — still the sole graph-level stale message; emitter list updated (cascade-walk-from-subscriptions replaces dependency-walk; lifecycle-handler-`invalidate.targets` removed). Notes entry.
- `concept:node` — Boundaries: `dependencies:` retired; `subscribes:` introduced; substitution refs auto-subscribe. Notes entry.
- `concept:lifecycle-handler` — Boundaries: `invalidate.targets` clauses retire across the three lifecycle slots; the doc's cross-references to `concept:on-event-handler` (the `on_event:` map shares the resolve+invalidate vocabulary with the lifecycle handlers per the existing concept text) drop along with on-event-handler's retirement, so the concept reduces to "three lifecycle slots with `resolve` + `error_class`." `resolve` and `error_class` stay. Notes entry.
- `concept:error-policy` — Boundaries: `action: invalidate` retires; `action: retry | give_up | pass` stay. Notes entry.
- `concept:named-event` — Boundaries: consumption paths updated (substitution stays, handler-invalidate retires, subscription-to-event is the new third path). Notes entry.
- `concept:frame` — Notes entry: `rimsky_wait_set` rows cascade-deleted on frame close.
- `concept:last-outcome` — Notes entry: values become filter predicates on `state` subscriptions.

Retire fully (move to `concepts/_retired/` with a tombstone):

- `concept:on-event-handler` — the per-node `on_event:` map disappears. Subscription-to-event replaces both effects.

New concepts:

- `concept:subscription` — the new reactive primitive. Topics (`state` / `attribute` / `event`), scope (per-node / `instance: true` cross-cutting), `frame: in | next` modifier, auto-subscribe from substitution refs. Invariant: subscriptions validate against upstream's declared output topology at template registration when reachable.
- `concept:wait-set` — the per-frame ledger that derives eligibility. Operations: insert on cascade-walk match; bulk-delete on sender resolution; cascade-delete on frame close. Invariant: a node dispatches iff `state=stale` AND its wait-set is empty for the current frame. Observable via `/admin/diagnostics/wait-sets`.

### Testing strategy

New scenario tests under `test/scenarios/` covering:

- Multiple-invalidator wait-set drain (3 upstreams stale; receiver waits until all resolve).
- Conditional-subgraph fan-in (the dissolved barrier problem) — a `finalize` node downstream of an always-required spine plus two conditionally-active subgraphs dispatches correctly when zero, one, or both optional subgraphs were applicable for the run.
- Cross-cutting `instance: true` subscriptions fire on any-node-in-instance topic match; do not fire on no-match.
- Frame-end `ON DELETE CASCADE` cleanup verifies no wait-set rows survive frame close.
- `frame: next` loop convergence: mutually-subscribed nodes with `frame: next` modifiers eventually converge across frames without deadlock.
- Eligibility predicate respects multiple senders (regression test for the "single-invalidator assumption" bug class).

Plus unit tests:

- Template validator rejects every old-shape construct with informative errors.
- Template validator accepts every new-shape construct.
- Substitution-ref auto-subscribe inference for the various directive shapes.
- Cross-checks against upstream output topology (declared events, attribute names, valid states).
- `/admin/diagnostics/wait-sets` controlapi test against fixture data.
- Race tests (`-race -count=3`) on the cascade-walk + wait-set insert/delete discipline.

---

## Piece 2: `parked_reason` typed

### What is missing today

`col:rimsky_node_runs.parked_reason TEXT` already exists in the schema (pre-baseline migration). `Park.reason` already exists on the wire as a free-form `string`. The diagnostics endpoint `/admin/diagnostics/parked-nodes?reason=<name>` already filters. The `rimsky_parked_nodes_by_reason` Prometheus gauge already exists. The concept doc for `parked-state` already lists `parked_reason` as a column.

What is missing:

1. Type at the proto layer — well-known reasons aren't checked at compile time.
2. A free-form human annotation distinct from the typed reason — today the same field carries both.
3. An MCP tool on `executors/claude-agent` letting the agent itself emit a Park with a typed reason (today only the rate-limit-detection harness path emits Park).
4. A `rimsky-cli parked` subcommand.
5. Dashboard visual treatment of distinct reasons.

### Proto change

```protobuf
enum ParkReason {
  PARK_REASON_UNSPECIFIED   = 0;
  PARK_REASON_TIME_WAIT     = 1;
  PARK_REASON_SIGNAL_WAIT   = 2;
  PARK_REASON_AWAITING_HUMAN = 3;
  PARK_REASON_RETRY_BACKOFF = 4;
}

message Park {
  ParkReason reason         = 1;   // was: string reason — type changes; pre-v1 wire-breaking is fine
  string     reason_note    = 5;   // free-form annotation; new
  bytes      payload        = 2;
  google.protobuf.Timestamp resume_at = 3;
  string     session_token  = 4;
}
```

Field 1 changes wire type (length-delimited string → varint enum). Pre-v1, every client rebuilds against the new proto. The Go protobuf library tolerates wire-type mismatch by treating the field as unknown and dropping it; a mixed-version cluster that slipped through would silently lose the reason on new-server-from-old-client traffic. The pre-v1 break-freely policy covers this; mentioned here so the implementation cycle's deploy notes capture "all binaries rebuilt against regenerated proto bindings."

The enum is the authoritative source for queries, filters, and metrics. The `reason_note` carries the long tail and is human-readable annotation only. `PARK_REASON_OTHER` is not introduced — the long tail belongs in `reason_note`, not in a catch-all enum value. `PARK_REASON_BARRIER_WAIT` is also not introduced — the barrier pattern dissolves under Piece 1, so no executor parks for fan-in.

### Schema

`col:rimsky_node_runs.parked_reason TEXT` stays as the storage column; values are the enum's `lower_snake_case` form (`awaiting_human`, etc.) so the existing `?reason=awaiting_human` filter and the existing Prometheus label format continue to work without consumer-visible churn. A new column is added:

```sql
ALTER TABLE rimsky_node_runs ADD COLUMN parked_reason_note TEXT;
```

Pre-v1: baseline-migration update rather than a new numbered migration, per the rules.

### Subscription composition with Piece 1

A `state` subscription with `when: parked` accepts an optional `reason: <kind>` filter. YAML filter values use the `lower_snake_case` form that matches the storage / CLI / Prometheus surface (the proto enum symbol `PARK_REASON_AWAITING_HUMAN` serializes to `awaiting_human` everywhere outside the proto layer):

```yaml
subscribes:
  - { instance: true, on: state, when: parked, reason: awaiting_human, frame: next }
```

The filter narrows the subscription to parks of a specific reason. This is the documented composition point with Piece 1 — a "human-attention alerter" or per-reason watchdog can fire on subscription rather than polling the diagnostics endpoint.

Until both pieces land, Piece 1's subscription validator silently accepts `reason:` filters without applying them at runtime (a no-op widening). Piece 2's landing wires the filter into the cascade walk's match predicate.

### Consumer surface

`route:GET /admin/diagnostics/parked-nodes?reason=<kind>`: already exists; gains validation that `<kind>` is a known enum value (snake_case form). Unknown values return 400 with a list of valid options.

`rimsky_parked_nodes_by_reason` Prometheus gauge: already exists; labels become the enum-derived snake_case strings.

New CLI subcommand `rimsky-cli parked list` (new file `control/cli/parked.go`):

```sh
rimsky-cli parked list                           # all parked
rimsky-cli parked list --reason=awaiting_human
rimsky-cli parked list --reason=signal_wait --older-than=1h
rimsky-cli parked list --instance=<uuid>
```

Output is a table with columns: instance, node_id, parked_at, resume_at, reason, reason_note. Implementation: one HTTP call to `/admin/diagnostics/parked-nodes` with the filter query params; matches the existing CLI shape.

Dashboard updates in `dashboards/rimsky-dashboard`:

- A parked-nodes view groups by reason.
- `AWAITING_HUMAN` is rendered with operator-attention styling (high-visibility); other reasons render uniformly.
- Per-reason counts surface from the existing `rimsky_parked_nodes_by_reason` Prometheus gauge or from the diagnostics endpoint, at the dashboard's discretion.

### Bundled-executor migration

`executors/stub/stub.go::Park` callsite migrates the free-form string reason to the enum + optional note.

`executors/claude-agent` adds a new MCP tool `mcp__rimsky-callback__report_park`:

- Signature: `report_park({ reason: ParkReason, reason_note?: string, resume_at?: ISO timestamp })`.
- Allowed `reason` values (MCP tool surface is JSON, so snake_case form per the spec-wide casing rule): `time_wait`, `signal_wait`, `awaiting_human`, `retry_backoff`. `unspecified` is rejected (the agent always knows why it's parking). `barrier_wait` is not in the enum.
- `session_token = runId` (mirrors the existing rate-limit-detection Park path).
- `payload` is left empty for v1; the agent stashes session state in its own CLI session via `--resume`.
- Coexists with the existing rate-limit-detection Park path. Both resolve the same outcome promise; whichever fires first wins. The two cover different layers — the rate-limit path handles "Claude itself is rate-limited"; `report_park` handles "the agent judged that an external resource it needs is rate-limited / temporarily unavailable / awaiting human."

`executors/http-node` is unchanged. Rate-limit-on-429 park behavior is deferred to its own design pass.

### Deferred follow-ups

The following are explicitly out of scope for this cycle and are listed here so a future cycle has them in view:

- `OnNodeParked` lifecycle subscriber event. Adding a node-level event opens design questions (subscriber routing, idempotency key shape, emission site control-api-vs-supervisor) that don't have settled answers in `concept:lifecycle-subscriber`. The Prometheus gauge plus the diagnostics endpoint cover the observability path adequately for now.
- Per-reason `max_park_duration`. Today's single `max_park_duration` watchdog stays. Per-reason caps remain a follow-up if consumer pressure surfaces.
- Rate-limit park behavior in `executors/http-node`. New opinionated behavior with its own design questions (when to park vs. error; what `resume_at` to compute; interaction with template `error_types:`). Separate pass.

### Concept-doc impacts

- `concept:parked-state` — Notes entry citing the type promotion of `parked_reason` to enum and the new `parked_reason_note` column.
- `concept:executor` — Notes entry citing the Park proto change (`reason: string` → `reason: ParkReason`; new `reason_note: string`).

### Testing strategy

- Proto regeneration; smoke test verifying Park end-to-end with the enum reason and reason_note.
- Schema migration test for `parked_reason_note`.
- CLI tests for `parked list` against a fake-fixture controlapi.
- Subscription-composition scenario test: subscribing to `{on: state, when: parked, reason: awaiting_human}` fires correctly when a node parks with that reason and does not fire for other reasons.
- Stub executor terminal smoke under the new enum.
- Claude-agent integration: new `report_park` MCP tool tested in `executors/claude-agent/src/server.test.ts`, including the allowed-reason restriction and coexistence with the rate-limit-detection Park path.

---

## Piece 3: Atomic-staging pattern + reference producer

### What this is

A pattern doc plus a worked example for consumers building custom `ClaimProducer`s where the desired semantics are:

- `Open` creates a staging area against the target substrate.
- Writes during the claim's lifetime go to staging, not to canonical state.
- `Commit` atomically swaps staging into canonical position.
- `Abandon` drops the staging area; canonical state is untouched.

Generic across substrates. The "atomic" part lives in the producer's implementation, not in rimsky — rimsky orchestrates the verb sequence and gates concurrent acquisition.

### Artifact map

- **Pattern doc:** `docs/agents/examples/atomic-staging.md`. Covers the four-verb mapping onto stage-then-swap semantics, atomicity-caveats appendix by substrate (Postgres / Iceberg / Filesystem POSIX-rename atomic; S3 not-atomic; BigQuery dependent; streaming substrates incoherent), held-subgraph integration, concurrent-stager handling, sweep / TTL discipline.
- **Reference producer:** new top-level directory `examples/atomic-staging-fs-producer/`:
  - `cmd/main.go` — server entrypoint.
  - `server/server.go` — gRPC ClaimProducer impl.
  - `store/store.go` — backing layout (configured root + canonical subdirectory per scope + staging subdirectory per `claim_id`); two-rename atomic swap on `Commit`; `rm -rf` on `Abandon`.
  - `sweep/sweep.go` — periodic loop dropping staging directories older than a configured TTL (default 24h) whose `claim_id` isn't in rimsky's `rimsky_claim_handles` table.
  - `template.yaml` — worked-example template.
  - `README.md` — how to run; what it demonstrates.
- **Config snippet** in `deploy/rimsky.yml` (illustrative; not added to the deploy compose by default):
  ```yaml
  claim_producers:
    - name: atomic-staging-fs
      endpoint: example-atomic-staging-fs:8090
      protocols: [claim_producer]
      write_semantics_allowed: [staged_async]
  ```

### Verb mechanics

- **`Open(scope, intent: rw)`** creates `staging/<scope>/<claim_id>/`; returns the absolute path as `OpenResponse.address`; declares `realized_write_semantics: staged_async`; records `(claim_id, staging_path, canonical_path)` in an internal `producer_state.db` SQLite file (example simplicity — chosen over an in-memory map so the sweep loop survives restart).
- **`Commit(claim_id)`** is a two-rename atomic swap: `mv canonical/<scope> canonical/<scope>._old`; `mv staging/<scope>/<claim_id> canonical/<scope>`; `rm -rf canonical/<scope>._old`. Atomic on same filesystem; documented limitation in the pattern doc.
- **`Abandon(claim_id)`** runs `rm -rf staging/<scope>/<claim_id>`. Producer's internal record is removed.
- **`Release(claim_id)`** for `r` intent is a no-op. For `rw` intent that never committed it is equivalent to `Abandon`.
- **`Capabilities()`** declares `protocols: [claim_producer]`; `write_semantics_envelope: [staged_async]`; `scope_conflict_matrix: rw-rw same scope = conflict, rw-r and r-r = compatible`.

### Concurrent stagers

Byte-equal scope serialization at rimsky's claim-handle gate means two `rw` claims on the same scope can never be open simultaneously. The producer does not need internal scope-coordination logic.

### Sweep / TTL discipline

The sweep loop runs on a configurable interval (default 5m). It queries `rimsky_claim_handles` for live `claim_id`s (via a configured Postgres DSN, since this is an example), then drops staging directories older than `staging_ttl` (default 24h) not in the live set. This handles the "leaked staging from a crashed run" case.

### Worked-example template

The example template demonstrates stage-then-verify-then-Commit:

- `stage-data` node opens the held claim (`stores: [{name: atomic-staging-fs, alias: target, selector: my-scope, intent: rw}]`) and writes data into the staging path returned by `Open`.
- `verify-staged` and `verify-staged-domain` nodes inherit the claim (`inherits: [{claim: target}]`), reading from the staging path via substitution.
- All-success aggregate outcome → auto-terminal fires `Commit` → swap. Any-failure aggregate outcome → auto-terminal fires `Abandon` → staging dropped; canonical untouched.

Template uses the new subscription syntax from Piece 1. Coupling between `verify-staged*` and `stage-data` is implicit via substitution refs to `stage-data`'s attributes; no explicit `subscribes:` needed.

### Interaction with Piece 1

- Held-claim inheritance is unchanged.
- Verify nodes' coupling to `stage-data` migrates from `dependencies:` to substitution-implied subscription (or to explicit `subscribes:` if the verify node reads nothing from `stage-data`).
- Auto-terminal mechanism (`runtime/auto_terminal.go`) is untouched. Aggregate-outcome rule fires as today.

### Concept-doc impacts

Light. `concept:claim-producer` Notes entry citing the new example. No new concept; the pattern is producer-side discipline, not a rimsky-level surface.

### Testing strategy

- Conformance run via `rimsky-claim-producer-conformance --endpoint atomic-staging-fs --transport grpc` confirms the producer satisfies the protocol contract.
- Worked-example template smoke test: aggregate-Commit-on-success and aggregate-Abandon-on-failure paths, end-to-end against a running supervisor + the example producer.
- Sweep-loop unit test verifying staging directories are dropped correctly given a mock live-handle-id set (cover: alive handle staging directory preserved; old leaked staging directory dropped; recent leaked staging directory preserved within TTL).

---

## Phasing within the cycle

Dependency ordering, not timing:

- **Piece 1** is foundational and lands first. It changes the cascade engine, template validator, scheduler tick (SweepReady), schema, and reaches into every existing scenario test, bundled-template fragment, and doc snippet.
- **Piece 2** is independent of Piece 1 and lands in parallel. The one composition point (`reason:` filter on `state, when: parked` subscriptions) is gated: Piece 1's validator accepts the filter syntax without applying it at runtime until Piece 2 lands.
- **Piece 3**'s producer impl, sweep loop, and pattern doc are independent of Piece 1. Its worked-example template uses the new subscription syntax, so the template is the last artifact written for Piece 3; Piece 1 must have landed at least through the template-validator changes for the example template to be writable.

The cycle's end state ships all three.

## What this isn't

- Not a held-claim subgraph mechanism change. `runtime/auto_terminal.go`, the aggregate-outcome rule, and `@blessed-invariant 13` are unchanged.
- Not a rename of any persisted enum or value (`fresh`, `stale`, `running`, `failed`, `parked` stay; `last_outcome` values stay; transition reasons stay).
- Not a change to the scheduler's per-instance frame discipline beyond wait-set cleanup at frame close.
- Not a change to MCP / Control API surface except `/admin/diagnostics/wait-sets` and the `parked list` CLI subcommand (which talks to the already-existing parked diagnostics endpoint).
- Not a runtime-config feature for "any-of dependencies" or "first-fresh-of-set" — eligibility-as-empty-wait-set IS the any-of semantic now.
- Not a foundation refactor of cascade engine module structure (the engine stays in `foundation/cascade/`; new types are added there for subscription edges and wait-set ops).
- Not a substitution-grammar change beyond the `deps.X.Y` → `nodes.X.attribute.Y` rename and the auto-subscribe rule. `{{nodes.X.event.Z}}`, `{{claim.X.Y}}`, `{{params.X}}` stay; the rename is purely surface (the resolution mechanics in `graph/attribute/substitution.go` are unchanged beyond the directive-prefix parsing).
- Not a change to `concept:inertness` invariants (`@blessed-invariant 11`, `20`, `21`). Subscription topic filters operate on metadata (state, attribute key, event name, error class), not payload bytes.
- Not an `OnNodeParked` lifecycle event addition (deferred).
- Not a per-reason `max_park_duration` addition (deferred).
- Not a rate-limit park behavior in `http-node` (deferred).

## Tensions resolved

These are new tensions surfaced and resolved during this brainstorm; recorded for the design log:

- **`tension:dependency-overloaded-bundle`** — the `dependencies:` block bundled read-access + cascade-subscription + eligibility-gate. **Resolution:** decomposed into substitution refs (read access, auto-subscribe), explicit `subscribes:` (non-reading cascade subscriptions), and wait-set-based eligibility. `dependencies:` retires as a node-template construct.
- **`tension:subscription-implies-cascade-dependency`** — attribute substitution required an explicit `dependencies:` declaration while event substitution did not; neither auto-extended cascade. **Resolution:** all substitution refs auto-subscribe; cascade is uniform.
- **`tension:rimsky-not-a-dag-vocabulary`** — the surface vocabulary lagged the reactive-message-graph reality (`dependency` as a noun framed rimsky as a DAG; `invalidate.targets` enabled bidirectional message flow). **Resolution:** subscriptions are first-class; `dependency` retires as a noun; cascade is documented as the message-routing layer.
- **`tension:send-vs-subscribe-asymmetry`** — push-style `invalidate.targets` coexisted with pull-style `dependencies:` over the same conceptual surface (reactive coupling). **Resolution:** send-style retires across the lifecycle-handler family and `error_types`; subscription-only is canonical.
- **`tension:frame-next-wait-set-placement`** (resolved during implementation) — the spec's original "next-frame wait-set row keyed on the current sender" shape was not realizable: by the time the next frame opens the current sender has already settled, so the drain trigger has come and gone and any such row would never drain. **Resolution:** under `serial_queue`, the `frame: next` cascade walk opens a new frame for the receiver via `frame.EnqueueOrCoalesce` and inserts no wait-set row; in-frame gating in the new frame is handled by the new frame's own cascade walks. See erratum above the "Cross-cutting topics" section.

## Concept impacts

New concepts (created in `.ok-planner/design/concepts/`):

- `subscription`
- `wait-set`

Mutated in place (Notes entries appended):

- `cascade`
- `invalidate`
- `node`
- `lifecycle-handler`
- `error-policy`
- `named-event`
- `frame`
- `last-outcome`
- `parked-state`
- `executor`

Retired (moved to `.ok-planner/design/concepts/_retired/`):

- `on-event-handler`

`claim-producer` gets a light Notes entry citing the atomic-staging example.
