# v3 Cleanup — Open-error / Open-outcome semantics + pick-policy excision

**Status:** Discussion notes from post-v3 review. To be developed into a
full spec in a new session.

---

## Issue 1 — `Open` error vs. "pool-empty" signal: rimsky shouldn't be guessing

**Status:** Resolved by `docs/specs/2026-04-30-stores-protocol-cleanup-design.md` (cycle landed 2026-04-30). The `OpenResponse` proto now carries a `oneof Acquired | Unavailable` discriminator; rimsky-side `acquireClaim` switches on the explicit outcome rather than inspecting all-empty bytes.

### What's there today

The v3 store-service protocol gives the substrate three ways to respond
to an `Open` RPC:

1. **Acquire success** — return `OpenResponse{address, payload, region}`
   with at least one field populated.
2. **"Pool-empty signal"** — return `OpenResponse{address: nil,
   payload: nil, region: nil}` (all three byte fields zero-length).
3. **Error** — return a non-nil gRPC error.

The first two are tagged in-band. There's no discriminator field; the
substrate signals "I have nothing for you" by zero-length-everything.

### Where the convention is documented

- **Spec §4.7 ("Pool-empty signal"):** "A `ClaimResult` with all three
  fields empty (zero-length bytes for address, payload, AND region) is
  the store's signal to rimsky that 'no item is available for this
  pick-policy claim.'"
- **`core/supervisor/runner_acquire.go::acquireClaim`** (lines ~357-359):
  ```go
  if len(cr.Address) == 0 && len(cr.Region) == 0 && len(cr.Payload) == 0 {
      return AcquiredLock{}, false, nil
  }
  ```
- **`core/store/types.go::ClaimResult`** docstring documents it.
- **`stores/postgres/store/store.go::openPickPolicy`** returns it on
  `pgx.ErrNoRows` from the items-table SELECT.
- **`stores/stub/store/store.go::Open`** returns it when the in-memory
  FIFO is empty.

### What goes wrong

#### 1. Vocabulary leak — "pool" is substrate-internal

"Pool" is a postgres-store concept (the items table backing a pick
policy). Rimsky has no notion of pools — it just sees claims. Calling
the wire signal "pool-empty" embeds substrate-implementation vocabulary
into the cross-cutting protocol. (See "Issue 2 — pick-policy excision"
below; this is the same class of leak.)

#### 2. Three-empty-bytes is a fragile in-band signal

The same shape could be returned by:
- A regional read claim where the substrate genuinely has no `payload`
  (returns address only) — but spec §4.7 says regional `r` returns at
  least an address, so this is "shouldn't happen if the substrate
  behaves." If a substrate has a bug and returns nil-nil-nil for a
  real claim, rimsky silently treats it as "not available" and the
  dispatch evaporates with no signal to the operator.
- A future substrate that has nothing to address (e.g., a
  side-effect-only claim) — would falsely signal "not available."

The signal is in-band with success values, with no explicit tag. That's
the structural fragility.

#### 3. Rimsky is asked to interpret bytes as state

The substrate knows whether this is "transient — try again" vs. "real
fault" vs. "claim legitimately not available, this is fine, retry on
next tick." Rimsky shouldn't have to read between the lines.

### Why the cycle-4 brief got it wrong

The cycle-4 review brief for issues 10/15 assumed an `Open` failure
would propagate as `RunNode` returning `(RunnerResult{Ran: false}, nil)`
— "no candidate ran, no error" — because the verify-before-run /
state-machine bail paths return that shape. The mental model was "Open
is part of acquisition; acquisition failure looks like 'no candidate
eligible right now.'"

Tracing the actual code path:

- `core/supervisor/runner_acquire.go::acquireClaim` (line ~349) returns
  `fmt.Errorf("acquireClaim: Open(...): %w", err)` verbatim on Open
  error.
