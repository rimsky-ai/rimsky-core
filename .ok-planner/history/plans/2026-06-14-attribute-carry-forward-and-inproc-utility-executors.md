# Attribute carry-forward + in-process utility executors — Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-14-attribute-carry-forward-and-inproc-utility-executors-design.md`

**Goal:** Land the smallest platform foundation that lets a multi-loop coding orchestrator be expressed as a rimsky template — scope-bounded attribute carry-forward, an in-process executor transport, opaque per-dispatch executor scratch, a `loop_counter` utility node, a `kind:` template sugar field, and a claude-agent change moving the CLI session token to an attribute.

**Architecture:** Five durable mechanisms ship together so the orchestrator pattern composes. (1) `rimsky_node_attributes` lookups are extended with a pre-substitution carry-forward step that hydrates the dispatch attribute bag from the most-recent prior run of the same (node, run_scope) before substitution overlays. (2) `rimsky_node_runs` gains a new scratch triple (`scratch_inline`, `scratch_handle`, `scratch_handle_backend`) mirroring the existing `parked_payload_*` triple, plus an executor-protocol scratch surface (ExecuteRequest field, stream-close outcome field, and a new `POST /v1/runs/{run_id}/scratch` HTTP callback paralleling the existing §12.5 attributes incremental writeback). The four recovery enqueue paths copy scratch from the prior_dispatch_id row at enqueue time so it survives stale-heartbeat / retry-after-error / recalculate transitions. (3) `lib/runtime/executor/client.go::ClientPool` learns a third `"inproc"` transport that dispatches to an explicitly-registered `InProcessHandler` over a channel-backed `EventStream`; built-in handlers live under `lib/runtime/executor/builtin/<name>/`. (4) `TemplateNodeDef` gains an optional `Kind string` field whose template-registration resolver maps `kind: <name>` to a pre-registered inproc `ExecutorEntry`; mixing `kind:` and `executor:` is a registration error. (5) The `loop_counter` built-in (the only utility handler in this plan's scope) ships with input `max`, executor-written carry-forward `count`, and emits named events `loop` / `done`. Plus one consumer change: claude-agent's `expectedAttributesSchema` gains `session_token: { readOnly: true }`, the executor reads it from incoming attributes to drive `--resume`, and writes the current dispatch's `runId` to it via `attributes_set` on terminal Success so carry-forward makes it visible on next dispatch in scope.

**Tech Stack:** Go (`lib/runtime`, `lib/foundation/persistence`, `lib/foundation/spec`, `lib/graph`), protobuf v1 (`lib/protocols/proto/v1`), Postgres + SQLite migrations under `lib/foundation/persistence/{postgres,sqlite}/migrations/`, TypeScript (`lib/services/executors/claude-agent`), testcontainers-go for storage / scenario harness, the bundled-services integration harness under `lib/services/test/`.

---

## Pass 1: Design-doc mutations + new story / decision artifacts

**Goal:** Land every design-change bullet from the spec's `## Design changes` section so subsequent code passes reference a stable, mutated conceptual model. No code in this pass.

**Scope:** Tasks 1–6.

**Falsifier:** `.ok-planner/design/concepts/attribute.md` contains no Self-state carry-forward section; OR `.ok-planner/design/concepts/executor.md` still frames executors exclusively as out-of-process; OR `.ok-planner/design/concepts/node-run.md` does not enumerate the scratch triple plus `prior_dispatch_id`; OR `.ok-planner/design/concepts/node.md` has no Kind sugar section; OR `.ok-planner/design/concepts/inertness.md` does not list scratch as a carrier; OR any of the 5 expected story files or 11 expected decision files is missing.

### Task 1: Mutate `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`

**Steps:**

1. Add the following new top-level section verbatim (between `## Invariants` and `## Static-default properties`):

   ```
   ## Self-state carry-forward

   On each dispatch of node X in RunScope S, the attribute bag is hydrated from the most-recent prior node-run of X in S (a JOIN of the per-run attribute ledger with the node-run rows, ordered by recency for (node, scope)), then per-field `source:` substitution overlays on top. First dispatch of X in S uses the schema's static-default values. Executor-written `readOnly: true` properties carry forward unchanged unless overwritten by a subsequent executor writeback. Cross-RunScope hydration is forbidden — sub-graph and fan-out RunScopes start with schema defaults (per `concept:run-scope`). Self-state carries via copy; cross-node values flow via substitution; the two channels are orthogonal and operate on different sources. The canonical stateful-property pattern is `readOnly: true` plus executor writeback; carry-forward is its expected behavior.
   ```

2. In the existing `## Non-goals` bullet that begins "**Cross-frame attribute caching.**", keep the substitution-grammar rule (everything up to and including the sentence "The per-run attribute rows are the persistent record of what each node-run produced — not a cache.") and replace the final sentence — "State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs." — with:

   ```
   Cross-node state across frames belongs in `params`, claim payloads, or threaded subgraph inputs. A node's own state across frames within a RunScope is the self-state carry-forward mechanism.
   ```

3. Replace the existing invariant bullet that begins "Attribute storage is per-run, keyed by the node-run identity" with the following text, preserving its position in the `## Invariants` list:

   ```
   - Attribute storage is per-run, keyed by the node-run identity (a cascade-deleting foreign key to the node-run row). A denormalized node-id column supports both forensic / observability lookups and the self-state carry-forward hydration step (latest prior run for this node in this RunScope). The dispatch-time substitution path looks up by run against the wait-set sender runs that contributed to this dispatch in this frame; the carry-forward hydration step looks up by (node-id, run-scope-id) for the same node's own prior writeback.
   ```

4. Replace the existing invariant bullet that begins "Substitution reads are scoped to the current frame." with the following text, preserving its position in the `## Invariants` list:

   ```
   - Substitution reads are scoped to the current frame. A `{{nodes.X.attribute.Y}}` directive resolves to the X-run that contributed to this dispatch via the frame's wait-set; reads of X-runs from earlier frames return a missing-source error. The per-run attribute rows are the persistent record of what each node-run produced; the substitution path treats them as wait-set-gated per-frame reads. Self-state carry-forward is a separate hydration step that uses the same rows as the source for a node's own prior writeback. Cross-node state across frames belongs in `params`, claim payloads, or threaded subgraph inputs; a node's own state across frames within a RunScope rides carry-forward.
   ```

5. Verify the document remains self-contained per the `.ok-planner/CLAUDE.md` "Self-containment rule" (no file paths, no `code:` / `pkg:` / external-doc references in the artifact body). Specifically: the new section and the rewritten invariants reference only concept slugs (`concept:run-scope`) and other in-concept terminology.

### Task 2: Mutate `concepts/executor.md`

**Files:** `.ok-planner/design/concepts/executor.md`

**Steps:**

1. Replace the entire `## What it is` section body with:

   ```
   An executor implements the gRPC executor's server-streaming execute method plus an optional executor-observability protocol. Implementations come in two forms — in-process handlers registered with the dispatch pool, and out-of-process services (gRPC or HTTP-bridge) — and the protocol surface (execute, the four stream-close outcome variants, the observability handshake) is identical across both. The executor receives one execute request, streams zero-or-more heartbeat / named-event messages, and exactly one stream-close event carrying one of four outcome variants (success, error, park, await-async-callback). The park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (per `concept:parked-state`). Production-side reference implementations (an HTTP-node executor, an LLM-agent executor, and two verifier executors) live on the consumption side, outside the platform. The stub test-double executor and the bundled in-process loop-counter handler are the in-rimsky implementations.
   ```

2. Replace the entire `## Purpose` section body with:

   ```
   Executors are where actual work happens. Out-of-process gRPC executors give language-portability, scale-independence, and async-callback handoff for long-running work. In-process executors deliver utility-node primitives (counters, gates, simple computations) without the deploy / image / IPC overhead, sharing the same protocol surface so the dispatch path treats both forms uniformly.
   ```

3. In `## Boundaries`, append to the existing `Owns:` enumeration (after "the userdata interpretation"): `; per-dispatch executor-attached opaque scratch bytes (the executor sets scratch mid-dispatch via the scratch callback route or at stream-close by attaching scratch bytes to the outcome)`.

4. Insert a new top-level section between `## Purpose` and `## Boundaries` titled `## Scratch`:

   ```
   ## Scratch

   Every executor receives a scratch field on its execute request carrying the dispatch row's currently persisted scratch bytes (empty on first dispatch). The executor may write scratch in two ways — mid-dispatch by POSTing to a scratch HTTP callback route (paralleling the executor protocol's existing attributes incremental-writeback HTTP callback), or at stream-close by attaching scratch bytes to the outcome. Both writes persist on the dispatch row. The bytes are opaque to rimsky — the inertness invariant (`concept:inertness` / `@blessed-invariant 21`) extends to scratch — and scratch carries forward to subsequent dispatches of the same node via the recovery enqueue path that creates the new dispatch row (per `concept:node-run`).
   ```

5. Verify self-containment: the body cites only concept slugs and invariant IDs, no file paths.

### Task 3: Mutate `concepts/node-run.md`

**Files:** `.ok-planner/design/concepts/node-run.md`

**Steps:**

1. In `## What it is`, after the existing first paragraph (ending "wake reason)."), insert this new paragraph as a continuation describing the row fields:

   ```
   The row also carries a `prior_dispatch_id` nullable reference to a preceding dispatch row, set whenever a new dispatch is enqueued to follow a prior one (under any of the prior-dispatch dispositions — heartbeat-stale, retry-after-error, recalculate). Optional scratch fields — `scratch_inline`, `scratch_handle`, `scratch_handle_backend` — carry executor-attached opaque bytes per dispatch, with spill following `concept:blob-backend`. The executor sets scratch either at stream-close (by attaching scratch bytes to the outcome) or mid-dispatch (by POSTing to the scratch HTTP callback route, paralleling the executor protocol's existing attributes incremental-writeback HTTP callback); both writes persist on the dispatch row that received them. When a subsequent dispatch row is created for the same node and the new row carries a non-null `prior_dispatch_id`, the enqueue path copies scratch from the prior dispatch row onto the new row at row creation, and the executor reads it from its own row on next dispatch.
   ```

2. In `## Boundaries`'s `Owns:` enumeration, append: `; executor-attached opaque scratch bytes per dispatch; the prior-dispatch linkage across re-dispatches of the same node`.

3. Verify self-containment.

### Task 4: Mutate `concepts/node.md`

**Files:** `.ok-planner/design/concepts/node.md`

**Steps:**

1. After the existing `## Invariants` section, append a new top-level section:

   ```
   ## Kind sugar

   A template node may declare `kind: <name>` as a shorthand for an `executor:` reference. The required `type:` field (the template-author-chosen dispatch routing key) is unchanged and unrelated. At registration, a static kind-alias map resolves `kind:` to a pre-registered executor entry. A node may declare `kind:` or `executor:` but not both; mixing is rejected at registration. Unknown `kind:` values are rejected the same way unknown executors are. The sugar exists so utility nodes (counters, gates, and similar in-process executors) can be referenced without spelling out their executor identity.
   ```

2. Verify self-containment.

### Task 5: Mutate `concepts/inertness.md`

**Files:** `.ok-planner/design/concepts/inertness.md`

**Steps:**

1. Replace the entire body of the `## What it is` section's bolded "Carrier streams the discipline governs" line with the following text (the count `seven` and the trailing "Plus executor error payloads" clause collapse into one enumeration so the count is no longer baked into prose):

   ```
   **Carrier streams the discipline governs:** claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, attribute values, named-event payloads, message payloads, scratch (per `concept:executor`), executor error payloads. Each stream is "inert" in rimsky — rimsky neither inspects nor interprets the bytes beyond a narrowly defined set of read sites.
   ```

2. In the same `## What it is` section's `Read-site sub-disciplines` block, replace the existing **Byte-opaque inertness** bullet's "Applies to:" enumeration with:

   ```
   - **Byte-opaque inertness** — rimsky never traverses the bytes at all. Applies to: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, scratch. Rimsky reads them only at substitution-leaf extraction or for transport into the executor's wire (per `@blessed-invariant 20` and `21`).
   ```

3. In the `## Invariants` section's "Sanctioned read sites" enumeration, after the existing **Attribute matcher evaluation** entry, add a new bullet:

   ```
   - **Scratch wire-attach + row-persist + lineage-copy** — on dispatch, rimsky reads the dispatch row's scratch bytes onto the executor's execute request; on stream-close, rimsky persists the executor-attached scratch bytes onto the dispatch row; on the mid-dispatch scratch callback route, rimsky persists the posted scratch bytes onto the dispatch row; on next-dispatch enqueue for the same node under any prior-dispatch disposition, the enqueue path copies scratch from the prior dispatch row onto the new dispatch row.
   ```

4. Verify self-containment.

### Task 6: Create 5 new stories + 11 new decisions

**Files:** All new:
- `.ok-planner/design/stories/attribute-carry-forward.md`
- `.ok-planner/design/stories/loop-counter-cap.md`
- `.ok-planner/design/stories/claude-agent-session-resume.md`
- `.ok-planner/design/stories/inproc-utility-executor.md`
- `.ok-planner/design/stories/opaque-executor-scratch.md`
- `.ok-planner/design/decisions/attribute-carry-forward.md`
- `.ok-planner/design/decisions/scratch-column.md`
- `.ok-planner/design/decisions/scratch-protocol.md`
- `.ok-planner/design/decisions/scratch-recovery.md`
- `.ok-planner/design/decisions/inproc-transport-client.md`
- `.ok-planner/design/decisions/inproc-handler-interface.md`
- `.ok-planner/design/decisions/inproc-registry.md`
- `.ok-planner/design/decisions/inproc-eventstream.md`
- `.ok-planner/design/decisions/kind-sugar-resolver.md`
- `.ok-planner/design/decisions/loop-counter-shape.md`
- `.ok-planner/design/decisions/claude-agent-session-attribute.md`

**Steps:**

1. For each story file, copy the **content** of the matching `STORY-<slug>` block from the spec's `## User outcomes` section into the new file, then strip every `code:` / `proto:` / `file:` / `pkg:` / `cfg:` token from the body and paraphrase inline (cite by concept slug or describe the behavior in prose). The artifact body must be **path-free** per the design-docs self-containment rule — see `.ok-planner/CLAUDE.md`'s "Self-containment rule". Use this template (the frontmatter `aliases:` list is empty):

   ```markdown
   ---
   story: <slug>
   status: as-is
   aliases: []
   ---

   # <Story title>

   ## Role and capability

   <As ... I can ... so that ... — from the spec story>

   ## Acceptance

   <Acceptance line from the spec story>

   ## Falsifier

   <Falsifier line from the spec story>

   ## Proof

   <Proof line from the spec story>
   ```

   The slug names map to the story files: `attribute-carry-forward.md` ↔ STORY-attribute-carry-forward, `loop-counter-cap.md` ↔ STORY-loop-counter-cap, `claude-agent-session-resume.md` ↔ STORY-claude-agent-session-resume, `inproc-utility-executor.md` ↔ STORY-inproc-utility-executor, `opaque-executor-scratch.md` ↔ STORY-opaque-executor-scratch. Per the design-docs current-state-only rule, the body describes the project as it stands once these mechanisms land; do not write any "previously was" / "TODO" / dated audit-trail language.

2. For each decision file, copy the **content** of the matching `TD-<slug>` block from the spec's `## Technical decisions` section, then strip every `code:` / `proto:` / `file:` / `pkg:` / `cfg:` token from the body and paraphrase inline. The TD-inproc-transport-client choice cites `code:lib/runtime/executor/client.go::ClientPool::GetOrCreate` and `code:lib/runtime/runner_dispatch.go#221`; the TD-kind-sugar-resolver choice cites `code:lib/foundation/spec/template.go::TemplateNodeDef`; the TD-claude-agent-session-attribute choice cites `code:lib/services/executors/claude-agent/src/cli-runner.ts#260` and `code:lib/services/executors/claude-agent/src/agent-run.ts#887` — every one of these tokens must be paraphrased away in the durable decision body (the spec keeps them for traceability; the decision artifact is path-free). Use this template:

   ```markdown
   ---
   decision: <slug>
   status: as-is
   aliases: []
   ---

   # <Decision title>

   ## Choice

   <Choice text from the TD>

   ## Rationale

   <Rationale text from the TD>

   ## Alternatives

   <Alternatives text from the TD; omit this section if the TD has none>
   ```

   The slug map: `attribute-carry-forward.md` ↔ TD-attribute-carry-forward, `scratch-column.md` ↔ TD-scratch-column, `scratch-protocol.md` ↔ TD-scratch-protocol, `scratch-recovery.md` ↔ TD-scratch-recovery, `inproc-transport-client.md` ↔ TD-inproc-transport-client, `inproc-handler-interface.md` ↔ TD-inproc-handler-interface, `inproc-registry.md` ↔ TD-inproc-registry, `inproc-eventstream.md` ↔ TD-inproc-eventstream, `kind-sugar-resolver.md` ↔ TD-kind-sugar-resolver, `loop-counter-shape.md` ↔ TD-loop-counter-shape, `claude-agent-session-attribute.md` ↔ TD-claude-agent-session-attribute. Substitute any spec-internal file path or `code:` / `proto:` / `cfg:` citation in the spec body with path-free wording per the self-containment rule (e.g. describe the field in prose rather than naming `code:lib/runtime/executor/client.go::ClientPool`).

3. Verify each file is self-contained per the `.ok-planner/CLAUDE.md` rule (no file paths, no `@source` / `@constraint` annotations in the body, frontmatter is the slug-form metadata schema only).

---

## Pass 2: Persistence — scratch triple, DispatchRequest carry, recovery enqueue copy

**Goal:** Add `scratch_inline` / `scratch_handle` / `scratch_handle_backend` to `rimsky_node_runs` on both Postgres and SQLite, surface them on `DispatchRequest` + `ResumeMetadataRow` + `Candidate`, and modify the four recovery enqueue paths so a new dispatch row carrying `prior_dispatch_id` copies scratch from the prior row at INSERT time. No proto / wire / inproc / kind code in this pass.

**Scope:** Tasks 7–13.

**Falsifier:** Migration 010 does not exist, OR the postgres `rimsky_node_runs` table has no `scratch_inline` column, OR EnqueueInTx does not write scratch onto the new row when `PriorDispatchID` is set, OR the four recovery sites (`SweepStaleHeartbeats`, `cascade_recalculate.go`, `on_error.go`, `applyResolvedAction`) do not flow scratch from the prior row to the new dispatch — verified by querying `scratch_inline` on the post-recovery `rimsky_node_runs` row in a unit test.

### Task 7: Add Postgres migration 010 for the scratch triple

**Files:** `lib/foundation/persistence/postgres/migrations/010-node-run-scratch.sql` (new)

**Steps:**

1. Create the file with the following body (mirroring the existing `parked_payload_*` triple under `001-schema.sql:211-213`):

   ```sql
   -- Migration 010 — Add per-dispatch executor scratch triple to rimsky_node_runs.
   -- Mirrors the parked_payload_inline/handle/handle_backend pattern so spill
   -- (via concept:blob-backend) reuses the same plumbing.

   ALTER TABLE rimsky_node_runs
       ADD COLUMN scratch_inline               BYTEA,
       ADD COLUMN scratch_handle               TEXT,
       ADD COLUMN scratch_handle_backend       TEXT;
   ```

2. Verify the file is included in the embed: `cat lib/foundation/persistence/postgres/migrations/embed.go`. The embed uses `//go:embed *.sql`, so no edit is needed.

3. Run `go test ./lib/foundation/persistence/postgres/... -count=1 -run TestSchemaConsolidation` to ensure the consolidated-schema test still passes (the test snapshots the embedded migration set; it will recompute against the new file).

### Task 8: Add SQLite migration 010 for the scratch triple

**Files:** `lib/foundation/persistence/sqlite/migrations/010-node-run-scratch.sql` (new)

**Steps:**

1. Create the file with the SQLite-flavored body:

   ```sql
   -- Migration 010 — Add per-dispatch executor scratch triple to rimsky_node_runs.
   -- Mirrors the parked_payload_inline/handle/handle_backend pattern.

   ALTER TABLE rimsky_node_runs ADD COLUMN scratch_inline BLOB;
   ALTER TABLE rimsky_node_runs ADD COLUMN scratch_handle TEXT;
   ALTER TABLE rimsky_node_runs ADD COLUMN scratch_handle_backend TEXT;
   ```

   (SQLite requires one `ALTER TABLE` per column. `BLOB` is the SQLite analogue of `BYTEA`, matching the existing `parked_payload_inline BLOB` column in `001-schema.sql:193`.)

2. Run `go test ./lib/foundation/persistence/sqlite/... -count=1` to ensure the SQLite migration runner accepts the new file.

### Task 9: Extend `persistence.DispatchRequest` + `Candidate` with InitialScratch

**Files:** `lib/foundation/persistence/node_runs.go`

(Despite the filename `node_runs.go`, this file holds the `DispatchRequest`, `Candidate`, `Queue` interface, and `ParkActiveInput` / `ResumeMetadataRow` types — see `lib/foundation/persistence/dispatch_row.go` for the separate `DispatchRow` persisted-row shape, which this plan does not touch.)

**Steps:**

1. In the `DispatchRequest` struct (current shape at lines 31-70), add the following fields after `PriorDispatchDisposition`:

   ```go
       // InitialScratchInline / InitialScratchHandle / InitialScratchHandleBackend
       // are the executor-attached scratch bytes copied forward from the prior
       // dispatch row when this enqueue carries a non-nil PriorDispatchID. All
       // three are empty on initial dispatches and on enqueues whose prior
       // dispatch had no scratch. Inert in rimsky per `concept:inertness` /
       // `@blessed-invariant 21`. The recovery enqueue sites populate these from
       // the prior dispatch row before calling EnqueueInTx; the persistence layer
       // writes them onto the new row's scratch_inline / scratch_handle /
       // scratch_handle_backend columns.
       //
       // @concept: executor
       InitialScratchInline        []byte
       InitialScratchHandle        string
       InitialScratchHandleBackend string
   ```

2. (`Candidate` struct, lines 117-143) — leave unchanged. Scratch flows to the dispatch path via a separate query on the row by the `runner_dispatch.go::buildExecuteRequest` path; surfacing it on `Candidate` would inflate the SelectCandidates result set unnecessarily.

3. Add a new method to the `Queue` interface in the same file (after `LoadResumeMetadataInTx`, line 397):

   ```go
       // LoadScratchInTx returns the scratch bytes persisted on a node-run
       // row for the dispatch path's wire-attach step. Resolves spill via
       // `concept:blob-backend`: when scratch_handle is non-empty the caller
       // is expected to materialize the bytes through its configured Blob.
       // Returns (nil, "", "", nil) when no scratch is set.
       //
       // @concept: executor
       LoadScratchInTx(ctx context.Context, tx Tx, dispatchID shared.UUID) (inline []byte, handle, handleBackend string, err error)

       // WriteScratchInTx persists the executor-attached scratch bytes onto
       // the dispatch row at stream-close or via the mid-dispatch HTTP
       // callback route. Either inline OR (handle + handleBackend) is set
       // per call; setting both is a writer error (returned, not panicked).
       // The opposite triple is cleared in the same UPDATE so a callback that
       // overwrites a previously-spilled scratch with smaller inline bytes
       // does not leave the stale handle dangling.
       //
       // @concept: executor
       WriteScratchInTx(ctx context.Context, tx Tx, dispatchID shared.UUID, inline []byte, handle, handleBackend string) error
   ```

4. Run `go build ./lib/foundation/persistence/...` and confirm the build still passes — the interface methods compile but their implementations are still missing, so any non-mock consumer will fail to link. The mock-backed tests under `lib/foundation/persistence/` are fine. The postgres / sqlite implementations land in Tasks 10–11.

### Task 10: Implement the new Queue methods + scratch copy in Postgres EnqueueInTx

**Files:** `lib/foundation/persistence/postgres/queue.go`, `lib/foundation/persistence/postgres/queue_park.go`

**Steps:**

1. In `queue.go::EnqueueInTx` (current INSERT at lines 127-140), extend the INSERT column list and the SELECT projection to include the scratch triple. The new statement reads:

   ```go
       tag, err := q.q(tx).Exec(ctx,
           `INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition, scratch_inline, scratch_handle, scratch_handle_backend)
            SELECT gen_random_uuid(), $1, $2, $3, $4, 'pending', $5, rs.id, $7, $8, $9, $10, $11
              FROM rimsky_run_scopes rs
             WHERE rs.id = $6
               AND rs.closed_at IS NULL
               AND NOT EXISTS (
                 SELECT 1 FROM rimsky_node_runs
                  WHERE node_id = $1
                    AND run_scope_id = $6
                    AND phase IN ('pending','active','held','parked')
               )`,
           req.NodeID, executor, stores, req.EnqueuedAt, req.FrameID, req.RunScopeID,
           priorID, priorDisposition,
           nilIfEmpty(req.InitialScratchInline), nullableText(req.InitialScratchHandle), nullableText(req.InitialScratchHandleBackend),
       )
   ```

   The `nilIfEmpty` helper already exists alongside the parked-payload writes (`queue_park.go::nilIfEmpty` / `nilIfEmptyStr`); re-use them. If `nilIfEmpty` is unexported and lives in `queue_park.go`, leave it where it is — `queue.go` is in the same package.

