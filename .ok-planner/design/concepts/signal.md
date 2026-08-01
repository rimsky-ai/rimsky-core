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

A held node-run's own `running → held` transition builds and cascades its
terminal signal (`terminal/success` or `terminal/error/<class>`) immediately,
filtered to holding-subgraph co-members only — this is how members coordinate
with each other while the claim is held. Delivery to non-member subscribers,
and the signal's audit-log write, are both deferred to the auto-terminal
handler, which re-fires the same signal kind (unfiltered, to non-members) and
writes the audit-ledger row at the moment the handle is promoted (per
`decision:held-as-state-not-phase`).

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

A signal's type-path is the only audit-event kind for the transition it
describes. `concept:transition-reason` is a distinct, narrower vocabulary
consulted only by the node-state machine's next-state function to validate a
transition — it is never written as an audit-event kind and carries no
payload; signal owns audit identity, transition-reason owns state-machine
validation.

Adjacent: `concept:node-subscription`, `concept:error-policy`,
`concept:cascade`, `concept:wait-set`, `concept:event-log`,
`concept:executor`, `concept:terminal-tag`, `concept:transition-reason`.

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