- `acquireOneLock` → `tryAcquire` → `tryAcquireWithTx` → `acquireCandidate`
  all propagate the error.
- `core/supervisor/runner.go::RunNode` step 1 returns
  `RunnerResult{}, err`.

So the caller sees a non-nil `error`, `Ran` is false, and the
transaction did roll back. The rollback observable assertion (zero
`rimsky_lock_holders` rows) is correct in the test.

The dichotomy in the current code is:
- **"Pool-empty" signal** (all-bytes-empty) → `(Ran=false, nil)` — no
  alert.
- **Open error** (gRPC error or substrate-error) → `(Ran=false, err)` —
  bubbles up, supervisor logs it.

This dichotomy is correct for "the substrate is dead" cases (operator
should hear about it) and for "the substrate has nothing right now"
cases (the next scheduler tick retries silently). The mental gap is
just that the cycle-4 brief didn't distinguish the two paths.

### What the protocol should say instead

The substrate's `Open` response should be one of three explicit
outcomes:

- **`acquired`** — got a claim. Returns `(address, payload, region)`.
- **`unavailable`** — substrate has nothing to give right now; this is
  normal; rimsky should retry on the next scheduler tick without
  alerting.
- **`error`** — substrate-side fault; rimsky surfaces it to the
  operator via the supervisor's logging / metrics layer.

The "transient retry policy" hint idea is over-specified for the
protocol — the substrate either says "try again later" (unavailable)
or "this is broken" (error). The *when* of retry is rimsky's policy
concern (next scheduler tick, with whatever backoff applies). The
substrate doesn't need to tell rimsky how long to wait. But it does
need to tell rimsky *whether* this is a "no work" signal vs. a "fault."

### Concrete protocol shape

Adding a discriminator to `OpenResponse`:

```proto
message OpenResponse {
  enum Outcome {
    ACQUIRED = 0;     // address/payload/region populated per substrate semantics
    UNAVAILABLE = 1;  // substrate has nothing right now; rimsky retries on next tick
    // ERROR is the gRPC status code path — not part of the response oneof
  }
  Outcome outcome = 1;
  bytes address = 2;
  bytes payload = 3;
  bytes region = 4;
}
```

Rimsky's supervisor verb becomes:

- `outcome == ACQUIRED` → proceed with INSERT + dispatch.
- `outcome == UNAVAILABLE` → roll back the rimsky tx, return
  `(Ran=false, nil)`. No error log; the candidate is just not eligible
  right now.
- gRPC error status → return error to RunNode caller; supervisor logs
  and the dispatch row stays unclaimed for retry.

### Cost / blast radius

- **Protocol change:** one enum field on `OpenResponse`. Backwards-
  incompatible if any third-party substrate has shipped, but in v3
  nobody has — the three reference impls under `stores/<kind>/` are
  the entire universe. Pre-v1, break freely.
- **Substrate updates:** ~5 lines each in postgres / stub `Open`.
  Filesystem doesn't have an "unavailable" case — it always returns
  `acquired` or errors.
- **Rimsky updates:** ~3 lines in `acquireClaim` (replace the
  all-empty check with an outcome check).
- **Spec update:**
  - §4.7 rewrites from "pool-empty signal" to "outcome enum"; drop
    the term "pool" entirely.
  - §3.3 ("store-internal capabilities") gets the language about
    "the substrate is the only entity that knows whether 'no claim
    right now' is normal or a fault" formalized.

### Recommendation

Worth doing as a focused mini-cycle. The protocol churn is small (1
enum field), the spec text gets cleaner (no more "all-bytes-empty is a
signal" tribal knowledge), and it removes a real bug class (substrate
returning nil-nil-nil due to a bug silently swallows a real claim).

Could brainstorm/spec/plan it as `docs/2026-04-29-open-outcome-enum.md`
— small enough to land in one cycle. Naturally pairs with the
pick-policy excision (Issue 2 below) since both are
substrate-vocabulary cleanups.