2. Add `LoadScratchInTx` and `WriteScratchInTx` to `queue_park.go` (alongside the park-related helpers since they share the same opaque-bytes spill pattern). Bodies:

   ```go
   // LoadScratchInTx returns the persisted scratch triple for a dispatch row.
   func (q *queueImpl) LoadScratchInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) ([]byte, string, string, error) {
       if tx == nil {
           return nil, "", "", errors.New("postgres.LoadScratchInTx: tx required")
       }
       var (
           inline  []byte
           handle  sql.NullString
           backend sql.NullString
       )
       err := q.q(tx).QueryRow(ctx,
           `SELECT scratch_inline, scratch_handle, scratch_handle_backend
              FROM rimsky_node_runs
             WHERE id = $1`,
           dispatchID,
       ).Scan(&inline, &handle, &backend)
       if err != nil {
           if errors.Is(err, pgx.ErrNoRows) {
               return nil, "", "", nil
           }
           return nil, "", "", fmt.Errorf("postgres.LoadScratchInTx: %w", err)
       }
       var hStr, bStr string
       if handle.Valid {
           hStr = handle.String
       }
       if backend.Valid {
           bStr = backend.String
       }
       return inline, hStr, bStr, nil
   }

   // WriteScratchInTx persists scratch onto a dispatch row. Sets either
   // inline OR (handle + handleBackend); the opposite triple is cleared in
   // the same UPDATE.
   func (q *queueImpl) WriteScratchInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, inline []byte, handle, handleBackend string) error {
       if tx == nil {
           return errors.New("postgres.WriteScratchInTx: tx required")
       }
       if len(inline) > 0 && handle != "" {
           return errors.New("postgres.WriteScratchInTx: inline and handle are mutually exclusive")
       }
       _, err := q.q(tx).Exec(ctx,
           `UPDATE rimsky_node_runs
               SET scratch_inline         = $2,
                   scratch_handle         = NULLIF($3, ''),
                   scratch_handle_backend = NULLIF($4, '')
             WHERE id = $1`,
           dispatchID, nilIfEmpty(inline), handle, handleBackend,
       )
       if err != nil {
           return fmt.Errorf("postgres.WriteScratchInTx: %w", err)
       }
       return nil
   }
   ```

3. Run `go test ./lib/foundation/persistence/postgres/... -count=1`. The schema-consolidation test will recompute. The existing `EnqueueInTx` tests should pass because old callers leave `InitialScratch*` as zero values, which serialize as NULL via `nilIfEmpty`. If a postgres-side EnqueueInTx test breaks, the failure must be a real wire-shape regression — debug, do not paper over.

4. Run `go test ./lib/foundation/persistence/postgres/... -race -count=3` on the queue paths specifically to confirm no race introduced by the wider INSERT statement.

### Task 11: Implement the new Queue methods + scratch copy in SQLite EnqueueInTx

**Files:** `lib/foundation/persistence/sqlite/queue.go`, `lib/foundation/persistence/sqlite/queue_park.go`

**Steps:**

