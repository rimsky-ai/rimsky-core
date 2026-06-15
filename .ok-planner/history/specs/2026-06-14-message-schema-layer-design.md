# Message schema layer: messages as virtual nodes, coalesce retires, single frame-creation path

**Date:** 2026-06-14
**Source sketches:** `.ok-planner/sketches/2026-06-13-message-schema-layer.md` (primary); `.ok-planner/sketches/2026-05-29-message-schema-layer.md` (superseded by the June 13 sketch). Both archive to `.ok-planner/history/sketches/` on spec approval.

## User outcomes

### STORY-message-schema

As a template author, I can declare which message types instances of this template accept, so that messages have a typed contract and unknown ones fail loud instead of silently dead-lettering.

**Acceptance:** I write a template-level `messages:` block enumerating accepted types, each with a body shape. When a sender posts a message of a declared type, the instance opens a frame and the receivers I have declared via subscriptions stale-mark, substituting the body into their attribute schemas. When a sender posts a message of an undeclared type, the request is refused with an error naming the unknown type and listing the declared set.

**Falsifier:** A message of an undeclared type lands in the ledger and is silently dropped; OR a declared message arrives and no subscribed node is marked stale.

**Proof:** Executable proof against a running rimsky instance — declared type opens a frame and stale-marks subscribed receivers; undeclared type refuses with the expected error.

### STORY-cascade-emit

As a template author, I can declare a node-type whose dispatch is to emit a message of a given type, so that cross-frame coupling is explicit as a graph object I can point at.

**Acceptance:** I write a node-type carrying `emits_message: <type>` (rather than `executor:` or `delegate:`). The node has `subscribes:` and `attributes:` blocks like any other node; its attribute schema matches the destination message type's body schema exactly. When its `subscribes:` entries fire, the node dispatches: substitution resolves its attributes from upstreams and instance params, the runtime constructs a message envelope with the resolved attribute set as the body, and inserts it into the message ledger. The next frame opens carrying that message.

**Falsifier:** A subscribed condition triggers and the emit-node's dispatch produces no message in the ledger; OR the emit-node's attribute schema can declare fields the destination message type's body schema doesn't, without registration-time error; OR the body fails to reflect the resolved attribute values.

**Proof:** Executable proof — emit-node dispatches when its subscriptions fire; resulting message body contains the expected substituted values; mismatched schemas reject at registration.

### STORY-cross-frame-coupling

As a template author, I can express cross-frame coupling (back-edges in cycles, self-drain-my-queue) through emit-nodes plus the message schema, with the receiver reading the sender's data via the message body, so that patterns that previously failed silently now work cleanly.

**Acceptance:** I write a 2-cycle A → B → A where B's settlement triggers a message-emitter-node whose dispatch emits a message that A subscribes to. When B settles, the emit-node runs, the message lands in the ledger, the next frame opens with A stale-marked, and A reads B's data through `{{messages.<type>.<field>}}` in its attribute schema. Separately, I write a self-emit (a message-emitter-node that subscribes to its own emit-source with `when: payload.changed`) and the loop drains until convergence.

**Falsifier:** A multi-node back-edge cycle silently drops the dispatch — the message envelope appears in the ledger but no frame opens for the receiver; OR the receiver re-runs but cannot read the sender's data; OR the self-drain loops infinitely without converging.

**Proof:** All-of-the-above — executable proofs for the back-edge cycle and the self-drain convergence, plus a demo walking through the scenario succeeding.

### STORY-one-message-per-frame

As a template author, I can rely on substitution from the message body always being well-defined in a node that's reacting to a message, so that no template ever has to refuse a multi-message coalesced frame at runtime.

**Acceptance:** Across all instances and templates, a frame carries at most one delivered message. A node whose attribute schema substitutes from a typed message body always has exactly one body to read; the substitution never refuses or returns an ambiguous value. Two messages posted in close succession produce two frames (one each).

**Falsifier:** Two messages share a frame; OR a template that substitutes from a message body fails at substitution time with a "multiple messages" error.

**Proof:** Executable proof — N messages posted in succession produce N distinct frames, each carrying one message, each settling cleanly with body substitution resolving the expected values.

### STORY-frame-origin-audit

As an operator, I can see for every frame what triggered it (an operator message, a publisher message, or a cascade-emitted message) through the existing frames-read observability surface, so that "why did this frame open" is always answerable directly.

**Acceptance:** Every frame carries a pointer back to the message ledger entry that triggered it, surfaced through the existing frames-read observability endpoint. No frame in the system has "cascade walker" or "internal" as its origin. Looking up a frame returns the originating message envelope (sender, type, body).

**Falsifier:** A frame appears without an originating message reference; OR an internal-to-runtime path creates a frame in any code path.

**Proof:** Demo — every frame in a representative end-to-end run (including back-edge cycles and self-drain) has an originating message visible through the observability surface.

### STORY-typed-message-substitution

As a template author, I read from and compose message bodies using the same substitution grammar that handles node attributes, with each message type addressable by its declared name (so I can disambiguate when a node could react to several types), so that message bodies are first-class typed attribute blocks that flow across frames.

**Acceptance:** A receiver node's attribute schema substitutes from a specific message type by naming the type — e.g., `{{messages.<type>.<field>}}` parallel to `{{nodes.<node-type>.attribute.<field>}}`. A message-emitter node's attributes compose the destination message body by the same substitution grammar (sources can be other nodes' attributes, instance params, the triggering signal's payload). The substitution engine validates references at template registration in both directions: a receiver reading a field the declared `messages:` body schema doesn't have rejects; an emitter declaring an attribute field the destination type's body schema doesn't have rejects. The runtime resolves message-body reads through the same code path that resolves attribute reads — one substitution engine, two surfaces.

**Falsifier:** The grammar for substituting from messages differs in shape from the grammar for substituting from node attributes; OR the engine has a separate code path for message-body reads vs attribute reads; OR a typo in a `messages:` body field on either side registers without error; OR a receiver attribute schema can read from a message type without naming that type in the substitution.

**Proof:** Executable proof — typo'd field names reject at registration in both directions; a running back-edge cycle's receiver reads through the typed-message grammar and resolves correctly; a code-path assertion confirms the same substitution-resolution function services both surfaces.

### STORY-debug-channel

As an operator, I can override-invalidate a specific node or override-set an attribute value via the control-api when the target instance is paused or at a breakpoint pause-mode hit, so that ad-hoc inspection and mutation are available exactly when I have explicitly entered debug mode, and unavailable otherwise.

**Acceptance:** With an instance `paused: true` or with an unresumed pause-mode breakpoint hit blocking a runner, I can post a debug override that stale-marks a specific node and/or sets a specific attribute value in the running frame; the override applies in that frame. When the instance is neither paused nor breakpoint-stopped, the same request is refused with an error citing the required state.

**Falsifier:** A debug override is accepted on an instance that is neither paused nor breakpoint-stopped; OR the override is refused on a paused-or-breakpointed instance.

**Proof:** Executable proof — override accepted on both legal states (paused, breakpoint); refused on a healthy running instance with the expected error.

## Architecture

Every frame opens because exactly one message — operator-posted, publisher-emitted, or cascade-emitted by a message-emitter node — landed in the instance's message ledger. The instance's template carries a `messages:` block declaring the type registry (the accepted message types and their body shapes). At the frame boundary, exactly one pending message delivers; the frame opens with a `triggering_message_id` pointer to that envelope. Receivers stale-mark by virtue of subscribing to the message-type as a virtual node — there is no envelope-side routing field that decides who fires.

