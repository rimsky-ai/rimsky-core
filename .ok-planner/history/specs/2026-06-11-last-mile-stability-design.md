# Last-mile stability — design

Date: 2026-06-11
Source sketches: `2026-06-10-last-mile-stability.md`,
`2026-06-10-subscribe-contention-hazard.md`,
`2026-05-23-unify-child-execution-sketch.md`,
`2026-06-11-v0.8.0-docs-reconcile-source-findings.md` (all archived to
`history/sketches/` on approval of this spec).

## Thesis

Rimsky's remaining instability is not scattered bugs: it is a small
number of places where the design allows two code paths to the same
outcome with parity maintained by hand, plus a set of places where the
wire contract or the comments promise behavior the runtime does not
deliver. This spec consolidates the duplicated paths, closes the
promise-vs-runtime gaps, and makes the test harness able to *see* the
failure class the consolidation work risks introducing — with the
harness work landing first (see TD-harness-first-ordering). It also
makes the all-in-one deployment what its env marker always claimed:
one process running all three roles (TD-single-process-mode) — the one
net-new capability in the spec, sanctioned because two existing
contracts (the unified-role marker and the memory blob backend's gate)
are false without it.

The reactive-nomenclature rework
(`tension:event-vocabulary-implies-delivery`, sketch
`2026-05-29-reactive-nomenclature-rework.md`) is not part of this spec;
that tension stays open and its sketch stays in `sketches/`.

This repo only. The sibling rimsky-docs project is not edited,
referenced, or relied on by any part of this spec.

## User outcomes

### STORY-subscription-mounting

As an operator deploying instances whose templates declare publishers,
I can observe each publisher subscription progress from `mounting` to
`active`, so that I know when my sensors are actually feeding the
instance — instead of trusting a create response that can silently mean
"failed."

**Acceptance:** The operator creates the instance and gets a fast
success response; inspecting the instance, they see each declared
subscription with its state; under publisher slowness or load the state
reads `mounting` and later flips to `active` without operator action,
after which the publisher's messages flow; a genuinely non-retryable
problem (e.g. a publisher name that is not registered) shows `failed`
with a reason.

**Falsifier:** A subscription that ends up unmounted with the operator
unable to see that from the instance surface — the silent-201 behavior
still exists; or `mounting` is observable but never reconciles without
operator intervention under conditions that should recover (publisher
merely slow or briefly down).

**Proof:** demo — against a running stack, create an instance whose
publisher is deliberately slow to respond; show the create returning
immediately, the subscription visibly `mounting`, the flip to `active`
once the publisher wakes, and the sensor's messages arriving.

### STORY-single-process-all-in-one

As an operator running the all-in-one deployment, I get one process
serving all three roles (scheduler, supervisor, control-api), so that
the deployment is genuinely unified — including the memory blob
backend working there, because the roles actually share a process.

**Acceptance:** Starting the all-in-one deployment (no role command)
runs migrations and then serves all three role surfaces from a single
OS process; a termination signal shuts all three down cleanly; with
the memory blob backend configured, blobs spilled by one role are
readable by the others and the orphan-blob sweep actually reaps them.
Single-role deployments (an explicit role command per container)
behave exactly as today.

**Falsifier:** The all-in-one deployment still runs the roles as
separate child processes; or the memory blob backend in all-in-one
loses blobs across role boundaries (sweep no-ops, cross-role reads
miss); or single-role deployments change behavior.

**Proof:** proof (executable) — an integration test boots the
all-in-one image, asserts a single rimsky process serves all three
role surfaces, drives a node to terminal, and round-trips a spilled
blob across roles under the memory backend.

### STORY-producer-error-passthrough

As an operator whose store or claim producer fails during an
API-triggered operation, I can read the producer's error class and
message in the API response, so that I can fix the underlying problem
from the response alone instead of grepping rimsky's logs.

**Acceptance:** The operator triggers an operation that causes their
producer to reject — e.g. a store rejecting an open because its backing
path is misconfigured → the API response carries the producer's error
class and message, under a status that distinguishes "your producer
rejected this" from "rimsky broke internally."

**Falsifier:** A producer failure that still surfaces as a bare
`500 Internal Server Error` with an empty or generic body — the
producer's transmitted error class discarded between the gRPC boundary
and the HTTP response.

**Proof:** demo — against a running stack with a real store, trigger a
producer rejection and show the API response carrying the producer's
own error class and message.

### STORY-validation-names-the-mode

As an operator registering a template whose service references cannot
be validated, I am told which reference-validation mode rejected it and
which config key changes the behavior, so that the
register-before-provision workflow is discoverable from the error
message itself.

**Acceptance:** The operator registers a template referencing a
not-yet-provisioned service under the strict default mode → the
rejection states that reference validation failed, names the active
mode, says that mode is what made the unprovisioned reference fatal,
and names the config key (with the relaxed settings) for register-first
workflows.

**Falsifier:** A reference-validation rejection that still reads as a
generic "validation rejected the registration" — mode unnamed, config
key unnamed.

**Proof:** proof (executable) — a test registers a template with an
unprovisioned reference under strict mode and asserts the rejection
body names the active mode and the config key; a companion assertion
registers the same template under the relaxed mode and succeeds,
proving the advice the error gives is true.

### STORY-all-upstream-gating

As a template author building a fan-in shape (a node subscribing to
several upstream siblings), I can rely on the receiver dispatching only
after all of its in-flight upstreams in the frame have resolved —
regardless of how their staleness arrived — so that the receiver never
runs against a half-settled upstream set.

**Acceptance:** In a diamond or N-parent shape where the upstream
staleness propagates by sender settlement (not only by an invalidation
walk), the receiver runs exactly once per frame, after the last
in-flight upstream resolves, and its substitution context contains all
upstream contributions.

**Falsifier:** A receiver observed dispatching while a subscribed
upstream still has an in-flight run in the same frame; or a receiver
that runs early and is never re-fired when stragglers settle, leaving
the frame's result computed from a partial upstream set.

**Proof:** proof (executable) — a deterministic scenario test builds the
diamond with settlement-propagated staleness, holds one upstream open
via an injection hook, and asserts the receiver is not dispatch-eligible
until the held upstream resolves — then asserts single dispatch with the
full upstream set in the substitution context.

### STORY-multi-hard-dep-rendezvous

As a template author declaring two or more `hard_dep: true` upstream
attribute sources on one node, I can rely on each upstream running once
and the receiver dispatching once with all hard-dep'd upstreams settled
— so the shape rendezvouses instead of livelocking.

**Acceptance:** A node with two hard-dep upstreams that settle
independently in the same frame: each upstream runs once; the receiver
runs once, after both; the frame terminates.

**Falsifier:** Upstreams re-running each other after settling in the
frame (mutual re-seeding), the frame never terminating, or the receiver
dispatching more than once for one frame.

**Proof:** proof (executable) — a deterministic reproducing scenario
test for the two-hard-dep shape, written before any fix, then kept as
the regression pin in either outcome (livelock confirmed and fixed, or
suspicion refuted and pinned).

### STORY-producer-class-routing

As a template author, I can route a producer-declared acquisition error
class in `error_types:` — and rely on `acquire/*` keys as a documented
fallback — so the error my producer takes care to classify is the error
I can configure a response to.

**Acceptance:** A template with `error_types: { pg/claim_unavailable:
retry }` on a node whose executor declares its own vocabulary registers
successfully, and at runtime an acquisition failure carrying that
producer class routes to the declared action. A template declaring only
`acquire/unavailable:` still matches a producer-classified acquisition
failure via the documented prefix fallback. An `error_types:` key the
validator can attribute to no declared vocabulary registers with an
advisory warning rather than a hard rejection.

**Falsifier:** Registration rejecting a producer-declared class that
the runtime would route; or an `acquire/*` key that registers but never
matches a producer-classified acquisition failure.

**Proof:** proof (executable) — a scenario registers both template
shapes and drives a producer-classified acquisition failure through
each, asserting the configured action fires.

### STORY-validation-warnings-surfaced

As a template author, I can see the static validator's advisory
warnings in the registration and validation responses — and promote
them to errors with the existing flag — so advice the validator already
computes reaches me.

**Acceptance:** Registering or validating a template that trips a
static-validator advisory (e.g. claims acquired with no
acquisition-failure policy declared) returns the advisory in the
response's `validation_warnings`; with `warnings_as_errors=true` the
same advisory rejects the registration.

**Falsifier:** A static-validator warning that is computed but absent
from both responses; or `warnings_as_errors=true` not tripping on it.

**Proof:** proof (executable) — register a template that trips the
acquisition-policy advisory and assert it appears in
`validation_warnings`; repeat with `warnings_as_errors=true` and assert
rejection.

### STORY-peer-tls-enforced

As an operator who configures `tls: required` on a peer service
(executor or store), I get a TLS-verified connection to that peer — and
a loud failure if the peer cannot present credentials — so that the
config key means what it says.

**Acceptance:** With `tls: required` on a peer entry, rimsky dials that
peer with verified TLS; against a TLS-serving peer the connection works
end-to-end; against a plaintext peer the dial fails with an error naming
the peer and the mode. With `tls: off` (and by default) behavior is
today's plaintext.

**Falsifier:** A `tls: required` peer connection observed on the wire
in plaintext; or the key accepted and silently ignored.

**Proof:** proof (executable) — integration test dials a TLS-enabled
stub peer under `required` and exchanges a request; companion test dials
a plaintext stub under `required` and asserts the loud failure.

### STORY-commit-response-honored

As a claim-producer author, I can set `version_id` and
`producer_metadata` on my base Commit response and see them land where
the protocol says — the claim-handle row's version and the fan-out
parent's writeback — so the fields the proto documents are real for the
base protocol, not only for the data-processing mix-in.

**Acceptance:** A producer whose base-protocol Commit response carries a
`version_id` sees it persisted on the corresponding claim-handle row
(today this works only via the data-processing mix-in's separate
commit-candidate path); a fan-out whose children's commits carry
`producer_metadata` sees it surfaced in the parent's writeback.

**Falsifier:** Base-protocol Commit response fields set by the producer
and absent from the row / writeback — the response body still
discarded.

**Proof:** proof (executable) — a scenario with a stub producer that
stamps both fields on the base Commit response asserts the persisted
version and the writeback-surfaced metadata.

### STORY-validation-mixin-uniform

As a service author, I can advertise the validation mix-in from an
executor or publisher — not only from a claim producer — and have my
declared validation roles actually honored, so the mix-in works for
every peer kind the protocol says it does.

**Acceptance:** An executor or publisher peer advertising the
validation protocol with supported roles is used for validation in
those roles, identically to a claim-producer peer advertising the same.

**Falsifier:** An executor or publisher advertising the mix-in whose
supported-roles list is still treated as empty — dialed but never used.

**Proof:** proof (executable) — a conformance-style test registers each
peer kind advertising the mix-in and asserts the handshake-learned
roles are identical across kinds.

### STORY-work-completed-emitted

As an operator or auditor reading the event log, I can pair every
`work_started` event with a `work_completed` event, so durations and
did-everything-finish audits are computable from the ledger.

**Acceptance:** Dispatching a node-run appends `work_started` (as
today); the run reaching its terminal appends `work_completed` carrying
the same identifying fields plus the terminal kind.

**Falsifier:** Runs that reach terminal with no `work_completed` in the
ledger — the kind still declared but never spoken.

**Proof:** proof (executable) — a scenario drives a run to terminal and
asserts the paired events with matching identifiers.

### STORY-named-lock-metric

As an operator, I can see named-lock acquisitions in the Prometheus
metrics — alongside producer-claim acquisitions — so lock saturation is
something I can graph and alert on rather than reconstruct from events.

**Acceptance:** Acquiring a named lock increments an acquisition metric
distinguishable from producer-claim acquisitions; an operator watching
the metrics endpoint sees named-lock activity move under load.

**Falsifier:** Named-lock acquisitions that move no metric — the events
ledger still the only trace.

**Proof:** proof (executable) — a test acquires named locks and asserts
the counter's movement and labeling.

## Architecture and context (prose)

Five areas of mechanism serve the stories above; the technical
decisions that follow are the atomic record.

**The harness.** `make test-all` runs no `-race` today and caps test
parallelism at `-parallel 4` in two places (the root-module run and the
`lib/services` run of the test-all target) — caps that paper over the
synchronous Subscribe budget (see STORY-subscription-mounting). Race
repetition exists only as a manual instruction in
`.claude/rules/rules.md`. One deterministic race-injection hook exists
(`PostCommitHook`, `lib/runtime/runner.go`) with its scenario test
(`test/scenarios/verify_before_run_post_commit_test.go`).

**The claim spine.** The claimant guard (blessed-invariant 4) is
method-encapsulated in the drivers (e.g.
`lib/foundation/persistence/postgres/claim_handles.go::Delete`), but the
guard predicate is hand-written ~15 times per driver and mirrored by
eye across `postgres/` and `sqlite/`. The unified claim-handle
resolution engine has two bypasses: the acquire-unavailable handler
(`lib/runtime/runner_lifecycle.go::handleAcquireUnavailable` — no rows
by construction, its acquisition tx already rolled back) and the
verify-before-run bail
(`lib/runtime/runner_acquire_postcommit.go::handleOrphanedClaim` —
caller-owned verb-then-delete, structurally the engine's own shape).

**Child execution.** Delegation and fan-out are two parallel
implementations of one run-side primitive
(`lib/runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller`
+ `CarryExitWriteback`;
`lib/runtime/fanout_dispatch.go::CreateFanOutChildren` +
`lib/runtime/auto_terminal_chain.go::resolveParentClaimChain`).
Delegation is fan-out with N=1 plus a carry-verbatim policy; the
genuine asymmetries are entry absorption (delegation-only) and
per-partition sub-claims (fan-out-only). No schema change is needed:
`partition_key` already discriminates. Template surfaces (`delegate:`,
`fan_out:`) are unchanged. The existing scenario suites
(`test/scenarios/fanout/`, `test/scenarios/subgraph/`) must stay green.

**Cascade gating.** The wait-set drives dispatch eligibility ("a stale
run is dispatch-eligible iff no undrained rows exist for it in the
frame"), and the invalidation walk seeds pessimistic gates across N
in-flight upstreams (`lib/runtime/cascade_invalidate.go`,
`walkCascadeForInvalidatedNode`). The settlement walk
(`lib/runtime/runner_terminal.go::cascadeSubscribersStaleInTx`) marks
direct subscribers stale without seeding next-tier gates, so a
multi-parent receiver whose upstreams went stale via settlement can
become eligible after the first parent settles. The contract this spec
adopts (STORY-all-upstream-gating): a stale node with in-flight
upstreams in its frame is not dispatched until they resolve, regardless
of propagation path. The hard-dep pull
(`lib/runtime/runner_terminal.go::pullHardDepUpstreams`) lacks a
settled-this-frame guard (STORY-multi-hard-dep-rendezvous).

**Deployment topology.** The all-in-one image's no-command entrypoint
path today spawns the three role binaries as separate OS processes,
stamping each with `RIMSKY_PROCESS_ROLE=unified`
(`cmd/rimsky-entrypoint/main.go`). The env var's one consumer — the
memory-blob gate
(`lib/foundation/persistence/blob_config.go::ValidateBlobConfig`) —
assumes "unified" means a shared process, which has never been true:
three processes cannot share an in-process blob map, and the
scheduler's orphan-blob sweep runs against its own empty map. The
SQLite driver's bare (no-transaction) read-then-write call sites rely
on in-process connection serialization for atomicity
(`lib/foundation/persistence/sqlite/database.go`), which does not hold
across the processes that share the default all-in-one database file.
This spec makes the no-command path genuinely single-process
(TD-single-process-mode), fixes the SQLite driver's bare
read-then-write sites (TD-sqlite-multiproc-safety), and keeps the
memory gate with a premise that is now true.

## Technical decisions

Note on nouns: the cross-driver test library under
`lib/foundation/persistence/conformance/` is referred to below as the
**driver-parity suite**. It is distinct from `concept:conformance`
(the `rimsky conformance <protocol>` subcommand family over the
protocols module), which this spec does not touch.

### Harness

**TD-harness-first-ordering** — sequencing is load-bearing.
**Choice:** the harness decisions (TD-race-gate-split,
TD-race-injection-hooks, TD-polling-audit) land before the
consolidation decisions (the claim-spine and child-execution groups),
and the child-execution unification lands last among consolidations.
**Rationale:** the consolidations refactor concurrency seams; doing so
against a race-blind harness is how stabilization passes produce new
races. Largest blast radius goes last, behind the most net.

**TD-race-gate-split** — where race detection runs.
**Choice:** `make test-all` gains a thin `-race -count=1` slice over
the race-sensitive packages (`lib/foundation/persistence/postgres`,
`lib/foundation/persistence/sqlite`, `lib/runtime`,
`lib/graph/scheduler`, queue paths); a new `make test-race` target runs
`-race -count=3` over the same set; the `make release` chain requires
`test-race`.
**Rationale:** races bite mid-refactor, so the everyday gate needs
baseline coverage; the full repetition budget belongs at release time.
**Alternatives:** all-in-test-all (too slow every run); release-only
(no everyday coverage).

**TD-race-injection-hooks** — deterministic injection at defended seams.
**Choice:** extend the existing `PostCommitHook` pattern
(`lib/runtime/runner.go`) with deterministic injection tests at: the
acquire-unavailable abandon path, the folded ownership-bail path (as a
post-fold regression pin), the held-claim aggregate check-and-fire, and
the orphan-reaper vs in-flight-terminal overlap.
**Rationale:** these are designed defenses against inherent
multi-replica collisions; deterministic forcing proves the defense and
pins it against refactors — strictly stronger than probabilistic
`-race` luck.

**TD-polling-audit** — event-driven waits where polling masks ordering.
**Choice:** audit the sleep/deadline-polling test sites (~113 files
under `test/` and `lib/`); leave genuine outcome-waits; convert the
subset whose polling masks an ordering assumption to event-log-tail
waits.
**Rationale:** waiting on the durable record of a transition cannot
miss or race the sampler; flaky-under-load tests erode the gate exactly
when it is the consolidation's net.

### Subscription mounting

**TD-subscription-mounting-state** — desired-state rows with a visible
lifecycle. **Choice:** add `mounting` to the publisher-subscription
state set (`mounting | active | failed | stopped`); instance-create
inserts rows in `mounting` and returns; the instance-detail surface
exposes per-subscription state. **Rationale:** the row set is already
documented as the source of truth that publisher-side state reconciles
against; the synchronous inline Subscribe was the anomaly. Failing on a
timeout is arbitrary; an observable `mounting` is robust against
contention.

**TD-subscription-reconciler** — who drives Subscribe.
**Choice:** a reconciliation worker performs Subscribe RPCs for
`mounting` rows with backoff and no attempt cap; `failed` is reserved
for non-retryable errors (e.g. unknown publisher name); the existing
startup resync pass remains the durable safety net.
**Rationale:** retry-forever matches desired-state semantics; bounded
budgets convert contention spikes into silent failures.

**TD-parallel-cap-removal** — lift both test-parallelism caps.
**Choice:** remove `-parallel 4` from both occurrences in the test-all
target (the root-module run and the `lib/services` run) once the
services tests wait on observable subscription state.
**Rationale:** the caps papered over the synchronous budget; with
mounting observable, the flake class is gone and an uncommented cap is
decay risk.

### Claim spine

**TD-claimant-guard-helper** — one written guard per driver.
**Choice:** each driver (`postgres/`, `sqlite/`) routes its
claimant-guarded mutations through one internal helper that appends the
guard predicate; the ~15 hand-written copies per driver collapse to it.
**Rationale:** a predicate written once per driver cannot be
inconsistently copied. **Alternatives:** a cross-driver query builder
(rejected: heavier than the codebase's explicit-SQL idiom).

**TD-guard-conformance-suite** — wrong-claimant is a provable no-op.
**Choice:** extend the driver-parity suite
(`lib/foundation/persistence/conformance/`) with a guard suite: for
every mutating claim-handle and node-run ownership operation, acting as
the wrong supervisor must change nothing — asserted identically against
both drivers.
**Rationale:** the helper cannot catch a future function that bypasses
it; the behavioral proof can. Together they close each other's blind
spot.

**TD-fold-ownership-bail** — the bail path joins the engine.
**Choice:** `handleOrphanedClaim`'s per-claim Abandon + caller-owned
claimant-guarded delete is re-routed through the unified claim-handle
resolution engine as a new source kind; the hand-rolled
verb-then-delete site is deleted.
**Rationale:** the path has rows and performs the engine's exact
sequence; it is the engine's shape duplicated, which is this campaign's
target disease.

**TD-acquire-unavailable-carveout** — the surviving exception, named
and tested. **Choice:** `handleAcquireUnavailable` remains outside the
engine, explicitly named as the single carve-out, with a deterministic
injection test (per TD-race-injection-hooks) pinning its behavior:
abandon partial opens, no row delete (rows rolled back), route via the
producer-declared class else synthetic `acquire/unavailable`.
**Rationale:** its acquisition tx has already rolled back, so there is
no claimant-guarded delete to fold; forcing it into the engine would
widen the engine's contract with a verb-only mode, diluting the single
audited verb-then-delete promise.

### Child execution

**TD-child-execution-unification** — one dispatch, one settlement.
**Choice:** a single dispatch-children primitive replaces the run-side
work of `applyTerminalCompleteSubgraphCaller` and
`CreateFanOutChildren`; a single settle-children primitive replaces
`CarryExitWriteback` and `resolveParentClaimChain`. Delegation wraps it
with one partition / carry-verbatim / entry-absorbed; fan-out with N
partitions / author policy. The parallel implementations are deleted.
No schema change; `delegate:` and `fan_out:` template surfaces
unchanged. **Rationale:** delegation is fan-out with N=1; two parallel
implementations of one primitive is the duplicated-path disease, with a
documented history of fixes landing in one path only.
**Alternatives:** shared settlement library only (rejected: leaves the
dispatch-side hand-parity seam alive).

**TD-entry-absorption-flag** — delegation's entry absorption is a flag
on the dispatch input, not a pre-step. **Choice:** the dispatch
primitive carries an entry-absorbed boolean. **Rationale:** one
primitive, one call site; a pre-step would reintroduce a second
dispatch shape.

**TD-subclaims-as-input** — the primitive accepts acquired sub-claims.
**Choice:** the dispatch primitive accepts already-acquired sub-claims
as input; it does not call the producer's split itself, and the
claim-tree machinery (`AcquireSubClaims` and relatives) is unchanged by
this decision.
**Rationale:** preserves current factoring; the unification's win is
run-side.

**TD-carry-verbatim-requires-one** — carry-verbatim is N=1 by
construction. **Choice:** the carry-verbatim aggregation policy
requires exactly one child, enforced at template canonicalization; a
delegation that somehow declares multiple children is a template error.
**Rationale:** makes the delegation degenerate case a checked
invariant instead of an assumption.

**TD-cascade-inside-settlement** — the parent-settlement cascade hook
lives inside the settle-children primitive. **Choice:** the cascade
bridge fires within settlement, not alongside it at call sites.
**Rationale:** no caller can settle without cascading; the bridge's
historical absence from one path is exactly the class of defect being
removed.

**TD-child-execution-naming** — plain names.
**Choice:** the primitive pair is named dispatch-children /
settle-children (Go: `DispatchChildren` / `SettleChildren`).
**Rationale:** descriptive, avoids overloading "delegation".

**Carry-rule atomicity constraint (applies to the group):** the
delegation carry-writeback is atomic with closing the child's execution
context today; the unified settlement must preserve that transaction
boundary unwidened and unnarrowed. The existing scenario suites
(`test/scenarios/fanout/`, `test/scenarios/subgraph/`) must stay green;
carry-rule tests re-target the settle-children path.

### Cascade gating

**TD-upstream-gating-at-eligibility** — the all-upstreams guarantee is
enforced at dispatch eligibility. **Choice:** the dispatch-eligibility
predicate gains the propagation-path-independent condition: a stale run
is not eligible while any subscribed upstream has an in-flight run in
the same frame. The wait-set ledger and its drained-rows substitution
role are retained unchanged. Self-edge ("drain my own queue") idioms
and cycle handling must keep working; existing scenario tests pin them
and the new deterministic diamond test (STORY-all-upstream-gating)
joins them. The multi-sender eligibility scenario
(`test/scenarios/subscription_cascade_test.go`,
`TestSubscriptionCascade_EligibilityRespectsMultipleSenders`) is
strengthened to actually pin the predicate its comments describe.
**Rationale:** a predicate cannot be forgotten by a new propagation
path, the same chokepoint logic as the claimant guard; walk-side
seeding would have to be remembered by every current and future
stale-transition site. **Alternatives:** uniform pessimistic seeding on
every walk (rejected: per-path bookkeeping discipline — the disease,
again). If implementation shows the predicate shape is wrong, that is a
recorded divergence to surface, not a silent re-design.

**TD-hard-dep-settled-guard** — test-first, then the guard.
**Choice:** write the deterministic reproducing scenario for two
`hard_dep: true` upstreams settling independently in one frame, before
any fix. If it confirms the livelock, add the settled-this-frame guard
to `pullHardDepUpstreams` so a settled upstream is not re-affirmed on
receiver re-visits, and keep the test as the regression pin. If it
refutes the suspicion, keep the test as the pin and record the
refutation in the decision file. **Rationale:** the suspicion is a
code-read without a covering test; the test decides which world we are
in and is valuable in both.

### Scheduler, drivers, deployment

**TD-sweep-lock-skip-on-error** — a lock error skips the sweep pass.
**Choice:** in the scheduler tick (`lib/graph/scheduler/scheduler.go`),
an error from the advisory-lock attempt is treated as lock-held: log
and skip, never run unlocked. **Rationale:** the sweeps are periodic
recovery; a one-interval delay is benign, while running unlocked under
DB flakiness allows the concurrent sweeping the lock exists to prevent.
**Alternatives:** prove all sweeps concurrent-safe and run anyway
(rejected: a standing proof obligation on a hot extension point).

**TD-parity-expansion** — the driver-parity suite covers what the
runtime depends on. **Choice:** extend the driver-parity suite until
every queue, claim-handle, and frame behavior the runtime depends on
has a parity test executed against both drivers (the guard suite of
TD-guard-conformance-suite is one slice).
**Rationale:** two ~10k-line hand-mirrored drivers drift; parity by
test beats parity by review.

**TD-sqlite-multiproc-safety** — the SQLite driver becomes safe for
multiple processes sharing one local file. **Choice:** two halves.
(1) Audit the driver's bare (no-transaction) read-then-write call
sites — the ones the driver's own connection-pool comment enumerates
as relying on in-process connection serialization
(`lib/foundation/persistence/sqlite/database.go`) — and make each
transactional, so immediate-mode transactions provide cross-process
atomicity. (2) Replace the SQLite advisory locker's in-process
scheduler-tick and migration locks
(`lib/foundation/persistence/sqlite/advisory_locker.go`, today a
`sync.Mutex`) with file-lock-based exclusion (a lock file alongside
the database file), so tick and migration exclusion hold across
processes sharing the file — without this, two scheduler processes
would sweep concurrently, the exact condition
TD-sweep-lock-skip-on-error exists to prevent. The per-name/per-scope
in-tx locks already hold cross-process via immediate-mode
transactions and are unchanged. No startup gate: outside the
all-in-one deployment rimsky defaults to Postgres, and an operator who
overrides to SQLite is presumed to have chosen deliberately.
**Rationale:** the safety must be real for any deliberate
multi-process SQLite operator; gating a deliberate config choice is
not this platform's policy (the resolution of
`tension:sqlite-vs-memory-reject-asymmetry`). Separate-files and
network-filesystem topologies remain physically unsupportable and
undetectable in-process.

**TD-single-process-mode** — the all-in-one runs all three roles in one
process. **Choice:** the entrypoint's no-command path
(`cmd/rimsky-entrypoint/main.go`) changes from spawning three child
processes to: run migrate synchronously, then start all three roles
in-process via the existing library entry points
(`config.StartScheduler`, `config.StartSupervisor`,
`config.StartControlAPI`), each on its configured port, with one
signal-handled shutdown. The single-role path (explicit role command)
keeps its current behavior. `RIMSKY_PROCESS_ROLE=unified` is set only
in (and now truthfully describes) the single-process mode.
**Rationale:** the env marker and the memory-blob gate both promise a
shared-process deployment that was never built; the role mains are thin
wrappers over library calls, so the promised deployment is the cheap
honest fix. **Alternatives:** keep three spawned processes and remove
the memory gate (rejected: leaves the unified marker meaningless and
the memory backend useless even in dev).

**TD-memory-gate-premise-corrected** — the memory-blob gate stays, with
a true premise. **Choice:** the memory backend remains rejected outside
`RIMSKY_PROCESS_ROLE=unified`
(`lib/foundation/persistence/blob_config.go::ValidateBlobConfig`); the
gate's error text and comments are corrected to describe the
single-process mode (the old text claims the per-process binaries are
the reason while the old all-in-one was itself per-process).
**Rationale:** cross-process memory blobs are broken by physics, not
policy — the asymmetry with ungated SQLite is justified and recorded:
SQLite multi-process is made safe; memory multi-process cannot be.

**TD-topology-test-coverage** — both deployment topologies are
integration-tested. **Choice:** the services integration harness covers
both shapes: the single-process all-in-one (boot, assert one rimsky
process serves all three role surfaces, drive a node to terminal,
round-trip a memory-backend blob across roles) and the three-container
split topology (boot scheduler + supervisor + control-api as separate
containers against shared Postgres, drive the same scenario to
terminal). **Rationale:** TD-single-process-mode changes the default
deployment's process model; both supported topologies need a standing
proof, not just the one the harness happens to boot today.

### Error surfaces

**TD-producer-error-passthrough** — producer errors cross the HTTP
boundary intact. **Choice:** the control-api error writer
(`lib/control/controlapi/app.go::writeError`) learns producer-error
types: the producer's error class and message are carried in the
response body, under a status distinguishing producer rejection from
rimsky internal error. **Rationale:** the error body is the one
document every operator and agent reads; discarding a structured class
into a bare 500 wastes diagnosis the producer already did.

**TD-validation-error-names-mode** — ref-validation rejections are
self-documenting. **Choice:** reference-validation failure messages
name the active `RefValidationMode`, state that the mode made the
failure fatal, and name the config key (`templates.ref_validation_mode`)
with its relaxed settings. **Rationale:** the mode exists precisely for
the register-before-provision workflow; an error that hides the knob
defeats the feature.

**TD-producer-declared-classes-capability** — producers can declare an
error-class vocabulary. **Choice:** the claim-producer capabilities
response (`claim_producer.proto::CapabilitiesResponse`) gains a
declared-error-classes field, mirroring the executor-observability
declaration; the capabilities handshake stores it in the discovery
cache alongside the executor vocabularies. Producers MAY declare;
declaring nothing remains legal. **Rationale:** the validator can only
range-check vocabularies that are declared somewhere; the runtime
already routes producer-declared classes, so the declaration surface is
the missing half of an existing contract. Pre-v1, the proto extension
is a compatible addition.

**TD-validator-learns-producer-classes** — `error_types:` accepts
producer vocabularies. **Choice:**
`lib/graph/node/template_validator.go::validateErrorTypes` range-checks
keys against the union of the executor's declared classes, the
`acquire/*` synthetic family, and the declared error classes (per
TD-producer-declared-classes-capability) of every producer reachable
from the node's claims. A key attributable to no declared vocabulary
becomes an advisory warning (surfaced per
TD-merge-validator-warnings), not a hard rejection. **Rationale:** the
runtime (`handleAcquireUnavailable`) already routes by
producer-declared class; the validator rejecting what the runtime
routes locks operators out of the system's own classification — and
producers that declare nothing must not lock their operators out
either.

**TD-acquire-prefix-fallback** — generic keys still catch classified
failures. **Choice:** policy lookup for acquisition failures falls back
from the exact producer-declared class to the `acquire/*` family before
the unknown-class default; the fallback is documented at the lookup
site. **Rationale:** an operator declaring only the generic policy
should not silently lose coverage the moment a producer starts naming
classes.

**TD-merge-validator-warnings** — warnings reach the responses.
**Choice:** both the register handler and the validate endpoint
(`lib/control/controlapi/templates.go`) merge the static validator's
warnings into the responses' `validation_warnings`;
`warnings_as_errors=true` trips on them. **Rationale:** the field, the
warnings, and the flag all exist; they are merely never connected.

### Wire-contract honesty

**TD-wire-commit-response-fields** — base Commit responses are read.
**Choice:** the producer client (`lib/runtime/peer/client.go::Commit`)
returns the response body; the unified resolution engine persists
`version_id` from the base Commit response to the claim-handle row
(today only the data-processing mix-in's commit-candidate path does
this, via `lib/runtime/terminal_decision.go`); the settle-children path
surfaces `producer_metadata` in the fan-out parent's writeback — as the
proto comments (`claim_producer.proto`) promise. **Rationale:** the
campaign closes contract-vs-runtime gaps in the contract's favor when
the contract is sensible; both sites are already open under the
claim-spine and child-execution groups.

**TD-plumb-validation-roles** — the mix-in works for every peer kind.
**Choice:** the all-peer-kind validation-registry dial
(`lib/control/config/publishers.go::DialPublisherAndValidationRegistries`,
which already walks stores, executors, and publishers) plumbs
`validation_supported_roles` from the publisher capability surface
(`publisher.proto::PublisherCapabilitiesResponse`, where the field
already exists) and from the executor side by adding a
`validation_supported_roles` field to the executor-observability
capabilities message
(`executor_observability.proto::ObservabilityCapabilities` — the
executor's capabilities surface, which lacks the field today), wiring
both identically to claim-producer peers.
**Rationale:** the wire contract implies all peer kinds; two of three
silently ignoring the field is a gap, not a design. The executor-side
proto addition is the same compatible-extension pattern as
TD-producer-declared-classes-capability.

**TD-peer-tls-enforcement** — the `tls` key works, for every peer
kind. **Choice:** the `tls` key — today parsed only on executor
entries (`lib/control/config/stores.go`) — is added to store and
publisher config entries as well, and every peer dial site honors the
configured mode — the `lib/runtime/peer/` clients (store, publisher,
data-processing, validation), the executor dial
(`lib/runtime/executor/client.go::NewGRPCClient`), and the
observability-handshake dial
(`lib/control/observability/handshake.go`): `required` dials with
verified TLS against system roots; `off` (default) stays plaintext;
failures under `required` name the peer and mode. **Rationale:** a
security-shaped config key that is accepted and ignored manufactures
false confidence exactly where it is costliest; a key only one peer
kind can even write would leave the other dial sites unconfigurable.

**TD-tls-mode-validation** — the `tls` value becomes a validated enum.
**Choice:** the `tls` config field — today an unvalidated passthrough
string whose comment documents `off | optional | required` — gains
parse-time validation accepting exactly `off | required` (empty
defaults to `off`); `optional` and any other value are config errors.
**Rationale:** opportunistic TLS is not a real gRPC client mode; a
documented third value with no honest semantics is surface noise.
Pre-v1, deletion over deprecation.

**TD-emit-work-completed** — the ledger speaks both halves.
**Choice:** the terminal-application step appends a `work_completed`
event (kind already declared in `lib/foundation/events/kinds.go`)
carrying the same identifiers as its `work_started` twin plus the
terminal kind. **Rationale:** a declared-but-never-emitted kind is a
catalog lie, and completion is the half a duration-or-audit consumer
needs.

**TD-named-lock-metric** — lock acquisitions are countable.
**Choice:** the named-lock acquisition path
(`lib/runtime/runner_acquire_named_locks.go`) increments the
acquisition metric family, labeled to distinguish named locks from
producer claims, following the existing metric naming convention.
**Rationale:** lock saturation is an operational condition; events are
forensics, metrics are monitoring.

### Mechanical sweeps

**TD-comment-drift-sweep** — fix the ten lying comments.
**Choice:** one mechanical pass correcting: the `/mcp` route comment
(`lib/control/controlapi/mcp_route.go`); the un-prefixed route in
`lib/protocols/publisherkit/publisher.go` godoc; the three
retry-arithmetic comments (`publisherkit/publisher.go::Send`,
`lib/runtime/publishers.go`); the `Retry-After` doc-comment
(`lib/services/executors/http-node/server.go::parseRetryAfter`); the
wait-set comment contradicting its code path
(`lib/runtime/runner_terminal.go` ~789-795); the internal-plan
vocabulary in `lib/control/config/stores.go` error text; and the stale
`feature-index.md` rows. **Rationale:** each already misled one
reader; comments that lie are negative documentation.

**TD-delete-archived-author-guide** — remove the obsolete guide.
**Choice:** delete
`.ok-planner/archive/internal/claim-producer-author-guide.md`.
**Rationale:** archives are not edited; this one describes a
verb-firing model two redesigns old and has already caused a published
drift. Git history retains it.

## Design changes

Tensions:

- Tension: resolve
  `tensions/delegation-and-fanout-share-runtime-primitive.md`. Move to
  `tensions/_resolved/` with `status: resolved` and a `resolution:`
  block summarizing: unified dispatch-children / settle-children
  runtime primitive; delegation and fan-out demoted to invocation
  patterns over a new child-execution concept; carry-verbatim is an
  aggregation policy requiring exactly one child.
- Tension: resolve `tensions/reaper-vs-bail-abandon-asymmetry.md`. Move
  to `tensions/_resolved/` with `status: resolved` and a `resolution:`
  block summarizing: the verify-before-run bail path is folded into the
  unified claim-handle resolution engine; the acquire-unavailable path
  is the single named carve-out, deterministically injection-tested;
  the periodic reaper continues to fire no producer verb.
- Tension: resolve `tensions/sqlite-vs-memory-reject-asymmetry.md`.
  Move to `tensions/_resolved/` with `status: resolved` and a
  `resolution:` block summarizing: no startup gate for SQLite — the
  driver is made safe for multiple processes sharing one local file,
  the platform defaults to Postgres outside the all-in-one deployment,
  and an operator overriding to SQLite is presumed deliberate; the
  memory blob backend remains gated to the single-process mode because
  cross-process in-memory state is broken by physics, not policy. The
  asymmetry is thereby justified and recorded rather than accidental.

Concepts:

- Concept: create `concepts/child-execution.md` from the
  `ok-planner:discover-design` template. Definition: the run-side
  primitive by which a parent node-run dispatches one or more child
  executions into their own execution contexts and settles on their
  aggregate outcome. Purpose: own the shared shape that delegation and
  fan-out are surfaces of. Boundaries: owns the dispatch primitive
  (N≥1 children into child execution contexts) and the settlement
  primitive (record child outcome → apply aggregation policy → close
  child contexts → settle parent, with the parent-settlement cascade
  fired from inside settlement); the execution contexts themselves and
  their tree structure are owned by `concept:run-scope`; template
  surfaces are owned by `concept:delegation` and `concept:fan-out`;
  sub-claim acquisition is owned by `concept:claim-tree`. Invariants:
  settlement is the only run-side path that closes child execution
  contexts (instance termination is the administrative exception, per
  `concept:run-scope`); the carry-verbatim aggregation policy requires exactly one child,
  enforced at template canonicalization; entry absorption is a property
  of the invoking pattern, not of child execution; the
  parent-settlement cascade cannot be skipped by any settlement caller;
  the settlement's outcome carry (writing the aggregated or
  carried-verbatim outcome back to the parent) is atomic with closing
  the child execution context (`@blessed-invariant: exit-node-writeback`).
- Concept: mutate `concepts/delegation.md` in place. Rewrite as an
  invocation pattern over `concept:child-execution`: a node carrying a
  delegate directive instead of an executor dispatches the named
  sub-graph as one child execution (one child, carry-verbatim policy,
  entry absorbed — the calling node is the sub-graph's entry).
  Boundaries shrink to the template surface and the entry-absorption
  asymmetry; the execution-context tree shape, settlement, and closure
  invariants move to `concept:child-execution` and are referenced, not
  restated.
- Concept: mutate `concepts/fan-out.md` in place. Rewrite as an
  invocation pattern over `concept:child-execution`: a node-level
  decision to partition a held claim into sub-claims and dispatch one
  child per partition, with an author-specified aggregation policy.
  Boundaries shrink to the template surface, partition cardinality, and
  the per-partition sub-claim asymmetry; shared shape moves to
  `concept:child-execution` and is referenced, not restated.
- Concept: mutate `concepts/terminal-resolution.md` in place. Rewrite
  the carve-out paragraph (the two upstream siblings) to describe a
  single carve-out: the acquire-unavailable handler, which abandons
  already-opened partial claims via the shared helper and routes
  through the error path with the producer-declared class else the
  synthetic acquisition class; it remains outside the unified engine
  because its acquisition transaction has already rolled back and there
  is no claimant-guarded delete to fold. State that the
  verify-before-run bail path resolves through the unified engine with
  its own source kind. Update Stage-4 and invariants accordingly (the
  unified claim-handle resolution remains the single audited
  verb-then-delete site; the engine's source kinds now include the
  ownership-bail source). Also rewrite the restatements elsewhere in
  the same file so no contradiction stands: the kind→signal→verb
  table's verify-before-run row (currently "Abandon (via helper)")
  changes to reflect resolution through the unified engine; the
  table's acquisition-failure row and the Stage-2, Stage-3, and intro
  sentences that name only the synthetic acquisition class (or the
  `acquire/*` family alone as the chain trigger) change to "the
  producer-declared class else the synthetic acquisition class."
- Concept: mutate `concepts/publisher-subscription.md` in place.
  Replace the lifecycle-state invariant AND the Boundaries sentence
  naming the state field's values, both to read: `state` is one of
  `mounting`, `active`, `failed`, `stopped`. Rows are created in
  `mounting`; a reconciliation worker drives the publisher Subscribe
  handshake with backoff and no attempt cap, flipping the row to
  `active` on success; `failed` is reserved for non-retryable errors
  (e.g. a publisher name not present in the registry); `stopped` on
  unsubscribe; resync re-drives `mounting` and recoverable `failed`
  rows. Add to Purpose: the row set is desired state; the instance
  surface exposes per-subscription state so an operator can observe
  mounting progress instead of inferring it from instance creation
  succeeding.
- Concept: mutate `concepts/publisher.md` in place. Two mutations:
  (1) add to Definition / Purpose the provider framing: a publisher
  service is a provider of broadcasters — one service process serves
  many instances, and each subscription provisions a logical,
  per-instance broadcaster within it (parameterized by the instance's
  resolved config); the per-instance analogue of how an executor
  provides per-node-run execution. (2) Replace the invariant stating
  that the subscribe verb is retried up to 3 times with exponential
  backoff before flipping the subscription row to failed, with: a
  reconciliation worker drives the subscribe verb for mounting
  subscriptions with backoff and no attempt cap; the failed state is
  reserved for non-retryable errors (per
  `concept:publisher-subscription`).
- Concept: mutate `concepts/advisory-lock.md` in place. Two
  mutations: (1) add invariant: for the scheduler-tick lock, an error
  from the lock attempt is treated as lock-held — the sweep pass is
  skipped, never run unlocked. (2) Rewrite the sentences describing
  the SQLite implementation of the scheduler-tick and migration locks
  as an in-process mutex / no-op, to state: under SQLite these locks
  are file-lock-based, holding exclusion across processes that share
  the database file on one host.
- Concept: mutate `concepts/persistence-database.md` in place. Replace
  the invariant sentence stating SQLite is dev-only, multi-host
  requires Postgres, and the configuration is documented but not
  gate-rejected, with: the SQLite driver is safe for multiple rimsky
  processes sharing one local database file (its read-then-write
  operations are transactional, so cross-process atomicity holds);
  separate database files per process and network filesystems are
  unsupported and undetectable from inside a process; there is no
  startup gate — the platform defaults to Postgres outside the
  all-in-one deployment and a SQLite override is presumed deliberate.
- Concept: mutate `concepts/blob-backend.md` in place. State that the
  in-memory backend is legal only in the single-process deployment mode
  (all roles in one process, where one in-process map is genuinely
  shared, the orphan-blob sweep reaps it, and cross-role reads work);
  it is startup-rejected in any per-role process.
- Concept: mutate `concepts/replica.md` in place. Add: the all-in-one
  deployment runs all three roles in a single process (one replica of
  one process serving every role surface); per-role replicas are the
  split deployment's shape.
- Concept: mutate `concepts/error-policy.md` in place. Three
  mutations: (1) correct the retry-cap sentence — the deployment-level
  cap on retries without progress is a supervisor default, not a
  scheduler default; (2) update the acquisition-failure routing
  invariant to state that lookup falls back from the exact
  producer-declared class to the `acquire/*` family before the
  unknown-class give-up default; (3) state that `error_types:` keys are
  validated against the union of executor-declared classes,
  producer-declared classes, and the `acquire/*` family, with
  unattributable keys registering as advisory warnings rather than hard
  rejections.
- Concept: mutate `concepts/wait-set.md` in place. Add invariant: a
  stale run is not dispatch-eligible while any subscribed upstream has
  an in-flight run in the same frame, regardless of which propagation
  path made the receiver stale; the eligibility predicate enforces this
  independent of wait-set seeding. Rewrite both standing sentences
  that state eligibility as "iff no undrained rows exist for it in the
  current frame" (one in the what-it-is section, one in Invariants) to
  state the two-condition predicate: no undrained wait-set rows AND no
  subscribed upstream with an in-flight run in the frame. The
  drained-rows substitution role is unchanged.
- Concept: mutate `concepts/cascade.md` in place. State that
  staleness propagation (by invalidation walk or by sender settlement)
  does not by itself confer dispatch eligibility; eligibility is the
  dispatch-time predicate per `concept:wait-set`, and the
  all-in-flight-upstreams-resolve-first guarantee is
  propagation-path-independent. Rewrite the pitfalls bullet whose
  parenthetical defines eligibility as wait-set-empty plus acquirable
  claims and locks, so it states the same two-condition predicate as
  `concept:wait-set`.
- Concept: mutate `concepts/claim-co-holdership.md` frontmatter only:
  add the missing `aliases: []` key (schema conformance; 70 of 71
  concept files carry it).

Stories: create one file per story in this spec's User outcomes —
`stories/subscription-mounting.md`,
`stories/single-process-all-in-one.md`,
`stories/producer-error-passthrough.md`,
`stories/validation-names-the-mode.md`,
`stories/all-upstream-gating.md`,
`stories/multi-hard-dep-rendezvous.md`,
`stories/producer-class-routing.md`,
`stories/validation-warnings-surfaced.md`,
`stories/peer-tls-enforced.md`,
`stories/commit-response-honored.md`,
`stories/validation-mixin-uniform.md`,
`stories/work-completed-emitted.md`,
`stories/named-lock-metric.md` — each capturing its story verbatim
(role, capability, business value, Acceptance, Falsifier, Proof),
rewritten path-free per the self-containment rule.

Decisions: create one file per TD in this spec's Technical decisions —
`decisions/harness-first-ordering.md`, `decisions/race-gate-split.md`,
`decisions/race-injection-hooks.md`, `decisions/polling-audit.md`,
`decisions/subscription-mounting-state.md`,
`decisions/subscription-reconciler.md`,
`decisions/parallel-cap-removal.md`,
`decisions/claimant-guard-helper.md`,
`decisions/guard-conformance-suite.md`,
`decisions/fold-ownership-bail.md`,
`decisions/acquire-unavailable-carveout.md`,
`decisions/child-execution-unification.md`,
`decisions/entry-absorption-flag.md`, `decisions/subclaims-as-input.md`,
`decisions/carry-verbatim-requires-one.md`,
`decisions/cascade-inside-settlement.md`,
`decisions/child-execution-naming.md`,
`decisions/upstream-gating-at-eligibility.md`,
`decisions/hard-dep-settled-guard.md`,
`decisions/sweep-lock-skip-on-error.md`,
`decisions/parity-expansion.md`,
`decisions/sqlite-multiproc-safety.md`,
`decisions/single-process-mode.md`,
`decisions/memory-gate-premise-corrected.md`,
`decisions/topology-test-coverage.md`,
`decisions/producer-error-passthrough.md`,
`decisions/validation-error-names-mode.md`,
`decisions/producer-declared-classes-capability.md`,
`decisions/validator-learns-producer-classes.md`,
`decisions/acquire-prefix-fallback.md`,
`decisions/merge-validator-warnings.md`,
`decisions/wire-commit-response-fields.md`,
`decisions/plumb-validation-roles.md`,
`decisions/peer-tls-enforcement.md`,
`decisions/tls-mode-validation.md`,
`decisions/emit-work-completed.md`, `decisions/named-lock-metric.md`,
`decisions/comment-drift-sweep.md`,
`decisions/delete-archived-author-guide.md` — each capturing its TD's
Choice / Rationale / Alternatives, rewritten path-free per the
self-containment rule.

## Manifest

### Stories

- **STORY-subscription-mounting** — operator observes subscriptions
  `mounting → active`; no silent mount failure (Proof: demo)
- **STORY-single-process-all-in-one** — all-in-one runs all three roles
  in one process; memory blobs shared and reaped (Proof: executable)
- **STORY-producer-error-passthrough** — producer error class and
  message reach the API response (Proof: demo)
- **STORY-validation-names-the-mode** — ref-validation rejections name
  the active mode and config key (Proof: executable)
- **STORY-all-upstream-gating** — fan-in receivers dispatch only after
  all in-flight upstreams resolve (Proof: executable)
- **STORY-multi-hard-dep-rendezvous** — multi-hard-dep shapes
  rendezvous; no livelock (Proof: executable, test-first)
- **STORY-producer-class-routing** — producer-declared classes route in
  `error_types:`; `acquire/*` prefix fallback (Proof: executable)
- **STORY-validation-warnings-surfaced** — validator advisories appear
  in responses; `warnings_as_errors` trips (Proof: executable)
- **STORY-peer-tls-enforced** — `tls: required` means verified TLS or
  loud failure (Proof: executable)
- **STORY-commit-response-honored** — base-Commit `version_id`
  persisted; `producer_metadata` in fan-out writeback (Proof:
  executable)
- **STORY-validation-mixin-uniform** — validation roles honored for all
  peer kinds (Proof: executable)
- **STORY-work-completed-emitted** — every start pairs with a
  completion in the event log (Proof: executable)
- **STORY-named-lock-metric** — named-lock acquisitions visible in
  metrics (Proof: executable)

### Technical decisions

- **TD-harness-first-ordering** — harness lands before consolidations;
  child-execution last
- **TD-race-gate-split** — thin `-race` in test-all; full `-count=3`
  in `test-race`; release requires it
- **TD-race-injection-hooks** — deterministic injection at four
  defended seams
- **TD-polling-audit** — convert ordering-masking polls to event waits
- **TD-subscription-mounting-state** — `mounting` state; rows created
  in it; state exposed on instance surface
- **TD-subscription-reconciler** — retry-forever worker; `failed` only
  non-retryable
- **TD-parallel-cap-removal** — both `-parallel 4` caps lifted
- **TD-claimant-guard-helper** — guard predicate written once per
  driver
- **TD-guard-conformance-suite** — wrong-claimant no-op proven on both
  drivers
- **TD-fold-ownership-bail** — bail path resolves through the unified
  engine
- **TD-acquire-unavailable-carveout** — single named carve-out,
  injection-tested
- **TD-child-execution-unification** — one dispatch + one settlement
  primitive
- **TD-entry-absorption-flag** — flag on dispatch input
- **TD-subclaims-as-input** — primitive accepts acquired sub-claims
- **TD-carry-verbatim-requires-one** — N=1 enforced at canonicalization
- **TD-cascade-inside-settlement** — cascade fires inside settlement
- **TD-child-execution-naming** — DispatchChildren / SettleChildren
- **TD-upstream-gating-at-eligibility** — dispatch-time predicate;
  wait-set substitution role retained
- **TD-hard-dep-settled-guard** — reproducing test first, then the
  settled-this-frame guard
- **TD-sweep-lock-skip-on-error** — lock error = skip pass
- **TD-parity-expansion** — driver-parity suite covers runtime-depended
  behaviors on both drivers
- **TD-sqlite-multiproc-safety** — bare read-then-write sites become
  transactional; no gate
- **TD-single-process-mode** — all-in-one entrypoint runs all three
  roles in one process
- **TD-memory-gate-premise-corrected** — memory gate stays; premise and
  text corrected
- **TD-topology-test-coverage** — single-process and split topologies
  both integration-tested
- **TD-producer-error-passthrough** — error writer carries producer
  class/message
- **TD-validation-error-names-mode** — rejection names mode + config
  key
- **TD-producer-declared-classes-capability** — producer capabilities
  gain declared error classes
- **TD-validator-learns-producer-classes** — `error_types:` accepts
  producer vocabularies; unattributable keys warn
- **TD-acquire-prefix-fallback** — exact class falls back to
  `acquire/*`
- **TD-merge-validator-warnings** — warnings merged into both responses
- **TD-wire-commit-response-fields** — base-Commit `version_id`
  persisted; metadata surfaced
- **TD-plumb-validation-roles** — handshake plumbed for
  executor/publisher peers
- **TD-peer-tls-enforcement** — `required` verified TLS at every peer
  dial site
- **TD-tls-mode-validation** — `tls` becomes a validated `off |
  required` enum
- **TD-emit-work-completed** — emitted at terminal application
- **TD-named-lock-metric** — labeled acquisition counter
- **TD-comment-drift-sweep** — ten lying comments fixed
- **TD-delete-archived-author-guide** — obsolete archive file deleted

### Design changes

- Tensions resolved: `delegation-and-fanout-share-runtime-primitive`,
  `reaper-vs-bail-abandon-asymmetry`,
  `sqlite-vs-memory-reject-asymmetry`
- Concept created: `child-execution`
- Concepts mutated: `delegation`, `fan-out`, `terminal-resolution`,
  `publisher-subscription`, `publisher`, `advisory-lock`,
  `persistence-database`, `blob-backend`, `replica`, `error-policy`,
  `wait-set`, `cascade`, `claim-co-holdership` (frontmatter only)
- Stories created: 13 (one per story above)
- Decisions created: 39 (one per TD above)