1. Mirror the Postgres edits in Task 10 for the SQLite queue implementation. The SQLite INSERT uses positional bindings (`?` placeholders) so the column ordering is the natural translation. Use the SQLite-side `nilIfEmpty` helper (it exists in `queue_park.go` by analogy with postgres) or add one if absent; the SQLite driver `modernc.org/sqlite` accepts `[]byte(nil)` directly for BLOB nulls.

2. The `LoadScratchInTx` / `WriteScratchInTx` bodies mirror the postgres versions — same SQL, SQLite placeholders.

3. Run `go test ./lib/foundation/persistence/sqlite/... -count=1`.

### Task 12: Make the four recovery enqueue sites copy scratch from the prior row

**Files:** `lib/runtime/conductor.go`, `lib/runtime/cascade_recalculate.go`, `lib/runtime/on_error.go`, `lib/runtime/runner_error_policy.go`

**Steps:**

1. In `conductor.go::SweepStaleHeartbeats` (current enqueue site at line 263), before the `args.Queue.EnqueueInTx` call, load scratch from the prior dispatch row (the `priorDispatchID` captured at line 199-202) inside the same transaction:

   ```go
       var scratchInline []byte
       var scratchHandle, scratchBackend string
       if priorDispatchID != (shared.UUID{}) {
           scratchInline, scratchHandle, scratchBackend, err = args.Queue.LoadScratchInTx(ctx, tx, priorDispatchID)
           if err != nil {
               return fmt.Errorf("load prior scratch: %w", err)
           }
       }
   ```

   Then add the InitialScratch fields to the `DispatchRequest` literal:

   ```go
       if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
           NodeID:                       cur.ID,
           ExecutorName:                 cur.Executor,
           RequiredStores:               []string{},
           EnqueuedAt:                   args.Clock.Now(),
           FrameID:                      *cur.FrameID,
           RunScopeID:                   curScopeID,
           PriorDispatchID:              priorPtr,
           PriorDispatchDisposition:     "heartbeat_stale",
           InitialScratchInline:         scratchInline,
           InitialScratchHandle:         scratchHandle,
           InitialScratchHandleBackend:  scratchBackend,
       }, tx); err != nil {
   ```

2. In `cascade_recalculate.go::RecalculateNode` (current enqueue site at line 163), the auto-commit Enqueue call runs outside a tx. Wrap the scratch load in `args.Persist.Transaction(...)` (or convert the call to an EnqueueInTx pair). Concretely: replace the `args.Queue.Enqueue(ctx, persistence.DispatchRequest{...})` call with a transactional pair:

   ```go
       if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           var scratchInline []byte
           var scratchHandle, scratchBackend string
           if priorDispatchID != nil && *priorDispatchID != (shared.UUID{}) {
               var lerr error
               scratchInline, scratchHandle, scratchBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, *priorDispatchID)
               if lerr != nil {
                   return fmt.Errorf("load prior scratch: %w", lerr)
               }
           }
           return args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
               NodeID:                       target.ID,
               ExecutorName:                 target.Executor,
               RequiredStores:               []string{},
               EnqueuedAt:                   args.Clock.Now(),
               FrameID:                      *target.FrameID,
               RunScopeID:                   runScopeID,
               PriorDispatchID:              priorDispatchID,
               PriorDispatchDisposition:     "recalculate",
               InitialScratchInline:         scratchInline,
               InitialScratchHandle:         scratchHandle,
               InitialScratchHandleBackend:  scratchBackend,
           }, tx)
       }); err != nil {
   ```

   Keep the `errors.Is(err, persistence.ErrRunScopeClosed)` defensive branch in place; route on `err` from the transaction.

3. In `on_error.go::OnError` (current enqueue site at line 273), the call already runs inside a transaction. Insert the scratch load before the EnqueueInTx call, using `priorID` (line 269) as the source row:

   ```go
       var scratchInline []byte
       var scratchHandle, scratchBackend string
       if priorID != nil && *priorID != (shared.UUID{}) {
           var lerr error
           scratchInline, scratchHandle, scratchBackend, lerr = args.Queue.LoadScratchInTx(ctx, tx, *priorID)
           if lerr != nil {
               return fmt.Errorf("load prior scratch: %w", lerr)
           }
       }
       if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
           NodeID:                       args.NodeID,
           // ... existing fields ...
           PriorDispatchID:              priorID,
           PriorDispatchDisposition:     "retry_after_error",
           InitialScratchInline:         scratchInline,
           InitialScratchHandle:         scratchHandle,
           InitialScratchHandleBackend:  scratchBackend,
       }, tx); err != nil {
   ```

4. In `runner_error_policy.go::applyResolvedAction` (current enqueue site at line 254 inside the `spec.DispositionRetry` case), `priorID` is `acq.DispatchID`. Mirror the pattern from Step 3:

   ```go
       priorID := acq.DispatchID
       scratchInline, scratchHandle, scratchBackend, lerr := args.Queue.LoadScratchInTx(ctx, tx, priorID)
       if lerr != nil {
           return fmt.Errorf("load prior scratch: %w", lerr)
       }
       if err := args.Queue.EnqueueInTx(ctx, persistence.DispatchRequest{
           NodeID:                       acq.NodeID,
           // ... existing fields ...
           PriorDispatchID:              &priorID,
           PriorDispatchDisposition:     "retry_after_error",
           InitialScratchInline:         scratchInline,
           InitialScratchHandle:         scratchHandle,
           InitialScratchHandleBackend:  scratchBackend,
       }, tx); err != nil {
   ```

5. Run `go test ./lib/runtime/... -count=1` and `go test ./lib/runtime/... -race -count=3` on the recovery-touching packages.

6. **Load-bearing property check (durability of executor in-flight state):** scratch round-trip across recovery is the load-bearing property STORY-opaque-executor-scratch's acceptance pins. The implementer MUST NOT skip the scratch load on a particular recovery path "because that path is rare" — every prior-dispatch-disposition (heartbeat-stale, retry-after-error, recalculate) must copy scratch or the executor's in-flight state silently vanishes on that particular recovery class. The conformance round-trip case landed in Pass 7 exercises this; if a path is skipped, the Pass 7 verification will fail at the dispatch under the affected disposition.

### Task 13: Add scratch round-trip case to the persistence conformance suite

**Files:** `lib/foundation/persistence/conformance/recovery_aware_dispatch.go`

**Steps:**

1. Extend `testRecoveryAwareDispatch` so after the recovery enqueue (line 130), the test:

   a. Writes scratch onto the **original** dispatch row before retiring it. Do this inside the same tx that calls `RemoveForNodeInTx` (lines 116-122), via `q.WriteScratchInTx(ctx, tx, originalDispatchID, []byte("scratch-bytes-fixture"), "", "")`.

   b. After the recovery enqueue, resolves the new dispatch's ID via SelectCandidates (the existing loop at lines 149-167 already does this; use the same surfaced ID).

   c. Asserts the recovery row's scratch matches the bytes written to the original. Open a fresh tx and call `q.LoadScratchInTx(ctx, tx, got.DispatchID)`; assert the returned `inline` equals `[]byte("scratch-bytes-fixture")` and that `handle` and `handleBackend` are empty.

2. Run the conformance suite via the consumer: `go test ./lib/foundation/persistence/postgres/... -count=1 -run RecoveryAware` and `go test ./lib/foundation/persistence/sqlite/... -count=1 -run RecoveryAware`. Both need a working Docker socket (postgres) — the existing testcontainers harness handles this.

---

## Pass 3: Executor wire + scratch HTTP callback route

**Goal:** Surface scratch on `proto:executor.proto::ExecuteRequest` and on the three writing outcome variants (`Success`, `Error`, `Park`) plus `AsyncCallbackBody`'s outcome variants, regenerate the proto bindings, thread scratch into the dispatch path (`buildExecuteRequest` reads from the row; `readExecutorStream` writes from terminal outcomes), and add a new HTTP callback route `POST /v1/runs/{run_id}/scratch` paralleling the existing `/v1/runs/{run_id}/attributes` §12.5 route. No kind / inproc / claude-agent code in this pass.

**Scope:** Tasks 14–19.

**Falsifier:** `proto:executor.proto::ExecuteRequest` does not have a `bytes scratch` field, OR `Success` / `Error` / `Park` do not have one, OR `AsyncCallbackBody.success` / `error` / `park` JSON parse does not extract scratch, OR `buildExecuteRequest` does not populate the wire scratch field from the dispatch row, OR `readExecutorStream` does not persist scratch from terminal outcomes onto the dispatch row, OR no HTTP route is mounted at `POST /v1/runs/{run_id}/scratch` on the supervisor's `CallbackServer`.

### Task 14: Add `scratch` to the four proto messages

**Files:** `lib/protocols/proto/v1/executor.proto`

**Steps:**

1. Add the new field to `ExecuteRequest` (lines 30-125). Pick the next free wire field number — `run_scope_id` occupies 16, and reserved field numbers in the message are 4, 10, 11. Use field 17:

   ```proto
     // scratch carries opaque executor-attached bytes persisted on the
     // dispatch row (col:rimsky_node_runs.scratch_inline / scratch_handle /
     // scratch_handle_backend). Empty on the initial dispatch. When this
     // dispatch supersedes a prior dispatch under any prior-dispatch
     // disposition (heartbeat_stale | retry_after_error | recalculate), the
     // enqueue path copies scratch from the prior row onto the new row;
     // the supervisor materializes spilled scratch via the configured
     // BlobBackend before populating this field.
     //
     // Inert in rimsky per @blessed-invariant 21 / concept:inertness. The
     // executor writes scratch back via the Success / Error / Park outcome
     // variants (terminal-final) or via the
     // POST {callback_url}/v1/runs/{run_id}/scratch callback (mid-dispatch).
     bytes scratch = 17;
   ```

2. Add the same `bytes scratch = 4;` field to each of `Success`, `Error`, and `Park` outcome messages (using the next free tag in each; `Success` ends at tag 3, `Error` at 2, `Park` at 6). For `Success` (field 4), `Error` (field 3), `Park` (field 7), use this single-line addition with the field number matching the per-message next-free tag:

   ```proto
     // Executor-attached opaque bytes the supervisor persists onto the
     // dispatch row at stream-close (analogous to the mid-dispatch scratch
     // HTTP callback). Inert in rimsky per @blessed-invariant 21.
     bytes scratch = <N>;
   ```

   Specifically:
   - `Success`: `bytes scratch = 4;`
   - `Error`: `bytes scratch = 3;`
   - `Park`: `bytes scratch = 7;`

   `AwaitAsyncCallback` (lines 295-302) does NOT get a scratch field — the await-async-callback variant has no terminal outcome yet; scratch lands on whichever outcome the async callback ultimately POSTs.

3. The proto's `AsyncCallbackBody` (lines 304-324) carries the same `Success` / `Error` / `Park` messages via oneof — the scratch additions to those messages automatically surface here through the JSON-decoded path; no separate `AsyncCallbackBody` edit is needed.

4. Run `make proto-gen`. Confirm the regenerated bindings under `lib/protocols/proto/v1/gen/` contain `Scratch []byte` on `ExecuteRequest`, `Success`, `Error`, and `Park`. Commit the regenerated files alongside the proto edit.

5. Run `go build ./...` and confirm no consumer of the regenerated types breaks (the regen is additive). If a switch on the StreamClose oneof shape touches the new field, fix the consumer in place.

### Task 15: Thread scratch onto `ExecuteRequest` from the dispatch row

**Files:** `lib/runtime/runner_dispatch.go`, `lib/runtime/runner.go`

**Steps:**

1. The dispatch path needs the row's scratch to populate `ExecuteRequest.scratch`. The cleanest source is the same path that already resolves the dispatch row inside the runner — specifically the `acquisition` struct. Add a field to `acquisition` (look up its definition in `runner.go` / `runner_acquire_helpers.go` — search for `type acquisition struct`):

   ```go
       // Scratch carries the dispatch row's scratch bytes for wire-attach
       // onto the executor's ExecuteRequest. Empty on initial dispatches
       // and on dispatches whose prior row had no scratch. Spilled handles
       // are materialized via the configured BlobBackend before this field
       // populates. @concept: executor
       Scratch []byte
   ```

2. In the acquisition-tx code path (search for the site that already reads other per-row metadata; the resume metadata loader pattern is at `LoadResumeMetadataInTx` — mimic that placement), add a call to `args.Queue.LoadScratchInTx(ctx, tx, acq.DispatchID)` after the row is confirmed claimed. When the returned `handle` is non-empty, materialize via `args.Blob.Read(ctx, persistence.Handle(handle))` (mirror the spilled-event-payload materialization in `runner_dispatch.go::lookupEventPayload` lines 745-775). Log via `args.Logger.Warn` on backend-mismatch / fetch failure exactly like the named-event lookup does (the dispatch proceeds with empty scratch on materialization failure — STORY-opaque-executor-scratch's load-bearing property is round-trip integrity when materialization succeeds; a transient backend outage degrading to empty is acceptable because the executor sees `len(scratch) == 0` and handles it the same as a fresh dispatch).

3. In `runner_dispatch.go::buildExecuteRequest` (lines 1179-1265), after the existing `req` literal is populated and before the recovery-aware-fields block, add:

   ```go
       req.Scratch = acq.Scratch
   ```

   The proto wire field carries `nil` and `[]byte{}` identically (proto3 bytes); leaving it as the acquisition's zero value covers the empty case.

4. Run `go build ./... && go test ./lib/runtime/... -count=1`.

### Task 16: Persist scratch from terminal outcomes in `readExecutorStream` + async callback

**Files:** `lib/runtime/runner_dispatch.go`, `lib/runtime/runner_terminal.go`, `lib/runtime/callback.go`

**Steps:**

1. Extend the `terminalEvent` struct (currently at `runner_dispatch.go` lines 92-112) with a Scratch field:

   ```go
       // Scratch is the executor-attached opaque bytes from a terminal
       // outcome (Success/Error/Park). Persisted onto the dispatch row by
       // applyTerminalSuccess / applyTerminalError / applyTerminalPark.
       // Empty when the executor did not attach scratch at terminal.
       // @concept: executor
       Scratch []byte
   ```

2. In `runner_dispatch.go::readExecutorStream`, in each of the three terminal outcome branches (Success at line 358, Error at 369, Park at 380), pull `oc.<Variant>.Scratch` onto the `terminalEvent`'s `Scratch` field. Pattern:

   ```go
       case *genv1.StreamClose_Success:
           t := terminalEvent{
               Kind:          terminalKindComplete,
               Changed:       oc.Success.Changed,
               ChangeSummary: oc.Success.ChangeSummary,
               NamedEvents:   pending,
               Scratch:       oc.Success.Scratch,
           }
   ```

   Equivalent edits for `StreamClose_Error` (set `Scratch: oc.Error.Scratch`) and `StreamClose_Park` (set `Scratch: oc.Park.Scratch`). `StreamClose_AwaitAsync` is untouched — that path's scratch lands via the async callback body.

3. In `callback.go::parseAsyncCallback` (lines 472-529), extend each outcome variant's JSON-decoded body shape with a `Scratch []byte` field (mirroring the proto's bytes-field JSON projection — base64 on the wire). The `asyncCallbackSuccess` / `asyncCallbackError` / `asyncCallbackPark` structs at lines 318-338 each gain:

   ```go
       Scratch []byte `json:"scratch,omitempty"`
   ```

   In `parseAsyncCallback`'s `terminalEvent` construction (the three branches at lines 498-525), populate `Scratch: body.Success.Scratch` / etc.

