# Instance Lifecycle: Durable-by-Default, Opt-in Auto-Terminate — Design Sketch

**Date:** 2026-06-03
**Status:** Sketch (not a spec; not authorization to build)

## Idea

An instance is meant to be **durable by default**: created from a template by
the user, it stays active until the user terminates it. That durability is the
whole point of an *instance* — it is what distinguishes it from a one-shot
"run" or "call." An instance runs whenever one of its nodes is invalidated
(by a direct user invocation or by a message), and each invalidation resolves
in a frame; a single instance resolves many frames over its life. Nothing
should terminate it on its own.

Two corrections fall out of that:

1. **Auto-termination becomes opt-in, not the default.** A flag set at
   instantiation time marks an instance "auto-terminate"; when set, the
   instance terminates after its next run. Unset (the default) means the
   instance never self-terminates. Force-terminate remains unconditional and
   is the normal end-of-life path.

2. **Termination is fully independent of sensors and nodes.** Whether an
   instance has a `publisher-subscription` (a sensor), or any particular
   nodes, has *no* bearing on whether or when it terminates.

This supersedes a longstanding behavior that was never captured in the
`instance` concept, and supersedes a wrong-shaped fix that the
acceptance-coverage-recovery plan introduced on top of it.

## Shape

### What the code does today (the behavior being corrected)

`MarkInstanceTerminatedIfDone` (`lib/foundation/persistence/postgres/frames.go:119`,
sqlite mirror at `lib/foundation/persistence/sqlite/frames.go:156`) stamps
`rimsky_instances.terminated_at = now()` whenever an instance has, in one
predicate:

- no `queued`/`running` frames, **and**
- no in-flight node-runs (`phase IN ('pending','active','held')` with
  `state IN ('stale','running')`), **and**
