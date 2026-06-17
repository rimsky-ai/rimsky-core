# Executor Protocol Coherence Implementation Plan — Revised (post-Pass-1)

**Spec:** `.ok-planner/specs/2026-06-16-executor-protocol-coherence-design.md`
**Supersedes:** `.ok-planner/plans/2026-06-16-executor-protocol-coherence.md` Passes 2–3 only. Pass 1 (Tasks 1–22) of the original is landed; this revision picks up from there and reshapes the remaining work into a single pass.

**Why a revision.** The original split the remaining work into a Pass 2 (consumer migration + per-story acceptance ceremony) and a Pass 3 (a 25-task batch of design-doc mutations). Two structural issues with that:
1. The spec's `## Design changes` bullets describe state the code already has after Pass 1 — most of them are not paired with code work to do, they're reconciliations of design docs to existing code. Batching 25 of them into their own pass treats each as a first-class task; in reality they're mechanical applications of prescriptive text to named files. One task with the spec as the checklist suffices.
2. All four affected stories are intent-preserving B-shifts: the underlying user-observable outcome is unchanged, the phrasing shifts to match the new mechanism. The consumer migration IS the proof for three of the four (the existing scenario tests, rewritten to the new shape). Only STORY-executor-protocol calls for net-new authoring (the example walkthrough). A four-task acceptance pass over-ceremonies three intent-preserving shifts.

This revision interleaves design-doc mutations into the consumer task where the pairing is natural, batches the remaining catalog reconciliation into one task, and treats three of four proof artifacts as a step inside the consumer-migration task they belong to. Net: one pass, eight tasks, same coverage.