### Action

- [ ] Draft a brainstorm doc when ready to start the cycle.
- [ ] Spec amendment: §4.7 rewrite + §3.3 formalization.
- [ ] Implementation: proto enum, substrate updates, rimsky update,
      tests.
- [ ] Doc-side: drop "pool-empty" vocabulary from rimsky-facing docs;
      glossary entry for the outcome enum if useful.

---

## Issue 2 — Frame-engine multi-source observation: needs investigation, not yet a confirmed bug

### What the cycle-6 agent observed

While writing `test/scenarios/locks/regional_conflict_race_test.go`,
the natural test shape was: deploy one template with two root executor
nodes (worker-A, worker-B), both holding regional `rw` claims against
the same selector `/region-A`. Create one instance. Drive both
`RunNode` goroutines simultaneously, each with a different
`SupervisorID`. Assert exactly one wins.

That shape didn't drive deterministically under
`HarnessOpts{NoSupervisor: true, NoScheduler: true}`. After a single
manual `frame.RunTick(ctx, pool, log)` call:

- `rimsky_frames`: one row in `'running'` state,
  `source_node_ids = [worker-A.id, worker-B.id]`.
- `rimsky_nodes`: worker-A had `state='stale', frame_id=<frame>`.
  worker-B had `state='fresh', frame_id=NULL`.
- `rimsky_dispatch`: one row (for worker-A only).

The agent's first interpretation was: "the frame engine fans out only
the first source per tick." That interpretation was used to motivate a
workaround — rewrite the test to use two instances with one root each,
which made the test pass.

### Why the original interpretation may be wrong

The rimsky model says a node only goes `fresh → stale` via:

1. Timer / cron self-invalidation (scheduled nodes).
2. Cascade from a committed parent
   (`cascadeChildrenStaleInTx`).
3. Operator-originated invalidate (`scheduler.InvalidateNode`).
4. Heartbeat-lost / orphan-reap.
5. Force-fire admin route.

Frames **do not initiate staling**. Frames coordinate the cascade of
work that's already been triggered by one of the five paths above. The
spec at `docs/specs/2026-04-26-frame-resolution-design.md` describes
frames as gathering already-stale sources under a running-frame label.

If the original "fan-out" interpretation were correct, frames would be
auto-staling — which contradicts the model. So either:

1. **Frame-creation populated `source_node_ids` incorrectly.** A frame
   was created at instance-create time with both root executor IDs in
   `source_node_ids` even though only worker-A had been invalidated.
   That would be a frame-creation bug, not an `advanceOneFrame` bug.

2. **The harness's `CreateInstance` helper invalidated worker-A but
   not worker-B.** Only worker-A was a legitimate source. The frame
   may have been correctly constructed with
   `source_node_ids = [worker-A]` and the cycle-6 agent's report
   misread the data (e.g., looked at a different frame's
   `source_node_ids` than the one they thought). That would mean
   there's no bug — just a confused test setup.

3. **Instance-create time silently flips root executor nodes to
   stale.** If the instance factory does this to make the initial run
   happen, both worker-A and worker-B would have been stale at
   frame-creation time, and the symptom (only one source's `frame_id`
   set after `RunTick`) would still be an `advanceOneFrame` fan-out
   bug. **But this case also violates the "no auto-invalidation"
   rule** — the instance factory shouldn't be auto-staling either. If
   this is what's happening, it's a different (and arguably worse)
   bug than the frame engine.

Without re-reading `core/frame/engine.go::advanceOneFrame`,
`core/storage/postgres/instances.go` (instance-create path), and
`core/scenario/harness.go::CreateInstance` /
`waitForRootDispatch` / `driveFrameAndEnqueue`, the cycle-6 agent (and
this doc) cannot distinguish the three cases.

### Why the cycle-6 workaround doesn't address the underlying question