- (added by the coverage plan's fix #2) no `active` row in
  `rimsky_publisher_subscriptions`.

It runs at **every** `frame.end`, for every instance, unconditionally — the
single call site is the frame engine (`lib/graph/frame/engine.go:135`, inside
`transitionFrameEnd`). So once an instance's work settles, the next `frame.end`
terminates it. `terminated_at` is **set-once and never cleared** (no
"un-terminate" exists; the reversible idle state is the separate `paused`
column). A terminated instance **rejects** any further message
(`lib/control/controlapi/messages.go:182`, `errInstanceTerminated`), so a
future invalidation cannot revive it — it must be re-instantiated from the
template.

Git history puts this auto-terminate-on-drain behavior at control-plane v1
(`git log -S` → commit `5b568c8`, "Control-plane v1 + store lifecycle
protocol", 2026-05-03); the named function arrived with the persistence
refactor (`206a7b5`) a day later. So this is longstanding, not a recent
regression — but it contradicts the durable-by-default intent and was never
written into the `instance` concept (which frames *force-terminate* as the
production path to terminal and never documents the on-drain path).

This behavior is fine for a *batch* instance (run a graph once, settle, get
reaped) — which is what the existing test corpus exercises — but wrong for the
durable/reactive instances the design actually intends. The
acceptance-coverage-recovery plan's Gate-1/8 (a real sensor driving a node
repeatedly) was plausibly the first test to drive a genuinely long-lived
instance, which is why it surfaced the conflict — and the fix applied there
(#2: "don't auto-terminate while an `active` publisher-subscription exists")
patched the symptom by coupling termination to sensors, exactly the coupling
this design forbids.

### Intended behavior

- **Default:** an instance never auto-terminates. It lives until force-terminate.
- **Opt-in `auto_terminate` flag** (set at instantiation): the instance
  terminates after its next run.
- **Force-terminate:** unconditional; terminate means terminated.
- **Independence:** the termination decision reads nothing about
  publisher-subscriptions or node presence.

### What changes (design-level — full survey deferred to spec/plan)

- **Schema:** add an `auto_terminate boolean NOT NULL DEFAULT false` column to
  `rimsky_instances`, with a new numbered migration in both
  `lib/foundation/persistence/{postgres,sqlite}/migrations/`. Pre-v1, this is a
  plain additive migration (no compat shim; see `.claude/rules/rules.md`
  "Pre-v1 — break freely").
- **Create path:** persist the flag at instance creation from wherever it is
  sourced (see Open questions) — `route:POST /instances` and the instance row
  builder in `lib/foundation/persistence/*/instances.go`.
- **Terminal predicate:** `MarkInstanceTerminatedIfDone` gates on
  `auto_terminate = true` and **drops the `rimsky_publisher_subscriptions`
  clause entirely** (reverting fix #2). The drain sub-predicate (no
  queued/running frames, no stale/running node-runs) stays, but only matters
  once the flag is set.
- **Concept docs:** update `concept:instance` to state the durable-by-default
  lifecycle, the opt-in auto-terminate flag, and the "terminates after its next
  run" semantics. `concept:cascade` needs **no** change — it already says
  "Cascade always happens in a frame"; the only cascade fix is to code comments
  / prose (e.g. fix #2's comment invoking "concept:cascade's
  reactive-to-external-change use case") that stretched "cascade" across frames.

### Relationship to the acceptance-coverage-recovery work (in flight)

- **fix #2** (`frames.go` publisher-subscription coupling) → **stays in the tree
  for now** — it ships with the coverage-recovery work being committed/archived,
  rather than being backed out piecemeal. **Backing it out is part of THIS
  sketch's implementation pass:** that pass removes the
  `rimsky_publisher_subscriptions` clause from the terminal predicate and, in the
  same pass, carries the dependent work the back-out forces — updating the tests
  that assume the publisher-subscription carve-out, reworking the Gate-1/8 test,
  and re-running review. Backing it out *now* (outside this pass) would incur the
  same test + review churn without the replacement `auto_terminate` flag in
  place, so it is deliberately sequenced into this pass instead.
- **fix #1** (`messages.go` seeds a delivery frame on message emit) → appears
  *consistent* with the durable model ("an instance runs when a node is
  invalidated through a message, resolving in a frame"), so likely stays — but
  re-verify against the corrected lifecycle.
- **fix #3** (`cmd/rimsky-control-api/main.go` wires `cfg:publishers`) →
  unrelated and correct; stays.
- **Gate 1/8** (`lib/services/test/scenarios/sensor_cascade_e2e_test.go`) →
  reworked: under durable-by-default the sensor instance stays alive with no
  flag and no publisher-subscription coupling, so the test should assert that,
  not the carve-out.

## Open questions

- **Flag source:** is `auto_terminate` a `route:POST /instances` create-request
  parameter, a template-level field, or both (template default + per-instance
  override)?
- **"Terminates after its next run" — exact semantics:** what counts as "its
  next run"? The first `frame.end` after creation? The first frame that
  actually executes a node-run (vs. an empty/no-op settle)? Define "a run"
  precisely against the frame/node-run model.
- **Existing batch-style callers and tests:** do they opt in via the flag, or
  is there a deployment-level default? How many scenario tests currently assume
  auto-termination, and do they switch to setting the flag or to asserting
  durability?
- **Naming:** `auto_terminate` vs `terminate_after_run` vs `one_shot` vs
  `ephemeral`.
- **Key reuse:** with durable-by-default, `instance_key` is freed only on
  explicit force-terminate + delete (the reaper requires terminal first,
  `handleDeleteInstance`). Is that the intended key-reuse story, or is a TTL /
  operator-sweep wanted? (Likely out of scope, but it changes the key-churn
  contract.)

## Risks / unknowns

- **Blast radius on tests is potentially large.** Many scenario/integration
  tests likely assert an instance reaches terminal after its work; flipping the
  default breaks those and each needs either the flag set or an assertion
  change. A full survey (`rg` for `TerminatedAt`/`terminated_at`/`MarkTerminated`
  assertions and the lifecycle-subscriber tests) is required before estimating
  the change.
- **Lifecycle fan-out frequency changes.** `OnInstanceTerminated` fires to
  subscribed stores on the `terminated_at` NULL→timestamp transition; durable
  instances fire it far less often (only on explicit terminate), which may
  matter to store-side per-instance cleanup expectations.
- **Resource growth.** Durable instances accumulate rows (instances, nodes,
  node-runs, frames, events) until explicitly terminated+deleted. Long-lived
  deployments need a reaping/retention story; today's auto-termination was
  doing some of that implicitly. Worth naming even though a retention design is
  separate work.
- **Held-durable claims / run-scopes** on now-long-lived instances: claim
  handles and run-scopes that auto-termination-then-delete used to release
  (`runtime.ReleaseHeldDurableClaims` on delete) now persist longer; verify no
  unbounded accumulation.

## What this is not

- Not a change to force-terminate or delete semantics — those stay as-is
  (`handleTerminateInstance` stamps `terminated_at` unconditionally; delete
  reaps a terminal instance and frees the key).
- Not a change to the `concept:cascade` doc (it already says "Cascade always
  happens in a frame"). The cascade correction is limited to code comments and
  agent/human prose that mis-stretched the word across frames.
- Not a redesign of frames, node-runs, the `paused` state, or the
  publisher/sensor protocol.
- Not the `failed`-state edge of fix #2 — fix #2 is being removed, so that edge
  disappears with it rather than needing a separate patch.
- Not a retention/TTL design for durable instances — flagged as a consequence,
  but its own piece of work.
