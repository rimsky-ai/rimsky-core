# Reactive Loops + Lifecycle Handlers — Design Sketch

**Date:** 2026-05-05
**Status:** Sketch (not a spec; not authorization to build)
**Driving consumer:** Verantel docs-corpus pipeline
(`/Users/patrick/Documents/projects/research/verantel/.ok-planner/sketches/2026-05-05-docs-corpus-rimsky-pipeline-sketch.md`)

## Idea

Refine Rimsky's reactive node-graph model so it can express "loop until
there's nothing left to do" patterns natively, without grafting forward-
workflow primitives (cron-as-heartbeat, retry-as-loop, error-state-as-
terminator) onto the reactive system.

Four small, composable changes:

1. **Per-emit message-frame control** — each invalidate emit point
   (operator API, error-policy invalidate action, new on-complete invalidate
   action, future on-xxx hooks) declares whether it fires in the current
   frame or buffers to the next. Sensible defaults match today's implicit
   behavior; templates override per-emit when they have reason to.

2. **Configurable lifecycle handlers** — each step in a node's per-frame
   lifecycle (claim acquire outcome, executor terminal outcome) has a
   resolution declared in the template. Defaults match today's hardcoded
   supervisor logic. The most useful initial overrides are
   `on_acquire_unavailable: { resolve: pass }` (silent no-op transition
   instead of silent retry) and a unified success-side message emitter
   that subsumes "on_complete: invalidate(targets)."