4. In `runner_terminal.go::applyTerminalComplete` (or whichever terminal-handler function persists the success terminal — search for the existing `Persist.NodeAttributes().Upsert` / `MergeDelta` call, since the success path already commits attribute writeback alongside terminal state), add a `args.Queue.WriteScratchInTx(ctx, tx, acq.DispatchID, t.Scratch, "", "")` call inside the same tx that commits the terminal state. The write is unconditional when `t.Scratch` is non-empty; for `len(t.Scratch) == 0` and no prior scratch on the row, the write is a no-op (the column was already NULL); for `len(t.Scratch) == 0` and existing scratch on the row, the column is set to NULL (the executor's empty terminal scratch erases prior state, which matches the executor-writeback semantics). Do the same in `applyErrorPolicy` / `applyTerminalInfraError` (`runner_error_policy.go`) and `applyTerminalPark` (search the codebase).

   **Spill threshold:** The terminal-write path picks inline-vs-spill the same way the parked-payload write does. Search for the existing parked-payload spill site (it lives in `applyTerminalPark` — call it the "park spill helper"). Reuse that helper's threshold check: when `len(t.Scratch) > args.BlobSpillThreshold` (with default-zero meaning "no spill"), write via `args.Blob.Write(ctx, t.Scratch)` to obtain a `Handle`, then call `WriteScratchInTx(ctx, tx, dispatchID, nil, string(handle), args.Blob.Name())`. Otherwise pass the bytes inline.

5. Run `go build ./... && go test ./lib/runtime/... -count=1 && go test ./lib/runtime/... -race -count=3`.

### Task 17: Add the `/v1/runs/{run_id}/scratch` HTTP callback handler

**Files:** `lib/graph/scratch/callback.go` (new), `lib/graph/scratch/callback_test.go` (new)

**Steps:**

1. Create `lib/graph/scratch/` as a new package directly mirroring `lib/graph/attribute/` (see `callback.go` for the pattern). The file body:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   // Incremental executor-scratch writeback callback handler — paralleling
   // the §12.5 attributes incremental writeback. Mirrors the wire shape
   // for symmetry:
   //
   //   POST {callback_url}/v1/runs/{run_id}/scratch
   //   Authorization: <cancel_token>          (matches §12.4 / §12.5 auth)
   //   Body: raw bytes (Content-Type: application/octet-stream) — the
   //         executor-attached opaque scratch payload, inert to rimsky.
   //   → 204 No Content
   //
   // The handler resolves the supervisor-issued cancel_token via the
   // AuthLookup callback, then persists the bytes onto the dispatch row
   // via the ScratchWriter dependency.

   package scratch

   import (
       "context"
       "errors"
       "io"
       "net/http"
       "strings"

       "github.com/go-chi/chi/v5"
       "github.com/google/uuid"

       "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
   )

   // ScratchWriter is the narrow interface the HTTP handler depends on.
   // The supervisor adapts the Queue.WriteScratchInTx surface to this
   // shape (the persistence method takes an additional `tx`; the callback
   // handler always runs outside any caller-owned tx). The supervisor
   // adapter wraps the call in a short tx + decides inline-vs-spill the
   // same way the stream-close terminal path does.
   type ScratchWriter interface {
       Write(ctx context.Context, runID shared.UUID, bytes []byte) error
   }

   type AuthLookup func(token string, runID shared.UUID) error

   var ErrUnauthorizedCallback = errors.New("scratch: unauthorized callback")

   type HandlerDeps struct {
       Writer ScratchWriter
       Auth   AuthLookup
       Logger shared.Logger
   }

   // Handler returns the chi-compatible http.Handler. Intended to be
   // mounted at `POST /v1/runs/{run_id}/scratch` so chi can supply the
   // URL parameter via chi.URLParam.
   //
   // Auth is required at construction. Passing a HandlerDeps with a nil
   // Auth panics, mirroring the attributes-callback handler's stance.
   func Handler(deps HandlerDeps) http.Handler {
       if deps.Auth == nil {
           panic("scratch.Handler: deps.Auth is required (nil would silently disable callback auth)")
       }
       if deps.Writer == nil {
           panic("scratch.Handler: deps.Writer is required")
       }
       logger := deps.Logger
       if logger == nil {
           logger = shared.SilentLogger{}
       }
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           runIDStr := chi.URLParam(r, "run_id")
           runID, err := uuid.Parse(runIDStr)
           if err != nil {
               http.Error(w, `{"error":"invalid run_id"}`, http.StatusBadRequest)
               return
           }
           token := strings.TrimSpace(r.Header.Get("Authorization"))
           token = strings.TrimPrefix(token, "Bearer ")
           token = strings.TrimSpace(token)
           if token == "" {
               http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
               return
           }
           if err := deps.Auth(token, runID); err != nil {
               logger.Warn("scratch callback: unauthorized",
                   "run_id", runID.String(), "error", err.Error())
               http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
               return
           }
           // Bound the body read so a malicious / runaway executor cannot
           // exhaust supervisor memory by streaming gigabytes. The cap
           // mirrors the attribute-writeback body limit; spill threshold
           // policy lives in the ScratchWriter adapter.
           const maxBody = 64 * 1024 * 1024
           body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
           if err != nil {
               http.Error(w, `{"error":"read_body"}`, http.StatusBadRequest)
               return
           }
           if len(body) > maxBody {
               http.Error(w, `{"error":"body_too_large"}`, http.StatusRequestEntityTooLarge)
               return
           }
           if err := deps.Writer.Write(r.Context(), runID, body); err != nil {
               logger.Error("scratch callback: write failed",
                   "run_id", runID.String(), "error", err.Error())
               http.Error(w, `{"error":"write_failed"}`, http.StatusInternalServerError)
               return
           }
           w.WriteHeader(http.StatusNoContent)
       })
   }
   ```

2. Add a co-located `callback_test.go` that exercises (a) the unauthorized path returns 401, (b) the OK path returns 204 + the writer received the bytes, (c) the invalid run_id path returns 400, (d) the body-too-large path returns 413, (e) constructing the handler with `nil` Auth or `nil` Writer panics. Use a stub `ScratchWriter` whose `Write` appends to a slice. Pattern mirrors `lib/graph/attribute/callback_test.go`.

3. Run `go build ./... && go test ./lib/graph/scratch/... -count=1`.

### Task 18: Mount the scratch handler on the supervisor's `CallbackServer`

**Files:** `lib/runtime/callback.go`

**Steps:**

1. In `CallbackServer::Start` (lines 233-275), alongside the existing `r.Method(http.MethodPost, "/v1/runs/{run_id}/attributes", ...)` mount (lines 245-249), add the scratch mount:

   ```go
       if c.Persist != nil && c.Queue != nil && c.Blob != nil {
           r.Method(http.MethodPost, "/v1/runs/{run_id}/scratch", rimskyscratch.Handler(rimskyscratch.HandlerDeps{
               Writer: scratchStoreAdapter{
                   persist:        c.Persist,
                   queue:          c.Queue,
                   blob:           c.Blob,
                   spillThreshold: c.BlobSpillThreshold,
                   logger:         c.Logger,
               },
               Auth:   c.attributesAuth,
               Logger: c.Logger,
           }))
       }
   ```

   Add the `rimskyscratch "github.com/rimsky-ai/rimsky-core/lib/graph/scratch"` import.

2. Add a `scratchStoreAdapter` private type in the same file, after `attributesStoreAdapter`. The spill path mirrors `code:runner_terminal_park.go::applyTerminalPark` lines 88-104 (the existing `BlobKey{NodeID, Hint}` + `shouldSpillBlob` pattern):

   ```go
   // scratchStoreAdapter bridges the persistence Queue.WriteScratchInTx to
   // the local scratch.ScratchWriter the HTTP callback handler depends on.
   // Picks inline vs. spilled-handle using the supervisor's
   // BlobSpillThreshold, mirroring the parked-payload + terminal-scratch
   // spill decision.
   type scratchStoreAdapter struct {
       persist        persistence.Tables
       queue          persistence.Queue
       blob           persistence.BlobBackend
       spillThreshold int
       logger         shared.Logger
   }

   func (a scratchStoreAdapter) Write(ctx context.Context, runID shared.UUID, bytes []byte) error {
       var inline []byte
       var handle, backend string
       if a.spillThreshold > 0 && len(bytes) > a.spillThreshold && a.blob != nil {
           // Resolve the node id for the BlobKey hint by reading the row
           // (the §scratch callback is keyed on dispatch id only; the blob
           // backend wants a node-id hint for path derivation on the
           // filesystem backend, exactly like the park spill).
           var nodeID shared.UUID
           if err := a.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
               row, err := a.persist.Nodes().GetRunByDispatchID(ctx, runID, tx)
               if err == nil && row != nil {
                   nodeID = row.NodeID
               }
               return nil
           }); err != nil && a.logger != nil {
               a.logger.Warn("scratchStoreAdapter: node id lookup failed; spill key has empty NodeID hint",
                   "dispatch_id", runID.String(), "error", err.Error())
           }
           key := persistence.BlobKey{NodeID: nodeID.String(), Hint: "scratch"}
           h, err := a.blob.Write(ctx, key, bytes)
           if err != nil {
               // Spill failure → fall back to inline; mirrors applyTerminalPark.
               if a.logger != nil {
                   a.logger.Warn("scratchStoreAdapter: blob spill failed; falling back to inline",
                       "dispatch_id", runID.String(), "error", err.Error())
               }
               inline = bytes
           } else {
               handle = string(h)
               backend = a.blob.Name()
           }
       } else {
           inline = bytes
       }
       return a.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           return a.queue.WriteScratchInTx(ctx, tx, runID, inline, handle, backend)
       })
   }
   ```

   If `GetRunByDispatchID` doesn't exist on `Tables().Nodes()` (search `grep -n 'GetRunByDispatchID\|GetRunByDispatchIDForUpdate' lib/foundation/persistence/*.go`), use whichever lookup IS available — `GetRunByDispatchIDForUpdate` exists at `code:lib/foundation/persistence/nodes.go` (the callback determinism tx uses it at `code:lib/runtime/callback.go:599`). Pick a read-locking variant or a plain Get; the spill path doesn't need a row lock.

3. Reuse `c.attributesAuth` for auth — the `cancel_token` shape (`<supervisorID>:<dispatchID>`) and run-id binding are identical for the scratch and attributes callbacks. The function is already exported on `CallbackServer`.

4. Run `go build ./... && go test ./lib/runtime/... -count=1`.

### Task 19: Add the runtime-helper direct-call surface for in-process executors

**Files:** `lib/runtime/executor/scratch_writer.go` (new)

**Steps:**

1. Create the file with a small helper struct that in-process handlers reach for the same wire-equivalent scratch write without going over HTTP. The spill path mirrors `code:runner_terminal_park.go::applyTerminalPark` (BlobKey hint + `shouldSpillBlob` pattern):

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package executor

   import (
       "context"

       "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
       "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
   )

   // ScratchWriter is the in-process equivalent of the §scratch HTTP
   // callback route. In-process executor handlers call this directly to
   // persist mid-dispatch scratch onto their dispatch row without going
   // over the wire. Same spill / threshold behavior as the HTTP-route
   // adapter. The dispatch path passes a *ScratchWriter into each
   // InProcessHandler invocation via the per-dispatch handler context
   // (introduced in Pass 5).
   //
   // @concept: executor
   type ScratchWriter struct {
       Persist        persistence.Tables
       Queue          persistence.Queue
       Blob           persistence.BlobBackend
       SpillThreshold int
       // DispatchID and NodeID are populated per-dispatch by the inproc
       // dispatch glue (Pass 5's HandlerContext factory threads the
       // acquisition's typed UUIDs in directly — no string parse).
       DispatchID shared.UUID
       NodeID     shared.UUID
   }

   // Write persists scratch bytes onto the dispatch row. Picks inline vs.
   // spilled-handle using SpillThreshold + the parked-payload-style
   // BlobKey hint.
   func (w *ScratchWriter) Write(ctx context.Context, bytes []byte) error {
       var inline []byte
       var handle, backend string
       if w.SpillThreshold > 0 && len(bytes) > w.SpillThreshold && w.Blob != nil {
           key := persistence.BlobKey{NodeID: w.NodeID.String(), Hint: "scratch"}
           h, err := w.Blob.Write(ctx, key, bytes)
           if err != nil {
               return err
           }
           handle = string(h)
           backend = w.Blob.Name()
       } else {
           inline = bytes
       }
       return w.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           return w.Queue.WriteScratchInTx(ctx, tx, w.DispatchID, inline, handle, backend)
       })
   }
   ```

2. The Pass 5 dispatch wiring threads a per-dispatch `*ScratchWriter` into the inproc handler context so handlers can call `writer.Write(ctx, bytes)` directly. No edits to InProcessClient yet — Pass 5 adds both.

3. Run `go build ./...`.

---

## Pass 4: Attribute carry-forward in `resolveAttributes`

**Goal:** Add the pre-substitution self-state carry-forward step to attribute hydration, scoped to (node, RunScope). Sub-graph and fan-out RunScopes inherit nothing from their parent scope. No proto / inproc / kind / claude-agent code in this pass.

**Scope:** Tasks 20–22.

**Falsifier:** `resolveAttributes` does not contain a pre-substitution hydration step that loads the most-recent prior `rimsky_node_attributes` row for `(node_id, run_scope_id)`, OR the carry-forward hydration query lookups cross the RunScope boundary (a sub-graph node sees its parent scope's prior writeback), OR the substitution overlay does not subsequently overwrite carry-forward values for source-bound properties.

### Task 20: Reuse the existing `GetLatestByNode` for carry-forward lookups

**Files:** `lib/foundation/persistence/node_attributes.go` (verification only — no edit)

**Steps:**

1. The persistence layer already exports the exact lookup the carry-forward step needs. Confirm by reading `code:lib/foundation/persistence/node_attributes.go#28-50`:

   ```go
   // GetLatestByNode returns the most-recent attribute row for the
   // (nodeID, runScopeID) pair, or nil when no prior row exists.
   //
   //    type NodeAttributeTable interface {
   //        ...
   //        GetLatestByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx Tx) (*NodeAttributesRow, error)
   //        ...
   //    }
   ```

   The Postgres implementation lives at `code:lib/foundation/persistence/postgres/node_attributes.go#66`; the SQLite implementation at `code:lib/foundation/persistence/sqlite/node_attributes.go#54`; a conformance test at `code:lib/foundation/persistence/conformance/node_attributes_per_run.go::testNodeAttributesGetLatestByNode`. No new persistence method is needed — Task 21 calls `GetLatestByNode` directly.

2. Run `grep -n GetLatestByNode lib/foundation/persistence/node_attributes.go lib/foundation/persistence/postgres/node_attributes.go lib/foundation/persistence/sqlite/node_attributes.go` and confirm the interface declaration + both impls are present. (If any of the three is missing, the implementer should surface that as a blocker — the carry-forward step depends on this method being callable across both backends.)

### Task 21: Add the carry-forward hydration step to `resolveAttributes`

**Files:** `lib/runtime/runner_dispatch.go`

**Steps:**

1. The hydration step inserts **after** the schema check (currently `resolveAttributes` line ~465-476, after `node.CheckEffectiveAttributesSchema`) and **before** `substituteAttributesSchema` (currently line 490). Place it after `acq.MergedAttributes = resolved` at line 504 — actually no: the carry-forward must seed the bag BEFORE substitution overlays, so the right insertion point is after the schema is validated (line 476) and before `substituteAttributesSchema` runs (line 490). Pseudocode:

   ```
   schema ← computeEffectiveAttributeSchema(...)
   validate schema composition
   carryForward ← LatestForNodeInScope(node, scope)
   resolved ← substituteAttributesSchema(schema, rctx)
   merged ← applyAttributeOverrides(resolved, ...)
   ```

   The carry-forward step needs to feed into the per-field result so source-bound properties overwrite carried values. Concretely, modify `substituteAttributesSchema` to accept a starter map (the carry-forward bag) and emit per-field output by overlaying source-bound resolution + static-defaults on top:

   ```go
   func substituteAttributesSchema(
       schema map[string]any,
       rctx attributes.ResolveContext,
       carryForward map[string]any,
   ) (map[string]any, error) {
       out := map[string]any{}
       // Seed with carry-forward — source-bound + static-default + executor-
       // written properties all start from this bag. Source-bound resolution
       // below overwrites; static-defaults overwrite ONLY when the carry-
       // forward bag has no value (defaults are the floor, not the
       // overwrite). Executor-written readOnly properties carry through
       // unchanged.
       for k, v := range carryForward {
           out[k] = v
       }
       // ... existing per-property switch ...
       case hasSource:
           // ... existing logic ...
           out[name] = val  // source-bound always overwrites carry-forward
       case hasDefault:
           if _, present := out[name]; !present {
               out[name] = defaultVal  // defaults are a floor under carry-forward
           }
       // executor-written: stays absent if no carry-forward; otherwise carries through
       ```

   The default-as-floor behavior is the spec-correct semantics — once a node has produced a value, subsequent dispatches see the executor's value, not the static default. First-dispatch behavior is unchanged because the carry-forward bag is empty.

2. In `resolveAttributes`, between the schema-validation block and the `buildResolveContextForDispatch` call (line 486-487), load the carry-forward bag via the existing `GetLatestByNode` method:

   ```go
       var carryForward map[string]any
       if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           priorRow, err := args.Persist.NodeAttributes().GetLatestByNode(ctx, acq.NodeID, acq.RunScopeID, tx)
           if err != nil {
               return err
           }
           if priorRow != nil {
               carryForward = priorRow.Data
           }
           return nil
       }); err != nil {
           args.Logger.Warn("resolveAttributes: carry-forward lookup failed; proceeding with empty bag",
               "node_id", acq.NodeID.String(),
               "run_scope_id", acq.RunScopeID.String(),
               "error", err.Error())
       }
   ```

3. Pass `carryForward` into the modified `substituteAttributesSchema(schema, rctx, carryForward)` call (line 490). Update the signature and update the one other caller (search `grep -rn substituteAttributesSchema lib/`) — if there's a test using the old signature, update it.

4. **Self-state carry-forward boundary check (load-bearing property):** the lookup is keyed on `(acq.NodeID, acq.RunScopeID)`. A sub-graph's nodes live in a different RunScope than the calling graph, so the JOIN filter `r.run_scope_id = $2` naturally excludes any parent-scope row. The implementer MUST NOT add a "walk up to the parent scope" fallback — sub-graph sealing is the spec's load-bearing property, and the absence of the fallback IS the enforcement. Test verification in Task 22 covers this.

5. Run `go build ./... && go test ./lib/runtime/... -count=1 && go test ./lib/runtime/... -race -count=3`.

### Task 22: Unit tests for carry-forward hydration + sub-graph sealing

**Files:** `lib/runtime/runner_dispatch_test.go` (the existing test file — likely `runner_test.go` if no per-file split exists; search for the existing carry-forward / resolveAttributes test stub patterns)

**Steps:**

1. Add `TestResolveAttributes_CarryForward_SameScope` — exercises the contract by seeding two `rimsky_node_runs` rows for the same `(node_id, run_scope_id)` with attribute writebacks `{count: 1}` and `{count: 2}`, then calling `resolveAttributes` for a third dispatch and asserting the bag pre-substitution contains `count: 2`. Use the SQLite path for the test fixture to avoid testcontainers in the unit test (the persistence tables run under SQLite for unit tests via the existing `lib/foundation/persistence/sqlite/` helpers).

2. Add `TestResolveAttributes_CarryForward_CrossRunScope_Empty` — seeds a writeback in RunScope A, then runs `resolveAttributes` for the same node in a sub-graph RunScope B (the table-test helper builds the scope chain). Asserts the carry-forward bag is empty.

3. Add `TestResolveAttributes_CarryForward_SourceBoundOverwrites` — verifies a source-bound property's substitution overwrites a carry-forward value of the same name. This is the spec's "source-bound substitution overlays on top" contract.

4. Add `TestResolveAttributes_CarryForward_DefaultIsFloor` — seeds a writeback `{model: "claude-opus-4-7"}`, then dispatches a node whose schema declares `model: { default: "claude-sonnet-4-5" }` and asserts the bag carries `claude-opus-4-7` (carry-forward beats default). The first dispatch in scope, with no carry-forward source, sees `claude-sonnet-4-5`.

5. Run `go test ./lib/runtime/... -count=1 -run CarryForward -v`.

---

## Pass 5: In-process executor transport + `kind:` sugar + `loop_counter` builtin

**Goal:** Add `"inproc"` as a third transport on `executor.ClientPool`, define `InProcessHandler` + `InProcessRegistry` + channel-backed `EventSink`, ship the `loop_counter` builtin under `lib/runtime/executor/builtin/loop_counter/`, add the `Kind string` field to `TemplateNodeDef` plus the registration-time sugar resolver, and wire all of this at supervisor startup so a template referencing `kind: loop_counter` registers and dispatches without external configuration.

**Scope:** Tasks 23–29.

**Falsifier:** `ClientPool::GetOrCreate`'s transport switch has no `"inproc"` case, OR no `InProcessHandler` interface exists in `lib/runtime/executor/`, OR `TemplateNodeDef` has no `Kind` field, OR a template node declaring both `kind:` and `executor:` is accepted by the template validator (it must be rejected), OR a template declaring `kind: loop_counter` cannot register without the operator also registering an external executor service for it, OR `loop_counter` is not in the supervisor's pre-registered inproc handler set at startup.

### Task 23: Add `InProcessHandler` / `EventSink` / `InProcessRegistry`

**Files:** `lib/runtime/executor/inproc_handler.go` (new), `lib/runtime/executor/inproc_registry.go` (new), `lib/runtime/executor/inproc_client.go` (new), `lib/runtime/executor/inproc_client_test.go` (new)

**Steps:**

1. `inproc_handler.go`:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package executor

   import (
       "context"

       genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
   )

   // EventSink is the in-process equivalent of a gRPC server-stream. The
   // handler emits ExecuteEvents (heartbeats, named events, and exactly
   // one StreamClose) by calling Send. Send returns an error when the
   // sink is closed (e.g. supervisor abandoned the dispatch); handlers
   // can ignore that error and return — the dispatch loop reaps cleanly.
   type EventSink interface {
       Send(*genv1.ExecuteEvent) error
   }

   // HandlerContext bundles per-dispatch metadata + dependencies the inproc
   // handler may need. Threaded through the channel-backed dispatch
   // boundary so handlers can call runtime-side helpers (the scratch
   // writer; future helpers as the inproc surface grows). Opaque to gRPC
   // / HTTP-bridge dispatches.
   //
   // @concept: executor
   type HandlerContext struct {
       Scratch *ScratchWriter
   }

   // InProcessHandler is the Go interface utility executors implement.
   // Shape-matched to Executor.Execute's server-streaming method but
   // idiomatic Go: emit events via the sink, return nil on success or an
   // error the InProcessClient surfaces as an error terminal.
   //
   // Handlers MUST emit exactly one StreamClose event and then return.
   // Returning without emitting StreamClose, or emitting more than one,
   // is a programmer error the dispatch loop reports as
   // `stream_closed_without_terminal`.
   //
   // @concept: executor
   type InProcessHandler interface {
       Execute(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error
   }
   ```

2. `inproc_registry.go`:

   ```go
   package executor

   import (
       "fmt"
       "sort"
       "sync"
   )

   // InProcessRegistry maps inproc executor identity (the URL string,
   // conventionally `inproc://<name>`) to the registered InProcessHandler.
   // Constructed explicitly at supervisor startup (no init-time
   // self-registration globals); passed into the ClientPool factory.
   //
   // @concept: executor
   type InProcessRegistry struct {
       mu       sync.RWMutex
       handlers map[string]InProcessHandler
   }

   func NewInProcessRegistry() *InProcessRegistry {
       return &InProcessRegistry{handlers: map[string]InProcessHandler{}}
   }

   func (r *InProcessRegistry) Register(url string, h InProcessHandler) error {
       r.mu.Lock()
       defer r.mu.Unlock()
       if _, exists := r.handlers[url]; exists {
           return fmt.Errorf("InProcessRegistry: duplicate registration for %q", url)
       }
       r.handlers[url] = h
       return nil
   }

   func (r *InProcessRegistry) Lookup(url string) (InProcessHandler, bool) {
       r.mu.RLock()
       defer r.mu.RUnlock()
       h, ok := r.handlers[url]
       return h, ok
   }

   func (r *InProcessRegistry) RegisteredURLs() []string {
       r.mu.RLock()
       defer r.mu.RUnlock()
       out := make([]string, 0, len(r.handlers))
       for url := range r.handlers {
           out = append(out, url)
       }
       sort.Strings(out)
       return out
   }
   ```

3. `inproc_client.go`:

   ```go
   package executor

   import (
       "context"
       "errors"
       "fmt"
       "io"

       "github.com/google/uuid"

       "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
       genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
   )

   // HandlerContextFactory builds the per-dispatch HandlerContext from
   // typed acquisition identifiers. The dispatch-time inproc glue parses
   // the proto request's DispatchId / NodeId fields into typed UUIDs ONCE
   // at the Execute entry point — parse failures surface as Execute
   // errors, never a silent zero-UUID context, since the supervisor
   // populates those proto fields from the acquisition's typed values at
   // buildExecuteRequest and a malformed id at this boundary is a runtime
   // invariant violation. The supervisor's startup binds this factory to
   // a closure over Persist/Queue/Blob/SpillThreshold (Pass 5 Task 27).
   type HandlerContextFactory func(dispatchID, nodeID shared.UUID) HandlerContext

   // InProcessClient is a Client implementation that dispatches to an
   // InProcessHandler registered in the supervisor's InProcessRegistry.
   // The handler runs on a goroutine; events flow through a buffered
   // channel-backed EventStream. The dispatch loop's Recv / Close
   // semantics are identical to the gRPC client.
   //
   // @concept: executor
   type InProcessClient struct {
       registry *InProcessRegistry
       url      string // inproc executor URL, e.g. "inproc://loop_counter"
       newHctx  HandlerContextFactory
   }

   // NewInProcessClient returns a Client backed by an InProcessHandler. The
   // newHctx hook lets the supervisor seed per-dispatch HandlerContext
   // dependencies (ScratchWriter wired to the dispatch row).
   func NewInProcessClient(endpoint Endpoint, registry *InProcessRegistry, newHctx HandlerContextFactory) (Client, error) {
       if endpoint.Transport != "inproc" {
           return nil, fmt.Errorf("executor.NewInProcessClient: transport=%q not inproc", endpoint.Transport)
       }
       if registry == nil {
           return nil, errors.New("executor.NewInProcessClient: registry required")
       }
       if _, ok := registry.Lookup(endpoint.URL); !ok {
           return nil, fmt.Errorf("executor.NewInProcessClient: no handler registered for %q", endpoint.URL)
       }
       return &InProcessClient{registry: registry, url: endpoint.URL, newHctx: newHctx}, nil
   }

   func (c *InProcessClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error) {
       h, ok := c.registry.Lookup(c.url)
       if !ok {
           return nil, fmt.Errorf("InProcessClient.Execute: no handler for %q", c.url)
       }
       // Parse the typed UUIDs ONCE at this boundary. The supervisor
       // populates these proto fields from the acquisition's typed values
       // at buildExecuteRequest; a parse failure here is a runtime
       // invariant violation, not user input — surface it as an Execute
       // error rather than a silent zero-UUID HandlerContext.
       dispatchID, err := uuid.Parse(req.DispatchId)
       if err != nil {
           return nil, fmt.Errorf("InProcessClient.Execute: parse dispatch_id %q: %w", req.DispatchId, err)
       }
       nodeID, err := uuid.Parse(req.NodeId)
       if err != nil {
           return nil, fmt.Errorf("InProcessClient.Execute: parse node_id %q: %w", req.NodeId, err)
       }
       hctx := HandlerContext{}
       if c.newHctx != nil {
           hctx = c.newHctx(shared.UUID(dispatchID), shared.UUID(nodeID))
       }
       // Buffered channel + close-on-handler-return is the EOF protocol.
       // Buffer of 16 covers heartbeat/named-event bursts without blocking
       // typical handler loops; a deeper buffer would mask handler bugs
       // (the dispatch loop is supposed to drain at gRPC-stream cadence).
       ch := make(chan *genv1.ExecuteEvent, 16)
       errCh := make(chan error, 1)
       sink := &channelSink{ch: ch}
       go func() {
           defer close(ch)
           err := h.Execute(ctx, req, sink, hctx)
           if err != nil {
               errCh <- err
           }
           close(errCh)
       }()
       return &inprocEventStream{ch: ch, errCh: errCh}, nil
   }

   func (c *InProcessClient) Close() error { return nil }

   type channelSink struct {
       ch chan<- *genv1.ExecuteEvent
   }

   func (s *channelSink) Send(ev *genv1.ExecuteEvent) error {
       select {
       case s.ch <- ev:
           return nil
       default:
           // Slow consumer: the gRPC analogue would block; we block too.
           // Re-attempt the send blocking so handler emission preserves
           // ordering. (The default-branch above only fires on full buffer;
           // by re-sending blocking, the channel-backed sink behaves
           // identically to a gRPC server-stream's Send blocking on
           // backpressure.)
           s.ch <- ev
           return nil
       }
   }

   type inprocEventStream struct {
       ch    <-chan *genv1.ExecuteEvent
       errCh <-chan error
   }

   func (s *inprocEventStream) Recv() (*genv1.ExecuteEvent, error) {
       ev, ok := <-s.ch
       if !ok {
           // Channel closed — handler returned. If the handler returned an
           // error, surface it; otherwise EOF.
           if err, ok := <-s.errCh; ok && err != nil {
               return nil, err
           }
           return nil, io.EOF
       }
       return ev, nil
   }

   func (s *inprocEventStream) Close() error { return nil }
   ```

4. Update `client.go::ClientPool::GetOrCreate` (lines 101-132) to thread the registry + handler-context hook, and add the third case:

   ```go
   // Update the ClientPool struct (line 94-97) to carry the registry +
   // hctx hook:
   type ClientPool struct {
       mu       sync.Mutex
       clients  map[string]Client
       registry *InProcessRegistry
       newHctx  HandlerContextFactory
   }

   func NewClientPool() *ClientPool {
       return &ClientPool{clients: map[string]Client{}}
   }

   // NewClientPoolWithInProcess returns a pool with the inproc registry
   // wired. Production startup uses this; tests using only out-of-process
   // executors keep NewClientPool().
   func NewClientPoolWithInProcess(registry *InProcessRegistry, newHctx HandlerContextFactory) *ClientPool {
       return &ClientPool{
           clients:  map[string]Client{},
           registry: registry,
           newHctx:  newHctx,
       }
   }
   ```

   In `GetOrCreate`'s switch, add the third case after the existing `"http"` branch:

   ```go
       case "inproc":
           if p.registry == nil {
               return nil, fmt.Errorf("ClientPool: inproc transport requested but registry is nil")
           }
           c, err = NewInProcessClient(ep, p.registry, p.newHctx)
   ```

5. `inproc_client_test.go` — co-located unit tests:
   - register a no-op handler that emits one heartbeat + one Success StreamClose, dispatch via `InProcessClient.Execute`, assert Recv returns both events and then `io.EOF`.
   - register a handler that returns an error without emitting StreamClose; assert Recv returns the error after the channel closes.
   - register a handler that emits scratch on Success; assert the StreamClose event reaches the consumer with `Success.Scratch` populated.
   - assert `NewInProcessClient` returns an error for an unregistered URL.

6. Run `go build ./... && go test ./lib/runtime/executor/... -count=1 && go test ./lib/runtime/executor/... -race -count=3`.

### Task 24: Add `Kind` field to `TemplateNodeDef` + kind-alias map

**Files:** `lib/foundation/spec/template.go`

**Steps:**

1. In `TemplateNodeDef` (lines 113-230), add the `Kind` field immediately after `Type`:

   ```go
       Type string `yaml:"type" json:"type"`
       // Kind, when non-empty, is a shorthand for `executor: <alias>` resolved
       // at template registration via the static kind-alias map seeded
       // alongside the supervisor's InProcessRegistry. A node MUST declare
       // either Kind or Executor (or neither, for pure-cascade nodes), never
       // both — mixing is rejected at registration with
       // template_validation_failed. Per `concept:node` §"Kind sugar".
       //
       // @concept: node
       Kind        string `yaml:"kind,omitempty" json:"kind,omitempty"`
       Description string `yaml:"description,omitempty" json:"description,omitempty"`
       Executor    string `yaml:"executor,omitempty" json:"executor,omitempty"` // optional; empty = no executor
   ```

2. Run `go build ./...`.

### Task 25: Add the kind-alias map + sugar resolver to template validation

**Files:** `lib/graph/node/template_validator.go`, `lib/graph/node/kind_resolver.go` (new), `lib/graph/node/kind_resolver_test.go` (new)

**Steps:**

1. Create `kind_resolver.go`:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package node

   import "sync"

   // KindAliasMap resolves a template node's `kind:` value to the executor
   // identity registered for it. Populated at supervisor startup alongside
   // the InProcessRegistry — every inproc handler with a kind sugar gets
   // both a registry entry AND a kind-alias entry, so authors can write
   // either `kind: loop_counter` or `executor: <its-alias>` and both
   // resolve to the same handler. Unknown kinds are rejected at
   // registration with the same error class as unknown executors.
   //
   // @concept: node
   type KindAliasMap struct {
       mu      sync.RWMutex
       aliases map[string]string
   }

   func NewKindAliasMap() *KindAliasMap {
       return &KindAliasMap{aliases: map[string]string{}}
   }

   func (m *KindAliasMap) Register(kind, executorAlias string) {
       m.mu.Lock()
       defer m.mu.Unlock()
       m.aliases[kind] = executorAlias
   }

   func (m *KindAliasMap) Resolve(kind string) (string, bool) {
       m.mu.RLock()
       defer m.mu.RUnlock()
       alias, ok := m.aliases[kind]
       return alias, ok
   }
   ```

2. Add a new per-node validator function `validateKindDeclaration` to `lib/graph/node/template_validator.go`, shaped like the existing `validateExecutorDeclared` (at line 826) and `validateExecutorCoherence` (at line 784) sibling validators. Keep `validateExecutorDeclared` untouched — overloading it with kind-validation semantics would conflict with its narrow stated purpose ("rejects nodes that reference an executor not declared in the operator's executors block"). Body:

   ```go
   // validateKindDeclaration rejects nodes that declare both `kind:` and
   // `executor:` (the two are mutually exclusive) and nodes whose `kind:`
   // value resolves to no registered alias. The kind → executor
   // substitution itself is performed by the canonicalizer after
   // validation succeeds (the validator MUST NOT mutate the input spec —
   // the caller may hash the spec bytes for content-addressed identity).
   //
   // @concept: node
   func validateKindDeclaration(n TemplateNodeDef, base string, hooks RegistryHooks, res *ValidationResult) {
       if n.Kind == "" {
           return
       }
       if n.Executor != "" {
           res.Errors = append(res.Errors, ValidationError{
               Path: base + ".kind",
               Msg:  "node declares both kind and executor; pick one",
           })
           return
       }
       if hooks.KindAliases == nil {
           res.Errors = append(res.Errors, ValidationError{
               Path: base + ".kind",
               Msg:  fmt.Sprintf("kind %q is not registered (no kind aliases configured)", n.Kind),
           })
           return
       }
       if _, ok := hooks.KindAliases.Resolve(n.Kind); !ok {
           res.Errors = append(res.Errors, ValidationError{
               Path: base + ".kind",
               Msg:  fmt.Sprintf("kind %q is not registered", n.Kind),
           })
       }
   }
   ```

   Call the new validator from `ValidateTemplate` at line 284 (alongside `validateExecutorDeclared`) — add one line in the per-node dispatch loop:

   ```go
   validateKindDeclaration(n, base, hooks, &res)
   ```

3. Add `KindAliases *KindAliasMap` to the `RegistryHooks` struct (search for `type RegistryHooks` in the same package).

4. Locate the canonicalizer (search `grep -rn 'Canonicalize\|canonicalizeTemplate' lib/`). It mutates the spec after validation runs (e.g. it materializes sub-graph absorption per the spec). Add a step in the canonicalizer that walks each node, and when `Kind != ""`, sets `Executor = hooks.KindAliases.Resolve(Kind)`'s result. Drop the `Kind` field (set it to empty) so post-canonicalization the spec is in normal form and downstream registration code does not need to know about kind. Update the canonicalizer's `KindAliases *KindAliasMap` dependency the same way as the validator.

5. Co-located unit tests in `kind_resolver_test.go`:
   - `kind: loop_counter` + registered alias → canonicalized spec has `Executor: <alias>`, `Kind: ""`.
   - `kind: loop_counter` + `executor: foo` → ValidationError mentioning "declares both".
   - `kind: unknown_kind` → ValidationError "is not registered".
   - No `kind:` and no `executor:` → unchanged (pure-cascade nodes still admitted).

6. Run `go build ./... && go test ./lib/graph/node/... -count=1`.

### Task 26: Ship the `loop_counter` builtin handler

**Files:** `lib/runtime/executor/builtin/loop_counter/handler.go` (new), `lib/runtime/executor/builtin/loop_counter/handler_test.go` (new), `lib/runtime/executor/builtin/loop_counter/schema.go` (new)

**Steps:**

1. `schema.go` — exports the per-node schema fragment that the supervisor advertises via the executor-observability hook so registration-time validation works the same as for out-of-process executors:

   ```go
   package loop_counter

   // SchemaBytes returns the JSON Schema fragment for loop_counter's
   // attributes. Advertised through the supervisor's
   // ExpectedAttributesSchemaFor hook for the inproc loop_counter executor
   // so registration-time validation works exactly as for out-of-process
   // executors.
   func SchemaBytes() []byte {
       return []byte(`{
       "$schema": "https://json-schema.org/draft/2020-12/schema",
       "type": "object",
       "required": ["max"],
       "properties": {
           "max": { "type": "integer", "minimum": 1 },
           "count": { "type": "integer", "default": 0, "readOnly": true }
       },
       "additionalProperties": false
   }`)
   }

   // DeclaredEvents is the loop_counter handler's named-event vocabulary.
   func DeclaredEvents() []string { return []string{"loop", "done"} }

   // ExecutorAlias is the rimsky-side executor identity for the kind sugar.
   const ExecutorAlias = "rimsky.loop_counter"

   // KindName is the value template authors use as `kind: loop_counter`.
   const KindName = "loop_counter"

   // InProcURL is the executor.Endpoint.URL for loop_counter's inproc
   // registration.
   const InProcURL = "inproc://loop_counter"
   ```

2. `handler.go`:

   ```go
   package loop_counter

   import (
       "context"
       "fmt"

       "google.golang.org/protobuf/types/known/structpb"

       genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
       "github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
   )

   // Handler implements executor.InProcessHandler for the loop_counter
   // utility node. Reads `count` from incoming attributes (carry-forward
   // yields prior, or 0 on first dispatch in scope), reads `max` (required
   // by schema), increments, emits named event `loop` while
   // new_count < max else `done`, then closes the stream with a Success
   // outcome carrying attributes_delta { count: new_count } so the
   // supervisor persists the new count for next-dispatch carry-forward.
   //
   // @concept: executor
   type Handler struct{}

   func New() *Handler { return &Handler{} }

   func (h *Handler) Execute(ctx context.Context, req *genv1.ExecuteRequest, sink executor.EventSink, _ executor.HandlerContext) error {
       attrs := req.Attributes.AsMap()
       maxRaw, ok := attrs["max"]
       if !ok {
           return h.errorTerminal(sink, "attributes_schema_invalid", "max is required")
       }
       maxN, err := asInt(maxRaw)
       if err != nil {
           return h.errorTerminal(sink, "attributes_schema_invalid", fmt.Sprintf("max: %v", err))
       }
       if maxN < 1 {
           return h.errorTerminal(sink, "attributes_schema_invalid", "max must be >= 1")
       }
       var count int
       if v, ok := attrs["count"]; ok {
           if n, err := asInt(v); err == nil {
               count = n
           }
       }
       newCount := count + 1

       var eventName string
       if newCount < maxN {
           eventName = "loop"
       } else {
           eventName = "done"
       }
       if err := sink.Send(&genv1.ExecuteEvent{
           Event: &genv1.ExecuteEvent_NamedEvent{
               NamedEvent: &genv1.NamedEvent{Name: eventName},
           },
       }); err != nil {
           return err
       }

       deltaStruct, err := structpb.NewStruct(map[string]any{"count": float64(newCount)})
       if err != nil {
           return err
       }
       return sink.Send(&genv1.ExecuteEvent{
           Event: &genv1.ExecuteEvent_StreamClose{
               StreamClose: &genv1.StreamClose{
                   Outcome: &genv1.StreamClose_Success{
                       Success: &genv1.Success{
                           Changed:         true,
                           ChangeSummary:   fmt.Sprintf("count=%d/%d", newCount, maxN),
                           AttributesDelta: deltaStruct,
                       },
                   },
               },
           },
       })
   }

   func (h *Handler) errorTerminal(sink executor.EventSink, errClass, msg string) error {
       payload, _ := structpb.NewStruct(map[string]any{"message": msg})
       return sink.Send(&genv1.ExecuteEvent{
           Event: &genv1.ExecuteEvent_StreamClose{
               StreamClose: &genv1.StreamClose{
                   Outcome: &genv1.StreamClose_Error{
                       Error: &genv1.Error{ErrorClass: errClass, Payload: payload},
                   },
               },
           },
       })
   }

   func asInt(v any) (int, error) {
       switch x := v.(type) {
       case float64:
           return int(x), nil
       case int:
           return x, nil
       case int64:
           return int(x), nil
       default:
           return 0, fmt.Errorf("not a number: %T", v)
       }
   }
   ```

3. Co-located `handler_test.go`:
   - First-dispatch test: `attributes = {max: 3}`, no `count` carry-forward — assert the channel-collected events: one `NamedEvent{Name: "loop"}` + one `StreamClose{Success{AttributesDelta: {count: 1}}}`.
   - Mid-run test: `attributes = {max: 3, count: 1}` — assert NamedEvent name="loop" + Success delta {count: 2}.
   - Terminal-iteration test: `attributes = {max: 3, count: 2}` — assert NamedEvent name="done" + Success delta {count: 3}.
   - Missing-`max` test: assert Error{`attributes_schema_invalid`} terminal.

4. Run `go test ./lib/runtime/executor/builtin/loop_counter/... -count=1`.

### Task 27: Wire the inproc registry, kind-alias map, and loop_counter handler at supervisor startup

**Files:** `lib/runtime/supervisor.go`, `lib/runtime/runner.go`

**Steps:**

1. In `supervisor.go`, add startup wiring around the `executor.NewClientPool()` call (currently line 344). Replace it with the inproc-aware pool construction + registry seeding:

   ```go
   // Inproc registry: every utility handler in scope ships under
   // lib/runtime/executor/builtin/<name>/ and is explicitly imported +
   // registered here. The kind-alias map maps the template-facing kind
   // sugar to the executor identity each handler is registered under.
   inprocReg := executor.NewInProcessRegistry()
   kindAliases := node.NewKindAliasMap()
   if err := inprocReg.Register(loop_counter.InProcURL, loop_counter.New()); err != nil {
       return nil, fmt.Errorf("supervisor: register loop_counter: %w", err)
   }
   kindAliases.Register(loop_counter.KindName, loop_counter.ExecutorAlias)
   // Plumb the loop_counter alias as a static Resolver entry so the
   // dispatch path's Resolver.Resolve(loop_counter.ExecutorAlias)
   // returns the inproc endpoint.
   // Extend cfg.Resolver — see Step 2.

   // The factory matches the typed-UUID HandlerContextFactory signature
   // introduced in Task 23 — the InProcessClient parses req.DispatchId
   // and req.NodeId into typed UUIDs at its Execute entry point and
   // threads them in here. Each closure call binds a fresh ScratchWriter
   // wired to the current dispatch's row.
   newHctx := executor.HandlerContextFactory(func(dispatchID, nodeID shared.UUID) executor.HandlerContext {
       return executor.HandlerContext{
           Scratch: &executor.ScratchWriter{
               Persist:        cfg.Persist,
               Queue:          cfg.Queue,
               Blob:           cfg.Blob,
               SpillThreshold: cfg.BlobSpillThreshold,
               DispatchID:     dispatchID,
               NodeID:         nodeID,
           },
       }
   })
   clientPool := executor.NewClientPoolWithInProcess(inprocReg, newHctx)
   ```

   Add the imports: `loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"`, `"github.com/rimsky-ai/rimsky-core/lib/graph/node"`.

2. The `loop_counter.ExecutorAlias` must resolve to `Endpoint{Transport: "inproc", URL: "inproc://loop_counter"}` via the runtime's Resolver. There are a couple of approaches; the cleanest is to extend the `StaticResolver` at supervisor startup with an additional pre-populated entry. Specifically, after the existing `cfg.Resolver` construction site (search for the call site that builds the resolver — likely `executor.NewStaticResolver` in `cfg.Resolver`), merge the inproc entries:

   ```go
   // Seed the resolver with inproc executor aliases so the dispatch
   // path's Resolver.Resolve(alias) returns the inproc endpoint
   // without operator config.
   if seeder, ok := cfg.Resolver.(*executor.StaticResolver); ok {
       // The StaticResolver currently has no public Register method;
       // add one via a small constructor extension. See Step 3.
       seeder.Register(loop_counter.ExecutorAlias, executor.Endpoint{
           Transport: "inproc",
           URL:       loop_counter.InProcURL,
       })
   }
   // LateBindResolver wraps StaticResolver, so reaching the inner static
   // map is fine via the unwrap above. If the resolver is not a
   // *StaticResolver (e.g. a test fake), the seeding is skipped — tests
   // wire their own inproc registration when needed.
   ```

3. Add a `Register` method to `executor.StaticResolver` in `lib/runtime/executor/resolver.go`:

   ```go
   // Register adds a name→endpoint mapping after construction. Used by
   // supervisor startup to seed inproc builtin executor aliases.
   func (r *StaticResolver) Register(name string, ep Endpoint) {
       r.mu.Lock()
       defer r.mu.Unlock()
       r.m[name] = ep
   }
   ```

4. Plumb `KindAliases` into the template-registration code path. Search for the call site that calls `node.ValidateTemplate(spec, hooks)` (the registry hooks construction site is probably in `lib/foundation/registration/` or `lib/control/`). Pass `KindAliases: kindAliases` on the hooks. The canonicalizer is called from the same site; plumb the alias map there too.

5. Plumb the per-executor schema for `loop_counter` through the existing `ExpectedAttributesSchemaFor` hook so registration-time effective-schema validation works for kind-resolved templates. The hook is wired on `RunArgs` (line 161 of `callback.go` references `ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)`). Extend the hook at supervisor startup:

   ```go
   // If a custom hook exists, wrap it so the inproc executor schemas
   // shadow it. Otherwise wire a fresh one keyed on the inproc aliases.
   baseHook := cfg.ExpectedAttributesSchemaFor
   cfg.ExpectedAttributesSchemaFor = func(name string) ([]byte, bool) {
       if name == loop_counter.ExecutorAlias {
           return loop_counter.SchemaBytes(), true
       }
       if baseHook != nil {
           return baseHook(name)
       }
       return nil, false
   }
   ```

   Equivalent plumbing for `DeclaredEvents` (search for the hook that surfaces `Capabilities.declared_events` — likely a sibling hook on `RunArgs`). The implementer follows the existing hook's surface: `loop_counter.DeclaredEvents()` returns `["loop", "done"]`.

6. Run `go build ./... && go test ./lib/runtime/... -count=1`.

### Task 28: Update the runner_test fixture pool constructor

**Files:** `lib/runtime/runner_test.go` and any other test file using `executor.NewClientPool()`

**Steps:**

1. The existing `NewClientPool()` factory stays in place for tests that don't exercise inproc. Tests that need inproc dispatch use `NewClientPoolWithInProcess` with a test-scoped registry. No edit to `runner_test.go` is required unless its tests need to exercise the new inproc path — most likely the existing test fixtures still pass as-is.

2. Run `go test ./lib/runtime/... -count=1`.

### Task 29: Verify end-to-end registration of a `kind: loop_counter` template

**Files:** (unit test) `lib/graph/node/template_validator_test.go` (extend) or a new file `lib/graph/node/kind_e2e_test.go`

**Steps:**

1. Write a unit test that constructs a `TemplateSpec` with one node declaring `kind: loop_counter`, runs `ValidateTemplate` with `RegistryHooks{KindAliases: <seeded map>}`, asserts no errors, then calls the canonicalizer and asserts the canonicalized node has `Kind == ""` and `Executor == loop_counter.ExecutorAlias`.

2. Write a negative test: a node with both `kind: loop_counter` and `executor: foo` → ValidationError mentioning "declares both".

3. Run `go test ./lib/graph/node/... -count=1 -run Kind`.

---

## Pass 6: claude-agent `session_token` as carry-forward attribute

**Goal:** Add `session_token` as a `readOnly: true` property on `expectedAttributesSchema`, have `server.ts` thread it from incoming attributes into `resumeContext.sessionToken` (so existing CLI-resume code in `agent-run.ts` reads it), and have `agent-run.ts` write the current dispatch's `runId` to the `session_token` attribute via `attributes_set` on every terminal Success. No Go code in this pass.

**Scope:** Tasks 30–32.

**Falsifier:** `expectedAttributesSchema.properties.session_token` does not exist, OR `server.ts::runAndCallback` does not thread `attributes.session_token` into the `resumeContext.sessionToken` passed to `runAgent`, OR `agent-run.ts`'s terminal Success path does not call `onAttributesSet({session_token: runId})`.

### Task 30: Extend `expectedAttributesSchema` with `session_token`

**Files:** `lib/services/executors/claude-agent/src/expected-attributes-schema.ts`

**Steps:**

1. In the `expectedAttributesSchema.properties` block (currently lines 36-160), after the `user_prompt` property and before the `cli:` object property, add:

   ```typescript
       // session_token is the claude-agent-owned CLI session identity that
       // rides the rimsky attribute carry-forward mechanism. The executor
       // writes the current dispatch's runId here on every terminal
       // Success via the §12.5 attributes_set callback; rimsky's
       // self-state carry-forward (per concept:attribute's carry-forward
       // step) makes the value visible on the next dispatch of the same
       // node within the same RunScope. When non-empty on a fresh
       // ExecuteRequest, the executor launches the CLI with
       // `--resume <session_token>` so the prior conversation continues.
       // Sub-graph and fan-out RunScopes start fresh — carry-forward is
       // RunScope-bounded — so a sub-graph invocation of an agent template
       // begins a fresh CLI conversation.
       session_token: {
         type: "string",
         readOnly: true,
         default: "",
       },
   ```

2. Run `cd lib/services/executors/claude-agent && npm install && npm test -- expected-attributes-schema -- --run`. Existing schema-shape tests should still pass.

### Task 31: Thread `session_token` from attributes into `resumeContext.sessionToken` in `server.ts`

**Files:** `lib/services/executors/claude-agent/src/server.ts`

**Steps:**

1. In `runAndCallback` (lines 403-487), the existing `runAgent({...})` call (lines 429-458) reads `resumeContext: parseResumeContext(req.resume_context)` at line 457. The `req.resume_context` is the supervisor-provided ResumeContext from the Park path — that path stays as-is.

   The new attribute-driven path: when `req.resume_context` is absent (the typical non-park path) but `attributes.session_token` is non-empty, synthesize an equivalent resumeContext from the attribute. Replace the `resumeContext` line with:

   ```typescript
       resumeContext: resolveEffectiveResumeContext(
         parseResumeContext(req.resume_context),
         attributes,
       ),
   ```

   Add the helper at module scope (after `parseResumeContext`):

   ```typescript
   /**
    * Resolves the effective resume context for the current dispatch. The
    * supervisor-provided ResumeContext (req.resume_context) is the Park
    * path and wins when set. Otherwise, when the carry-forward
    * `session_token` attribute is non-empty, synthesize an attribute-
    * driven resume context so the CLI continues the prior conversation.
    *
    * Per the 2026-06-14 carry-forward design — the two paths are
    * independent: the Park path's session_token comes from the prior
    * Park terminal; the attribute path's session_token comes from the
    * prior dispatch's attribute writeback. Sub-graph invocations =
    * empty session_token = fresh CLI conversation.
    */
   function resolveEffectiveResumeContext(
     fromParkPath: { payload?: Uint8Array; sessionToken?: string; resumeReason?: string } | undefined,
     attributes: Record<string, unknown>,
   ): { payload?: Uint8Array; sessionToken?: string; resumeReason?: string } | undefined {
     if (fromParkPath && fromParkPath.sessionToken && fromParkPath.sessionToken.length > 0) {
       return fromParkPath;
     }
     const fromAttribute = stringOr(attributes.session_token, "");
     if (fromAttribute.length === 0) {
       return fromParkPath;
     }
     return {
       payload: new Uint8Array(),
       sessionToken: fromAttribute,
       resumeReason: "carry_forward",
     };
   }
   ```

2. Run `cd lib/services/executors/claude-agent && npm test -- server -- --run`.

### Task 32: Write the dispatch's `runId` to `session_token` on terminal Success in `agent-run.ts`

**Files:** `lib/services/executors/claude-agent/src/agent-run.ts`

**Steps:**

1. Locate the `onComplete` callback inside `runAgent` (the existing wiring lives around the terminal-resolve path that constructs the `kind: "complete"` outcome — the area near lines 840-852). Before the existing `attributesDelta` is constructed, merge `session_token: runId` into the delta so the supervisor's attribute writeback persists it as part of the terminal Success:

   ```typescript
       // session_token rides the attribute carry-forward mechanism so the
       // next dispatch within the same RunScope can `--resume <runId>`
       // and continue this CLI conversation. Per the 2026-06-14 carry-
       // forward design. Always written on terminal Success — overwriting
       // any prior carry-forward value with the current dispatch's runId
       // is the desired behavior (the latest dispatch's CLI session is
       // the one the next dispatch should resume).
       const effectiveBagWithSession: Record<string, unknown> = {
         ...effectiveBag,
         session_token: runId,
       };
       const committedDelta =
         Object.keys(effectiveBagWithSession).length > 0 ? effectiveBagWithSession : null;
   ```

   Replace the existing `committedDelta` derivation that used the un-augmented `effectiveBag`.

2. **Load-bearing property: every terminal Success must commit the session_token.** Without this write, a successful first dispatch would leave the carry-forward bag empty, and the next dispatch would launch a fresh CLI conversation — silently breaking STORY-claude-agent-session-resume. The implementer MUST NOT add a conditional gating the session_token write on "did the conversation actually start" or similar — write it unconditionally on Success. Park terminals are unaffected — the Park path already plumbs sessionToken through `ResumeContext`.

3. Run `cd lib/services/executors/claude-agent && npm install && npm test && npm run build`.

---

## Pass 7: Acceptance — STORY-attribute-carry-forward, STORY-loop-counter-cap, STORY-inproc-utility-executor, STORY-opaque-executor-scratch

**Goal:** Deliver the four in-process / scenario-level user-outcome stories with their proof artifacts. All four stories share the `test/scenarios/` harness bring-up, so they batch into one acceptance pass; each story still produces its own committed proof artifact with its own observable assertion.

**Acceptance pass — STORY-attribute-carry-forward, STORY-loop-counter-cap, STORY-inproc-utility-executor, STORY-opaque-executor-scratch.**

**Scope:** Tasks 33–37.

**Falsifier:** Any of the four proof files is missing, OR a proof file asserts only registration / schema (not behavior), OR the carry-forward proof uses a stub for the value-delivering component (the executor must be the real handler doing real work), OR the loop_counter proof asserts events fired but not their count and ordering (`loop` × 3 then `done` × 1), OR the opaque-scratch proof's "scratch bytes round-tripped verbatim" assertion is replaced with a registration-only check.

### Task 33: STORY-attribute-carry-forward proof — `test/scenarios/attributes/carry_forward_e2e_test.go`

**Files:** `test/scenarios/attributes/carry_forward_e2e_test.go` (new)

**Story:** STORY-attribute-carry-forward.

**Proof form (from spec):** demo — scenario test under `test/scenarios/` that runs a deterministic stateful node through three dispatches in one RunScope (observes `count` 0→1→2→3 via writeback + carry-forward), then invokes a sub-graph and observes a node in the sub-graph sees the schema default.

**Steps:**

1. Author a scenario test that:
   - Stands up the full scenario harness via the existing pattern (see `test/scenarios/attributes/*_test.go` for examples — uses `test/support/scenario` to boot a real postgres via testcontainers and a real supervisor).
   - Registers a minimal template with one node `kind: loop_counter, max: 999` whose subscriptions trigger three sequential dispatches in the same RunScope.
   - **Asserts the incoming attribute bag of each dispatch**, which exercises the carry-forward READ path (not just the writeback persistence). Use the existing observability surface (`/v1/observability/node-runs/{id}` returns the per-run substituted attribute bag, including carry-forward overlays — search `grep -rn 'observability/node-runs\|substituted_fields' lib/runtime/` to confirm the read site) OR a test-side instrumentation hook on the dispatch path that snapshots `acq.MergedAttributes` per run. Required assertions:
     - Dispatch #1's incoming bag carries `count: 0` (the schema default — no prior writeback in scope).
     - Dispatch #2's incoming bag carries `count: 1` (carry-forward from dispatch #1's writeback).
     - Dispatch #3's incoming bag carries `count: 2` (carry-forward from dispatch #2's writeback).
   - Asserts the `rimsky_node_attributes.data` writeback row for each of the three runs carries `count: 1, 2, 3` respectively (the loop_counter's `attributes_delta` on each Success).
   - Adds a sub-graph invocation step: registers a child template referencing the same node-type and asserts the first dispatch within the sub-graph RunScope sees `count: 0` in its incoming bag (the schema default, not the parent scope's prior writeback), proving carry-forward did NOT cross the RunScope boundary.

2. The test drives the real supervisor (via `test/support/scenario`); the value-delivering component is the real `loop_counter` handler from Pass 5. No stub replaces it.

3. Run `go test ./test/scenarios/attributes/... -count=1 -run CarryForward -v` (Docker socket required for testcontainers postgres).

### Task 34: STORY-loop-counter-cap proof — `test/scenarios/loop_counter_cap_e2e_test.go`

**Files:** `test/scenarios/loop_counter_cap_e2e_test.go` (new)

**Story:** STORY-loop-counter-cap.

**Proof form (from spec):** demo — scenario test wiring loop_counter (max=3) to a sink subscriber on `loop` and a different sink on `done`; observes `loop` fires three times then `done` fires once.

**Steps:**

1. Author a scenario test that registers a template with three nodes:
   - `counter` (`kind: loop_counter, max: 3`) — the production loop_counter handler from Pass 5.
   - `loop_sink` — subscribes to `counter`'s `loop` named event. Use a test-only inproc handler that records each dispatch's invocation in a test-side counter (this is a downstream observer, NOT a stub of the loop_counter itself — the loop_counter is the value-delivering component and stays real).
   - `done_sink` — subscribes to `counter`'s `done` event. Same shape as `loop_sink`.

2. Cycle the counter three times by wiring the subscriber cascade so each `loop` event re-fires `counter` in the same RunScope.

3. Assert: `loop_sink.invocations == 3`, `done_sink.invocations == 1`, in that order (the third loop_counter invocation transitions to emitting `done`, not `loop`). Also assert the `rimsky_node_attributes.data` rows show `count = 1, 2, 3` across the three counter runs.

4. Run `go test ./test/scenarios/... -count=1 -run LoopCounter -v`.

### Task 35: STORY-inproc-utility-executor proof — automated example + scenario runner

**Files:** `examples/inproc-loop-counter/template.yml` (new), `examples/inproc-loop-counter/README.md` (new), `test/scenarios/inproc_utility_executor_e2e_test.go` (new)

**Story:** STORY-inproc-utility-executor.

**Proof form (from spec):** example — a minimal template referencing `kind: loop_counter` with no external executor configured for it; the template registers and runs to completion in a deployment with no utility-executor service.

The example is the human-readable artifact (template + README a reader can study), and the co-located scenario test (under `test/scenarios/`, the in-process supervisor harness that uses real postgres via testcontainers but does NOT require `rimsky-all-in-one:latest` to be pre-built) drives a real supervisor through the registration → dispatch → terminal flow with **no operator-side executor configuration for `loop_counter`**. This way the artifact stays alive — silent rot breaks the test in CI rather than going undetected — without adding an image build dependency to Pass 7.

**Steps:**

1. Author `examples/inproc-loop-counter/template.yml` — the smallest possible template referencing `kind: loop_counter`:

   ```yaml
   name: inproc-loop-counter-demo
   version: 1.0.0
   frame_resolution_mode: coalesce
   nodes:
     - type: counter
       kind: loop_counter
       attributes:
         schema:
           type: object
           properties:
             max:
               type: integer
               default: 3
   ```

2. Author `examples/inproc-loop-counter/README.md` walking a reader through what the example demonstrates — STORY-inproc-utility-executor's "no external executor needed" outcome.

3. Author `test/scenarios/inproc_utility_executor_e2e_test.go` — the automated scenario runner. Pattern: follow an existing scenario test under `test/scenarios/` that uses `test/support/scenario` for in-process supervisor bring-up + testcontainers postgres. Steps:
   - Stand up the supervisor via `test/support/scenario` with **no external `loop_counter` executor configuration** (the harness's `cfg.Resolver` does not pre-register one; the inproc registry seeded at supervisor startup per Pass 5 Task 27 is the only resolution path for `kind: loop_counter`).
   - Read the YAML at `examples/inproc-loop-counter/template.yml` from disk.
   - Register the template via the supervisor's in-process template-registration call (the same path the HTTP `POST /v1/templates` route uses). Assert it registers without error — the `kind: loop_counter` sugar resolved locally to the inproc executor alias.
   - Create an instance and wait for the counter's `done` event to surface on the events feed within a bounded timeout.

4. Run `go test ./test/scenarios/... -count=1 -run InprocUtilityExecutor -v` (requires a working Docker socket for testcontainers postgres; the existing scenario suite has the same precondition).

### Task 36: STORY-opaque-executor-scratch proof — Go executable test

**Files:** `test/scenarios/scratch_round_trip_e2e_test.go` (new)

**Story:** STORY-opaque-executor-scratch.

**Proof form (from spec):** proof — Go-side executable test exercising the round-trip: executor writes scratch (mid-dispatch via the scratch callback route, or via stream-close attach); enqueue a follow-on dispatch row with the prior-dispatch link (using the same mechanism the cascade re-dispatch / stale-heartbeat recovery / retry-after-error paths use); assert the new dispatch's request carries the original scratch bytes verbatim.

**Steps:**

1. Author a scenario test that:
   - Stands up the real supervisor + persistence via `test/support/scenario`.
   - Registers a fake inproc executor (test-only) that on first dispatch writes scratch bytes `[]byte{0xDE, 0xAD, 0xBE, 0xEF, ...random suffix...}` via stream-close attach, then exits with Success.
   - Forces a recovery transition that creates a new dispatch row carrying `prior_dispatch_id` (the cleanest path is to simulate a stale-heartbeat sweep — drive `SweepStaleHeartbeats` via the same path the runtime uses; alternatively trigger a retry-after-error via the test-side executor returning an Error class the template's policy routes to retry).
   - On the second dispatch, asserts the incoming `ExecuteRequest.Scratch` field equals the bytes the first dispatch wrote — byte-for-byte.

2. Run the test under three prior-dispatch dispositions in a table-driven form: `heartbeat_stale`, `retry_after_error`, `recalculate`. Each variant exercises a different recovery enqueue site (Pass 2 Task 12). All three must pass — the assertion's load-bearing property is "the bytes round-trip across every disposition."

3. Add a second test that exercises the mid-dispatch HTTP callback path: the fake executor POSTs `[]byte{...}` to `${callback_url}/v1/runs/{run_id}/scratch` mid-dispatch, completes Success without re-attaching, then a heartbeat-stale recovery fires; the second dispatch's request must carry the bytes POSTed via the callback.

4. Run `go test ./test/scenarios/... -count=1 -run ScratchRoundTrip -v`.

### Task 37: Verification — run the full test sweep affected by Passes 1–7

**Files:** none (verification only).

**Steps:**

1. Run the union of verification commands for Passes 1–7:

   ```
   go build ./...
   go test ./... -count=1
   make lint
   make proto-gen   # confirm regen is deterministic (no diff)
   go test ./test/scenarios/... ./lib/foundation/persistence/... -count=1
   go test ./lib/foundation/persistence/postgres/... ./lib/runtime/... ./lib/graph/scheduler/... -race -count=3
   ```

2. Confirm every command exits 0. If a command fails, debug and fix the root cause; do not paper over.

---

## Pass 8: Acceptance — STORY-claude-agent-session-resume

**Goal:** Deliver the claude-agent session-resume user outcome via the bundled-services integration harness under `lib/services/test/`, which boots a real rimsky stack (`rimsky-all-in-one:latest`) and drives claude-agent through three dispatches in one RunScope with CLI continuity, then a sub-graph invocation with a fresh CLI.

**Acceptance pass — STORY-claude-agent-session-resume.**

**Scope:** Tasks 38–40.

**Falsifier:** The new test under `lib/services/test/scenarios/` does not exist, OR the test uses a fake CLI that does not exercise the real `--resume <token>` path, OR the test does not assert each of the three in-scope dispatches sees the prior turn's context (a real semantic continuity check, not just "CLI was launched with --resume"), OR the test does not assert the sub-graph dispatch starts with a fresh CLI conversation.

### Task 38: Rebuild the affected images

**Files:** none (build commands).

**Steps:**

1. Run `make core-images` to rebuild `rimsky-all-in-one:latest` with the carry-forward + inproc + scratch code from Passes 2–5.

2. Run `make service-images` to rebuild `rimsky-store-filesystem:latest` (peer image used by the services harness) and the claude-agent image (which now embeds the Pass 6 changes).

3. Verify both target image tags exist locally: `docker images | grep -E 'rimsky-all-in-one|claude-agent|rimsky-store-filesystem'` confirms `:latest` tags are present.

### Task 39: STORY-claude-agent-session-resume proof — services integration test

**Files:** `lib/services/test/scenarios/claude_agent_session_resume_e2e_test.go` (new)

**Story:** STORY-claude-agent-session-resume.

**Proof form (from spec):** demo — scenario test using the bundled-services integration harness under `lib/services/test/` that runs claude-agent through three dispatches in one RunScope (agent's responses must reference content from prior turns), then invokes a sub-graph and observes the agent starts fresh.

**Steps:**

1. Use the existing `code:lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go` and `code:lib/services/test/scenarios/claude_agent_fake_cli/` as the pattern (both live under the `scenarios/` subdirectory, alongside the new test file). Boot `rimsky-all-in-one:latest` via testcontainers (the harness handles this). Use a deterministic fake CLI runner (the `claude_agent_fake_cli` helper) that returns scripted responses keyed on the prompt + `--resume` arg.

2. Register a template wiring `claude-agent` as the agent for a node, whose subscription re-fires the agent three times in the same RunScope (the wiring pattern mirrors Pass 7 Task 33's three-dispatch cascade).

3. Configure the fake CLI to:
   - First dispatch (no `--resume`): respond with "Turn 1: I am told my name is Alpha."
   - Second dispatch (`--resume <runId-of-first>`): assert the CLI launched with `--resume`. Respond with "Turn 2: I recall my name is Alpha."
   - Third dispatch (`--resume <runId-of-second>`): assert the CLI launched with `--resume`. Respond with "Turn 3: I still recall my name is Alpha."

4. Drive a sub-graph invocation as a fourth observation. Assert the fake CLI was launched WITHOUT `--resume` (fresh conversation).

5. Assert the on-disk attribute writes: after each dispatch the `rimsky_node_attributes` row for the agent node carries `session_token == <the runId of that dispatch>`. The 2nd and 3rd dispatches see the prior dispatch's `runId` as their incoming attribute. The sub-graph's first dispatch sees `session_token == ""` (the schema default).

6. Run `go test ./lib/services/test/scenarios/... -count=1 -run ClaudeAgentSessionResume -v`.

### Task 40: Verification — full final sweep

**Files:** none (verification).

**Steps:**

1. Run the full verification sweep:

   ```
   go build ./...
   go test ./... -count=1
   make lint
   make proto-gen
   go test ./test/scenarios/... ./lib/foundation/persistence/... -count=1
   go test ./lib/foundation/persistence/postgres/... ./lib/runtime/... ./lib/graph/scheduler/... -race -count=3
   go test ./lib/services/test/scenarios/... -count=1
   cd lib/services/executors/claude-agent && npm install && npm test && npm run build && cd -
   ```

2. Confirm every command exits 0.

3. Run the conformance executor against the modified executor surface to confirm wire compatibility:

   ```
   go run ./cmd/rimsky conformance executor --endpoint claude-agent --transport grpc
   ```

   Confirm it exits 0 (the scratch field is additive on the wire; conformance ignores fields it does not recognize).

---

## Manual checks after completion

None. Every user-outcome story is exhibited by a proof artifact (Tasks 33–36 + 39) that drives the real assembled product through the real delivery surface, and every load-bearing property (scratch round-trip, carry-forward RunScope-bounding, session-token write on terminal Success) has an automated verification.