The race test was rewritten to use two instances with one root node
each. Two independent root frames, each with `source_node_ids =
[single]`; both flip cleanly. The assertion (single-writer-per-region)
still works because the two root nodes from two instances both target
the same selector against the same store, exercising the per-`(store,
region)` advisory lock from the cycle-4 fix.

The race test passes. But whatever the cycle-6 agent observed — be it
a real frame-engine bug, an instance-factory auto-invalidation bug, or
just a misread test setup — is still unaddressed.

### Where this could matter in production

- **If case 1 is right** (frame-creation populates `source_node_ids`
  with non-stale nodes): coalesce mode silently miscounts which nodes
  are part of the frame. Frame-end detection
  ("no `rimsky_nodes` rows in state stale or running for this
  instance") still works because the never-stale nodes remain fresh,
  so the frame ends correctly. But observability is wrong: the frame
  appears to have more sources than it actually advanced.

- **If case 2 is right** (test artifact): no production impact.

- **If case 3 is right** (instance-factory auto-staling root
  executors): violates the "no auto-invalidation" rule. Production
  effect depends on whether the instance factory has been doing this
  all along. If so, every newly-created instance immediately enters a
  cascade — which may be intentional (executors run on first
  instantiation) but contradicts the model the user just
  articulated. Worth understanding the design intent vs. the
  implementation.

### Recommended investigation

A focused 15-minute read pass:

1. `core/frame/engine.go::advanceOneFrame` — does it walk all
   `source_node_ids`, and what guard conditions does it apply to each?
2. `core/storage/postgres/instances.go` (or wherever instance-create
   lives) — does the factory flip any root nodes to stale at create
   time?
3. `core/scenario/harness.go::CreateInstance`,
   `waitForRootDispatch`, `driveFrameAndEnqueue` — does the harness
   itself invalidate roots, and if so, does it do so for *all* roots
   or just the first?
4. The cycle-6 race test as it actually landed at
   `test/scenarios/locks/regional_conflict_race_test.go` — what
   exactly was the agent inspecting and how was it instantiating the
   instance(s)?

That should disambiguate the three cases.

### Cost / blast radius

- **Investigation:** read pass + a focused test in
  `core/frame/engine_test.go` that pins multi-source advancement
  expectations. Half a session.
- **Fix:** depends on which case is right. Case 1 or 3 is likely a
  small SQL or factory-logic change. Case 2 is no fix at all.
- **Test coverage:** new `engine_test.go` test that pins multi-source
  advancement under the assumption that the spec's fan-out semantics
  are correct.
- **Spec text:** likely no change — spec already says what it says;
  the implementation either matches or doesn't.
- **No protocol change.** No deployment surface changes.

### Recommendation

This is **not** a v3 cleanup item per se — it surfaced during v3 work
but the bug (if it exists) is pre-existing in the frame engine or the
instance factory. Worth a separate focused
"investigate + fix + test" session under the "fix every bug you find"
project rule. If the investigation reveals case 2 (test artifact),
just close out with a note. If case 1 or 3, land a fix as its own
small cycle.

Capturing here in `v3-cleanup.md` because the v3 cycle is what
surfaced it; if the investigation reveals the bug is meaningfully
distinct in scope from the v3 cleanup work, split it into its own
`docs/2026-04-29-frame-engine-multi-source-bug.md` (or
`instance-factory-auto-stale.md`, depending on what the read pass
finds).

### Action

- [ ] 15-minute read pass across the four files listed above.
- [ ] Decide which of the three cases is the actual symptom.
- [ ] Land a `core/frame/engine_test.go` test that pins the desired
      multi-source behavior.
- [ ] Fix whichever code path is wrong (frame engine, instance
      factory, or scenario harness).
- [ ] Update this doc with the resolution.

---

## Issue 3 — Pick-policy excision: rimsky-side surface still leaks substrate vocabulary

**Status:** Resolved by `docs/specs/2026-04-30-stores-protocol-cleanup-design.md` (cycle landed 2026-04-30). `policy_override` is gone from `CommitRequest` / `AbandonRequest`; the `Delete` wire verb is gone (4+1 verbs total); `claim_resolutions` template grammar is gone (`node.ClaimResolution` deleted; `selectResolutionAction` and `fireResolutionVerb` deleted from `core/supervisor/auto_terminal.go`). Substrate disposition is governed entirely by per-substrate config.

### What's there today

The v3 spec §3.3 says pick-policy is store-internal:

> "Queue maintenance, items tables, pick policies, visibility-timeout
> sweeps, item seeding, staging cleanup — all store-internal."

But §4.5 contradicts that by defining a wire field whose entire
purpose is to carry pick-policy vocabulary across the rimsky↔store
boundary:

> "**`policy_override`.** Optional argument on `Commit` / `Abandon`.
> Store-internal vocabulary for stores that implement pick policies
> (`release_to_back`, `release_to_head`, `delete`, etc.). Stores that
> don't run pick policies ignore the field. Rimsky reads the value
> from the template's `claim_resolutions:` block on the acquirer node
> and passes it through; rimsky does not enumerate or validate the
> values."

§4.7 also leaks the term ("pool-empty signal" — see Issue 1).

### Where the leak lives in code

- **`proto/v1/store_service.proto`**: `CommitRequest.policy_override`
  and `AbandonRequest.policy_override` fields. Wire-level leak.
- **`core/store/types.go::ClaimSpec`** doc references
  "regional access vs. configured pick policy."
- **`core/supervisor/runner_acquire.go::acquireClaim`** doc mentions
  "pick-policy claims" and the substrate's `FOR UPDATE SKIP LOCKED`
  mechanism by name.
- **`core/supervisor/auto_terminal.go::fireResolutionVerb`** switch
  enumerates `release_to_back` / `release_to_head` / `delete` —
  pick-policy-specific action strings hardcoded in rimsky-side code.
- **`core/supervisor/auto_terminal.go::selectResolutionAction`** picks
  `r.OnCommit` on success vs. `r.OnGiveUp` on failure — the
  per-success/failure split is itself a pick-policy artifact (the
  substrate's Commit always means "successful terminal"; Abandon
  always means "failed terminal"; the substrate decides what those
  mean for its own state).
- **`core/supervisor/runner_terminal.go::releaseClaim`** passes
  `verbAction` through to `Store.Commit / Abandon` as
  `policyOverride`.
- **Template grammar**: `claim_resolutions[<alias>]` has separate
  `on_commit` and `on_give_up` fields (each carrying substrate-
  specific strings). Operator-facing template language carries
  pick-policy semantics through to the substrate via rimsky.
- **`docs/glossary.md`**: "pick policy" is a defined glossary term
  alongside claim / named lock / region — implying it's part of the
  rimsky vocabulary.
- **`CLAUDE.md`**, **`docs/architecture.md`** §1.2: rimsky-facing
  prose describes the postgres reference store as "supports regional
  access AND substrate-side pick policies."

### Why this is the same class as Issue 1

Issue 1 surfaced "pool-empty" as a leak — "pool" is a postgres-store
concept that the wire shouldn't name. Pick-policy excision is the
broader category: any substrate-internal mechanism (queues, rings,
visibility timeouts, stages, items tables) shouldn't appear in the
rimsky surface. The protocol carries claims, regions, and addresses;
substrates do whatever they want behind that surface.

### What full excision looks like

**Wire protocol:**
- `OpenRequest`: keep `(claim_id, store_name, selector, intent,
  alias)`. Selector remains opaque; the substrate parses it however
  it likes.
- `CommitRequest`: shrinks to `(claim_id, region, address)` — drop
  `policy_override`.
- `AbandonRequest`: shrinks to `(claim_id, region, address)` — drop
  `policy_override`.
- `DeleteRequest`: stays `(claim_id, region)`.
- `ReleaseRequest`: stays `(claim_id, region, address)`.

**Rimsky-side template grammar:**
- `claim_resolutions[<alias>]` shrinks or disappears entirely. The
  acquirer's success path always calls `Store.Commit`; failure path
  always calls `Store.Abandon`. The substrate decides what its
  Commit / Abandon mean for its own state — possibly indexed by
  selector or by items-table state, captured in the substrate's
  *own* config.
- For pick-policy stores: the substrate's per-policy config (e.g.,
  `pick_policies[*].on_commit_default` and `on_give_up_default` in
  the postgres store-service's own `config.yml`) is where
  per-policy behavior lives. If an operator wants conditional
  dispositions ("commit successful items but release-to-back
  failed-validation items"), that's captured by whether the
  executor reports success or failure — Commit vs. Abandon — and
  the substrate's config interprets each terminal accordingly.

**Source code:**
- `core/supervisor/auto_terminal.go::fireResolutionVerb` shrinks to
  three branches: `Delete` (regional-only), `Commit` (success), and
  `Abandon` (failure). No `release_to_*` strings.
- `core/supervisor/auto_terminal.go::selectResolutionAction` deleted
  (no per-claim resolution selection happens rimsky-side; success
  bool directly drives Commit-vs-Abandon).
- `core/node/template.go::ClaimResolution` struct deleted (or
  shrunk to nothing).
- `core/controlapi/templates.go` `claim_resolutions` JSON shape
  removed.
- `core/supervisor/runner_terminal.go::releaseClaim` passes nothing
  extra to `Store.Commit / Abandon`; just region + address.

**Spec text:**
- §4.5 deleted entirely.
- §4.7 — pool-empty discussion folds into the Open-outcome enum
  from Issue 1; "pool" vocabulary disappears.
- §13.1 — items-seeding admin endpoint mention stays (operator-
  author concern, substrate-side endpoint), but the framing makes
  clear that anything beyond claim+region+address is the
  substrate's own concern.

**Documentation:**
- `docs/glossary.md` — drop "pick policy" entry, or relabel it as
  substrate-internal vocabulary that does not appear in the rimsky
  protocol surface.
- `CLAUDE.md` and `docs/architecture.md` §1.2 — reword references
  to the postgres reference store away from "pick policies" toward
  "substrate-recognized special-form selectors" or similar
  rimsky-neutral phrasing.
- `docs/store-author-guide.md` — the postgres-store-specific section
  (when written) can use "pick policy" freely as a substrate-author
  concept; the rimsky-facing sections cannot.
- `docs/operator-guide.md` — the timing-constraint discussion
  (`visibility_timeout > 5 × heartbeat_interval`) stays as guidance
  for operators of *the postgres reference store-service* — labeled
  as such, not as a rimsky-level constraint.

### Cost / blast radius

- **Protocol change:** drops 2 fields. Pre-v1, free to break.
- **Substrate updates:** ~10 lines per substrate to drop the
  `policy_override` argument acceptance. The postgres store-service
  reads its action vocabulary from its own per-policy config
  defaults instead of from the rimsky-passed string.
- **Rimsky updates:** ~30 lines (`fireResolutionVerb` simplifies,
  `selectResolutionAction` deletes, `ClaimResolution` shrinks,
  template-deploy / controlapi / template_validator stop carrying
  `claim_resolutions`).
- **Template grammar change:** removes a field from the JSON
  template surface. Pre-v1, no production templates yet, free to
  break.
- **Spec amendment:** §4.5 delete, §4.7 rewrite (folds into
  Issue 1), §13.1 wording tightened, glossary updated.
- **Documentation cascade:** rimsky-facing docs drop "pick policy"
  vocabulary; substrate-author docs (when written) keep it.

### Blast radius vs. Issue 1

Bigger than Issue 1. Issue 1 is a one-field protocol cleanup with
substrate-side updates that are mechanical. Issue 3 touches the
template grammar (operator-author-visible), the wire protocol, the
auto-terminal vocabulary in rimsky source, and the doc surface
across CLAUDE.md / architecture.md / glossary.md / operator-guide.md
/ store-author-guide.md / spec. Worth doing as its own focused
spec-amendment cycle, naturally paired with Issue 1.

### Recommendation

Brainstorm + spec amendment + plan + implementation cycle. The
brainstorm should answer:

1. Does `claim_resolutions` survive at all? If so, what does it look
   like in the v3-corrected form? Most likely answer: deleted
   entirely, since the success-vs-failure decision is implicit in
   the executor's terminal event (Complete vs. Errored), and the
   per-substrate response is the substrate's own config concern.
2. How do operators of the postgres reference store-service express
   per-claim disposition preferences (when needed)? Probably:
   they don't — the postgres store-service's config has per-policy
   defaults, and that's the granularity. Per-claim conditional
   dispositions are out of scope for the reference impl. Custom
   substrates can implement whatever per-claim logic they want
   within Commit/Abandon, keyed on selector or claim state.
3. What's the migration story for any in-tree tests that use
   `claim_resolutions`? They get rewritten or deleted as part of
   the cycle.

### Action

- [ ] Brainstorm doc to confirm the shape (especially #1 above —
      deletes `claim_resolutions` entirely).
- [ ] Spec amendment: delete §4.5; revise §4.7 (paired with Issue 1's
      Open-outcome enum); tighten §13.1; update glossary.
- [ ] Implementation: proto field removal, template-grammar removal,
      `fireResolutionVerb` / `selectResolutionAction` simplification,
      substrate updates.
- [ ] Doc-side: pick-policy vocabulary out of rimsky-facing docs;
      retained only in substrate-author / postgres-store-specific
      docs.
- [ ] Tests: update / delete tests that exercise `claim_resolutions`.

---

## Other unfinished work (lower-priority follow-ups)

These items surfaced during v3 cycles but are smaller in scope or
already explicitly deferred. Listed here so the v3-cleanup successor
session has the full landscape.

### Held-claim aggregate-failed scenario test

`test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go`
covers the **commit** path end-to-end through the loopback gRPC
fixture (`TestAutoTerminalAggregateCommitEndToEnd`). The
**aggregate-failed** path is delegated to the existing unit test
`core/supervisor/auto_terminal_test.go::TestCheckAndFireResolution_AnyFailedFiresGiveUp`.

The unit-level test is sufficient for invariant 13 coverage; the
scenario-level commit test pins the wire path. Adding a scenario-
level aggregate-failed test would give symmetry but isn't required
for invariant coverage. Land if you want symmetric end-to-end
testing.

### `docs/store-author-guide.md` body rewrite

The v3 banner at the top points readers at the v3 spec, but the
body underneath is still v2 reference material (~700 lines of stale
`Factory` / `TxFromContext` / `RegionsConflict` prose). I previously
recommended holding off on this — synthesizing a v3 store-author
guide is a writing task, not a porting task, and rushing it risks
introducing inconsistencies. Better to land it deliberately when
there's time for thoughtful prose-writing, *after* the
pick-policy-excision cycle (Issue 3 above) so the guide doesn't
contradict the wire protocol again.

### Local-environment verifications (T55, T56, T57) — RAN 2026-04-30

All three tasks executed. Results below.

- **T55 — TS executor smoke**: PASS. `npm install && npm test && npm
  run build` clean; 32/32 vitest tests pass; `tsc` compiles without
  diagnostics.
- **T56 — Docker-compose stack health**: PASS. `bash
  deploy/build-images.sh` built all 9 images (4 rimsky binaries, 2
  executors, 3 store-services). `docker compose up -d` brought up
  9 containers (postgres healthy; migrate + init-items exited 0;
  scheduler / supervisor / control-api / http-node /
  claude-agent / store-filesystem / store-postgres up). `curl
  http://localhost:8080/health` returned the
  `{"status":"ok","supervisors":[{...,"accepted_executors":["claude-agent","http-node"],...}],...}`
  envelope.
- **T57 — Conformance probe**: MIXED.
  - **http-node**: PASS after fixing a pre-existing bug.
    `--require-stub-mode --endpoint http-node:9091 --transport
    grpc` reports **7 passed, 0 failed, 1 skipped** (`async_handoff`
    skipped because http-node is sync-only). Bug fixed:
    `executors/http-node/server.go::executeCore` validated
    `userdata.url` before the stub-mode short-circuit, so the
    suite's executor-agnostic scenarios (which all send
    `{stub_probe: true}` with no URL) errored out before reaching
    `executeStub`. Moved the stub-probe escape hatch ahead of URL
    validation; `TestStubMode_RejectsMalformedUserdata` (which
    omits `stub_probe`) still passes. Logged in CHANGELOG under
    Unreleased.
  - **claude-agent**: pre-existing structural gaps unrelated to
    v3, kept here for the cleanup successor:
    1. **Probe is sync-only; claude-agent is async-only.**
       `conformance/runner.go::probeStubMode` reads the gRPC
       stream looking for a synchronous `Complete` carrying
       `attributes_delta.stub: true`. claude-agent always responds
       with `Heartbeat + AsyncAccepted`, closes the stream, and
       POSTs the terminal via the supervisor's callback URL. So
       `--require-stub-mode` cannot work against any async-only
       executor. CLAUDE.md's "stub mode is required for conformance
       runs of LLM-calling executors" is therefore unenforceable
       today — the probe needs to either also handle the async
       callback path (spin up a tiny callback-receiver fake during
       the probe) or be split into sync-stub-probe vs.
       async-stub-probe variants.
    2. **claude-agent has no userdata-shape validation.** Without
       `--require-stub-mode` it gets 5 passed, 1 failed, 2 skipped
       — the failure is `malformed_userdata` (the suite expects an
       `Errored` terminal for a userdata shape with `_invalid` and
       `missing_url` keys; claude-agent silently runs with default
       prompts and never errors). Fixing this is an executor
       feature decision (what counts as malformed for an
       agent-style executor: empty `user_prompt_template`?
       missing required `attributes_schema`?).
  - **Net**: v3 didn't regress the executor surface — the http-node
    bug pre-dates v3 and the claude-agent gaps are pre-existing
    conformance-design questions. Neither is a v3-cleanup item;
    both are filed here so the successor session has the picture.

### Design-deferred items per spec §15

These were explicitly deferred during the v3 spec session, not
oversights. Listed for completeness:

- **mTLS / transport credentials** for `core/store/remote/dial.go`.
  Currently uses `insecure.NewCredentials()` unconditionally per
  spec §13.3 ("rimsky has no machinery for credentials, encryption,
  key management, or access control"). Operator-deployment auth
  (mTLS, service mesh, IAM) lives in the deployment layer. Adding
  in-process TLS knobs is a future cycle once a concrete operator
  pain point motivates it.
- **Store-side conformance probe.** No `rimsky-store-conformance`
  binary exists; spec §15 lists "Bridge framework / polyglot SDKs
  for store-service authors" and store-conformance as deferred to a
  future cycle (after v3 stabilizes and there are external store
  authors).
- **Bridge handler error-class mapping.**
  `stores/internal/bridge/bridge.go` currently maps every substrate
  error to HTTP 500 / gRPC Internal. Spec §15 defers finer-grained
  `status.Code` mapping (mapping store-side commit errors vs.
  transport errors vs. protocol errors per spec §5.3). Worth
  revisiting if/when an operator surfaces a real pain point.

### Branch / commit state

Nothing committed. All v3 + cycle-1-through-6 work is staged-or-
unstaged on `main`. User has opted to continue work on `main`.