3. **`last_outcome` as a resolution flavor** — a node's terminal-for-this-
   frame outcome lives in a separate field, not as a new state value. The
   state machine stays small (`fresh | stale | scheduled | running`, with
   `scheduled` transient inside the supervisor's acquisition tx). Flavors
   like `fresh_changed`, `fresh_unchanged`, `passed`, `failed` are
   metadata; the dashboard renders them; the scheduler/supervisor branch
   on them.

4. **Eager-cascade with pre-dispatch upstream check** — invalidate
   continues to mark the transitive dependent set stale (today's
   behavior; preserves operator "rebuild this whole subtree" semantics).
   At each node's dispatch attempt, the supervisor checks the node's
   direct upstreams in the current frame: if at least one resolved with a
   propagating flavor (`fresh_changed`), dispatch; otherwise the node
   passes itself (transitions `stale → fresh` with `last_outcome: passed`
   without invoking the executor). The pass cascades naturally because
   each pass-through node's `last_outcome` becomes the next node's
   upstream signal in dep order.

Together these let a one-node template loop tightly while work is
available and terminate cleanly when it's not — and they do it with
strictly less moving state than today's "stale + invalidate-via-error-
policy" workarounds.

## Why this is reactive-shaped, not forward-workflow-shaped

The dependency graph is acyclic; the message graph is not required to be.
`error_types[X].policy.invalidate` already produces message-edge cycles
on the failure side, and frames terminate via the existing predicate
("no nodes in `stale`, `scheduled`, or `running`"). Adding the symmetric
success-side message emit, plus per-emit frame control, simply finishes
the symmetry. It doesn't introduce new cycle classes; it just gives
templates declarative control over them.

The split-dispatch refinement separates "the node was scheduled to run"
from "the node decided to actually run." Today the supervisor flips a
node `stale → running` atomically with claim acquisition, which means a
claim-Unavailable result has to be expressed as either silent retry
(today, leaks dead polling) or an error class (conflates "no work" with
"failure"). Splitting the transition lets the template declare what
no-work means via `on_acquire_unavailable`.

The eager-cascade-with-upstream-check resolves what looked like a
schedule/unschedule problem in earlier framings. We don't need to
unschedule false-stales — we let the dispatch attempt itself be the
deciding gate, and the pass-through cascade carries the decision forward
in dep order. No new persistent bookkeeping; today's `state` plus the
proposed `last_outcome` is sufficient state to fully describe the
system.

## Shape

### State machine

```
fresh → stale                              (on invalidate)
stale → scheduled                          (on dispatch claim, before claim acquisition)
scheduled → running                        (claim-acquire path returned all-Acquired AND
                                            pre-dispatch upstream check passed)
scheduled → fresh (last_outcome: passed)   (claim-acquire returned Unavailable for any
                                            required claim, OR pre-dispatch upstream check
                                            found no propagating upstream in this frame)
running → fresh                            (executor Complete; last_outcome ∈ {fresh_changed,
                                            fresh_unchanged} per `changed: bool`)
running → failed                           (give_up via error_types policy chain)
running → stale                            (retry / invalidate via error_types policy chain)
failed → stale                             (operator reset / invalidate)
stale → fresh                              (pure-cascade inline; unchanged from today;
                                            last_outcome: pure_cascade)
```

Frame-end predicate: "no nodes in `stale`, `scheduled`, or `running`."
`scheduled` is transient (lives only inside the supervisor's acquisition
tx) and rarely observed by the predicate. `failed` is terminal-for-this-
frame, like the `last_outcome`-flavored `fresh` states.

`last_outcome` enum (initial set; extensible):

- `fresh_changed` — ran, propagating to dependents
- `fresh_unchanged` — ran, not propagating
- `passed` — did not run (claim Unavailable or no propagating upstream)
- `pure_cascade` — pure-cascade inline transition
- `failed` — policy exhausted

The state-transition event log already carries a `cause` field; this
ratifies the field as first-class and gives the dashboard a stable
vocabulary to render.

### Lifecycle handlers

A new template-spec section per node:

```yaml
- type: area-pass
  executor: claude-agent
  stores:
    - { name: content, selector: "@docs-once", intent: rw, alias: doc }

  on_acquire_unavailable:
    resolve: pass                           # default could be `retry: silent` (today) or `pass` (this proposal)
    # alternatives:
    #   resolve: error
    #   error_class: claim_unavailable
    #   resolve: retry
    #   count: 60
    #   backoff: linear
    #   base_delay_ms: 30000

  on_executor_complete:
    resolve: by_changed                     # default: changed=true → fresh_changed; changed=false → fresh_unchanged
    # alternatives:
    #   resolve: always_propagate           # ignore changed:bool, always fresh_changed
    #   resolve: never_propagate            # always fresh_unchanged
    invalidate:                             # NEW success-side message emit (subsumes on_complete)
      targets: [self]
      frame: next                           # default for invalidate emits

  on_executor_blocked:
    resolve: error
    error_class: executor_blocked           # today's hardcoded behavior

  on_executor_errored:
    resolve: error                          # routes through error_types as the executor-supplied class

  error_types: { ... unchanged ... }
```

Semantics:

- Each handler has a default that preserves today's behavior. Templates
  override only the handlers they care about.
- `resolve:` is one of: `pass` (transition fresh, last_outcome=passed,
  no dispatch), `error` + `error_class:` (route through error_types),
  `retry` + retry-shaped fields (today's silent retry, now declarable),
  `by_changed` / `always_propagate` / `never_propagate` (success-path
  flavors).
- `invalidate:` (optional) on any handler emits invalidates alongside
  the resolution. Each `invalidate:` block declares `targets:` and
  optional `frame: in | next`. This subsumes the previously-discussed
  `on_complete: { targets: [...] }` block — `on_complete` is just
  `on_executor_complete.invalidate`.
- The error model (`error_types[X].policy`) is unchanged; lifecycle
  handlers route into it (or around it) per declaration.

Initial implementation surface picks the smallest useful overrides:

- `on_acquire_unavailable` — required for queue-drain semantics.
- `on_executor_complete.invalidate` — required for `on_complete: [self]`
  loops.

`on_executor_blocked` / `on_executor_errored` ship with their defaults
hardcoded; templates can declare them as a template-author-friendly
form of error-types routing if they want, but the initial mechanism
preserves today's hardcoded paths.

### Per-emit message-frame control

Each invalidate emit point gets an optional `frame: in | next` field:

- `in` — the invalidate joins the current cascade; the frame stays open
  until the cascade quiesces. Used when the loop should compress into a
  single frame (long-running drains where per-iteration frames create
  too much bookkeeping noise).
- `next` — the invalidate buffers through `frame.EnqueueOrCoalesce`;
  current frame closes; next frame opens with the target stale. Used
  when each iteration should be a discrete unit on the dashboard
  (typical observability default).

Defaults that match today's implicit behavior:

- Cascade `recalculate` from `running → fresh (changed: true)`: **in-frame.**
- Operator-originated `invalidate` via API: **next-frame.**
- `error_types[X].policy.invalidate`: **next-frame.**
- Lifecycle-handler `invalidate`: **next-frame** by default; template
  overrides per declaration.

Note on `frame_timeout_ms`: under in-frame loops the existing soft warning
("this frame has been open longer than X") becomes uninformative. The
brainstorm should refine this to "no node has made progress in this
window" as the underlying signal — better metric for any frame, not just
loop-y ones.

### Eager-cascade + pre-dispatch upstream check

`invalidate(A)` continues to mark A and all transitive dependents stale.
Today's eager-cascade behavior is preserved for operator-originated and
internal invalidates alike. The dashboard's "blast radius" view still
shows the full subtree at the moment of invalidate.

The new behavior lives at the dispatch boundary. When the supervisor
considers transitioning a stale node to scheduled, it runs a pre-dispatch
upstream check:

```
Are all my direct upstreams resolved (fresh or failed) in this frame?
  No  → stay stale; will be re-checked when remaining upstreams resolve.
  Yes → did any of them resolve with a propagating last_outcome
        (i.e., fresh_changed) in this frame?
        Yes → transition stale → scheduled, proceed to claim acquisition.
        No  → transition stale → fresh (last_outcome: passed), no dispatch,
              no executor invocation, no on_executor_complete handler fires
              (so no success-side invalidate emit either).
```

The "in this frame" qualifier uses the existing `frame_id` correlation:
the check is `WHERE frame_id = current_frame_id` against the state-
transition event log (or the equivalent column on `rimsky_nodes` if we
materialize it).

The pass cascades naturally because pass-through nodes set their own
`last_outcome: passed` and the next dispatch check downstream sees this
when it queries its upstreams. No explicit traversal; no derived
counters; no new persistent state.

### What this collapses

The combined story (lifecycle handlers + per-emit frame control + last_outcome
flavor + eager-cascade-with-upstream-check) collapses several things we
were sketching separately:

- **No `claim_unavailable` error class.** Subsumed by
  `on_acquire_unavailable: { resolve: pass }`; templates that want
  error-class routing can still declare `resolve: error, error_class: ...`
  but the default for queue-shape templates is `pass`.
- **No dedicated `on_complete:` block.** Subsumed by
  `on_executor_complete.invalidate`. The general lifecycle-handler
  `invalidate:` slot replaces it.
- **No new `passed` state.** Subsumed by `last_outcome: passed` on the
  existing `fresh` state.
- **No BFS-frontier bookkeeping.** Subsumed by the pre-dispatch upstream
  check on the existing eager-cascade. Same observable behavior; less
  moving state.
- **No silent-retry-forever footgun.** Templates either declare `retry:
  silent` explicitly (today's behavior, declarable) or get `pass` /
  `error` / bounded-retry semantics by override.

### Cascade behavior summary

- Upstream resolves `fresh_changed` (propagating) → downstream sees
  propagating signal at its own pre-dispatch check; dispatches.
- Upstream resolves `fresh_unchanged` (ran, no propagate) → downstream
  treats this as a non-propagating signal; if no other upstream
  propagated, downstream passes.
- Upstream resolves `passed` (no-work) → same as `fresh_unchanged`:
  non-propagating; downstream may pass.
- Upstream resolves `failed` → today's behavior preserved: downstream
  stays stale until operator intervention. Pre-dispatch upstream check
  treats `failed` as a non-propagating signal AND as "not ready"; the
  node remains stale.

This means a sub-graph downstream of a fully-passed wave passes itself;
a sub-graph downstream of a failed node freezes (correctly, since the
failure was unhandled).

## Templates that benefit

- **Bounded fan-out / drains:** queue-shape pick policies pair with
  `on_acquire_unavailable: { resolve: pass }` and
  `on_executor_complete.invalidate: { targets: [self] }` for
  loop-until-empty. Verantel docs-pipeline is the driving consumer.
- **Probe sweeps:** "probe until you see five clean passes" — the agent
  self-counts and stops declaring `changed: true`; downstream of the
  probe stops cascading once changed-is-false.
- **Ring buffers:** ring-mode pick policies pair with
  `on_executor_complete.invalidate: { targets: [self], frame: next }`
  for tight perpetual loops; no cron required.
- **Long-running drains in one frame:** if observability prefers one
  big frame per drain over per-iteration frames,
  `on_executor_complete.invalidate: { targets: [self], frame: in }`
  collapses the loop into a single frame whose end is the drain.

## Implementation surface (rough)

Tightened during the brainstorm. Indicative only:

- `core/node/state.go` — add `scheduled` as transient; add `last_outcome`
  enum; declare new transitions; reject illegal ones per blessed inv 1.
- `core/storage/postgres/migrations/*.sql` — new migration adding
  `last_outcome` column to `rimsky_nodes` (or extending state-transition
  event's `cause` enum if we materialize it there instead).
- `core/spec/template.go` (or wherever template parsing lives) — accept
  the lifecycle-handler block on node specs; validate handler shapes;
  validate `invalidate.targets` against declared node types in the
  instance; validate `frame` values.
- `core/supervisor/runner_acquire.go` — split acquisition tx as in
  §"State machine"; on Unavailable, route through
  `on_acquire_unavailable` resolution.
- `core/supervisor/runner_dispatch.go` (new helper) — pre-dispatch
  upstream check; consult upstream `last_outcome` filtered by
  `frame_id`; transition pass-through if no propagating upstream.
- `core/supervisor/runner_terminal.go` — fire lifecycle-handler
  `invalidate` emits using `frame.EnqueueOrCoalesce`, respecting
  `frame: in | next`.
- `core/scheduler/recalculate.go` — adjust to use `last_outcome` rather
  than just state when deciding whether to nudge dependents.
- `core/frame/engine.go` — refine `frame_timeout_ms` semantics to
  "no progress in this window" rather than "frame age."
- `test/scenarios/` — at minimum: (a) `on_executor_complete.invalidate:
  [self]` cycles cleanly; (b) `on_acquire_unavailable: { resolve: pass }`
  produces passed transition without firing on_complete; (c) cascade
  pass-through (multi-level downstream of a no-work upstream all pass);
  (d) per-emit frame: in vs next observable difference; (e) operator-
  originated transitive invalidate still cascades eagerly; (f) failed
  upstream still freezes downstream (today's behavior preserved).
- `docs/concepts/node.md`, `docs/concepts/node-state.md`,
  `docs/concepts/cascade.md` — extend descriptions; add lifecycle-
  handler block to node-spec docs; document `last_outcome` enum.
- `docs/architecture.md` — note the split-dispatch refinement and the
  pre-dispatch upstream check.
- `CHANGELOG.md` — Unreleased entry capturing the change and rationale.
- `CLAUDE.md` — update if any new blessed invariant emerges (probably
  none; existing inv 1 covers state-machine legality if new transitions
  are declared properly).

This is a meaningful brainstorm-spec-plan cycle. Not a weekend hack.

## Open questions

- **`on_executor_complete.invalidate` on `changed: false`?** The
  recommendation is "fire it" — `invalidate: [self]` is a "keep going"
  declaration whose semantics are independent of whether *this*
  iteration changed anything. The two propagations (recalculate to
  dependents via `changed: true`; invalidate to targets via the handler)
  are intentionally orthogonal.
- **Mixed dependency outcomes.** A node with deps {A: fresh_changed,
  B: passed} — should the node dispatch (A propagated, that's enough)
  or pass (B suggests no work)? Recommendation: dispatch if **any**
  upstream propagated. The "any" rule preserves today's "any
  invalidation triggers re-render" intuition.
- **`scheduled` state visibility.** Transient inside the supervisor's
  acquisition tx; never observable for long. If a supervisor crashes
  mid-tx, the orphan reaper's view (post-rollback) sees `stale`, not
  `scheduled`. That's the right semantic — the rollback unwound the
  transition.
- **Held claims and split dispatch.** If the held-claim acquirer's
  `Open` returns Unavailable, the held subgraph never activates;
  inheriting nodes never run. Default behavior: those inheriting nodes
  resolve `passed` themselves at their own pre-dispatch check (since
  no upstream propagated to them in this frame). Need to confirm in
  the brainstorm — likely correct but worth nailing.
- **`run_attempt` semantics on no-work.** The executor never ran, so
  `run_attempt` should not advance on the passed path. Confirm.
- **Predicate language for handler conditions.** The current shape
  has `resolve: <enum>` per handler. Future handlers might want
  conditional resolution ("if attribute X says Y, pass; else error"),
  which would introduce a predicate language. Out of scope for v1;
  noted for completeness.
- **Frame-coalesce interaction with self-invalidate.** A node with
  `on_executor_complete.invalidate: { targets: [self], frame: next }`
  produces one self-invalidate per commit. Under `frame_resolution:
  coalesce`, multiple pending self-invalidates collapse to one
  pending frame. That's correct (no double-execute) but worth a
  scenario test.
- **Per-template `frame_timeout_ms` interaction with in-frame loops.**
  Under `frame: in` self-invalidate, the frame stays open for the
  entire drain. The "no progress in this window" refinement above
  handles this, but we should ensure the metric is computable per
  frame without expensive scans.

## Risks / unknowns

- **Test surface for the new cascade behavior.** Multi-node graphs
  with mixed dependency outcomes need broad scenario coverage. Easy
  to write; easy to forget an important case (e.g., diamond invalidate
  with one arm passing).
- **Existing `failed → stale` operator path** unchanged; semantics
  preserved. But operators who currently treat `failed` as "queue
  drained" (workflows that retro-fitted that semantics) get different
  behavior — they see `passed` instead. CHANGELOG must call this out.
- **`on_acquire_unavailable` + held claims.** A node holding multiple
  claims where one is queue-shape and one is regional: queue-shape
  Unavailable should produce `passed`; regional Unavailable is unusual
  (regional claims either succeed or block on conflict). Handler
  semantics may need to distinguish per-claim — or we ship v1 with
  "any Unavailable on any required claim → handler fires" and refine
  later.
- **Migration of in-flight nodes.** If we ship this against a Postgres
  deployment that has live nodes, the new `last_outcome` column needs
  a backfill default and the supervisor's check has to tolerate
  pre-migration nodes (no `last_outcome` recorded → treat as
  propagating to preserve today's behavior).

## What this is not

- **Not a generalized "frame-end predicate hooks" design.** That's a
  bigger primitive (template-level frame-end predicate evaluation,
  predicate language, new scheduling phase). Right shape for a future
  class of patterns; overkill for what this addresses.
- **Not a replacement for cron-driven nodes.** Scheduled nodes still
  exist and are still right for "run this every Tuesday at 3am." This
  proposal is for "run as soon as possible after the previous run,
  until there's nothing left."
- **Not a redesign of the error model.** `error_types` and the
  error-policy chain are unchanged. Lifecycle handlers route into
  error_types via `resolve: error, error_class: ...`, not around it.
- **Not a backwards-compat-breaking change for templates.** Templates
  without lifecycle-handler blocks behave identically. Templates
  without `last_outcome`-aware downstream dependents behave
  identically. New surface is additive; defaults preserve today's
  behavior.
- **Not a generalized state-machine extension.** We're adding one
  transient state (`scheduled`) and one outcome-flavor field
  (`last_outcome`). The proliferation stops there.
- **Not a BFS-frontier scheduler rewrite.** The eager-cascade is
  preserved; the pre-dispatch upstream check is the sole new gate.
  Today's scheduler walks stay essentially unchanged.
