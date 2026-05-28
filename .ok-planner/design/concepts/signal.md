---
concept: signal
status: as-is
aliases: []
---

# Signal

## What it is

A **signal** is the unified emission shape for any transition that affects a node-run. Every signal carries a canonical hierarchical type-path and a structured payload:

```
Signal {
  type:    <type-path>   // slash-separated, hierarchical, validator-enforced
  payload: <object>      // typed per type-path; see "Payload schemas" below
}
```

The signal travels two independent paths once emitted:

1. **Cascade walker.** Subscription edges keyed by type-path prefix select candidate receivers; a CEL `when:` predicate evaluated against the payload gates wait-set insertion.
2. **Audit log.** Every signal writes one row to the persisted audit-event ledger with the event kind set to the signal's type-path string and the audit payload set to the signal payload. Audit emission is unconditional and independent of subscribers.

The signal vocabulary collapses three historically parallel surfaces — `last_outcome`, `transition_reason`, and the subscription entry's structured-filter fields (when, outcome, error-class, reason, name, kind, sender, sender-kind, target) — into one type-path-plus-payload contract.

## Purpose

Make "what just happened to a node-run" one vocabulary across cascade-fire, audit, and subscription. A single subscription surface (`type:` path + `when:` CEL predicate) lets operators reason uniformly about every observable transition; a single audit vocabulary (the signal type-path as the audit-event kind) lets observability tooling describe what happened without spelunking through three overlapping enums; a single emission discipline lets new transitions (retries, heartbeat misses, attribute writes) become first-class observable events without inventing new switch-paths.

## Signal type-path taxonomy

Five top-level kinds. Type-paths are canonical and validator-enforced.

### `terminal/*` — dispatch finished, run row settled

```
terminal/success
terminal/error/<error_class>          # error_class may itself contain '/'
terminal/park/snooze
terminal/park/await_callback
terminal/infra/<reason>
```

Emitted exactly once per run, at the moment the run settles. `terminal/park/*` leaves are exactly the park-reason enum (a two-value closed set fixed on the wire executor protocol).

### `transient/*` — mid-dispatch transitions, dispatch not yet settled

```
transient/retry/<attempt>/<error_class>
transient/heartbeat_missed
transient/await_async
```

`transient/await_async` is the executor await-async-callback outcome — the node stays in `running` state until the callback's eventual terminal settles it. It is NOT a `terminal/park/*` leaf.

### `attribute/<key>/changed` — attribute writes

Emitted per changed attribute key when a node settles with a non-empty attribute delta.

### `event/<name>` — named-event emissions

Emitted when an executor produces a non-terminal named event (`concept:named-event`). The payload's `event_payload` field carries the executor-provided bytes (renamed from the wire carrier's opaque `payload` field per the field-naming convention below).

### `message/<kind>/<sender_kind>/<target>` — boundary-crossing messages

Emitted when a `concept:message` arrives at an instance. The three structured filter dimensions that today live as separate fields on the subscription entry (`kind`, `sender_kind`, `target`) collapse into the type-path leaves.

## Payload schemas

Each signal type's payload is a typed object. The CEL environment binds these field types at template registration so subscribers' `when:` predicates parse-check. The per-type payload schema is a property of the concept: each type-path resolves to one payload shape.

### Field-naming convention

The signal envelope's outer field is `payload`. To avoid a bare-`payload` collision when a signal's payload itself wraps an opaque sub-object whose wire carrier also names its own opaque field `payload` (the executor error carrier, the named-event carrier, the park carrier, and the message envelope each carry one), the inner field is renamed with a domain prefix:

| Wire carrier | Renamed-in-signal field |
| --- | --- |
| executor error payload | `error_payload` |
| named-event payload | `event_payload` |
| park payload | `park_payload` |
| message envelope payload | `message_payload` |

This is a rimsky-side rename only; wire field names do not change. CEL predicates against the signal payload see the renamed fields (`when: payload.error_payload.foo > 5`).

## CEL filter language

Subscription `when:` predicates compile at registration time and evaluate at cascade-walk time.

- **Bindings:** `type` (string) and `payload` (object).
- **Schema binding for exact type-paths:** when `type:` is an exact emit-shape path (no trailing `*`), field references in `when:` parse-check against the resolved payload schema (the per-type payload shape). References to fields not in the schema reject at registration.
- **Schema binding for prefix type-paths:** when `type:` ends in `*`, `payload` is bound as CEL `dyn` (dynamically-typed); no field-name check at registration. Field references that don't resolve on the actual signal evaluate to the spec's safe-navigation default (Eval returns false).
- **Functions:** CEL's standard library (string, list, map, math, time). No domain-specific helpers in this spec.

## Boundaries

Owns:
- The canonical type-path taxonomy (five top-level kinds + leaf rules).
- The per-type payload schema.
- The CEL filter language: env construction, predicate compilation, evaluation.
- The audit-emit pathway that writes each signal to the persisted audit-event ledger.
- The signal-envelope construction helpers shared by all emission sites.

Does NOT own:
- The cascade walk itself or subscription-edge map construction (lives in `concept:node-subscription` / `concept:cascade` — both signal-driven post-2026-05-23).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- Policy resolution — what tuple the runtime should produce on a given terminal kind (lives in `concept:error-policy` / `concept:terminal-resolution`).
- The wire executor protocol (signals are emitted on the rimsky side from the wire outcomes, not by the executor directly).

Adjacent: `concept:node-subscription`, `concept:error-policy`, `concept:cascade`, `concept:wait-set`, `concept:event-log`, `concept:executor`.

## Invariants

- **Type-paths are canonical and validator-enforced.** Emit-shape validation rejects paths outside the taxonomy; subscription-type validation additionally rejects positional wildcards.
- **Every transition that affects a node-run emits exactly one signal.** No double-emit; no missing emit.
- **Audit-log emission is unconditional.** Every signal writes one row to the persisted audit-event ledger regardless of whether any subscriber exists.
- **Cascade-fire is `subscription edge match && CEL predicate evaluates true`.** No separate sender-side gate. The historical `last_outcome == fresh_changed` cascade-fire gates retired with the 2026-05-23 signal-taxonomy reshape.
- **Wildcard syntax is trailing-`*` only.** `terminal/error/*` matches `terminal/error/foo` and `terminal/error/foo/bar`; no positional wildcards (no `terminal/*/foo`); no full glob. Operators wanting more complex patterns express them via CEL.
- **CEL is the filter language; exact-type subscriptions parse-check field references against the resolved payload schema; prefix-type subscriptions bind `payload` as `dyn`.** This keeps tight checking for the common exact-type case while letting prefix subscriptions span heterogeneous payload shapes.
- **`terminal/park/*` leaves are the closed two-value set determined by the park-reason enum.** Extending the set requires a wire-protocol change to the park-reason enum plus a storage-constraint update plus a spec change first; the signal taxonomy is downstream. The await-async-callback outcome is a transient (`transient/await_async`), not a park — the node stays in `running` state during the callback wait.

## Notes

- 2026-05-24 — `concept:breakpoint` consumes signal type-paths via the signal_type filter on after_terminal breakpoints (prefix-only, trailing-* wildcards, validated through the same type-path validator). No taxonomy change; `concept:signal` is read-only consumer.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