```
                      ┌─────────────────────────┐
  POST /instances     │ messages: registry      │
  /{id}/messages   ──▶│  (template-level)       │── unknown type ──▶ refuse
  (operator |         │                         │
   publisher |        │   type T:               │
   cascade-emit)      │     body_schema         │   declared type
                      └─────────────────────────┘         │
                                                          ▼
                                                  ┌────────────────┐
                                                  │ message ledger │
                                                  └───────┬────────┘
                                                          │ one-per-frame
                                                          ▼
                                          ┌──────────────────────────────┐
                                          │ frame opens                  │
                                          │  triggering_message_id = id  │
                                          │  audit: frame.start          │
                                          └───────────────┬──────────────┘
                                                          ▼
                                  message-virtual-node T emits terminal/success
                                                          │
                                                          ▼
              receivers re-run                    in-frame cascade walk
              attribute schema substitutes        node A → terminal/success
              from {{messages.T.<field>}}                │
                                                          ▼
                                                  message-emitter node M
                                                  dispatches:
                                                    attributes resolve from
                                                      upstreams + params
                                                    body = attribute set
                                                    envelope → ledger
                                                  ──── next frame opens ────▶
```

Each non-receiver node-type takes one of these dispatch modes today; this spec adds the fourth:

- `executor: <name>` — invoke an executor for normal computation.
- `delegate: <graph-name>` — invoke a sub-graph (per `concept:sub-graph`).
- Fan-out (composite with `executor:` plus partitioning, per `concept:fan-out`).
- `emits_message: <type>` — produce a message envelope with the node's attribute set as the body, insert into the ledger.

Receivers subscribe to message-types using the existing `subscribes:` block with `node: <message-type>` — the message-type is treated as a virtual node-type that emits `terminal/success` on arrival. The body fills via standard attribute substitution: `{{messages.<type>.<field>}}`. Substitution into message bodies and substitution into node attributes use the same engine; the message body IS an attribute block, declared by JSON Schema, that carries across frames.

`coalesce` is gone — frames are serial per instance. The frame-resolution-mode toggle disappears at template, instance, and runtime layers. The `EnqueueOrCoalesce` call inside the cascade walker (`code:lib/runtime/runner_terminal.go:732`) deletes; the single frame-creation path is the message-delivery boundary.