**Pass-1 state of the tree (don't redo this work).** `go build ./...` clean. `make lint` clean. `make test-all` green. Pass-1-deferred consumers are `t.Skip`-ed with forward pointers naming the revised tasks below:
- `code:lib/services/test/scenarios/claude_agent_session_resume_e2e_test.go` → waits on task R2.
- `code:lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go` → waits on task R2.
- `code:lib/services/test/scenarios/atomic_staging/pg_verifier_conformance_test.go::TestPGFusedStore_ExecutorConformance` → waits on task R3.

(Inventory the rest at task R4 step 1 — Pass 1 may have skipped more than these three.)

---

## Reading: orient on the spec before any task

Re-read `.ok-planner/specs/2026-06-16-executor-protocol-coherence-design.md` if its content isn't already in working memory. The spec's `## Design changes` section enumerates every concept / decision / story file mutation and the proto rename; task R7 below applies those bullets as prescribed text to named files. Spec `## Technical decisions` enumerates the 13 TDs; task R7 also creates the 13 corresponding decision files.

Honor project rules throughout: pre-v1 break-freely (`.claude/rules/rules.md`); after-code-changes verification block; Plumbline conventions (`.claude/rules/plumbline-cheatsheet.md`).

---

## Pass R: Consumer migration + design-doc reconcile

**Goal:** Migrate every remaining consumer of the old executor protocol (loop_counter, claude-agent, scenarios, conformance), rewrite the four affected stories' proof artifacts to exhibit the new mechanism, sweep retired-symbol references across the tree, and reconcile the `.ok-planner/design/` catalog (concept mutations, decision creations + mutations, story mutations, terminal-tag concept creation, named-event concept retirement) to the post-Pass-1 mechanism.
**Scope:** Tasks R1–R8.
**Falsifier:** `loop_counter`'s handler still emits a NamedEvent (or its `Execute` signature is still streaming) — OR `claude-agent`'s `Execute` still streams instead of returning AwaitAsyncCallback immediately — OR `claude-agent` still writes `Park.sessionToken` instead of `attributes_delta.session_token` — OR `code:lib/services/executors/claude-agent/src/server.ts::resolveEffectiveResumeContext` still has the dual-path code — OR any scenario test under `test/scenarios/` or `lib/services/test/scenarios/` is `t.Skip`-ed pointing at a Pass-2 task — OR `pkg:lib/protocols/conformance/executor/scenarios/` still contains `heartbeats.go` / `stream_close_without_terminal.go` / `terminal_is_last.go` — OR any of the four proof artifacts (executor-protocol example, loop-counter-cap scenario, opaque-executor-scratch executable proof, cascade-signal-blind table-driven proof) is missing, stubbed, or unannotated — OR any concept file under `.ok-planner/design/concepts/` still carries a reference to a retired surface (named-event, streaming Execute, heartbeats, ResumeContext, Park.payload, Park.session_token, `event/<name>` signal) — OR `concepts/named-event.md` is still in the live directory rather than `concepts/_retired/` — OR `concepts/terminal-tag.md` is missing — OR any of the 13 new decision files (one per spec TD) is missing — OR any of the four affected story files (`executor-protocol.md`, `loop-counter-cap.md`, `opaque-executor-scratch.md`, `cascade-signal-blind.md`) still carries pre-mutation Acceptance / Falsifier / Proof text — OR `make test-all`, `make lint`, or `cd lib/services/executors/claude-agent && npm test` fails after the pass's edits land.

---

### Task R1: Migrate `loop_counter` to tag-based Outcome + reconcile its design pair

**Files:** `lib/runtime/executor/builtin/loop_counter/handler.go`, `lib/runtime/executor/builtin/loop_counter/handler_test.go`, `lib/runtime/executor/builtin/loop_counter/schema.go`, `test/scenarios/loop_counter_cap_scenario_test.go`, `.ok-planner/design/decisions/loop-counter-shape.md`, `.ok-planner/design/stories/loop-counter-cap.md`.

**Steps:**

1. `handler.go`: replace the streaming-EventSink `sink.Send(...)` NamedEvent call with the new Outcome-returning unary shape. Compute the tag (`"loop"` while count < max, else `"done"`) and return `&genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{Changed: true, ChangeSummary: ..., AttributesDelta: <count delta>, Tags: ["loop" | "done"]}}}`. Update the handler signature to `Execute(ctx context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error)` per the in-process handler interface landed in Pass 1.
2. `schema.go`: rename `declared_events` → `declared_tags` in the observability registration. The tag set is `["loop", "done"]`.
3. `handler_test.go`: rewrite assertions to inspect the returned Outcome — Success with the expected tag and the expected `attributes_delta`. Drop any sink-mock or streaming-emit assertions.
4. `test/scenarios/loop_counter_cap_scenario_test.go`: this is also the STORY-loop-counter-cap proof artifact. Rewrite to use the new shape:
   - Boot rimsky-all-in-one via testcontainers.
   - Register a template with a loop-counter node configured `max: 3` and two downstream sink subscribers:
     ```yaml
     - subscribes: [{ node: loop_counter, type: terminal/success, when: "\"loop\" in payload.tags" }]
     - subscribes: [{ node: loop_counter, type: terminal/success, when: "\"done\" in payload.tags" }]
     ```
   - Drive cascade three iterations; assert the loop-sink dispatched three times, the done-sink dispatched once on the third iteration, and the carry-forward `count` attribute crossed dispatches correctly.
   - Add `// @story: loop-counter-cap` as the first comment line.
5. `decisions/loop-counter-shape.md`: apply the spec's `## Design changes` `decisions/loop-counter-shape.md` bullet (rewrites around tags, drops named-event references). Current-state-only — no audit trail.
6. `stories/loop-counter-cap.md`: apply the spec's story-mutation bullet (Acceptance / Falsifier / Proof rewritten to reference `terminal/success + tags` and the new scenario test as Proof). Frontmatter stays.
7. Verify: `go test ./lib/runtime/executor/builtin/loop_counter/... -count=1` clean; `go test ./test/scenarios/ -run LoopCounterCap -count=1 -v` clean.

---

### Task R2: Migrate `claude-agent` (TS) + reconcile its design quartet

**Files:** `lib/services/executors/claude-agent/src/server.ts`, `lib/services/executors/claude-agent/src/agent-run.ts`, `lib/services/executors/claude-agent/src/server.test.ts`, `lib/services/test/scenarios/claude_agent_session_resume_e2e_test.go`, `lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go`, `.ok-planner/design/decisions/claude-agent-session-attribute.md`, `.ok-planner/design/decisions/async-callback-outcome-oneof.md`, `.ok-planner/design/decisions/async-callback-post-json.md`.

The claude-agent migration profile per spec: async mode (returns AwaitAsyncCallback immediately, spawns CLI subprocess, POSTs verdict from subprocess), generous `max_quiet_period` (5 minutes), `max_runtime = 0`, session token rides attributes only.

**Steps:**

1. `server.ts`: rewrite the gRPC `Execute` handler from server-streaming to unary. Signature becomes `(call: ServerUnaryCall<ExecuteRequest, Outcome>, callback: sendUnaryData<Outcome>) => void`. The handler returns `AwaitAsyncCallback` with a fresh UUID `async_ack_id` immediately and spawns the CLI subprocess via `agent-run.ts::runAgent` in the background.
2. `server.ts::resolveEffectiveResumeContext` (~line 955): collapse the dual-path code. The function reads `req.attributes.session_token` only. If the function becomes a trivial pass-through, delete it and inline at callers.
3. `agent-run.ts`:
   - Remove `sessionToken: opts.runId` and `payload: new Uint8Array()` from the Park outcome construction. The Park fields are now `reason`, `resume_at`, `reason_note`, `reason_label`, `attributes_delta` (carrying `session_token: opts.runId`), `tags`, `scratch`.
   - Remove `resumeContext?.sessionToken` reads; replace with `req.attributes.session_token`.
   - Send the eventual settling Outcome via HTTP POST to `${callback_url}/v1/callback/${async_ack_id}` with an `AsyncCallbackBody` body carrying the chosen outcome variant. No `events` field.
   - Add keepalive: on each natural milestone (tool-call boundary, turn boundary), POST to `${callback_url}/v1/runs/${runId}/keepalive` with the `cancel_token` bearer. Debounce — batch to once per tool-call or once per 30s, whichever fires first.
   - Above the Park-construction site, write a JSDoc block: `/** Session token rides attributes_delta — never on the Park outcome itself (the field no longer exists). Reads on next dispatch come from req.attributes.session_token. */`
4. `server.test.ts`: rewrite to assert: `Execute` returns AwaitAsync immediately; the subsequent HTTP callback delivers the real Outcome; session_token reads/writes happen against attributes only; keepalive POSTs land. Use the camelCase `toValue`/`toStruct` helpers for `Struct` attribute encoding (the gRPC test-client gotcha noted in `file:CLAUDE.md`).
5. Update the claude-agent observability registration to declare tags instead of events. Inventory the existing `declared_events` set via `rg 'declared_events' lib/services/executors/claude-agent/` and rename each to `declared_tags`.
6. `cd lib/services/executors/claude-agent && npm install && npm test && npm run build`.
7. Rebuild the claude-agent service image: `make service-images` (the integration harness consumes locally-built images per `file:CLAUDE.md`).
8. Un-skip `lib/services/test/scenarios/claude_agent_session_resume_e2e_test.go` and `lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go` (remove the `t.Skip` lines). Run `go test ./lib/services/test/scenarios/ -run 'ClaudeAgentSessionResume|ClaudeAgentCrossStack' -count=1 -v` clean.
9. Apply the spec's design-doc bullets for `decisions/claude-agent-session-attribute.md`, `decisions/async-callback-outcome-oneof.md`, `decisions/async-callback-post-json.md`. Current-state-only.

---

### Task R3: Rewrite the executor conformance suite

**Files:** `lib/protocols/conformance/executor/runner.go`, `lib/protocols/conformance/executor/callback_receiver.go`, `lib/protocols/conformance/executor/await_terminal_test.go`, every file under `lib/protocols/conformance/executor/scenarios/`, `lib/services/test/scenarios/atomic_staging/pg_verifier_conformance_test.go`.

**Steps:**

1. Walk the ten existing scenario files under `lib/protocols/conformance/executor/scenarios/`:
   - **Delete** `heartbeats.go`, `stream_close_without_terminal.go`, `terminal_is_last.go` (no analogue under unary RPC; keepalive is HTTP and out of executor-conformance scope).
   - **Rewrite** `async_handoff.go`: assert AwaitAsyncCallback registers persistently and the callback arrives + drives the verdict.
   - **Rewrite** `attributes_serialization.go`: assert `attributes_delta` round-trips on Success AND Error AND Park.
   - **Rewrite** `cancel.go`: the cancel_token cancellation path now applies to the unary RPC context. Assert that cancellation surfaces as the RPC's error.
   - **Rewrite** `execute_happy_path.go`: assert the unary `Execute(req) → Outcome{Success}` round-trip succeeds with expected attributes_delta and tags.
   - **Rewrite** `malformed_attributes.go`: schema-validation rejection on the unary path; assert the Error outcome with documented `error_class`.
   - **Rewrite** `park_reason_emission.go`: Park outcome carries the right `reason` enum; subscribers see `terminal/park/<reason>` signal. Drop assertions about `Park.payload` / `Park.session_token`.
   - **Rewrite** `unknown_ack_id.go`: callback handler returns 404 / `unknown_async_ack_id` when an inbound POST carries an unknown ack_id.
2. Add three new scenario files:
   - `tags_round_trip.go`: emit Success with tags, assert downstream subscriber matches via CEL `in payload.tags`.
   - `attributes_delta_on_error_park.go`: emit Error and Park each with `attributes_delta`; assert persistence onto the dispatch row.
   - `async_callback_survives_restart.go`: kill and restart the supervisor between AwaitAsyncCallback registration and the callback POST; assert the callback still lands. Use the testcontainers harness's stop/start primitives — a same-process replay does not exercise the persistent-registry property.
3. `runner.go`, `callback_receiver.go`, `await_terminal_test.go`: drop heartbeat / stream-close handling.
4. Un-skip `pg_verifier_conformance_test.go::TestPGFusedStore_ExecutorConformance` (remove the `t.Skip` line). The test depends on `execute_happy_path` being in the passing set — verify the rewritten happy-path scenario lands there.
5. Run the conformance suite against a stub executor and against claude-agent:
   - `go run ./cmd/rimsky conformance executor --endpoint <stub> --transport grpc`
   - `go run ./cmd/rimsky conformance executor --endpoint <claude-agent> --transport grpc`
6. `go test ./lib/protocols/conformance/executor/... ./lib/services/test/scenarios/atomic_staging/... -count=1` clean.

**Load-bearing constraint** (state in the new `async_callback_survives_restart.go` scenario's GoDoc): the persistent-registry property is the whole point. A same-process replay would silently pass on an in-memory registry. The scenario MUST restart the supervisor container.

---

### Task R4: Rewrite remaining scenario tests + the two remaining proof artifacts

**Files:** Inventory under `test/scenarios/` and `lib/services/test/scenarios/`. Specifically `test/scenarios/opaque_executor_scratch_test.go` and `test/scenarios/cascade_signal_blind_test.go` are the two remaining story proof artifacts.

**Steps:**

1. Inventory every `t.Skip` left in the tree pointing at a Pass-2 task: `rg -n 't\.Skip\(' test/ lib/services/test/`. Tasks R2 and R3 cleared three; this step catches anything else Pass 1 forward-pointed.
2. For each, identify the protocol element it exercised (NamedEvent → tag; Heartbeat → keepalive or delete; ResumeContext → attributes carry-forward; old Park → new Park with attributes_delta) and rewrite. Un-skip on green.
3. STORY-opaque-executor-scratch proof — rewrite `test/scenarios/opaque_executor_scratch_test.go`:
   - Boot rimsky-all-in-one.
   - Register an executor that on dispatch reads `req.scratch`, writes it to `attributes_delta` as a sanity assertion, and returns Success with `scratch: <some new bytes>`.
   - Trigger the three re-dispatch paths: stale-recovery (via a synthesized `PRIOR_STALE_RECOVERY` disposition, e.g. force a `max_quiet_period` exceedance), retry-after-error (synthesize an Error with policy `retry`), recalculate (synthesize an upstream invalidate that re-fires the same node).
   - For each: assert the new dispatch's `req.scratch` carries the bytes the prior dispatch wrote.
   - Add `// @story: opaque-executor-scratch` as the first comment line.
4. STORY-cascade-signal-blind proof — rewrite `test/scenarios/cascade_signal_blind_test.go`:
   - Table-driven over the cascade-firing signal types: `terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`. Remove the `event/<name>` row.
   - Add a `terminal/success + tags` row that exercises the CEL `in payload.tags` filter.
   - For each row assert: (a) per-sender subscription dispatches; (b) cross-cutting (`instance: true`) subscription dispatches; (c) audit row in event log; (d) trailing-`*` prefix match.
   - Add `// @story: cascade-signal-blind` as the first comment line.
5. Apply the spec's design-doc bullets for `stories/opaque-executor-scratch.md` and `stories/cascade-signal-blind.md`. Current-state-only.
6. `go test ./test/scenarios/... ./lib/services/test/... -count=1` clean; no `t.Skip` remaining anywhere this plan touched.

---

### Task R5: Author the `executor-protocol` example walkthrough (STORY-executor-protocol proof)

**Files:** `examples/executor-protocol/custom_executor.go` (new), `examples/executor-protocol/README.md` (new), `examples/executor-protocol/run.sh` (new, optional wrapper), `examples/executor-protocol/docker-compose.yml` or equivalent boot artifacts, `.ok-planner/design/stories/executor-protocol.md`.

This is the one net-new proof artifact in the pass — the other three stories are intent-preserving B-shifts whose proof is the rewritten consumer/scenario tests above. STORY-executor-protocol's post-rewrite proof form is an Example: a shipped executor reference paired with a worked walkthrough that boots a running rimsky and exhibits each protocol surface end-to-end.

**Steps:**

1. Survey for any existing pre-spec proof: `rg '@story: executor-protocol' . examples/ docs/`. If present, refactor in place; otherwise create new under `examples/executor-protocol/`.
2. `custom_executor.go`: self-contained Go program that:
   - Implements the unary `Execute(ctx, req) → Outcome` RPC.
   - Declares two tags (`work_started`, `work_done`) via the `declared_tags` observability capability.
   - On `Execute`, runs a small piece of real work synchronously and returns `Outcome{Success{tags: ["work_started", "work_done"], attributes_delta: {result: "ok"}}}`.
   - Demonstrates the async path: a second node-type entry returns AwaitAsyncCallback, spawns a goroutine that briefly sleeps, then POSTs the callback body with Success.
   - Declares an `error_class: "demo_failure"` and a node that triggers it; the template's error-policy routes it.
   - Add `// @story: executor-protocol` as the first comment line.
3. `README.md`: worked walkthrough that:
   - States what the example demonstrates.
   - Names the boot commands (`docker compose up rimsky-all-in-one` + the example executor as a sidecar).
   - Walks template registration → instantiation → dispatch landing.
   - Shows the operator event log where `terminal/success` carries `payload.tags = ["work_started", "work_done"]` and a downstream `when: "work_done" in payload.tags` subscriber dispatches.
   - Shows the async-callback path delivering a verdict after a simulated supervisor restart.
   - Includes expected output snippets at each step.
   - Top-of-file: `<!-- @story: executor-protocol -->`.
4. Run the walkthrough end-to-end against a fresh rimsky stack. Capture observed output in the README. The example MUST exhibit real work (the executor's body does real work and returns real-looking attributes, not stubbed).
5. Apply the spec's `stories/executor-protocol.md` design-doc bullet. Current-state-only.

---

### Task R6: Sweep retired-symbol references + Plumbline annotation reconcile

**Files:** every file under `lib/`, `cmd/`, `examples/`, `test/` (sources only — exclude `.ok-planner/`, `vendor/`, `lib/protocols/proto/v1/gen/`, generated paths).

**Steps:**

1. `@source:` sweep — `rg '@source: .*(executor\.proto::(StreamClose|Heartbeat|NamedEvent|ExecuteEvent|ResumeContext))' .` across the tree. For each hit, repoint at the surviving symbol (e.g., `@source: executor.proto::StreamClose` → `@source: executor.proto::Outcome`). Plumbline's `source_validity` check must stay clean.
2. `@blessed-invariant 6` retirement — `rg '@blessed-invariant.*6\b' .`. The heartbeat-cutoff invariant is gone. Remove the annotations or migrate to a new invariant id if the spirit survives in the new sweep code. Update `.plumbline.json` blessed-invariant catalog accordingly.
3. New `@blessed-invariant` annotations for the load-bearing properties this spec introduces:
   - **AttributesDelta-with-verdict atomic commit** — at the runner-side site that writes the verdict + attributes_delta + tags in one tx (the Success/Error/Park handler paths in `runner_terminal_handlers.go` / `runner_terminal_park.go`).
   - **Persistent-async-registry survives restart** — at `lib/runtime/callback.go::handleCallback` where the dispatch lookup keys on the persisted `async_ack_id` column.
4. Add `@concept: terminal-tag` annotations at the runner-side site that handles tag persistence + cascade-fire (the post-Pass-1 Success/Error/Park terminal handlers). End-of-plan verification (`grep @concept: terminal-tag`) keys on at least one code-side anchor existing.
5. `rg 'NamedEvent|StreamClose|ExecuteEvent|Heartbeat\b|ResumeContext|parked_payload|session_token\b|declared_events|PRIOR_HEARTBEAT_STALE|heartbeat_stale' . --type go --type ts --type proto --type sql` should return zero hits in source (hits in `.ok-planner/`, `git log`, and `history/` paths are acceptable — those are point-in-time records).
6. `make lint` clean.

---

### Task R7: Reconcile the `.ok-planner/design/` catalog to the post-Pass-1 mechanism

**Files:** every file named by the spec's `## Design changes` section under `.ok-planner/design/concepts/`, `.ok-planner/design/decisions/`, `.ok-planner/design/stories/`, plus `.ok-planner/design/concepts.md`, `.ok-planner/design/decisions.md`, `.ok-planner/design/stories.md` TOCs.

The spec's `## Design changes` section IS the checklist for this task. Each bullet names a file and the prescriptive new text. Apply mechanically. Skip the bullets task R1, R2, R4, R5 already applied (the design-doc files paired with consumer migrations).

**Steps:**

1. **Concept mutations.** For each of the following concept files, apply the spec's `## Design changes` bullet's prescribed text. Frontmatter stays; body sections (Definition / Purpose / Boundaries / Invariants / Adjacent) get replaced where the bullet directs. Current-state-only (no `## Notes`, no audit trail, no "previously X" phrasing).
   - `concepts/executor.md`
   - `concepts/parked-state.md`
   - `concepts/signal.md`
   - `concepts/node-subscription.md`
   - `concepts/blob-backend.md`
   - `concepts/auto-terminal.md`
   - `concepts/supervisor.md`
   - `concepts/claim-handle.md`
   - `concepts/orphan-reaper.md`
   - `concepts/node-run.md`
   - `concepts/cascade.md`
   - `concepts/attribute.md`
   - `concepts/breakpoint.md`
   - `concepts/lineage-record.md`
   - `concepts/rimsky.md`
   - `concepts/terminal-resolution.md`
   - `concepts/inertness.md`
   - `concepts/event-log.md`
   - `concepts/observability.md`
   - `concepts/message.md`

2. **New concept: `concepts/terminal-tag.md`.** Create the file with the spec's `## Design changes` `terminal-tag-create` bullet body verbatim. Frontmatter:
   ```yaml
   ---
   concept: terminal-tag
   status: as-is
   aliases: []
   ---
   ```
   Body: Definition / Purpose / Boundaries / Invariants per the spec. Path-free.

3. **Retire `concepts/named-event.md`.** `git mv .ok-planner/design/concepts/named-event.md .ok-planner/design/concepts/_retired/named-event.md` (fallback to plain `mv` if the file is untracked). In the moved file, replace the body with the retirement note prescribed by the spec and change frontmatter `status:` from `as-is` to `retired`.

4. **Decision mutations.** Apply the spec bullet for each:
   - `decisions/scratch-protocol.md`
   - `decisions/scratch-column.md`
   - `decisions/scratch-recovery.md`
   (The other four decisions named in the original Task 61 — `loop-counter-shape`, `claude-agent-session-attribute`, `async-callback-outcome-oneof`, `async-callback-post-json` — rode with R1 / R2.)

5. **Create the 13 new decision files**, one per spec TD. For each, frontmatter:
   ```yaml
   ---
   decision: <slug>
   status: as-is
   aliases: []
   ---
   ```
   Body: `# <title>`, `## Choice`, `## Rationale`, `## Alternatives` (when the TD has alternatives), text copied verbatim from the TD's prescriptive content. Current-state-only.
   - `decisions/executor-unary-rpc.md` (TD-execute-rpc-unary)
   - `decisions/terminal-tags.md` (TD-collapse-named-event-to-tags)
   - `decisions/uniform-attributes-delta.md` (TD-attributes-delta-on-all-settling-terminals)
   - `decisions/no-resume-context.md` (TD-remove-resume-context)
   - `decisions/async-callback-persistent-registry.md` (TD-persist-async-callback-registry)
   - `decisions/three-dispatch-deadlines.md` (TD-three-dispatch-deadlines)
   - `decisions/keepalive-endpoint.md` (TD-keepalive-endpoint)
   - `decisions/writeback-bumps-progress.md` (TD-writeback-bumps-progress)
   - `decisions/tag-based-subscription.md` (TD-subscription-grammar-shift)
   - `decisions/no-event-substitution.md` (TD-remove-event-substitution-path)
   - `decisions/orphan-reaper-connection-state.md` (TD-orphan-reaper-no-heartbeat)
   - `decisions/claude-agent-attribute-only-session.md` (TD-claude-agent-session-attribute-only)
   - `decisions/prior-stale-recovery-rename.md` (TD-prior-stale-rename)

6. **Story file mutations.** Tasks R1, R4, R5 already applied the story file mutations for `loop-counter-cap.md`, `opaque-executor-scratch.md`, `cascade-signal-blind.md`, `executor-protocol.md`. Verify this step is a no-op — `rg` over the four story files for any pre-mutation phrasing should return clean. If anything was missed, apply the spec bullet now.

7. **Regenerate TOCs.** For each catalog whose directory was touched, refresh the TOC:
   - `concepts.md`: alphabetical list of every file under `concepts/` with a one-sentence definition; add a "Retired concepts" section listing `_retired/named-event.md` with the prescribed one-line description.
   - `decisions.md`: alphabetical list with a one-sentence summary.
   - `stories.md`: alphabetical list with a one-sentence summary.

8. Verify catalog cleanliness:
   - `rg 'NamedEvent|StreamClose|ResumeContext|parked_payload|session_token|event/<name>|@blessed-invariant 6|declared_events|heartbeat_stale|PRIOR_HEARTBEAT_STALE' .ok-planner/design/` → zero hits in body (frontmatter / TOC entries naming `_retired/named-event.md` are acceptable).
   - `bash plumbline .ok-planner/design/` → clean (the plumbline lint covers `@source:` validity in design docs; design docs cite by slug, so violations should be zero).

---

### Task R8: End-of-plan verification

**Files:** (verification only — no edits.)

**Steps:**

1. `go build ./...` — clean.
2. `make test-all` — fully green. No `t.Skip` left in anything this plan touched.
3. `make lint` — clean.
4. `cd lib/services/executors/claude-agent && npm test && npm run build` — green.
5. `go run ./cmd/rimsky conformance executor --endpoint <stub-executor> --transport grpc` — passes (run against a locally booted stub).
6. `make core-images && make service-images` — both build chains succeed.
7. Sweep for retired symbols across the tree:
   ```
   rg 'NamedEvent|StreamClose|ExecuteEvent|Heartbeat\b|ResumeContext|parked_payload|session_token\b|declared_events|@blessed-invariant 6\b|nodes\..*\.event\.|topic_kind = .event|PRIOR_HEARTBEAT_STALE|heartbeat_stale' . --type go --type ts --type proto --type sql --type md
   ```
   Acceptable hits only in `.ok-planner/specs/`, `.ok-planner/plans/`, `.ok-planner/history/` (point-in-time records). Source-tree hits → not done.
8. `rg '@story:' . --type go --type ts` — at least four annotated proof artifacts, one per affected story.
9. `rg '@concept: terminal-tag' . --type go` — at least one code-side anchor (terminal-tag concept must be enforced somewhere).

---

## Manual checks after completion

These cannot be expressed as runnable commands; walk them after the automated pass is green:

- **Restart-survival sanity check.** Boot a local rimsky-all-in-one. Register a template using claude-agent (or the new `examples/executor-protocol/` custom executor from R5) in async mode. Trigger a dispatch. While the dispatch is in `phase='active'` with an async_ack_id registered, kill the supervisor and restart. POST the callback. The eventual terminal lands on the dispatch row correctly. (R3's `async_callback_survives_restart` conformance scenario exercises this in CI; the manual walk is the human-eye confirmation.)
- **Operator-cancel of a pending callback.** Manually inspect `table:rimsky_node_runs` for a row with a registered async_ack_id; flip the row to a failed terminal via admin tooling (or direct SQL); confirm a subsequent callback POST for that ackID returns 404 / rejected_run_terminal rather than landing.
- **Keepalive cadence by eye.** Watch the claude-agent CLI produce keepalive POSTs at the configured cadence. The cadence should land in operator-friendly territory (every ~30s or per tool-call boundary), not flooding the supervisor with sub-second pings.

---

## Notes on the revision shape

For the reader picking this up cold: this revision collapses what the original plan structured as 41 tasks across two passes (Pass 2: Tasks 23–38; Pass 3: Tasks 39–63) into 8 tasks in one pass. The collapse is:

- **Design-doc mutations ride with the code task they describe.** R1 includes the loop-counter design quartet; R2 includes the claude-agent decision quartet; R4 includes the opaque-executor-scratch + cascade-signal-blind story mutations; R5 includes the executor-protocol story mutation. R7 then handles only the design-doc work that has no natural code-task pair — the infrastructure concept mutations describing Pass-1-landed runtime state, the 13 new decision files, the terminal-tag concept creation, and the named-event retirement.
- **Intent-preserving B-shifts don't get a separate acceptance pass.** Three of the four stories are vocabulary shifts of unchanged behavior; the rewritten consumer / scenario test IS the proof and rides in the same task. Only STORY-executor-protocol calls for net-new authoring (R5).
- **Sweep work batches by surface, not by file.** R6 is one annotation-sweep task; R7 is one design-catalog reconciliation task. The mechanical operation is "apply spec bullet to file" repeated; that doesn't warrant a separate task per file.

Coverage is identical to the original Passes 2 + 3 — every spec bullet, every TD, every story, every retired-symbol assertion in the falsifier maps to a step in R1–R8.
