# Sprint: GitHub-issue queue drain

## Intent

Drain the queue rows corresponding to the repository's remaining open GitHub
issues. No single theme — the items are a vocabulary sweep, a dead-knob
deletion, a demonstrability recipe, and two design-corpus honesty fixes.

Promoted issue ids:

- `store-claim-producer-vocabulary-split` (gh#34)
- `callback-host-dead-knob` (gh#29)
- `no-bundled-park-resume-demo` (gh#39)
- `object-store-sensor-cloud-backends-absent` (gh#37)
- `signal-taxonomy-prose-enumeration` (gh#32)

## Corpus deltas

### Amend decision: object-store-watching-model

Full new body of `.ok-planner/design/decisions/object-store-watching-model.md`.
The Choice section is corrected to stop asserting cloud backends that do not
ship: the model is generalized and pluggable by design, the current build
registers the filesystem backend only, and the in-memory backend is a test
fixture, not a shipped store. The Rationale gains the owner's ruling for
keeping the generalization; the Alternatives gain the considered-and-rejected
rename.

```markdown
---
decision: object-store-watching-model
status: as-is
---

# Deposits are watched through one object-store abstraction

## Choice

The deposited-content story is delivered by a single sensor built on an
object-store abstraction — named buckets and key prefixes served by pluggable
backend listers. The abstraction admits real object stores as backends by
design; the current build ships the local filesystem as its only registered
backend (first-level directories as buckets, files as objects), alongside an
in-memory backend that is a test fixture, not a shipped store.

## Rationale

Everything that makes watching trustworthy — subscriptions, polling,
watermarks, durable seen-state, idempotent publishing, the settle window — is
identical regardless of where content physically lives; only listing differs.
Folding the filesystem in as a backend keeps one idiom for the job, and the
filesystem maps onto the object model losslessly. A dedicated filesystem
sensor would duplicate roughly ninety percent of the machinery to change only
the listing call. Keeping the generalization while shipping only the
filesystem backend is deliberate: the backend seam is a single listing
operation, so a real object-store backend is a drop-in lister, and retiring
the abstraction would buy nothing while closing that door.

## Alternatives

- A dedicated filesystem sensor with native path and glob semantics —
  considered and rejected: a second idiom for the same job, duplicating the
  watch machinery.
- Event-driven detection (filesystem notification, bucket notification) —
  per-backend mechanisms that fracture the uniform model, buying latency the
  use case does not need.
- Renaming the sensor to a dedicated filesystem watcher and retiring the
  object-store abstraction until a cloud backend ships — considered and
  rejected: the abstraction's cost is one small interface, and the rename
  would churn the shipped surface twice.

## Proof

The sensor's tests drive multiple backends — in-memory and local filesystem —
through the identical watch-and-publish path, and a backend has no other path
to publish through, so per-backend machinery divergence has nowhere to exist.
```

### Amend concept: signal

Full new body of `.ok-planner/design/concepts/signal.md`. Two changes, both
removing duplication of code-owned closed sets (adopting the posture
`concept:transition-reason` already uses — describe the set's semantics, defer
membership to the code): (1) the `transient/*` taxonomy subsection no longer
hand-enumerates the transient leaf list; it states the semantics, defers
membership to the signal-taxonomy code, and keeps only the two leaves that
carry design commitments of their own (`transient/park`,
`transient/await_async`); (2) the payload-schemas section no longer enumerates
per-type field membership or the field-rename table; it states the renaming
principle and defers the concrete forms to the envelope code. The three
top-level kinds, the closed `terminal/*` family, `attribute/<key>/changed`,
and every invariant remain — they are design commitments the corpus owns.

```markdown
---
concept: signal
status: as-is
aliases: []
---

# Signal

## What it is

A **signal** is the unified emission shape for any transition that affects a
node-run. Every signal is a type-path plus a typed payload object: a `type`
field carrying a canonical hierarchical type-path (slash-separated,
hierarchical, validator-enforced) and a `payload` field carrying a structured
object (typed per type-path; see "Payload schemas" below).

Once emitted, the signal feeds two consumers:

1. **Cascade walker.** Subscription edges keyed by type-path prefix select
   candidate receivers; a CEL `when:` predicate evaluated against the payload
   gates wait-set insertion. The payload is consumed at walk-time and is NOT
   propagated to subscribers — wait-set rows carry no payload (see
   `concept:wait-set`). Subscribers receive the wake (the wait-set drain
   triggers their gate-eval), not the payload.
2. **Audit log.** Every signal writes one row to the persisted audit-event
   ledger with the event kind set to the signal's type-path string and the
   audit payload set to the signal payload. This is the only path on which the
   payload is durably preserved. Audit emission is unconditional and
   independent of subscribers.

The signal vocabulary unifies the historical parallel surfaces (run outcome,
transition reason, subscription's structured-filter fields) into one
type-path-plus-payload contract.

## Purpose

Make "what just happened to a node-run" one vocabulary across cascade-fire,
audit, and subscription. A single subscription surface (`type:` path + `when:`
CEL predicate) lets operators reason uniformly about every observable
transition; a single audit vocabulary (the signal type-path as the audit-event
kind) lets observability tooling describe what happened without spelunking
through overlapping enums; a single emission discipline lets new transitions
become first-class observable events without inventing new switch-paths.

## Signal type-path taxonomy

Three top-level kinds. Type-paths are canonical and validator-enforced.

### `terminal/*` — run-terminating: the run ends at this signal

The `terminal/*` leaves are exactly two kinds: `terminal/success` and
`terminal/error/<error_class>` (the `error_class` segment may itself contain
`/`). These are the ONLY signals that end a run — no further dispatch will
follow on the same run. Subscribers fire on these via the cascade walker; they
are the surface authors target when wiring downstream reactions to a node's
outcome.

**`terminal/error/abandoned`** is a canonical `terminal/error/<class>` leaf
with `class=abandoned`, emitted by the auto-terminal handler when a held claim
resolves to Abandon (per `concept:auto-terminal`). It is not a new top-level
root; subscribers match it via the exact path or via the `terminal/error/*`
wildcard, uniform with other error-class signals. See
`decision:terminal-error-abandoned-as-error-class`.

A held node-run's own `running → held` transition emits NO terminal signal —
the cascade walk is deferred to the auto-terminal handler, which fires the
terminal/success or terminal/error/abandoned at the moment the handle is
promoted (per `decision:held-as-state-not-phase`).

### `transient/*` — dispatch-internal: dispatch settled, run continues

Transient signals mark moments when this particular dispatch concluded but the
run continues via another dispatch — a retry, an infra-classed error caught
mid-retry, a release-and-requeue, an async-callback deferral, or a park.
Membership of the `transient/*` leaf set is owned by the signal-taxonomy code,
not enumerated here; the taxonomy validator is the closed set's enforcement
point. Two leaves carry design commitments of their own: `transient/park` is a
single leaf — there is no park-reason taxonomy; a park's WHY-annotation rides
its tags — and `transient/await_async` is the async-callback deferral, which
is not a park (the node stays in `running` state until the callback's eventual
terminal settles it).

Every `transient/*` leaf is **audit-only**: it writes to the audit ledger for
forensics but does not fire cascade, and subscriptions targeting any transient
leaf are rejected at template registration — operators reacting to an
in-flight event subscribe instead to the eventual `terminal/success` or
`terminal/error/<class>` emitted when the run settles for real.

### `attribute/<key>/changed` — attribute writes

Emitted per attribute key whose value differs from the prior run's persisted
value at settlement (per `concept:attribute`). The diff is computed against
the most-recent settled run of the same node in the **same RunScope** as the
settling run — nowhere else. RunScopes never span frames (per
`concept:run-scope`), so same-scope necessarily means same-frame: the
diff-gate sees only runs produced inside the current frame. When no prior run
of the same node exists in the current scope, the diff-gate has nothing to
compare against, and every populated attribute key differs from "nothing" — so
`attribute/<key>/changed` fires for every key in the settling bag. First
dispatch of a node in a new frame's root RunScope is that unconditional-fire
case, by design: a new frame starts fresh (per `concept:frame`,
`concept:attribute`); there is no "prior frame's value" for the diff-gate to
observe. Keys present in the prior same-scope run but absent from the current
run emit with `value: null` (deletion).

The diff-gate's job is to suppress redundant cascade rounds **inside a single
frame** (self-cascade converging on a stable value across intra-frame rounds)
— not to suppress cascade rounds across frames. Cross-frame convergence is not
a rimsky feature; a multi-frame workflow that must terminate on value
stability carries its convergence signal in the message payload or observes it
via external state (per `concept:frame` invariants and common pitfalls).

## Payload schemas

Each signal type's payload is a typed object. The CEL environment binds these
field types at template registration so subscribers' `when:` predicates
parse-check. The per-type payload schema is a property of the concept — each
type-path resolves to one payload shape — but per-type field membership is
owned by the emission code and the CEL environment construction, not
enumerated here.

`terminal/*` payloads (the run-terminating settlements: success and
error/<class>) bind two parallel discriminator slots that subscribers can
predicate on via CEL: a `tags: list<string>` field for CEL `in` predicates
over `payload.tags` (the executor-emitted, emission-scoped discriminator set —
see `concept:terminal-tag`), and an `attributes_delta: map<string,dyn>` field
for CEL predicates over the individual attribute mutations carried by the
settling verdict (the executor-emitted, node-state-persistent mutation set —
see `concept:attribute`). The two slots have independent lifecycles (tags are
ephemeral; attributes-delta merges into the per-run attribute ledger) but
share the same emission and the same CEL environment, so a subscriber's
`when:` filter may reference either or both freely. Transient park payloads
carry tags and the park's reason fields for audit forensics; they carry no
`attributes_delta` slot because park is dispatch-internal and does not write
attributes — attribute mutation is a feature of the run-terminating verdicts
only (see `decision:uniform-attributes-delta`).

The signal envelope's outer field is `payload`. Where a signal's payload wraps
an opaque sub-object whose wire carrier also names its own opaque field
`payload`, the inner field is renamed with a domain prefix on the rimsky side
— a rimsky-side rename only; wire field names do not change, and CEL
predicates against the signal payload see the renamed field. The concrete
renamed forms are owned by the envelope code, not enumerated here.

## CEL filter language

Subscription `when:` predicates compile at registration time and evaluate at
cascade-walk time.

- **Bindings:** `type` (string) and `payload` (object).
- **Schema binding for exact type-paths:** when `type:` is an exact emit-shape
  path (no trailing `*`), field references in `when:` parse-check against the
  resolved payload schema (the per-type payload shape). References to fields
  not in the schema reject at registration.
- **Schema binding for prefix type-paths:** when `type:` ends in `*`,
  `payload` is bound as CEL `dyn` (dynamically-typed); no field-name check at
  registration. Field references that don't resolve on the actual signal
  evaluate to the spec's safe-navigation default (Eval returns false).
- **Functions:** CEL's standard library (string, list, map, math, time). No
  domain-specific helpers in this spec.

## Boundaries

Owns:
- The canonical type-path taxonomy (three top-level kinds + leaf rules).
- The per-type payload schema.
- The CEL filter language: env construction, predicate compilation,
  evaluation.
- The audit-emit pathway that writes each signal to the persisted audit-event
  ledger.
- The signal-envelope construction helpers shared by all emission sites.

Does NOT own:
- The cascade walk itself or subscription-edge map construction (lives in
  `concept:node-subscription` / `concept:cascade` — both signal-driven).
- The wait-set ledger that drives dispatch eligibility (lives in
  `concept:wait-set`).
- Policy resolution — what tuple the runtime should produce on a given
  terminal kind (lives in `concept:error-policy` /
  `concept:terminal-resolution`).
- The wire executor protocol (signals are emitted on the rimsky side from the
  wire outcomes, not by the executor directly).

Adjacent: `concept:node-subscription`, `concept:error-policy`,
`concept:cascade`, `concept:wait-set`, `concept:event-log`,
`concept:executor`, `concept:terminal-tag`.

## Invariants

- **Type-paths are canonical and validator-enforced.** Emit-shape validation
  rejects paths outside the taxonomy; subscription-type validation
  additionally rejects positional wildcards.
- **Every transition that affects a node-run emits exactly one signal.** No
  double-emit; no missing emit.
- **Audit-log emission is unconditional.** Every signal writes one row to the
  persisted audit-event ledger regardless of whether any subscriber exists.
- **Cascade-fire is `signal is a settling kind && subscription edge match &&
  CEL predicate evaluates true`.** The settling kinds are `terminal/success`,
  `terminal/error/<class>`, and `attribute/<key>/changed` (see
  `concept:cascade`'s firing-gate predicate); every `transient/*` signal is
  excluded before any edge lookup runs, and template registration rejects a
  subscription targeting one. No separate sender-side gate.
- **Wildcard syntax is trailing-`*` only.** A trailing-`*` prefix matches all
  leaves under it; no positional wildcards; no full glob. Operators wanting
  more complex patterns express them via CEL.
- **Only `terminal/*` signals end a run.** A run terminates at exactly one of
  `terminal/success` or `terminal/error/<class>` and at no other type-path.
  `transient/*` signals (including `transient/park`) mark dispatch-internal
  moments where this dispatch concluded but the run continues via another
  dispatch — a retry, a release-and-requeue, an async-callback resolution, or
  a park-wake — that eventually settles on a `terminal/*` leaf.
- **`terminal/*` payloads carry both `tags` and `attributes_delta`.** Tags are
  the emission-scoped discriminator slot (matched via CEL `when:` filter on
  `payload.tags`; see `concept:terminal-tag`); attributes_delta is the
  persistent state-mutation slot (matched via CEL predicates over
  `payload.attributes_delta`; merged into the per-run attribute ledger per
  `concept:attribute`). Both ride every `terminal/success` and
  `terminal/error/<class>` payload so subscribers express interest with one
  filter language across the run-terminating family.
- **Every `transient/*` signal is audit-only and writes no attributes.** Each
  transient leaf writes to the audit ledger for forensics but does not fire
  cascade; template registration rejects any subscription whose `type:`
  targets a transient leaf, exact or wildcarded. Operators reacting to an
  in-flight event subscribe instead to the eventual `terminal/*` settlement.
  Transient payloads do not carry `attributes_delta` on the wire and do not
  mutate the per-run attribute row — attribute writeback is a feature of
  run-terminating verdicts only (see `decision:uniform-attributes-delta`).
  Executors that need to thread state across a park-and-resume boundary use
  scratch (see `concept:parked-state`).
- **CEL is the filter language; exact-type subscriptions parse-check field
  references against the resolved payload schema; prefix-type subscriptions
  bind `payload` as `dyn`.** This keeps tight checking for the common
  exact-type case while letting prefix subscriptions span heterogeneous
  payload shapes.
- **`transient/park` is a single leaf.** There is no park-reason taxonomy; a
  park's WHY-annotation rides its tags. The await-async-callback outcome is a
  transient (`transient/await_async`), not a park — the node stays in
  `running` state during the callback wait.
- **The wait-set `topic_kind` discriminator is a faithful projection of the
  signal top-level kind:** each of the three canonical kinds (terminal,
  transient, attribute) maps to its own `topic_kind` value, and `state` is
  admitted as a defensive fallback for rows whose pattern does not map to a
  canonical kind (see `concept:wait-set`,
  `decision:wait-set-topic-kind-taxonomy`).
- **The `attribute/<key>/changed` diff-gate baseline is same-RunScope only.
  There is no cross-scope fallback.** The gate consults exactly one row: the
  most-recent settled run of the same node in the same RunScope as the
  settling run. Same-scope necessarily means same-frame (RunScopes never span
  frames — `concept:run-scope`). When no such row exists, every populated key
  differs from "nothing" and the signal fires. This is the frame-isolation
  invariant applied to signal emission: a signal-emission decision may never
  read data from any other frame.
- **No signal-emission decision reads persisted state from a prior frame.**
  Every emission decision — subscription-edge match, CEL `when:` evaluation,
  diff-gate comparison, cascade-mode bag-equality dedup — consults only data
  produced inside the running frame. Persisted attribute rows from prior
  frames exist on disk for operator observability and audit forensics only,
  and are never consulted by the runtime while it decides whether to emit or
  suppress a signal.
```

### New story: bundled-park-resume-recipe

Full body of `.ok-planner/design/stories/bundled-park-resume-recipe.md`. The
section-heading convention (`## Role` / `## Capability` / `## Business value`,
"I can" verb) deliberately matches every other story in the project; the
project-wide story-form sweep is already queued as its own issue
(`stories-non-canonical-heading-shape`).

```markdown
---
story: bundled-park-resume-recipe
status: as-is
---

# Operator demonstrates park-then-resume on the bundled stack

## Role

As an operator evaluating rimsky's parking behavior, I can run a
self-contained, copy-runnable recipe on the bundled stack that drives a node
through a real park and its resumed completion, so that I can see
park-then-resume work end to end before wiring a real rate-limited upstream.

## Capability

A bundled park-then-resume recipe that is self-contained — everything it needs
ships with the stack, no external endpoint — and that induces its park through
the production parking path, not a conformance probe.

## Business value

Operators and template authors can observe rimsky's most temporal behavior —
park, timed wake, re-dispatch, settle — on a clean checkout, without standing
up an external service to trigger it.

## Acceptance

An operator runs the recipe against the bundled stack on a clean checkout: the
driven node enters the parked state with a near-term resume time; the
supervisor wakes it at that time and re-dispatches; the run settles
successfully. All of it is observable through the stack's ordinary surfaces,
and nothing outside the checkout is involved.

## Falsifier

The recipe requires an endpoint that does not ship with the stack, OR the
induced park travels a stub or probe path rather than the production parking
mechanism, OR the parked node never resumes to a successful settlement.

## Proof

Executable proof — an end-to-end exercise on the bundled stack exhibiting the
parked state and the subsequent resumed success.
```

### Amend TOC: stories.md

One added bullet in the alphabetical position under `## Stories` in
`.ok-planner/design/stories.md` (no other line changes):

```markdown
- `bundled-park-resume-recipe` — Operator demonstrates park-then-resume on the bundled stack.
```

## Work items

- **Store-vocabulary sweep, both tiers.** No "store" vocabulary remains where
  claim-producer is meant, per the repo uniformity rule, pre-v1 break-freely.
  User-facing tier: the `STORE_FILESYSTEM_CONFIG` / `STORE_POSTGRES_CONFIG`
  config env vars (renamed per `decision:env-var-convention-across-modes`),
  the `store-filesystem` / `store-postgres` container binary/entrypoint names,
  and the claude-agent `cwd_from_store` attribute with its error text (renamed
  to claim-producer vocabulary in the shipped attribute schema). Internal
  tier: the `stores :=` locals and exported parameter names in the config
  loader, the harness `StartFilesystemStore` / `FilesystemStoreSpec` /
  `StartPostgresStore` exports, the `package stores` declaration and
  `stores_redesign_smoke_test.go` filename under the claim-producer scenario
  tree, and the `fs-store` / `pg-store` example template names. No compat
  shims; the renames are flagged for the next release notes as a pre-v1
  break (existing deployments carrying the old env vars or entrypoint names
  fail loudly). Close GitHub issue 34 when this ships.

- **Delete the dead callback-host knob.** The claude-agent executor no longer
  reads `RIMSKY_EXECUTOR_CALLBACK_HOST` and carries no `CallbackHost` option:
  the internal MCP server's loopback bind is the deliberate, only behavior
  (it serves a child process on the same host). Operators are no longer
  offered a knob that has no effect. Close GitHub issue 29 when this ships.

- **Bundled park-then-resume recipe.** Realizes
  `story:bundled-park-resume-recipe`. The bundled stack gains a
  self-contained way to induce a real park: a reference endpoint that
  rate-limits the first request (429 with a short retry-after) and succeeds
  the retry, shipped with the examples surface, plus a copy-runnable
  walkthrough driving the bundled HTTP-node executor against it through
  park → timed wake → re-dispatch → success. The recipe's end-to-end proof
  exhibits the parked state and the resumed success and carries the
  `@story: bundled-park-resume-recipe` annotation. Close GitHub issue 39 when
  this ships.

- **Apply the object-store decision amendment.** Delta-only (no code change):
  copy the amended `decision:object-store-watching-model` body into place.
  Close GitHub issue 37 when applied — the reworded decision is the
  deliberate-boundary statement that issue asked for.

- **Apply the signal-concept amendment.** Delta-only (no code change): copy
  the amended `concept:signal` body into place. Close GitHub issue 32 when
  applied.

## How to execute this sprint

This sprint is self-sufficient. Whoever executes it — an inline
working session, an agent this file is handed to via the native
`goal` mechanism, or an orchestrator that does its own planning —
proceeds the same way.

1. Read the sprint whole first — intent, deltas, work items,
   completion contract — before touching anything. Do not go looking
   for context behind it (not in `issues.jsonl`, not in `history/`).
   The sprint is self-sufficient by construction; a genuine gap is
   raised with the owner, never filled by inference.

2. Stage the work. The items above are a flat, unordered list; group
   them by theme, file surface, or dependency and order the groups so
   nothing is built on something not yet there. Staging lives in the
   executor's working state — a task list, an orchestrator's graph.
   It is never rewritten into a plan document: this sprint is the
   whole brief.

3. Apply each corpus delta as part of the work that realizes it —
   copy the final-form body into `.ok-planner/design/` verbatim, or
   delete the file for a retirement. A delta no work item implements
   (a clarification, a retirement) is applied on its own.

4. Build stage by stage. Every new or amended story and decision gets
   its proof: present, carrying its `@story:` / `@decision:`
   annotation, and able to actually fail under a producible falsifier.
   Write the proof with the work, not at the end.

5. Completeness is the floor. Never stub, defer, narrow, no-op, or
   leave a `TODO` in place of a promised outcome. A capability the
   deltas or work items promise is delivered in full, or the blocker
   that prevents it is surfaced — never silently dropped.

6. Never destroy uncommitted work. Stage progress as each stage
   finishes (`git add -A`) so a stray revert cannot reach it. Do not
   run `git checkout`/`restore`/`reset`/`stash`/`clean` on your own
   initiative; fix a bad edit forward by editing again.

7. Work unsupervised to a defensible done — no pausing for approval,
   confirmation, or progress checks. Stop only on a genuine blocker:
   a credential or access that cannot be obtained, a step literally
   impossible in the current state, or a destructive/irreversible
   action not clearly authorized. Ambiguity is not a blocker — pick
   the most plausible reading and continue, surfacing the choice at
   the end. (An orchestrator that supervises its own executors folds
   this into its own control.)

8. Close by running `/certify`. It brings the work into alignment
   with this sprint, discharges the completion contract below via
   `/prove` and `/audit`, runs the code-review and
   design-doc-compliance cycles, drives every fixable finding to
   clean through a no-discretion fix loop, and presents outcomes and
   divergences to the owner. `/certify` archives the sprint once it
   certifies clean.

## Completion contract

The work is not done until all of the following hold:

1. The design corpus matches every delta above (applied verbatim).
2. `/prove` returns clean over all new and touched stories and
   decisions: every proof present, passing, and non-vacuous.
3. `/audit` has been run last: mechanical findings fixed in-cycle;
   judgment findings filed to `.ok-planner/issues.jsonl` for the next
   sprint.