Operator-initiated invalidate (today's `kind: "invalidate"` message) folds into the general typed-message path: an operator who wants to invalidate posts a message of a type the template declares for that purpose. Ad-hoc override (force-stale a node, force-set an attribute) lives at a separate control-API endpoint, gated to instances that are paused or holding a pause-mode breakpoint hit.

Backfill is no longer a primitive — it's a use case of typed messages. The dedicated backfill endpoints retire; the partition-override mechanism is just substitution from a message-body field into a fan-out node's `partition_request:`.

## Components

### Message-schema registry — the `messages:` block

Top-level template block, parallel to `attributes:` (per `concept:attribute`) and `publishers:` (per `concept:publisher-subscription`). One entry per accepted message type:

```yaml
messages:
  - type: ping/recheck
    body_schema:
      type: object
      properties:
        pong_status: { type: string }
      required: [pong_status]

  - type: flush/cache
    body_schema:
      type: object
      properties:
        cache_keys: { type: array, items: { type: string } }
```

Per-entry fields:
- `type:` — the discriminator, in the type-path grammar of `concept:signal` (slash-separated, validator-enforced).
- `body_schema:` — JSON Schema declaring the body's typed key-value shape. Structurally identical to the `attributes:` block's schema; same engine, same grammar.

Registration validation:
- `type:` is unique across `messages:` entries; conforms to the type-path grammar.
- `body_schema:` is a valid JSON Schema.
- Every `{{messages.<type>.<field>}}` ref in any node's `attributes:` block resolves against a declared message type's body schema; typos reject.
- Every message-emitter node's `attributes:` block (a node carrying `emits_message: <type>`) has an attribute set whose field names and types match the named message type's `body_schema` exactly.

Receipt behavior:
- `POST /instances/{id}/messages` (existing universal endpoint, idempotency-keyed per `story:message-bus`).
- Type lookup against the template's `messages:` registry.
- Unknown type → `HTTP 400`, naming the type and listing the declared set.
- Known type → persist to ledger; at the next frame boundary, one message delivers, frame opens with `triggering_message_id`, the message-virtual-node emits `terminal/success`, subscribers stale-mark.

Body validation timing — design choice locked at receiver dispatch, not at receipt:
- The schema's `body_schema` is documentation plus a registration-time check on substitution refs.
- The actual body bytes are validated only when a receiver pulls them via substitution at dispatch (the existing attribute-validation gate per `concept:attribute`).
- Preserves `@blessed-invariant: 21` verbatim — body is read only at the sanctioned substitution leaf and the persistence-layer fetch (the same two sites today).

### Message-emitter node-kind — `emits_message:` dispatch mode

A node-type whose dispatch is "produce a message envelope from the node's attributes and insert it into the message ledger." The dispatch mode is declared on the node-type by `emits_message: <type>` instead of `executor: <name>`. The node has the standard `subscribes:` and `attributes:` blocks; what makes it an emit-node is the dispatch field.

```yaml
- type: ping-recheck-emitter
  emits_message: ping/recheck
  subscribes:
    - node: pong
      type: terminal/success
  attributes:
    pong_status:
      source: "{{nodes.pong.attribute.status}}"
```

Per-node-type semantics:
- The `subscribes:` block fires the node on the named signal (here, `pong`'s `terminal/success`), the same as for any other node.
- At dispatch, the node's attributes resolve through the standard attribute-substitution engine — pulling from upstream nodes, instance params, and the triggering signal's payload via `{{nodes.<self-type>.attribute.<field>}}`-style directives. By the time those values land, the body is built.
- The runtime constructs the envelope:
  - `type: <emits_message value>`.
  - `sender_kind: "instance"` (existing legal value in `code:lib/runtime/message_delivery.go::EnqueueMessage`).
  - `sender: "instance:<id>"`.
  - `payload`: the resolved attribute set, serialized.
  - `Idempotency-Key`: runtime-generated, deterministic on the dispatching node-run's `node_run_id` so a tx retry collapses to the same ledger row.
- The envelope inserts into the message ledger inside the emit-node's terminal-resolution tx. If the tx rolls back, the emit doesn't.

Aggregation across multiple senders falls out of the node-as-graph-object model: the emit-node subscribes to A and B via two `subscribes:` entries; its `attributes:` block pulls from both via standard substitution; the body is the aggregation. The author needs no special "multi-source emit" construct.

Registration-time validation:
- `emits_message:` value matches a declared `messages:` entry in this template.
- The node's `attributes:` schema matches the destination message type's `body_schema` exactly (same field set, same types — superset is rejected; the emit-node exists to produce the message, hidden state is rejected).
- `subscribes:` entries validate per `concept:node-subscription`.

### Receivers — subscribe by message-type as virtual node

A message-type is a virtual node-type. Real node-types subscribe to it through the standard `subscribes:` block:

```yaml
- type: ping
  subscribes:
    - node: ping/recheck
      type: terminal/success
      when: payload.pong_status == "needs_work"
  attributes:
    pong_status_in:
      source: "{{messages.ping/recheck.pong_status}}"
```

- `node: <message-type>` names the virtual node-type — same syntax as subscribing to any other node.
- `type: terminal/success` is the signal the message-virtual-node emits on arrival (every message arrival is structurally a virtual-node settling).
- `when:` CEL predicate filters on the body (`payload` is the message envelope; `payload.<field>` reads body fields per the existing CEL predicate machinery in `concept:signal`).
- Substitution into attributes uses `{{messages.<type>.<field>}}` — same engine as `{{nodes.<node-type>.attribute.<field>}}`, separate namespace for readability.

Cross-frame coupling falls out for free: the message-virtual-node settles in a new frame (the one the message-delivery opens), so subscriptions to message-types are inherently cross-frame. No `frame:` modifier needed (and the modifier is retired anyway).

The auto-subscribe rule extends uniformly: a `{{messages.<type>.<field>}}` reference in an attribute schema implicitly subscribes that node to the message-type (parallel to how `{{nodes.X.attribute.Y}}` implicitly subscribes to `X`'s `attribute/Y/changed`), per `concept:node-subscription`'s auto-subscribe rule.

### Wire envelope

Field set (revised from today's `concept:message` envelope):

| Field | Required | Notes |
|---|---|---|
| `id` | yes | UUID; rimsky-assigned |
| `instance_id` | yes | target instance |
| `type` | yes | the message type-path (renames from today's `kind`) |
| `sender` | yes | identity of the sender |
| `sender_kind` | yes | `operator | publisher | instance` |
| `payload` | optional | typed body, inert per `@blessed-invariant: 21` |
| `received_at` | yes | rimsky-assigned timestamp |

Dropped: `target` (today's optional node-alias). Receivers subscribe by type; the schema-by-subscription model decides who gets the message. The legacy `target: self` fallback path in `code:lib/runtime/message_delivery.go::cascadeMessageSubscribersInTx` retires.

Publisher routing (in `concept:publisher-subscription`):
- `message_kind` renames to `message_type` for vocabulary consistency.
- The current default value (`"invalidate"`) retires; publishers declare a specific type their messages carry, validated against the target instance's `messages:` registry at subscription-mounting time.

### Frame-creation single path + frame-origin audit

A frame opens only at the message-delivery boundary. Creation paths:
- Operator/publisher message: `POST /instances/{id}/messages` persists envelope; at the next frame boundary, the frame producer inserts the frame row with `triggering_message_id = <message id>`.
- Cascade-emit message: the emit-node's terminal-resolution inserts the envelope with the runtime-generated idempotency key inside the sender's tx; the next frame opens with `triggering_message_id = <new message's id>`.
- Debug-channel override (see below): no new frame; the override applies to the running frame, whose `triggering_message_id` stays whatever opened it.

New column: `triggering_message_id UUID NOT NULL` on `rimsky_frames`. Schema-level enforcement of the invariant; nullable would let internally-created frames slip in as a regression. Forward migration adds the column.

`col:rimsky_frames.source_node_ids` retires. Today the column carried the coalesce-merged source-node list. Under one-message-per-frame the source is always the virtual-node corresponding to the triggering message's type — derivable from `triggering_message_id` via the message's `type`. Redundant; column drops in the same migration.

Observability surfaces:
- The `triggering_message_id` column is exposed on the existing frames-read endpoint in `concept:cascade-graph`'s observability surface.
- Forward (frame → message) and reverse (message → frames it triggered) queries are available on the cascade-graph endpoints by joining on the new column.
- The slog `frame.start` log line gains the `triggering_message_id` field.

Frame producer:
- `code:lib/graph/frame::EnqueueOrCoalesce` retires under that name. The replacement (`EnqueueFrame` or equivalent) takes the message id, inserts a single frame row, and returns. No coalesce-merge branch; no upsert.
- The partial uniqueness index on queued coalesce frames drops.
- The operator-API message path still creates queued frames in arrival order.

### Debug channel

A new control-API endpoint for ad-hoc operator override of node state and attribute values.

Endpoint:

```
POST /instances/{id}/debug/override
{
  "action": "invalidate_node" | "set_attribute",
  "node_type": "<type>",
  "attribute_key": "<key>",     # required for set_attribute
  "attribute_value": <json>     # required for set_attribute
}
```

- `invalidate_node` stale-marks every node-run of `node_type` in the running frame.
- `set_attribute` writes `attribute_value` to the named attribute on the named node-type's run row, then stale-marks it.

Gate enforcement, checked in the request tx:
- `col:rimsky_instances.paused = TRUE`, OR
- The instance has at least one unresumed pause-mode breakpoint hit blocking a runner in its running frame (per `concept:breakpoint`'s hit ledger).

If neither holds → `HTTP 409 Conflict` with a body naming the gate predicate.

Synchronous application: because the gate guarantees no runner is advancing, the override applies synchronously inside the request tx — no race with the frame's progress, no async queueing. The response returns once the mutation has committed.

Effect scope:
- For `invalidate_node`: the node-run row's state transitions to `stale`; the cascade walker's next pull picks it up.
- For `set_attribute`: the attribute write commits to the attribute ledger; substitution into downstream nodes' attribute schemas sees the new value when they re-evaluate.

The override does not persist beyond the running frame. When the frame settles, the override's effect has resolved into the same lineage records every other attribute write produces.

Authorization: a new permission scope `instance:debug:override` (working name) — an operator API key without it gets `HTTP 403`. Uses the existing permission model per `concept:permission`.

Audit: every override emits an audit-event row with kind `debug.override.applied`, payload carrying the action, node_type, attribute_key (if applicable), attribute_value (if applicable), and the gate-state that authorized it (paused vs breakpoint).

This endpoint does NOT replace `POST /instances/{id}/messages` (the normal message channel) and does NOT replace the breakpoint "resume with overlay" surface from `concept:breakpoint` (that one-shot L6 overlay scoped to one paused runner stays).

### Backfill as ordinary message use

`concept:backfill` retires entirely. Backfill becomes a documentation example of message use, not a named primitive.

What retires:
- The control-API backfill endpoints (`POST /instances/{id}/backfills`, `GET /instances/{id}/backfills`, `GET /instances/{id}/backfills/{id}`, `GET /instances/{id}/backfills/{id}/partitions`, `POST /instances/{id}/backfills/{id}/cancel`) — all drop.
- CLI `backfill` subcommands — drop.
- Backfill-specific lineage chain key (`backfill_operation_id` column on message rows) — drops. Authors who want to correlate related messages put an opaque identifier in their message body; the runtime treats it as normal body content.
- The backfill-target-validity check ("warn/reject if the target isn't a fan-out node wired for the override") — becomes ordinary attribute-substitution validation. A fan-out node with `partition_request:` pulling from `{{messages.<type>.<field>}}` validates at registration the same way any substitution does.

What survives, through general surfaces:
- "Operator triggers a backfill" → POST a message of the declared type, body carries the partition override. The template author declares the message type in `messages:`; the fan-out node's `partition_request:` substitutes from `{{messages.<type>.<field>}}`.
- "See past backfill operations" → general message-history endpoint, filtered by type.
- "See the rollup of one operation's children" → given a `message_id`, query frames where `triggering_message_id = ?`, then runs within those frames, then their fan-out children. General observability via `concept:cascade-graph`.

What disappears entirely:
- The "cancel an in-flight backfill" capability — operators can no longer cancel a queued backfill once it's been emitted.
- Backfill-specific operator UX (dedicated list/cancel CLI verbs). Operators use the universal message endpoint plus general history queries.

### What retires (no remnants)

Retired DSL surfaces and code paths are removed from the code entirely. Templates and requests using them fail through the normal validator paths (unknown field, unknown signal type, unknown message type). No detection rule, no migration error string, no parser case that names the old shape.

`coalesce`:
- No `frame_resolution_mode` field in the template DSL; a template carrying it fails through the existing unknown-field handler.
- `col:rimsky_instances.frame_delivery_mode` column dropped from the schema; the runtime doesn't know about it.
- Control-API request structs don't carry the field; JSON decoder's unknown-field policy handles requests that include it.
- `code:lib/runtime/message_delivery.go::FrameDeliveryMode` constants, `coalesceDeliverSet`, `buildCoalesceConflictResolver` delete. `DeliverPendingMessages` reduces to "deliver the oldest one message; the rest stay pending."
- The "no silent override loss under coalesce" `@blessed-invariant` annotation on `coalesceDeliverSet` retires — the invariant is now satisfied by construction.
- `code:lib/graph/frame::EnqueueOrCoalesce`'s coalesce-merge branch deletes; the partial uniqueness index on queued coalesce frames drops.

`frame:` modifier:
- No `Frame` field on the subscription struct.
- `code:lib/graph/node/template.go::FrameIn` / `FrameNext` constants delete.
- The cascade walker's branch at `code:lib/runtime/runner_terminal.go:732` collapses to one path (in-tx, in-frame). The `case node.FrameNext` arm and its `frame.EnqueueOrCoalesce` call delete.

`subscribes:` `message` topic kind:
- The `message/<kind>/<sender_kind>/<target>` type-path retires from `concept:signal`'s taxonomy.
- The existing canonical-type-path validator rejects any `type: message/*` because it's not in the taxonomy.
- `code:lib/runtime/message_delivery.go::cascadeMessageSubscribersInTx` retires — the subscription-walk-by-envelope-fields path is gone; cascade walks the message-virtual-node's `terminal/success` through the existing terminal-resolution machinery.

Legacy `kind: "invalidate"`:
- Envelope's `kind` field renames to `type` (above).
- A request body using `kind:` fails the JSON decoder's unknown-field check.
- A `type: "invalidate"` fails the `messages:` registry lookup (no template declares `"invalidate"` as a type); the unknown-type 400 surfaces.

In every case, the failure mode is the generic failure mode for an invalid template or request — not a feature-aware failure that documents the retired shape. The code carries no knowledge that these things ever existed.

### Cross-stack proof retirement (publisher / sensor stories)

The cross-stack e2e proofs that subscribed to the retired `message/<kind>/<sender_kind>/<target>` signal type-path retire alongside that taxonomy entry. The deleted artifacts are `lib/services/test/scenarios/sensor_cascade_e2e_test.go`, `sensor_cron_restart_recovery_e2e_test.go`, `sensor_http_e2e_test.go`, `sensor_object_store_e2e_test.go`, `sensor_webhook_e2e_test.go`, and `examples/publisher/main_e2e_test.go`.

The five durable stories whose proofs lived in those files — `story:publisher-protocol`, `story:sensor-cron`, `story:sensor-http`, `story:sensor-webhook`, `story:sensor-object-store` — keep their existing in-process proofs as the durable acceptance artifacts:

- `story:publisher-protocol` — the shipped publisher reference under `examples/publisher/` (excluding the deleted `main_e2e_test.go`); the protocol-conformance runner under `lib/protocols/conformance/publisher/` exercises Subscribe / Unsubscribe / ListSubscriptions against the bundled and reference publishers.
- `story:sensor-cron` — `lib/services/sensors/sensor-cron/{sensor_test.go,multi_replica_test.go,replica_posture_test.go,state_db_test.go}`. Restart-survives-state and one-replica-fires-once / two-replicas-fire-twice both have unit-test coverage.
- `story:sensor-http`, `story:sensor-webhook`, `story:sensor-object-store` — analogous `sensor_test.go` + `state_db_test.go` pair under each `lib/services/sensors/sensor-{http,webhook,object-store}/`.

The cross-stack proof surface is intentionally retired; the in-process surface is durable and sufficient. Future regeneration of cross-stack proofs against the universal `/instances/{id}/messages` endpoint is a separate spec, not in scope here.

## Technical decisions

### TD-emit-as-node-kind

**Choice:** Cascade-driven message emission lives on a dedicated node-kind, declared by `emits_message: <type>` instead of `executor:` on a node-type. Per-node-type `emits:` block is not introduced.

**Rationale:** The emit becomes a first-class graph object — visible in topology, audit, and the operator dashboard backplane. Aggregation across multiple senders works through the standard `subscribes:` + `attributes:` machinery (the emit-node subscribes to multiple senders; its attributes pull from each). No new validation or substitution machinery; every check reuses the existing attribute and subscription validators.

**Alternatives considered:** A per-node-type `emits:` block in which any node could embed an emission directive. Rejected: aggregation is unnatural (the only node where a multi-source body composes is one that already had all sources as upstreams, coincidental rather than designed); the emit is hidden inside the sender's settle behavior rather than visible as a graph object.

### TD-attribute-set-as-body

**Choice:** A message-emitter node's `attributes:` block IS the message body. The attribute schema must match the destination message type's `body_schema` exactly — same field names, same types. No mapping layer between attributes and body fields.

**Rationale:** One source of truth for attribute field names. A mapping layer would mean two separately-named things kept in sync by a third layer; that's the redundancy the design avoids. The "message body is an attribute block that carries across frames" framing is literal — the emit-node's attribute resolution at dispatch IS the body construction.

**Alternatives considered:** A `body:` sub-block under `emits_message:` mapping attribute names to body field names (or substituting from arbitrary sources into body fields). Rejected as redundant; users who want to decouple shapes can author a separate triggering node feeding a separate emitter node.

### TD-single-frame-creation-path

**Choice:** A frame opens only when a message lands in the ledger and the next frame boundary picks it up. The cascade walker has no path that creates a frame. The `case node.FrameNext` branch in `code:lib/runtime/runner_terminal.go:732` and its `frame.EnqueueOrCoalesce` call delete.

**Rationale:** Every frame's origin becomes auditable via a single mechanism (`triggering_message_id` on `rimsky_frames`). "Why did this frame open" is always answerable from the frames-read observability surface. Cross-frame coupling becomes explicit at the sender (a message-emitter node), not hidden under cascade-walker behavior.

**Alternatives considered:** Preserve `frame: next` on subscriptions as a second frame-creation path. Rejected: the multi-node back-edge silent-failure footgun stems precisely from this dual path — a downstream sender's settle does not re-dispatch the upstream receiver in a multi-node cycle; keeping the dual path perpetuates the failure mode.

### TD-debug-channel-gate-paused-or-breakpoint

**Choice:** The control-API debug-override endpoint (`POST /instances/{id}/debug/override`) is legal iff `col:rimsky_instances.paused = TRUE` OR the instance holds at least one unresumed pause-mode breakpoint hit blocking a runner. Otherwise the request returns `HTTP 409 Conflict` with a body naming the gate predicate.

**Rationale:** The override is a debug feature, not a general operator capability. Both legal states are operator-engineered — the operator deliberately paused or set a breakpoint — so the override is contextually expected. Healthy frames have no override path; the operator must opt in to debug mode first.

**Alternatives considered:** Include "frame held by parked node-run" and "last-progress past frame-timeout" in the gate. Rejected: those are degraded states (normal-operation park, or a bug surfacing as `frame.stuck.observed`); per `.claude/rules/rules.md` the right response is investigation and fix, not override.

### TD-envelope-type-discriminator

**Choice:** The message envelope's `kind` field renames to `type` and carries the message type-path (matching the receiver's `subscribes:` `node:` target and the emitter's `emits_message:` value). The publisher-subscription's `message_kind` field similarly renames to `message_type`.

**Rationale:** The envelope's `kind` carried `"invalidate"` as its only value, providing no information once messages are typed. The rename aligns the wire vocabulary with the conceptual vocabulary; one field, one purpose.

### TD-one-message-per-frame

**Choice:** Every frame carries at most one delivered message. `DeliverPendingMessages` always delivers the oldest single pending message; the rest stay pending until the next frame boundary. N pending messages produce N sequential frames.

**Rationale:** Substitution into the message body is always well-defined — `{{messages.<type>.<field>}}` resolves against exactly one body. The "no silent override loss under coalesce" invariant (today a hand-maintained `@blessed-invariant` on `code:lib/runtime/message_delivery.go::coalesceDeliverSet`) is satisfied by construction; two messages binding the same receiver to different values can never land in the same frame.

**Alternatives considered:** Keep `coalesce` as a non-default opt-in mode for instances that explicitly want it. Rejected: coalesce-as-frame-mode does multiple jobs (queue-merging plus message-bundling plus cascade-walker re-fire); splitting them to keep only the queue-merging job leaves one narrow use case better served by message authoring decisions.

### TD-pre-v1-pure-removal

**Choice:** Retired DSL surfaces (`frame_resolution_mode`, `frame_delivery_mode`, `frame:` modifier, `kind: "invalidate"`, `subscribes:` `[type: message/*]`, dedicated backfill endpoints, `backfill_operation_id` column) are removed from the code entirely. Templates and requests using them fail through normal validator paths (unknown field, unknown signal type, unknown message type). No detection rule, no migration error string, no parser case that names the old shape.

**Rationale:** No remnants of retired features in the code. Pre-v1, per `.claude/rules/rules.md`, the bias is clean removal; backwards-compatibility shims and migration helpers are not warranted. The normal validator's "unknown field" error is the rejection.

## Design changes

Per `.ok-planner/CLAUDE.md`'s spec-driven mutation model, the following changes to `.ok-planner/design/` are applied by `execute-plan` carrying out this spec's plan tasks. Each artifact body below follows the self-containment rule (no file paths, no `code:` citations, no external doc references).

### Concept changes

**Mutate `concepts/message.md` in place.** Replace Definition with:

> A typed envelope whose arrival at an instance opens a frame. The envelope's `type` field selects an entry from the instance's template message-schema registry; an undeclared type is refused at receipt with an unknown-type response. Persisted in the message ledger on receipt; delivered to subscribers at the next frame boundary, one message per frame. Cascade-emitted, operator-emitted, and publisher-emitted messages traverse the same delivery path.
>
> Envelope shape: `id`, `instance_id`, `type` (the message type-path), `sender`, `sender_kind` (`operator | publisher | instance`), `payload` (the typed body, inert), `received_at`. Receivers are decided by subscription to the message type as a virtual node-type — there is no envelope-side routing field.

Replace Boundaries with:

> Owns: the envelope shape and the message ledger; the one-message-per-frame delivery rule; the subscription-walk-as-virtual-node at frame boundary (each message type is a virtual node-type emitting `terminal/success` on arrival); the dead-letter audit (no-subscriber landings still write a ledger row with a `terminal/success` emission); the universal `Idempotency-Key` dedup ledger; the registry lookup gate on receipt. Does NOT own: the type registry itself (see `concept:message-schema`); cascade walks within a frame (see `concept:cascade`); event emissions from executors (see `concept:named-event`); the frame creation mechanics (see `concept:frame`); the publisher's substrate state (see `concept:publisher` / `concept:publisher-subscription`); the emit-node's dispatch (see `concept:message-emitter-node`). Adjacent: `concept:frame`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:message-schema`, `concept:message-emitter-node`.

Replace Invariants with:

> - Two external emit sites and one internal: operator API (the message-emit endpoint with `sender_kind: "operator"`), publisher emissions (the same endpoint with `sender_kind: "publisher"` + a publisher-subscription capability token), and cascade-emit (a message-emitter node's dispatch, with `sender_kind: "instance"` + sender `instance:<id>`). All three paths land in the same ledger and follow the same delivery rules.
> - One message per frame. At each frame boundary, exactly one pending message delivers; the rest stay pending until the next frame.
> - Type lookup at receipt: a message whose `type` is not declared in the target template's message-schema registry is refused with an unknown-type response; loud miss, not silent dead-letter.
> - Delivery at frame boundary: the message-virtual-node settles in the new frame and emits `terminal/success`; nodes subscribing to that virtual node-type stale-mark; the message's `delivered_at` and `frame_id` populate.
> - Payload is inert (see `@blessed-invariant: 21`). Read only at the substitution leaf and the persistence-layer fetch.
> - Publisher requests are capability-checked at the existing publisher-subscription validation: rimsky validates that the publisher-subscription is a live, active binding for the target instance.

**Mutate `concepts/frame.md` in place.** Replace Definition with:

> A frame is one cascade resolution. It is a persisted frame row carrying a triggering-message reference and a lifecycle state (`queued`, `running`, `completed`, or `failed`). Every dispatched run carries the frame it belongs to (the run row's frame reference is non-null). A frame begins only when a message lands in the message ledger and the next frame boundary picks it up — operator-emitted, publisher-emitted, or cascade-emitted by a message-emitter node, all converging on the same delivery path. Resuming a parked node — park-wake, via async callback or snooze timer — does not begin a frame; it resumes the still-running frame the parked node belongs to. The frame ends when every node_run in the frame is resolved; a `parked` node_run holds its frame open.
>
> Frames are serial per instance: at most one running frame, queued frames dispatched in arrival order.

Replace Purpose with:

> Frames are the unit of cascade resolution. They let new messages arriving during in-flight propagation queue cleanly without preempting the running work — at most one frame runs per instance, queued frames dispatch in arrival order. They also tie the audit trail together: every terminal handler attributes back to its frame, and every frame back to the triggering message.
>
> Ordering is per-instance, not template-wide: two instances of the same template execute independently. A consumer expecting template-wide serialization must coordinate above rimsky.

Replace Boundaries with:

> Owns: the per-instance concurrency rule (≤1 running frame), the serial-per-instance ordering, the last-progress timestamp, frame-timeout warning emission, the triggering-message-id pointer that every frame carries. Does NOT own: node state (lives on the node-run, see `concept:node-run`), claim conflict (lives in `concept:claim-handle`), scheduling cadence (lives in `concept:sensor`), the message itself (see `concept:message`). Adjacent: `concept:cascade`, `concept:node`, `concept:node-run`, `concept:message`, `concept:sensor`.

Replace Invariants with:

> - At most one `running` frame per instance.
> - Every frame row carries a non-null triggering-message reference. There is no path that creates a frame without a triggering message.
> - Every dispatched run row carries a non-null frame reference.
> - Frames are processed in arrival order per instance; cross-instance ordering is independent.
> - The frame timeout is purely advisory: when the last-progress timestamp falls outside the window, the scheduler emits a single `frame.stuck.observed` warning and takes no destructive action.

Drop the Held frames section's content as-is (the held-frame concept is unchanged in substance; the section remains describing how parked node-runs hold the frame open).

Drop the Common pitfalls section's references to coalesce-as-debouncer and serial_queue-as-template-wide.

**Mutate `concepts/cascade.md` in place.** Replace the Boundaries section's "Does NOT own" listing to drop "frame scheduling" wording that referenced the coalesce/serial-queue duality; replace with: "Does NOT own: invalidate emission (see `concept:message`), frame creation (see `concept:frame`), terminal-handler resolution (see `concept:terminal-resolution`)."

Replace the Invariants section's third bullet ("The walk + per-node behaviors are scheduler actions; they are NOT configurable via the per-emit `frame: in | next` discipline") with:

> The cascade walker operates entirely within a single frame. It never creates a new frame; cross-frame coupling is expressed by message-emitter nodes whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path.

**Mutate `concepts/node-subscription.md` in place.** Replace the "What it is" section's references to the `frame:` modifier and sender-side filters; replace with:

> A node-subscription declares `type:` (a canonical signal type-path, exact or trailing-`*` prefix per `concept:signal`) plus an optional `when:` CEL predicate over the signal payload. Sender-side filters (`node:` selects a specific upstream node-type, `instance: true` is cross-cutting) apply. Subscriptions are declared per node under `subscribes:` in the template DSL. The auto-subscribe rule from substitution refs in a node's attribute schema applies uniformly: `{{nodes.X.attribute.Y}}` auto-subscribes to `X`'s `attribute/Y/changed`; `{{nodes.X.event.Y}}` auto-subscribes to `X`'s `event/Y` (unchanged from today); `{{messages.T.field}}` auto-subscribes to message-virtual-node `T`'s `terminal/success`.

Replace the Invariants section's third bullet ("The `frame:` modifier defaults to `in` for per-node subscriptions and `next` for cross-cutting") with:

> Subscriptions are unambiguously in-frame: a subscription's signal source must already exist in the receiver's current frame (a real node settling, or a message-virtual-node settling on the message that opened the frame). Cross-frame coupling is not expressed on `subscribes:`; it is expressed by message-emitter nodes whose dispatch lands a message in the ledger.

Drop the "Self-subscription is first-class in both `frame: in` and `frame: next` shapes" section's content; self-emission is now expressed by a message-emitter node subscribing to its own emit-source.

**Mutate `concepts/signal.md` in place.** Update the taxonomy from five top-level kinds to four:

- Remove the `message/<kind>/<sender_kind>/<target>` subsection entirely from the "Signal type-path taxonomy" section. The four surviving kinds are `terminal/*`, `transient/*`, `attribute/<key>/changed`, `event/<name>`.
- Update the section preamble ("Five top-level kinds") to read "Four top-level kinds."
- Add the following sentence after the four surviving kind subsections: "Message arrivals at an instance are emitted as `terminal/success` on the virtual node-type that corresponds to the message's `type` field — there is no dedicated `message/*` signal class; message subscriptions go through the standard `terminal/*` subscription path against the virtual message-type node."
- In the "Field-naming convention" table, remove the `message envelope payload | message_payload` row (the renamed-in-signal field for the message envelope's opaque sub-object) since there is no `message/*` signal carrying it.
- In the Invariants section, replace the `topic_kind` bullet ("each of the five canonical kinds (terminal, transient, attribute, event, message) maps to its own `topic_kind` value") with: "Each of the four canonical kinds (terminal, transient, attribute, event) maps to its own `topic_kind` value. Message subscriptions reuse the `terminal` topic_kind through the virtual-node-type mechanism. (`state` remains admitted as a defensive fallback for unrecognized rows.)"

**Mutate `concepts/publisher-subscription.md` in place.** Replace the Boundaries section's "Owns" listing to rename `message_kind` to `message_type`. Replace the Invariants section's `message_kind` bullet with:

> `message_type` must match a declared entry in the target instance's template message-schema registry at the publisher-subscription's mounting time; mismatches keep the row in `mounting` state and the failed-reason field carries the diagnostic.

**Mutate `concepts/cascade-graph.md` in place** — small mutation. Extend the "What it is" paragraph's enumeration of read-endpoint coverage to include forward and reverse joins by triggering message: given a `triggering_message_id`, list the frames it produced; given a frame, return its triggering message.

**Retire `concepts/invalidate.md`** — move to `concepts/_retired/invalidate.md` with a retirement note: "The 'sole graph-level message' framing dissolves into the typed-message machinery; every message arrival is structurally an invalidate by virtue of cascade subscribers to the message-virtual-node. The `frame: in | next` discipline retires; cross-frame coupling is expressed by message-emitter nodes. → `concept:message`, `concept:message-schema`, `concept:message-emitter-node`."

**Retire `concepts/backfill.md`** — move to `concepts/_retired/backfill.md` with a retirement note: "Backfill is a use case of the typed-message machinery: a template declares a message type whose body carries the partition-request override; a fan-out node's `partition_request:` substitutes from the message body. No dedicated primitive. → `concept:message`, `concept:message-schema`, `concept:fan-out`."

**Create `concepts/message-schema.md`** from the template in `ok-planner:discover-design`'s SKILL.md, with:

Definition:

> A message-schema is the template-level registry of accepted message types for instances of that template. Declared in a `messages:` block at template top level, parallel to the `attributes:` block on a node and the `publishers:` block at template level. Each entry pairs a message type-path with a body shape declared in JSON Schema. The registry is content-addressed into the template's spec at registration.

Purpose:

> Give messages a typed contract instead of opaque envelopes. An instance receiving a message of an undeclared type refuses the request with an unknown-type response, replacing today's silent dead-letter. The body shape is what receivers substitute from and what message-emitter nodes match their attribute schemas against; both surfaces share the same engine.

Boundaries:

> Owns: the registry's persisted shape (content-addressed into the template spec), the per-entry fields (`type:`, `body_schema:`), the registration-time validation pass that checks substitution references against declared types and validates message-emitter nodes' attribute schemas against the destination type's body schema, the receipt-time registry lookup gate. Does NOT own: the message envelope (see `concept:message`), the message-emitter node-kind (see `concept:message-emitter-node`), receiver-side subscription (see `concept:node-subscription`), substitution into bodies (see `concept:attribute`).

Invariants:

> - The registry is template-level; `messages:` entries are content-addressed into the template's spec at registration.
> - `type:` is unique across entries in the registry.
> - `body_schema:` is a valid JSON Schema.
> - Receipt-time lookup against the registry is the gate: unknown type refuses with an unknown-type response.
> - The body-schema is documentation and a registration-time check on substitution references; the actual body bytes are validated at the receiver's dispatch via the existing attribute-validation machinery. The body remains inert at receipt (see `@blessed-invariant: 21`).

**Create `concepts/message-emitter-node.md`** from the template, with:

Definition:

> A message-emitter-node is a node-type whose dispatch mode is "build a message envelope from the node's attributes and insert it into the message ledger." Declared on a node-type by `emits_message: <type>` instead of `executor: <name>` or `delegate: <graph-name>`. The node carries `subscribes:` and `attributes:` blocks like any other node; what makes it an emit-node is its dispatch field.

Purpose:

> Make cascade-driven message emission a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute substitution machinery: the emit-node subscribes to multiple upstreams; its attributes pull from each; the resolved attribute set is the message body. No new validation, substitution, or routing primitive is introduced; every check reuses the existing attribute and subscription validators.

Boundaries:

> Owns: the `emits_message:` node-type field, the exact-shape-match validation of attribute schema against the destination message type's body schema, the dispatch behavior (substitution resolves attributes, the runtime constructs the envelope with `sender_kind: "instance"` and a runtime-generated `Idempotency-Key`, the envelope inserts into the message ledger inside the node's terminal-resolution tx), the registration-time check that the named message type exists in the template's `messages:` registry. Does NOT own: the message envelope shape (see `concept:message`), the message-schema registry (see `concept:message-schema`), substitution into the attributes themselves (see `concept:attribute`), subscription declaration (see `concept:node-subscription`). Adjacent: `concept:node`, `concept:message`, `concept:message-schema`, `concept:attribute`, `concept:node-subscription`.

Invariants:

> - The node's attribute schema matches the destination message type's `body_schema` exactly: same field set, same types. Supersets are rejected at registration; the emit-node exists to produce the message, hidden state is rejected.
> - At dispatch, the runtime constructs the envelope with `sender_kind: "instance"`, sender `instance:<id>`, payload = resolved attribute set serialized, `Idempotency-Key` deterministic on the dispatching node-run's `node_run_id`. The envelope inserts in the same tx as the node's terminal-resolution; if the tx rolls back, the emit does not occur.
> - Cross-frame coupling is expressed solely through these nodes; no other path opens a frame.

### Story changes

The seven new story files match the structured-heading format of existing files in `design/stories/` (Role / Capability / Business value / Acceptance / Falsifier / Proof as named subsections). Each body is path-free.

**Create `stories/message-schema.md`** with:
- Role: As a template author,
- Capability: I can declare which message types instances of this template accept,
- Business value: so that messages have a typed contract and unknown ones fail loud instead of silently dead-lettering.
- Acceptance: I write a template-level `messages:` block enumerating accepted types, each with a body shape. When a sender posts a message of a declared type, the instance opens a frame and the receivers I have declared via subscriptions stale-mark, substituting the body into their attribute schemas. When a sender posts a message of an undeclared type, the request is refused with an error naming the unknown type and listing the declared set.
- Falsifier: A message of an undeclared type lands in the ledger and is silently dropped; OR a declared message arrives and no subscribed node is marked stale.
- Proof: Executable proof. Declared type opens a frame and stale-marks subscribed receivers; undeclared type refuses with the expected error.

**Create `stories/cascade-emit.md`** with:
- Role: As a template author,
- Capability: I can declare a node-type whose dispatch is to emit a message of a given type,
- Business value: so that cross-frame coupling is explicit as a graph object I can point at.
- Acceptance: I write a node-type carrying `emits_message: <type>` (rather than `executor:` or `delegate:`). The node has `subscribes:` and `attributes:` blocks like any other node; its attribute schema matches the destination message type's body schema exactly. When its `subscribes:` entries fire, the node dispatches: substitution resolves its attributes from upstreams and instance params, the runtime constructs a message envelope with the resolved attribute set as the body, and inserts it into the message ledger. The next frame opens carrying that message.
- Falsifier: A subscribed condition triggers and the emit-node's dispatch produces no message in the ledger; OR the emit-node's attribute schema can declare fields the destination message type's body schema doesn't, without registration-time error; OR the body fails to reflect the resolved attribute values.
- Proof: Executable proof. Emit-node dispatches when its subscriptions fire; resulting message body contains the expected substituted values; mismatched schemas reject at registration.

**Create `stories/cross-frame-coupling.md`** with:
- Role: As a template author,
- Capability: I can express cross-frame coupling (back-edges in cycles, self-drain-my-queue) through emit-nodes plus the message schema, with the receiver reading the sender's data via the message body,
- Business value: so that patterns that previously failed silently now work cleanly.
- Acceptance: I write a 2-cycle A → B → A where B's settlement triggers a message-emitter node whose dispatch emits a message that A subscribes to. When B settles, the emit-node runs, the message lands in the ledger, the next frame opens with A stale-marked, and A reads B's data through the typed-message substitution grammar in its attribute schema. Separately, I write a self-emit (a message-emitter node that subscribes to its own emit-source with a change-gate) and the loop drains until convergence.
- Falsifier: A multi-node back-edge cycle silently drops the dispatch — the message envelope appears in the ledger but no frame opens for the receiver; OR the receiver re-runs but cannot read the sender's data; OR the self-drain loops infinitely without converging.
- Proof: All-of-the-above. Executable proofs for the back-edge cycle and the self-drain convergence, plus a demo walking through the scenario succeeding.

**Create `stories/one-message-per-frame.md`** with:
- Role: As a template author,
- Capability: I can rely on substitution from the message body always being well-defined in a node that's reacting to a message,
- Business value: so that no template ever has to refuse a multi-message coalesced frame at runtime.
- Acceptance: Across all instances and templates, a frame carries at most one delivered message. A node whose attribute schema substitutes from a typed message body always has exactly one body to read; the substitution never refuses or returns an ambiguous value. Two messages posted in close succession produce two frames (one each).
- Falsifier: Two messages share a frame; OR a template that substitutes from a message body fails at substitution time with a "multiple messages" error.
- Proof: Executable proof. N messages posted in succession produce N distinct frames, each carrying one message, each settling cleanly with body substitution resolving the expected values.

**Create `stories/frame-origin-audit.md`** with:
- Role: As an operator,
- Capability: I can see for every frame what triggered it (an operator message, a publisher message, or a cascade-emitted message) through the existing frame observability surface,
- Business value: so that "why did this frame open" is always answerable directly.
- Acceptance: Every frame carries a pointer back to the message ledger entry that triggered it, surfaced through the existing frames-read observability endpoint. No frame in the system has "cascade walker" or "internal" as its origin. Looking up a frame returns the originating message envelope (sender, type, body).
- Falsifier: A frame appears without an originating message reference; OR an internal-to-runtime path creates a frame in any code path.
- Proof: Demo. Every frame in a representative end-to-end run (including back-edge cycles and self-drain) has an originating message visible through the observability surface.

**Create `stories/typed-message-substitution.md`** with:
- Role: As a template author,
- Capability: I read from and compose message bodies using the same substitution grammar that handles node attributes, with each message type addressable by its declared name,
- Business value: so that I can disambiguate when a node could react to several types, and message bodies are first-class typed attribute blocks that flow across frames.
- Acceptance: A receiver node's attribute schema substitutes from a specific message type by naming the type in the substitution grammar, parallel to substituting from a specific node's attribute by naming the node-type. A message-emitter node's attributes compose the destination message body by the same substitution grammar (sources can be other nodes' attributes, instance params, the triggering signal's payload). The substitution engine validates references at template registration in both directions: a receiver reading a field the declared body schema doesn't have rejects; an emitter declaring an attribute field the destination type's body schema doesn't have rejects. The runtime resolves message-body reads through the same code path that resolves attribute reads.
- Falsifier: The grammar for substituting from messages differs in shape from the grammar for substituting from node attributes; OR the engine has a separate code path for message-body reads vs attribute reads; OR a typo in a message body field on either side registers without error; OR a receiver attribute schema can read from a message type without naming that type.
- Proof: Executable proof. Typo'd field names reject at registration in both directions; a running back-edge cycle's receiver reads through the typed-message grammar and resolves correctly; an assertion confirms a single substitution-resolution function services both surfaces.

**Create `stories/debug-channel.md`** with:
- Role: As an operator,
- Capability: I can override-invalidate a specific node or override-set an attribute value via the control-api when the target instance is paused or at a breakpoint pause-mode hit,
- Business value: so that ad-hoc inspection and mutation are available exactly when I have explicitly entered debug mode, and unavailable otherwise.
- Acceptance: With an instance whose instance-level pause flag is true OR with an unresumed pause-mode breakpoint hit blocking a runner, I can post a debug override that stale-marks a specific node and/or sets a specific attribute value in the running frame; the override applies in that frame. When the instance is neither paused nor breakpoint-stopped, the same request is refused with an error citing the required state.
- Falsifier: A debug override is accepted on an instance that is neither paused nor breakpoint-stopped; OR the override is refused on a paused-or-breakpointed instance.
- Proof: Executable proof. Override accepted on both legal states (paused, breakpoint); refused on a healthy running instance with the expected error.

**Mutate `stories/message-bus.md` in place.** Replace Acceptance with:

> A sender (operator or publisher, through the control-api message-emit endpoint or its MCP equivalent) emits a message carrying a dedup key and a `type` selecting an entry from the target instance's template message-schema registry; the message is persisted and visible in the instance's message history. A second emission with the same key returns the original message identifier and produces no second envelope. A request with no dedup key is refused. A request whose `type` is not declared in the target instance's message-schema registry is refused with an unknown-type response. Senders with structurally distinct identities (operator vs. publisher; one operator key vs. another) do not replay each other when they happen to choose the same dedup key.

Replace Falsifier with:

> A second emission with the same key produces a second envelope, OR the no-key request is silently accepted, OR an undeclared `type` request lands in the ledger, OR a publisher named the same as an operator-sender replays the operator's emit.

**Retire `stories/backfill-ops.md`** — create the directory `.ok-planner/design/stories/_retired/` if it does not exist, then move the file to `stories/_retired/backfill-ops.md`. No body mutation; retirement is by location.

### Decision changes

The seven new decision files match the format of existing files in `design/decisions/` (Choice / Rationale / Alternatives as named subsections). Each body is path-free.

**Create `decisions/emit-as-node-kind.md`** with:
- Choice: Cascade-driven message emission lives on a dedicated node-kind, declared on a node-type by `emits_message: <type>` instead of `executor:`. A per-node-type `emits:` block is not introduced.
- Rationale: The emit becomes a first-class graph object — visible in topology, audit, and the operator dashboard. Aggregation across multiple senders works through the standard subscription and attribute machinery (the emit-node subscribes to multiple senders; its attributes pull from each). No new validation or substitution machinery; every check reuses the existing attribute and subscription validators.
- Alternatives considered: A per-node-type `emits:` block in which any node could embed an emission directive. Rejected: aggregation is unnatural (the only node where a multi-source body composes is one that already had all sources as upstreams, coincidental rather than designed); the emit is hidden inside the sender's settle behavior rather than visible as a graph object.

**Create `decisions/attribute-set-as-body.md`** with:
- Choice: A message-emitter node's `attributes:` block is the message body. The attribute schema must match the destination message type's body schema exactly — same field names, same types. No mapping layer between attributes and body fields.
- Rationale: One source of truth for attribute field names. A mapping layer would mean two separately-named things kept in sync by a third layer; that is the redundancy the design avoids. The "message body is an attribute block that carries across frames" framing is literal: the emit-node's attribute resolution at dispatch is the body construction.
- Alternatives considered: A mapping sub-block under the emit-node's dispatch field, mapping attribute names to body field names (or substituting from arbitrary sources into body fields). Rejected as redundant; users who want to decouple shapes can author a separate triggering node feeding a separate emitter node.

**Create `decisions/single-frame-creation-path.md`** with:
- Choice: A frame opens only when a message lands in the ledger and the next frame boundary picks it up. The cascade walker has no path that creates a frame; the in-walker frame-creation branch and the helper it called are removed entirely.
- Rationale: Every frame's origin becomes auditable via a single mechanism (a triggering-message-id column on the frame row). "Why did this frame open" is always answerable from the observability surface. Cross-frame coupling becomes explicit at the sender (a message-emitter node), not hidden under cascade-walker behavior.
- Alternatives considered: Preserve a per-subscription "next frame" modifier as a second frame-creation path. Rejected: the multi-node back-edge silent-failure footgun stems precisely from this dual path — a downstream sender's settle does not re-dispatch the upstream receiver in a multi-node cycle, and the documented affordance is buried in the cascade-walker's discipline; keeping the dual path perpetuates the failure mode.

**Create `decisions/debug-channel-gate-paused-or-breakpoint.md`** with:
- Choice: The control-API debug-override endpoint is legal iff the instance is paused (the existing instance-level pause flag is true) OR the instance holds at least one unresumed pause-mode breakpoint hit blocking a runner. Otherwise the request returns a conflict response.
- Rationale: The override is a debug feature, not a general operator capability. Both legal states are operator-engineered — the operator deliberately paused or set a breakpoint — so the override is contextually expected. Healthy frames have no override path; the operator must opt in to debug mode first.
- Alternatives considered: Include "frame held by parked node-run" and "last-progress past frame-timeout" in the gate. Rejected: those are degraded states (normal-operation park, or a bug surfacing as the frame-stuck warning); the project's pre-v1 rule is to investigate and fix the underlying issue rather than engineer around it with an override.

**Create `decisions/envelope-type-discriminator.md`** with:
- Choice: The message envelope's `kind` field renames to `type` and carries the message type-path (matching the receiver's subscription target and the emitter's `emits_message:` value). The publisher-subscription's `message_kind` field similarly renames to `message_type`.
- Rationale: The envelope's `kind` carried one value providing no information once messages are typed. The rename aligns the wire vocabulary with the conceptual vocabulary; one field, one purpose.

**Create `decisions/one-message-per-frame.md`** with:
- Choice: Every frame carries at most one delivered message. The message-delivery pass at each frame boundary delivers the oldest single pending message; the rest stay pending until the next boundary. N pending messages produce N sequential frames.
- Rationale: Substitution into the message body is always well-defined — a typed message-body read resolves against exactly one body. The "no silent override loss" property the prior coalesce mechanism maintained by hand is satisfied here by construction: two messages binding the same receiver to different values cannot land in the same frame because they cannot share a frame.
- Alternatives considered: Keep `coalesce` as a non-default opt-in mode for instances that explicitly want it. Rejected: coalesce-as-frame-mode does multiple jobs (queue-merging plus message-bundling plus cascade-walker re-fire); splitting them to keep only the queue-merging job leaves one narrow use case better served by message authoring decisions.

**Create `decisions/pre-v1-pure-removal-for-retired-surfaces.md`** with:
- Choice: Retired DSL surfaces are removed from the code entirely. Templates and requests using them fail through normal validator paths (unknown field, unknown signal type, unknown message type). No detection rule, no migration error string, no parser case that names the old shape.
- Rationale: No remnants of retired features in the code. Pre-v1, the project's bias is clean removal; backwards-compatibility shims and migration helpers are not warranted. The normal validator's "unknown field" error is the rejection.

### Tension changes

**Resolve `tensions/serial-queue-per-instance.md`** — move to `tensions/_resolved/serial-queue-per-instance.md` with `status: resolved` and a resolution block:

> The rename-the-mode resolution candidate is moot: there is no longer a mode name to rename, since the alternative frame-resolution mode (`coalesce`) retires. The substantive concern — per-instance ordering versus template-wide expectations — survives as a documented property of `concept:frame`: ordering is per-instance, and consumers needing template-wide ordering must coordinate above rimsky.

**Resolve `tensions/coalesced-fire-observability-gap.md`** — move to `tensions/_resolved/coalesced-fire-observability-gap.md` with `status: resolved` and a resolution block:

> Moot: `coalesce` retires entirely as a frame mode. There is no coalesced-fire surface left to observe.

**Resolve `tensions/frame-lookup-on-every-enqueue.md`** — move to `tensions/_resolved/frame-lookup-on-every-enqueue.md` with `status: resolved` and a resolution block:

> Moot: the `frame_resolution_mode` template field retires. There is no template lookup on enqueue.

## Manifest

### Stories

- **STORY-message-schema** — template author declares accepted message types; unknown types refused at receipt (Proof: executable proof)
- **STORY-cascade-emit** — message-emitter-node dispatches a message when subscribed signals fire (Proof: executable proof)
- **STORY-cross-frame-coupling** — back-edge cycles and self-drain express through emit-nodes plus message schema (Proof: all-of-the-above)
- **STORY-one-message-per-frame** — substitution from a message body is always well-defined (Proof: executable proof)
- **STORY-frame-origin-audit** — every frame has a pointer to the triggering message, surfaced through the frames-read observability endpoint (Proof: demo)
- **STORY-typed-message-substitution** — message body reads and emit-node attribute composition share the substitution engine; types are addressable by name (Proof: executable proof)
- **STORY-debug-channel** — operator override is gated to paused-or-breakpointed instances (Proof: executable proof)

### Technical decisions

- **TD-emit-as-node-kind** — `emits_message:` dispatch mode on a node-type
- **TD-attribute-set-as-body** — emit-node attributes are the body, exact shape match
- **TD-single-frame-creation-path** — frames open only at message-delivery boundary
- **TD-debug-channel-gate-paused-or-breakpoint** — override legal only when paused or breakpoint-stopped
- **TD-envelope-type-discriminator** — envelope `kind` → `type`; publisher-subscription `message_kind` → `message_type`
- **TD-one-message-per-frame** — delivery yields one message per frame, always
- **TD-pre-v1-pure-removal** — retired surfaces leave no code traces

### Design changes (durable artifacts touched)

**Concepts:**
- Mutate: `message`, `frame`, `cascade`, `node-subscription`, `signal`, `publisher-subscription`, `cascade-graph` (small)
- Retire: `invalidate`, `backfill`
- Create: `message-schema`, `message-emitter-node`

**Stories:**
- Create: `message-schema`, `cascade-emit`, `cross-frame-coupling`, `one-message-per-frame`, `frame-origin-audit`, `typed-message-substitution`, `debug-channel`
- Mutate: `message-bus`
- Retire: `backfill-ops`

**Decisions:**
- Create: `emit-as-node-kind`, `attribute-set-as-body`, `single-frame-creation-path`, `debug-channel-gate-paused-or-breakpoint`, `envelope-type-discriminator`, `one-message-per-frame`, `pre-v1-pure-removal-for-retired-surfaces`

**Tensions:**
- Resolve: `serial-queue-per-instance`, `coalesced-fire-observability-gap`, `frame-lookup-on-every-enqueue`
