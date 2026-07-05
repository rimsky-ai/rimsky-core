---
concept: signal
status: as-is
aliases: []
---

# Signal

## What it is

A **signal** is the unified emission shape for any transition that affects a node-run. Every signal is a type-path plus a typed payload object: a `type` field carrying a canonical hierarchical type-path (slash-separated, hierarchical, validator-enforced) and a `payload` field carrying a structured object (typed per type-path; see "Payload schemas" below).

Once emitted, the signal feeds two consumers:

1. **Cascade walker.** Subscription edges keyed by type-path prefix select candidate receivers; a CEL `when:` predicate evaluated against the payload gates wait-set insertion. The payload is consumed at walk-time and is NOT propagated to subscribers — wait-set rows carry only `(frame, receiver_run, sender_run, topic_kind, subscription_scope)` (see `concept:wait-set`). Subscribers receive the wake (the wait-set drain triggers their gate-eval), not the payload.
2. **Audit log.** Every signal writes one row to the persisted audit-event ledger with the event kind set to the signal's type-path string and the audit payload set to the signal payload. This is the only path on which the payload is durably preserved. Audit emission is unconditional and independent of subscribers.

The signal vocabulary unifies the historical parallel surfaces (run outcome, transition reason, subscription's structured-filter fields) into one type-path-plus-payload contract.

## Purpose

Make "what just happened to a node-run" one vocabulary across cascade-fire, audit, and subscription. A single subscription surface (`type:` path + `when:` CEL predicate) lets operators reason uniformly about every observable transition; a single audit vocabulary (the signal type-path as the audit-event kind) lets observability tooling describe what happened without spelunking through overlapping enums; a single emission discipline lets new transitions become first-class observable events without inventing new switch-paths.

## Signal type-path taxonomy

Four top-level kinds. Type-paths are canonical and validator-enforced.

### `terminal/*` — run-terminating: the run ends at this signal

The `terminal/*` leaves are exactly two kinds: `terminal/success` and `terminal/error/<error_class>` (the `error_class` segment may itself contain `/`). These are the ONLY signals that end a run — no further dispatch will follow on the same run. Subscribers fire on these via the cascade walker; they are the surface authors target when wiring downstream reactions to a node's outcome.

**`terminal/error/abandoned`** is a canonical `terminal/error/<class>` leaf with `class=abandoned`, emitted by the auto-terminal handler when a held claim resolves to Abandon (per `concept:auto-terminal`). It is not a new top-level root; subscribers match it via the exact path or via the `terminal/error/*` wildcard, uniform with other error-class signals. See `decision:terminal-error-abandoned-as-error-class`.

A held node-run's own `running → held` transition emits NO terminal signal — the cascade walk is deferred to the auto-terminal handler, which fires the terminal/success or terminal/error/abandoned at the moment the handle is promoted (per `decision:held-as-state-not-phase`).

### `transient/*` — dispatch-internal: dispatch settled, run continues

The `transient/*` leaves are `transient/retry/<attempt>/<error_class>` (mid-retry signal carrying the attempt counter and the operator-declared error class), `transient/infra/<error_class>` (infra-class error caught mid-retry, fires when `applyTerminalInfraError` increments its retry counter), `transient/release_and_requeue/<error_class>` (fires when the policy chain resolves to release-and-requeue for the given class), `transient/await_async` (the executor await-async-callback outcome — the node stays in `running` state until the callback's eventual terminal settles it), and `transient/park/snooze` plus `transient/park/await_callback` (the executor parks the dispatch; a wake event eventually re-dispatches the run, which then settles to a `terminal/*` for real). `transient/park/*` leaves are exactly the park-reason enum (a two-value closed set fixed on the wire executor protocol).

Transient signals mark moments when this particular dispatch concluded but the run continues via another dispatch. The cascade-firing transients (retry / infra / release_and_requeue / await_async) let advanced subscribers react to in-flight events. `transient/park/*` is **audit-only**: it writes to the audit ledger for forensics but does not fire cascade, and subscriptions targeting it are rejected at template registration — operators reacting to park subscribe instead to the eventual `terminal/success` or `terminal/error/<class>` emitted when the parker wakes and settles for real.

### `attribute/<key>/changed` — attribute writes

Emitted per attribute key whose value differs from the prior run's persisted value at settlement (per `concept:attribute`). The diff is computed against the most-recent settled run of the same node visible from the current run's scope: same-scope prior first (preserving fan-out partition and sub-graph isolation), and only if none, then the most-recent cross-frame prior — the second lookup applies only when the current run is in a root run-scope (i.e., spans frames within one instance, not a nested scope). Same-value resettlements emit nothing for that key, whether the prior run was in the current frame (intra-frame convergence for self-cascade) or an earlier frame at the root-scope layer (cross-frame convergence for message-emit loops). Keys present in the prior run but absent from the current run emit with `value: null` (deletion).

### `message/<kind>/<sender_kind>/<target>` — boundary-crossing messages

Emitted when a `concept:message` arrives at an instance. The three structured filter dimensions (`kind`, `sender_kind`, `target`) live on the type-path leaves, not as separate subscription-entry fields.

## Payload schemas

Each signal type's payload is a typed object. The CEL environment binds these field types at template registration so subscribers' `when:` predicates parse-check. The per-type payload schema is a property of the concept: each type-path resolves to one payload shape.

`terminal/*` payloads (the run-terminating settlements: success and error/<class>) bind two parallel discriminator slots that subscribers can predicate on via CEL: a `tags: list<string>` field for CEL `in` predicates over `payload.tags` (the executor-emitted, emission-scoped discriminator set — see `concept:terminal-tag`), and an `attributes_delta: map<string,dyn>` field for CEL predicates over the individual attribute mutations carried by the settling verdict (the executor-emitted, node-state-persistent mutation set — see `concept:attribute`). The two slots have independent lifecycles (tags are ephemeral; attributes-delta merges into the per-run attribute ledger) but share the same emission and the same CEL environment, so a subscriber's `when:` filter may reference either or both freely. The `transient/park/*` payloads carry tags and the park's reason fields for audit forensics; they carry no `attributes_delta` slot because park is dispatch-internal and does not write attributes — attribute mutation is a feature of the run-terminating verdicts only (see `decision:uniform-attributes-delta`).

### Field-naming convention

The signal envelope's outer field is `payload`. To avoid a bare-`payload` collision when a signal's payload itself wraps an opaque sub-object whose wire carrier also names its own opaque field `payload` (the executor error carrier and the message envelope each carry one), the inner field is renamed with a domain prefix:

| Wire carrier | Renamed-in-signal field |
| --- | --- |
| executor error payload | `error_payload` |

This is a rimsky-side rename only; wire field names do not change. CEL predicates against the signal payload see the renamed fields (`when: payload.error_payload.foo > 5`).

## CEL filter language

Subscription `when:` predicates compile at registration time and evaluate at cascade-walk time.

- **Bindings:** `type` (string) and `payload` (object).
- **Schema binding for exact type-paths:** when `type:` is an exact emit-shape path (no trailing `*`), field references in `when:` parse-check against the resolved payload schema (the per-type payload shape). References to fields not in the schema reject at registration.
- **Schema binding for prefix type-paths:** when `type:` ends in `*`, `payload` is bound as CEL `dyn` (dynamically-typed); no field-name check at registration. Field references that don't resolve on the actual signal evaluate to the spec's safe-navigation default (Eval returns false).
- **Functions:** CEL's standard library (string, list, map, math, time). No domain-specific helpers in this spec.

## Boundaries

Owns:
- The canonical type-path taxonomy (four top-level kinds + leaf rules).
- The per-type payload schema.
- The CEL filter language: env construction, predicate compilation, evaluation.
- The audit-emit pathway that writes each signal to the persisted audit-event ledger.
- The signal-envelope construction helpers shared by all emission sites.

Does NOT own:
- The cascade walk itself or subscription-edge map construction (lives in `concept:node-subscription` / `concept:cascade` — both signal-driven).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- Policy resolution — what tuple the runtime should produce on a given terminal kind (lives in `concept:error-policy` / `concept:terminal-resolution`).
- The wire executor protocol (signals are emitted on the rimsky side from the wire outcomes, not by the executor directly).

Adjacent: `concept:node-subscription`, `concept:error-policy`, `concept:cascade`, `concept:wait-set`, `concept:event-log`, `concept:executor`, `concept:terminal-tag`.

## Invariants

- **Type-paths are canonical and validator-enforced.** Emit-shape validation rejects paths outside the taxonomy; subscription-type validation additionally rejects positional wildcards.
- **Every transition that affects a node-run emits exactly one signal.** No double-emit; no missing emit.
- **Audit-log emission is unconditional.** Every signal writes one row to the persisted audit-event ledger regardless of whether any subscriber exists.
- **Cascade-fire is `subscription edge match && CEL predicate evaluates true`.** No separate sender-side gate.
- **Wildcard syntax is trailing-`*` only.** A trailing-`*` prefix matches all leaves under it; no positional wildcards; no full glob. Operators wanting more complex patterns express them via CEL.
- **Only `terminal/*` signals end a run.** A run terminates at exactly one of `terminal/success` or `terminal/error/<class>` and at no other type-path. `transient/*` signals (including `transient/park/*`) mark dispatch-internal moments where this dispatch concluded but the run continues via another dispatch — a retry, a release-and-requeue, an async-callback resolution, or a park-wake — that eventually settles on a `terminal/*` leaf.
- **`terminal/*` payloads carry both `tags` and `attributes_delta`.** Tags are the emission-scoped discriminator slot (matched via CEL `when:` filter on `payload.tags`; see `concept:terminal-tag`); attributes_delta is the persistent state-mutation slot (matched via CEL predicates over `payload.attributes_delta`; merged into the per-run attribute ledger per `concept:attribute`). Both ride every `terminal/success` and `terminal/error/<class>` payload so subscribers express interest with one filter language across the run-terminating family.
- **`transient/park/*` is audit-only and writes no attributes.** The signal writes to the audit ledger for forensics but does not fire cascade; template registration rejects subscriptions whose `type:` explicitly targets `transient/park/snooze`, `transient/park/await_callback`, or `transient/park/*`. Operators reacting to park subscribe to the eventual `terminal/*` settlement emitted when the parker wakes and settles. Park does not carry `attributes_delta` on the wire and does not mutate the per-run attribute row — attribute writeback is a feature of run-terminating verdicts only (see `decision:uniform-attributes-delta`). Executors that need to thread state across a park-and-resume boundary use scratch (see `concept:parked-state`).
- **CEL is the filter language; exact-type subscriptions parse-check field references against the resolved payload schema; prefix-type subscriptions bind `payload` as `dyn`.** This keeps tight checking for the common exact-type case while letting prefix subscriptions span heterogeneous payload shapes.
- **`transient/park/*` leaves are the closed two-value set determined by the park-reason enum.** The taxonomy is downstream of the wire park-reason enum. The await-async-callback outcome is a transient (`transient/await_async`), not a park — the node stays in `running` state during the callback wait.
- **The wait-set `topic_kind` discriminator is a faithful projection of the signal top-level kind:** each of the three canonical kinds (terminal, transient, attribute) maps to its own `topic_kind` value, and `state` is admitted as a defensive fallback for rows whose pattern does not map to a canonical kind (see `concept:wait-set`, `decision:wait-set-topic-kind-taxonomy`).
