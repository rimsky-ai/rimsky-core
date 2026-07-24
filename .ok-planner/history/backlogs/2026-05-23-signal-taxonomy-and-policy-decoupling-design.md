# Signal Taxonomy and Policy Decoupling

**Date:** 2026-05-23
**Topic:** Replace `SubscriptionEntry`'s N×M structured-field filtering with a hierarchical type-path signal taxonomy; decouple the conflated `PolicyAction` into a `(signal, dispatch_disposition, color)` resolution tuple; retire `concept:lifecycle-handler`, `concept:last-outcome`, and `concept:transition-reason`; fold runtime acquisition failure into the unified error-policy surface; restructure bundled-executor error vocabularies along the new taxonomy. Builds on the RunScope-first reshape (`spec:2026-05-22-fan-out-safety-scope-first-design`, plan committed and archived to `history/`).

---

## Goal

Make "what just happened to a node-run" one vocabulary across cascade-fire, audit, and subscription. Today the platform carries three overlapping vocabularies for this question — `last_outcome`, `transition_reason`, and `SubscriptionEntry`'s structured-filter fields — plus a separate `PolicyAction` axis that conflates three orthogonal decisions (what signal subscribers see, whether the dispatch continues, what color the run settles in). This spec unifies the vocabularies into a single hierarchical signal type-path with CEL-predicable payloads, splits the policy axis into the three independent dimensions it actually carries, and retires the lifecycle-handler indirection that the unified model makes vestigial.

The product is a single subscription surface that operators can reason about uniformly (`type:` path + `when:` CEL predicate over payload), a single audit vocabulary (signal type-path appears in `rimsky_events.kind`), and a single error-policy chain that handles every error-shaped outcome regardless of whether the source was an executor or the runtime's acquisition path. Pre-v1, no consumer beyond the project itself; no backwards-compat shim.

## Background

The project's downstream consumer (the zonebase migration) surfaced a cluster of related friction during its cycle-5 verification:

- Subscription filtering relies on N×M structured fields (`When`, `Outcome`, `ErrorClass`, `Reason`, plus message-envelope fields and event/attribute `Name`) — fixed vocabulary, no payload predicates, no way to filter on attribute values or response durations.
- Several state transitions are not subscribable at all: retries, on-error policy resolutions, heartbeat misses. Internal evaluator state is tracked but never surfaced as a cascade signal.
- The `PolicyAction` vocabulary (`retry`, `discard_then_retry`, `resume_then_retry`, `give_up`, `pass`) conflates the signal, the scheduler effect, and the landing state. `pass` per its docstring routes to `fresh+passed` "with no cascade-fire" — but cascade-fire is supposed to be driven by `SubscriptionEntry` on receivers, not by emitters (per the 2026-05-14 subscription-cascade resolution), so the docstring contradicts the architecture.
- `resume_then_retry` is a confirmed alias for `discard_then_retry` (per the 2026-04-30 stores cleanup) but the action vocabulary still exposes both.
- The retired `invalidate` action still has a special-case rejection branch at `code:graph/node/template_validator.go:357`.
- Bundled-executor error vocabularies (`http_request_failed`, `http_unexpected_status`, `http_response_parse_failed` in http-node; opaque flat classes in claude-agent and postgres-stores) emit as flat strings with no structural relationship — subscribers wanting "any HTTP timeout" must enumerate every leaf class.

The sketch at `.ok-planner/sketches/2026-05-22-signal-taxonomy-and-scheduler-decoupling.md` proposed the unification this spec carries out. The orthogonal R5 item (relative `source_file` paths in the CLI) is explicitly excluded; it ships as its own micro-spec.

A parallel earlier sketch `.ok-planner/sketches/2026-05-19-cascade-filter-evaluation.md` proposed making the existing `SubscriptionEntry` structured filter fields (`When` / `Outcome` / `ErrorClass` / `Reason` / `Name` / `Kind` / `Sender` / `SenderKind` / `Target`) load-bearing at cascade-walk time — same goal (filters gate cascade), narrower mechanism (keep the structured fields, just evaluate them). This spec entirely supersedes that sketch: the structured filter fields retire in favor of the type-path + CEL `when:` model, which subsumes everything the earlier sketch proposed and adds payload-predicate expressivity. No design from the earlier sketch carries into this spec verbatim; the sketch stays as historical context in `sketches/`.

Two existing tensions become moot under this spec rather than being directly resolved:
- `tension:_resolved/transition-reason-vs-last-outcome` — resolved-by-documentation in `spec:2026-05-12-nomenclature-resolution`; this spec collapses both vocabularies into the signal type-path, so neither vocabulary survives.
- `tension:_resolved/error-action-count-drift` — resolved-by-collapse to 4 actions in `spec:2026-05-12-nomenclature-resolution`; this spec further tightens to 4 ErrorPolicy values with the redundant flavors retired.

One open tension is partially addressed by this spec but does not fully resolve:
- `tension:events-kind-no-enum` — stays open. The tension records that `rimsky_events.kind` is a free-form `TEXT` field with no enum constraint. This spec standardizes the *node-run-transition* subset of `kind` values (the signal type-paths covered by `concept:signal`'s taxonomy: `terminal/*`, `transient/*`, `attribute/*`, `event/*`, `message/*`). It does NOT cover the substantial set of non-signal audit kinds the runtime already emits: `state_transition`, `lock_acquired`, `lock_released`, `work_started`, `work_completed`, `attributes_substituted`, `attributes_schema_failed`, `unresolved_executor`, `orphaned_claim_lost_race`, `subgraph.dispatched`, `fanout.children_created`, `subclaim.begin_candidate`, `park_requested`, `park_timeout`, `parked_resume_started`, `auth.access_attempted`, `auth.access_denied`, `auth.key_created`, `auth.key_revoked`, `auth.key_rotated`, etc. Those are admin/internal-mechanics events outside the cascade/subscription surface; a separate spec would need to define their taxonomy (or to formally retire some of them in favor of signal emissions). The tension's resolution-candidate "add an enum CHECK" remains unaddressed for that subset; this spec deliberately leaves it open. Append a Notes entry to the tension file documenting the partial coverage.

### New dependency

This spec adds `github.com/google/cel-go` as a Go-module dependency for the CEL filter language. Rimsky's `.claude/rules/rules.md` resists heavier alternatives (Viper, Cobra, Gin, Echo) where lighter stdlib equivalents exist. CEL is justified because no stdlib equivalent exists: sandboxed, non-Turing-complete, typed-environment predicate evaluation over structured data is precisely CEL's niche, it is the Kubernetes-ecosystem default for the same use case, and the alternatives considered in the sketch (JMESPath — no arithmetic; JSONLogic — too verbose; custom DSL — more code, less standard) are worse fits. The dependency is well-maintained, has a small surface (one package, one factory call at registration, one Eval call at walk time), and ships no transport-stack baggage of its own.

## Architecture

### One signal, three orthogonal dimensions

Every transition that affects a node-run emits a **signal**:

```
Signal {
  type:    string  // hierarchical, slash-separated path
  payload: object  // structured per type
}
```

The signal travels two paths:

1. **Cascade walker.** The walker consults the subscription-edge map for the signal's type and evaluates each matching subscriber's CEL `when:` predicate against the payload. Match → wait-set row inserted for that receiver. No match → no work. Cascade-fire IS subscriber match; there is no separate cascade-fire gate.
2. **Audit log.** Every signal writes one row to `rimsky_events` with `kind = <signal type-path>` and `payload = <signal payload as JSONB>`. Independent of subscribers; audit always emits.

A terminal-bearing transition (one that finishes the dispatch) additionally settles the run row in a final color. The full resolution decomposes into three independent dimensions:

```
Resolution {
  signal:                Signal              // emitted to cascade walker + audit log
  dispatch_disposition:  Disposition         // Retry | ParkAsync | ParkScheduled | End
  color:                 SettledColor        // fresh | failed | parked (only when disposition = End or Park*)
  retry_discard_claims:  bool                // only when disposition = Retry
  retry_delay_ms:        int                 // only when disposition = Retry
  wake_at:               timestamp           // only when disposition = ParkScheduled
}
```

- **Signal** is the observable. Subscribers react to it; audit records it. Subscribers earn cascade by declaring filters; no per-resolution decision about "fire cascade or not."
- **Dispatch disposition** is the question "what becomes of this dispatch?" Three answers extend the dispatch (`Retry` re-enqueues; `ParkAsync` parks awaiting external callback; `ParkScheduled` parks with a wake timer). `End` is the absence — dispatch finishes, run row settles in `color`, supervisor moves on. (Term chosen for symmetry with `prior_dispatch_disposition` introduced by the RunScope-first reshape on `proto:executor.proto::ExecuteRequest`; that field describes the past dispatch's fate, this dimension describes the current one's.)
- **Color** is the run row's final state when the dispatch ends or parks: one of `fresh | failed | parked`. Informational only; cascade does not gate on color. (Today's `last_outcome` granularity — `changed` / `unchanged` / `passed` / `pure_cascade` — moves into signal payload fields, not into color.)

These three dimensions are jointly determined by the resolution producer:
- For executor `Success`: runtime produces `(signal=terminal/success, disposition=End, color=fresh)` with `payload.changed` from the executor's bit.
- For executor `Error` + operator's `error_types:` chain: the operator's preset choice determines the tuple (see "ErrorPolicy presets" below).
- For executor `Park{reason: SNOOZE}`: runtime produces `(signal=terminal/park/snooze, disposition=ParkScheduled, color=parked)`.
- For executor `Park{reason: AWAIT_CALLBACK}`: runtime produces `(signal=terminal/park/await_callback, disposition=ParkAsync, color=parked)`.
- For executor `AwaitAsyncCallback`: runtime emits a **transient** signal `transient/await_async` only — no settling resolution. The dispatch stays in flight (node remains in `running` state, claim retained, dispatch row open) until the HTTP callback arrives carrying the actual settling terminal (`Success | Error | Park`), which then runs through this same resolution machinery via `runtime/callback.go` re-entering `applyTerminal`. Per `concept:terminal-resolution` invariant ("Every kind except `Park` and `AwaitAsyncCallback` flows through `applyTerminal` and ends in `releaseLocksInTx`"), AwaitAsyncCallback is not a settling terminal; the eventual callback's payload is.
- For runtime acquisition failure + operator's `error_types:` chain (keyed by synthetic class `acquire/unavailable`): same as executor Error, just with a different sourcing path.
- For runtime `Infra` failures (heartbeat loss, supervisor crash detection): runtime produces `(signal=terminal/infra/{reason}, disposition=End, color=failed)` with platform-decided semantics; no operator policy chain involved.

### Cascade-fire becomes subscriber match

Today: `cascade fires iff last_outcome == fresh_changed` (a column read on the sender). Per `concept:cascade` invariants.

Under this spec: cascade fires iff some subscription edge matches the emitted signal AND the subscriber's CEL `when:` predicate evaluates true. The `last_outcome` column retires; there is no longer a sender-side cascade-fire gate. Senders emit signals; receivers decide.

This makes the new capability ("subscribe to retry==cap", "subscribe to terminal/error/* with duration > 60s") work as a first-class part of the cascade engine — subscriber filters are now actual gates on wait-set row insertion, not passive observation hints. The current pessimistic-invalidate rule (insert regardless of filter compatibility, per the comment block at `code:graph/node/subscription_edges.go:11-13`) retires in favor of walk-time filter evaluation.

Cost model: type-path matching is `strings.HasPrefix` — free. CEL predicate evaluation is bounded (CEL is non-Turing-complete by language design: no loops, no I/O, no side effects, bounded execution). The runtime may fast-path "filter is exactly `type == 'foo/bar'`" or "filter is exactly `type startsWith 'prefix/'`" via prefix-match instead of invoking CEL; that's an implementation detail. The design treats all subscriber filters as CEL predicates with negligible per-evaluation cost.

## Signal type-path taxonomy

Five top-level kinds. Type-paths are canonical (validator-enforced).

### `terminal/*` — dispatch finished, run row settled

```
terminal/success
terminal/error/<error_class>          # error_class may itself contain `/` (Q9a)
terminal/park/snooze
terminal/park/await_callback
terminal/infra/<reason>
```

Emitted exactly once per run, at the moment the run settles. Sub-leaves under `terminal/error/` follow the executor's (or runtime's, for `acquire/*`) classification — hierarchical, with operator policy keyed by the same path.

**`terminal/park/*` leaves are exactly the `ParkReason` enum, no extensibility.** The two `terminal/park/snooze` and `terminal/park/await_callback` leaves correspond exactly to the closed two-value `ParkReason` enum (`PARK_REASON_SNOOZE`, `PARK_REASON_AWAIT_CALLBACK`) per `proto:executor.proto`'s `@blessed-invariant` on `ParkReason` ("closed two-value set... No UNSPECIFIED, no OTHER, no fallback. Storage CHECK on `col:rimsky_node_runs.parked_reason` mirrors the closed set"). Adding a new `terminal/park/<X>` leaf requires extending the `ParkReason` proto enum + storage CHECK + spec change first, then propagating to the signal taxonomy — not a one-side change. Note that the `AwaitAsyncCallback` wire outcome is NOT a `terminal/park/*` leaf — it does not settle the node into `parked` state (the node stays `running` during the callback wait); it emits `transient/await_async` instead.

### `transient/*` — mid-dispatch transitions, dispatch not yet settled

```
transient/retry/<attempt>/<error_class>
transient/heartbeat_missed
transient/await_async
```

Emitted during the lifetime of a dispatch, when something happens that observers may want to react to but that doesn't finish the dispatch. The three introduced here are:

- `transient/retry/<attempt>/<error_class>` — emitted when ErrorPolicy resolves to `retry` or `discard_claims_then_retry`. `<attempt>` is the 1-indexed retry attempt (e.g., the third retry emits `transient/retry/3/...`). Subscribers wanting "the final retry" filter `when: payload.attempt == payload.cap`.
- `transient/heartbeat_missed` — emitted when the supervisor's heartbeat watchdog detects a dispatch that's missed its threshold. Observability-grade; the dispatch may still recover.
- `transient/await_async` — emitted when the executor returns `AwaitAsyncCallback`. Node stays in `running` state; the actual settling happens when the HTTP callback arrives and runs through the resolution machinery from the top.

The sketch flagged `transient/on_error_resolved` and `transient/policy_resolved` as candidates. Both retire: `on_error_resolved` becomes vestigial under the lifecycle-handler retirement; `policy_resolved` duplicates the information already in the terminal signal that follows. Lean taxonomy; subscribers wanting "the policy chose to escalate" subscribe to the resulting terminal directly.

### `attribute/<key>/changed` — attribute writes

```
attribute/<key>/changed
```

Emitted when an upstream node writes an attribute. The `<key>` is the attribute name (e.g., `attribute/budget_cents/changed`). Subscribers wanting "any attribute change on this sender" use `type: attribute/*/changed`; subscribers wanting a specific key use `type: attribute/budget_cents/changed`. Payload carries the new value and (optionally) the old value.

### `event/<name>` — named-event emissions

```
event/<name>
```

Emitted when an executor produces a non-terminal named event (`concept:named-event`). The `<name>` is the event's name. Subscribers can write `when: payload.<field>` predicates against the executor's event payload. Shape preserves the existing named-event surface; the new wrapping is just the canonical signal envelope.

### `message/<kind>/<sender_kind>/<target>` — boundary-crossing messages

```
message/<kind>/<sender_kind>/<target>
```

Emitted when a `concept:message` arrives at an instance. Path encodes the three structured filter fields that today live as separate columns on `SubscriptionEntry`: `kind` (e.g., `invalidate`), `sender_kind` (`operator | publisher | instance`), and `target` (specific node alias, or empty for instance-targeted). Operator-side or publisher-side metadata stays in payload.

### What retires from the top level

The sketch's `lifecycle/*` is **not** introduced. The use cases (node created/canceled, instance terminated) are already covered by `concept:lifecycle-subscriber` — a separate service protocol fired synchronously from control-api with DB-tracked idempotency. Adding `lifecycle/*` to the node-subscription surface would create a parallel path with ambiguous semantics. YAGNI; no in-repo subscriber asking for it.

## Payload schemas

Each signal type's payload is a typed object. The CEL environment binds these field types at template registration so subscribers' `when:` predicates parse-check.

### `terminal/success`

```
{
  changed:           bool        // executor's changed bit
  attributes_delta:  object      // executor's attribute write (may be empty)
  change_summary:    string?     // executor's optional human description
}
```

### `terminal/error/<error_class>`

```
{
  error_class:       string      // the leaf path (may contain `/`)
  error_payload:     object?     // executor's optional error payload (renamed from proto's `payload` to avoid envelope collision — see "Field-naming convention" below)
  attempt:           int         // which dispatch attempt produced this (0-indexed; 0 if first)
  retries_so_far:    int         // counter at the moment of resolution
}
```

### `terminal/park/snooze`

```
{
  resume_at:             timestamp  // absolute time the supervisor will wake the run (from Park.resume_at)
  session_token:         string?    // inert; passed back as ResumeContext.session_token
  park_payload:          object?    // inert bytes per Park.payload (renamed from proto's `payload` to avoid envelope collision)
  parked_reason_label:   string?    // executor's freeform label (Park.reason_label)
  parked_reason_note:    string?    // executor's freeform note (Park.reason_note)
}
```

### `terminal/park/await_callback`

```
{
  resume_at:             timestamp?  // optional — supervisor wakes if elapsed (Park.resume_at)
  session_token:         string?
  park_payload:          object?
  parked_reason_label:   string?
  parked_reason_note:    string?
}
```

### `terminal/infra/<reason>`

```
{
  reason:                string
  last_heartbeat_at:     timestamp?  // when reason == "heartbeat_lost"
  details:               object?     // platform-decided fields per reason
}
```

### `transient/retry/<attempt>/<error_class>`

```
{
  attempt:              int        // which retry this is (1-indexed)
  cap:                  int        // max retries before escalation
  error_class:          string     // the class being retried
  discarded_claims:     bool       // true if discard_claims_then_retry
  delay_ms:             int        // backoff delay applied
  error_payload:        object?    // the error that prompted this retry
}
```

### `transient/heartbeat_missed`

```
{
  last_heartbeat_at:    timestamp
  dispatch_id:          uuid
  threshold_ms:         int
}
```

### `transient/await_async`

```
{
  async_ack_id:         string      // correlation id the executor echoes on callback
  callback_url:         string      // the URL the supervisor advertised in ExecuteRequest.callback_url
}
```

### `attribute/<key>/changed`

```
{
  key:                  string
  value:                any        // dynamic-typed; CEL handles per-field
  old_value:            any?       // optional, supplied when known
}
```

### `event/<name>`

```
{
  name:                 string
  event_payload:        object     // executor-provided (renamed from proto's `payload` to avoid envelope collision)
}
```

### `message/<kind>/<sender_kind>/<target>`

```
{
  kind:                 string
  sender_kind:          string     // "operator" | "publisher" | "instance"
  sender:               string     // sender identity
  target:               string     // target node alias, or "" for instance-targeted
  message_payload:      object     // operator- or publisher-provided (renamed from `payload` per the field-naming convention above)
}
```

### Field-naming convention

The signal envelope's outer field is `payload`. To avoid the bare-`payload` collision when a signal's payload itself wants to wrap an opaque sub-object that the proto originally named `payload` (executor `Error.payload`, executor `NamedEvent.payload`, `Park.payload`, message envelope `payload`), the convention is: rename the inner field with a domain prefix when it lives inside `payload`. So:

- proto `Error.payload` → payload field `error_payload`
- proto `NamedEvent.payload` → payload field `event_payload`
- proto `Park.payload` → payload field `park_payload`
- message envelope `payload` → payload field `message_payload`

This is rimsky-side only — the wire proto field names don't change. The rename happens at signal-envelope construction in the runtime. Subscribers writing CEL predicates against the signal payload see the renamed fields (`when: payload.error_payload.foo > 5`, `when: payload.event_payload.duration_ms > 60000`).

## Subscription model

### YAML surface

A node's `subscribes:` block becomes a list of entries with this shape:

```yaml
subscribes:
  - type: terminal/error/payment_processor_unreachable     # exact match
    when: payload.attempt >= 3                              # optional CEL predicate
    frame: in                                               # optional (existing semantic)

  - type: terminal/error/*                                  # trailing-* prefix match
    node: payment_node                                      # optional — restrict to specific sender (existing semantic)

  - type: transient/retry/*
    when: payload.attempt == payload.cap                    # "the final retry"

  - type: attribute/budget_cents/changed
    node: budget_node

  - type: message/invalidate/operator/self                  # the common case
```

### Fields

- **`type:`** (required) — canonical type-path or trailing-`*` prefix pattern. Validated at registration against the canonical taxonomy enumerated above (range-check rejects paths outside the taxonomy).
- **`when:`** (optional) — CEL expression evaluated against the signal envelope (`type`, `payload`). When omitted, all signals matching `type:` fire.
- **`node:`** (optional, mutually exclusive with `instance:`) — restrict to signals from a specific upstream node (sender-side filter). Existing semantic preserved.
- **`instance:`** (optional, mutually exclusive with `node:`) — cross-cutting subscription; fires on signals from any node in the instance. Existing semantic preserved.
- **`frame:`** (optional) — `in | next` semantics carry over from current `SubscriptionEntry`. Defaults: `in` for per-node, `next` for cross-cutting.

### Wildcard rule

Trailing-`*` only. `terminal/error/*` matches `terminal/error/foo`, `terminal/error/foo/bar`, etc. No positional wildcards (no `terminal/*/foo`); no full glob. Operators wanting more complex patterns express them via CEL (`when: type.startsWith("terminal/") && type.contains("foo")`).

### CEL environment

- **Bindings:** `type` (string) and `payload` (object) per signal type's payload schema.
- **Functions:** CEL's standard library (string, list, map, math, time). No domain-specific helpers in this spec; future helpers can land as additive extensions if a pattern emerges.
- **Schema binding for exact type-paths:** when the subscription's `type:` field is an exact path (no trailing `*`), the `payload` binding is the typed schema for that exact signal type. CEL parse-checks field references against the schema; references to fields not in the schema reject at registration.
- **Schema binding for prefix type-paths:** when `type:` ends in `*`, `payload` is bound as **CEL `dyn`** (dynamically-typed), unconditionally — independent of which top-level kind the prefix sits under. This applies whether the prefix is `terminal/*` (matching five schemas: success, error, park/snooze, park/await_callback, infra — different fields each), `attribute/*/changed` (one schema shape but the `value` field is already `any`), `event/*` (matches many events with different payload shapes), `message/*` (different kinds carry different message-payload shapes), or `transient/*` (retry, heartbeat_missed, await_async — three schemas with different fields). Predicates run dynamically: a field reference that resolves on the actual signal's payload evaluates; a reference that doesn't resolve evaluates to `null` (CEL's safe-navigation default). No registration-time field-existence warnings for prefix subscriptions — the prefix author opted into the imprecision. This keeps wildcard subscriptions expressive while preserving tight schema-checking for the common exact-type case.
- **Validation:** parsed at template registration. Syntactically invalid expressions reject with a precise error.

### Auto-subscribe carry-forward

The existing rule from `concept:node-subscription` — substitution refs in attribute schemas (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Y}}`) imply implicit subscriptions on the reading node — carries forward unchanged. The implicit subscriptions become:

- `{{nodes.X.attribute.Y}}` → implicit `type: attribute/Y/changed, node: X` subscription
- `{{nodes.X.event.Y}}` → implicit `type: event/Y, node: X` subscription
- `{{nodes.X.attribute}}` (whole-attribute pull) → implicit `type: attribute/*/changed, node: X` subscription

`code:graph/node/subscription_edges.go::ExtractSubstitutionRefsFromTemplate` continues to drive this. The output shape changes (substitution refs produce `(node, type, optional when)` triples rather than `(SenderNodeType, TopicKind, Name)` triples) but the auto-subscribe semantic is identical.

## ErrorPolicy

Four operator-decided values that map to canonical resolution tuples. The operator writes one of these names in `error_types: { <class>: { policy: [{ action: <X>, ... }] } }`.

| Name                          | signal                                       | dispatch_disposition | color   | sub-options                       |
|-------------------------------|----------------------------------------------|----------------------|---------|-----------------------------------|
| `retry`                       | `transient/retry/<n>/<class>`                | `Retry`              | (n/a)   | `discard_claims=false`, `delay`   |
| `discard_claims_then_retry`   | `transient/retry/<n>/<class>` (payload `discarded_claims=true`) | `Retry`  | (n/a)   | `discard_claims=true`, `delay`    |
| `give_up`                     | `terminal/error/<class>`                     | `End`                | failed  | —                                 |
| `pass`                        | `terminal/error/<class>`                     | `End`                | fresh   | —                                 |

Semantic framing:
- **`pass`** ignores the error — settle this run as if nothing went wrong (color = fresh; next cascade trigger can re-dispatch). Subscribers reading `terminal/error/<class>` still see the signal and decide independently whether to react.
- **`give_up`** accepts the error — settle this run as failed (color = failed). The run does not re-dispatch on next cascade; subscribers may react.
- **`retry`** / **`discard_claims_then_retry`** defer the decision — try again, possibly with different claim hygiene. The dispatch re-enqueues; the signal emitted is `transient/retry/<n>/<class>` (not terminal). After the cap is exceeded, the chain escalates per the next chain entry (or to `give_up` if the chain is exhausted).

### Retirements

- **`resume_then_retry`** retires entirely (no shim — pre-v1, no compat). The 2026-04-30 stores cleanup made it a behavioral alias for `discard_then_retry`; this spec deletes both the action name and the docstring.
- **`discard_then_retry`** renames to **`discard_claims_then_retry`** — the original name was opaque about what gets discarded; "claims" is the load-bearing word.
- **`invalidate`** action stays retired (per the 2026-05-14 subscription-cascade resolution). The validator special-case rejection at `code:graph/node/template_validator.go:357` deletes — the validator's range-check against the canonical ErrorPolicy vocabulary (added by this spec) catches it generically.
- **`pass_record`** (proposed in the sketch) is not introduced. The use case ("silently treat error as success") is misleading under the new model (subscribers can't tell that an error happened) and is better served by either retry, by `pass`, or by an executor-level translation.

### Starting state to converge from

`concept:error-policy` today documents a 3-action vocabulary (`retry | give_up | pass`, per the 2026-05-14 Notes entry on `concepts/error-policy.md`). Code under `code:foundation/spec/policy.go` and `code:graph/node/policy.go` still carries 6 action strings (`retry | discard_then_retry | resume_then_retry | invalidate | give_up | pass`). The validator at `code:graph/node/template_validator.go` rejects `invalidate` as a migration message, but the runtime's `step` switch in `policy.go` still has live branches for `discard_then_retry` and `resume_then_retry`. This spec converges code-and-docs to a single 4-value vocabulary (`pass | give_up | retry | discard_claims_then_retry`); the convergence is the point, not the starting position.

### Configuration shape

```yaml
nodes:
  - type: my_node
    error_types:
      payment_processor_unreachable:           # an executor-emitted class
        policy:
          - action: retry
            count: 3
            backoff: exponential
            base_delay_ms: 500
          - action: give_up
      http/timeout:                            # a hierarchical class
        policy:
          - action: retry
            count: 5
          - action: pass
      acquire/unavailable:                     # runtime acquisition failure
        policy:
          - action: retry
            count: 3
          - action: give_up
```

### Acquisition failure as a synthetic-but-conventional error class

Pre-dispatch acquisition failure (today's `code:runtime/runner_lifecycle.go::handleAcquireUnavailable`, called from `code:runtime/runner_acquire.go:280-281`) routes through the same `error_types:` chain via a reserved class prefix `acquire/`. The runtime emits the failure as an error to the operator's chain just as it would an executor Error:

- `acquire/unavailable` — claim couldn't be granted (today's `errAcquireUnavailable` sentinel)
- Other `acquire/*` leaves as the acquisition path's failure modes grow

The runtime code surface stays distinct (acquisition tx vs executor stream are genuinely different code paths) but the operator-facing surface collapses into one. The signal emitted on a `give_up` resolution is `terminal/error/acquire/unavailable`, namespaced under `terminal/error/*` so subscribers wildcard-matching `terminal/error/*` catch it alongside executor errors. This is intentional: from a subscriber's perspective, "the node failed" doesn't care whether the failure was the executor's or the runtime's.

**Intentional behavior change: no implicit-retry default.** Today's `handleAcquireUnavailable` defaults to silent-retry when no handler is configured (`code:runtime/runner_lifecycle.go:50-54`). Under the new model, acquisition failure routes through the operator's `error_types:` chain; if no `acquire/unavailable` entry exists, `code:graph/node/policy.go::Evaluate` returns `give_up("unknown_error_class")` and the node fails immediately. This is a deliberate change: the old default ("retry forever, silently") was a footgun for jobs that never make progress; the new default ("fail with a precise error class") surfaces the operator's intent or its absence. Operators that want retry behavior declare it explicitly:

```yaml
error_types:
  acquire/unavailable:
    policy:
      - action: retry
        count: 10
        backoff: exponential
        base_delay_ms: 1000
      - action: give_up
```

The validator emits an informational warning at registration when a node declares any `stores:` entries (claim-producer references) but its `error_types:` does not contain an `acquire/unavailable` policy entry, surfacing the implicit-retry → fail-fast change for operators upgrading templates.

### Cap-resolution chain

The per-node `max_retries_without_progress` cap (existing semantic per `concept:error-policy` invariants) carries forward unchanged. The counter increments on `Retry`-disposition resolutions; when exhausted, the chain forces an escalation to the next entry (or to the existing synthetic `give_up` with `error_class: "retry_loop_no_progress"` — same name and same emit site as today's `code:runtime/runner_error_policy.go::shouldForceRetryLoopGiveUp`; this spec reuses it, doesn't introduce it).

## Bundled-executor error vocabularies

Restructured along the `terminal/error/<error_class>` taxonomy. The executor emits `Error{error_class}` with a slash-containing classification; the runtime constructs the signal type-path by concatenation.

### http-node (`code:executors/http-node/`)

Replaces today's flat classes from two surfaces:
- gRPC executor (`server.go`): `http_request_failed`, `http_unexpected_status`, `http_response_parse_failed`, `invalid_attribute`
- HTTP bridge (`bridge.go`): `internal_server_error`

Vocabulary:

- `http/network_error` — DNS, connection refused, TLS handshake fail, transport error
- `http/timeout` — request timeout
- `http/request_invalid/<body_class>` — non-`expect_status` response where the body parses as JSON with a `error_class` field (configurable field name; default `error_class`). `<body_class>` is the value of that field. Falls back to `http/request_invalid/_unspecified` if the field is absent.
- `http/server_error/<status>` — 5xx without a body class
- `http/expectation_mismatch` — status outside `expect_status`, no body class, not 5xx
- `http/response_unparseable` — body wasn't valid JSON when one was expected
- `http/attribute_invalid` — node attributes didn't validate (today's `invalid_attribute`)
- `http/internal_error` — replacement for the bridge's `internal_server_error` (the http-bridge process itself errored, distinct from a remote HTTP server returning 5xx)

Subscribers can write `type: http/timeout` for one node's reaction; `type: http/*` for "any HTTP problem"; `type: http/server_error/*` for any 5xx; `when: payload.error_payload.status == 429` for rate limits.

### claude-agent (`code:executors/claude-agent/`)

Replaces today's flat classes (`schema_validation_failed`, `invalid_cwd_from_store`, `cli_spawn_failed`, `silence_timeout`, `subprocess_exit_before_complete`, `invalid_attribute`, `invalid_attributes_schema`, `executor_blocked`, `executor_internal_error`):

- `agent/rate_limited`
- `agent/context_exceeded`
- `agent/tool_use_failed/<tool>`
- `agent/refused` — model refused the request
- `agent/schema_violation` — structured output didn't match expected schema
- `agent/timeout` — silence timeout or wall-clock exceeded
- `agent/cli_spawn_failed`
- `agent/subprocess_exit/<reason>` — `before_complete | unexpected_status | etc.`
- `agent/attribute_invalid`
- `agent/blocked` — replacement for today's `executor_blocked`. The wire-emission site is `code:executors/claude-agent/src/server.ts:515` (where `outcome.kind === "blocked"` maps to `error_class: "executor_blocked"`); the `report_blocked` MCP tool at `code:executors/claude-agent/src/internal-mcp-tools.ts` sets the outcome kind, but the StreamClose construction is server-side. Update the literal in server.ts (and the matching comment at line 512) to emit `agent/blocked`.
- `agent/internal_error` — replacement for today's `executor_internal_error` (emitted at `code:executors/claude-agent/src/server.ts` and `http-bridge.ts`). Reserved for the executor's own runtime mishaps (panic recovery, transport bridge failures).

### Reserved cross-executor synthetic classes

Some platform-level error classes were synthesized historically as a bridge between proto versions or as a shared "the executor itself broke" channel. Under the hierarchical taxonomy these get explicit homes:

- `executor_blocked` (synthesized from the retired pre-2026-05-12 `Blocked` terminal): retires entirely. Each executor that wants to express "the work is blocked, operator should decide" uses its own per-executor leaf (e.g., `agent/blocked` for claude-agent). There is no shared/synthetic `*/blocked` reserved class.
- `executor_internal_error` (the executor binary itself errored): same — each executor uses its own `<exec>/internal_error` leaf. The http-node bridge's `internal_server_error` becomes `http/internal_error` (added below); claude-agent's becomes `agent/internal_error`.

### postgres-stores (`code:stores/postgres/server/executor.go`)

**Note: introduces a new vocabulary, not a restructuring of existing.** The postgres-store today emits only two flat error classes: `invalid_attribute` and `verifier_failed`. The hierarchical vocabulary below is new — the planner should treat these as additive emissions tied to specific failure paths in the store's `runOne` and verifier flow, not a rename of existing strings. Carryovers map: `invalid_attribute` → `pg/attribute_invalid`; `verifier_failed` → `pg/verifier_check_failed/<check_kind>` (the check_kind requires plumbing the failed check identity through the verifier-result envelope; that's part of the work).

- `pg/attribute_invalid` (rename of today's `invalid_attribute`)
- `pg/claim_unavailable` (new; emitted on producer-side conflict, replacing whatever flat class the store would emit today)
- `pg/connection_lost` (new; emitted on transient DB connection failure)
- `pg/swap_failed` (new; emitted on atomic-staging swap failure)
- `pg/verifier_check_failed/<check_kind>` (replacement of today's `verifier_failed`; check_kind plumbed through the verifier envelope)

### stub-executor

The stub executor (`code:executors/stub/`) follows the same convention: `stub/<configured-class>` to match the configured `errorClass` field, preserving the existing test-fixture surface in restructured form.

## Validator tightening

Registration-time validation extends to range-check the new surfaces:

- **`error_types.<class>.policy[].action`** — range-check against the canonical ErrorPolicy vocabulary (`pass | give_up | retry | discard_claims_then_retry`). Reject unknown values with a precise error naming the offending field and listing the valid set. This subsumes today's special-case `action: invalidate` rejection at `code:graph/node/template_validator.go:357`; that block deletes.
- **`error_types:` keys (class names)** — when the upstream executor is reachable via the observability handshake and declares its error-class vocabulary, range-check operator-declared keys against the executor's declared set. This requires adding `declared_error_classes` to `proto:executor_observability.proto::ObservabilityCapabilities` (currently only `declared_events` exists at field 7) and a parallel `hooks.ExecutorDeclaredErrorClasses(name string) ([]string, bool)` accessor on `code:graph/node/template_validator.go::ValidatorHooks`, mirroring the existing `ExecutorDeclaredEvents` shape used at `template_validator.go:230,571`. Silent-skip when the executor is unreachable (mirrors existing pattern).
- **Declared-error-class format and parametrized leaves.** `declared_error_classes` carries strings in the same trailing-`*` wildcard format as subscription `type:`. Executors with parametrized leaves declare them as prefix patterns: http-node declares `http/server_error/*` (not enumerating each status code), `http/request_invalid/*`, `http/tool_use_failed/*` (n/a for http-node, but the principle); claude-agent declares `agent/tool_use_failed/*`, `agent/subprocess_exit/*`; postgres-stores declares `pg/verifier_check_failed/*`. Plain leaves (e.g., `http/timeout`) declare verbatim. The validator's range-check accepts an operator's `error_types:` key if it exactly matches a declared plain leaf OR matches a declared `<prefix>/*` pattern by prefix.
- **Subscription `type:`** — range-check against the canonical top-level taxonomy (the five top-level kinds enumerated above). Reject paths outside the taxonomy. Trailing-`*` accepted; positional wildcards rejected. For `terminal/error/*` subscriptions (both exact `terminal/error/<class>` and prefix `terminal/error/.../*`) naming a specific sender via `node:`, additionally cross-check against the sender executor's `declared_error_classes` (mirror of the `error_types:` key range-check; silent-skip when unreachable). Cross-sender subscriptions (`instance: true`) skip the cross-check (no single sender to query).
- **Subscription `when:` (CEL)** — parse at registration. Syntactically invalid expressions reject. For exact-type subscriptions, field references not in the resolved payload schema reject. For prefix-type subscriptions, no field warnings (the prefix author opted into `dyn`-typed payloads; see "Schema binding for prefix type-paths" above).
- **Subscription `node:` / `instance:`** — existing range-check preserved (mutual exclusion; `node:` references an existing node-type in the template).
- **Validator warning on missing `acquire/unavailable` policy.** When a node declares any `stores:` entries (claim-producer references) but its `error_types:` does not contain an `acquire/unavailable` entry (exact or via a wildcard pattern), the validator emits an informational warning at registration surfacing the intentional behavior change documented in §ErrorPolicy ("Intentional behavior change: no implicit-retry default"). Not a rejection — operators may genuinely want fail-fast — but a single audible reminder.

## Runtime renames

- **`isTerminal` / `IsTerminal` → `isSettled` / `IsSettled`** — rename all "is-this-state-settled" predicates and fields to use "settled" instead of "terminal", which now belongs to the signal type-path top-level kind. Sites:
  - `code:runtime/state_propagation.go:255` (function definition `isTerminal`); call sites at lines 187 and 197.
  - `code:runtime/run_tree.go:85` (method `(c ChildState) IsTerminal()`).
  - `code:runtime/run_tree.go:137` (struct field `AggregateResult.IsTerminal bool`).
  - `code:runtime/state_propagation.go:152` (usage `if !result.IsTerminal`).
  - Plus any callers picked up by `rg 'IsTerminal\b'` across `runtime/` and `foundation/`.
- **`applyTerminalError` / `applyTerminalPass` / `applyTerminal` / `applyErrorPolicy`** — these names already use "terminal" in the wire-protocol sense (per `concept:terminal-resolution`'s vocabulary note about the post-E.2 split). No rename needed; the existing usage is consistent with the spec's use of `terminal/*` for the equivalent signal-path top-level.

## What this spec does not change

- **Executor wire protocol.** `proto:executor.proto::StreamClose` (`Success | Error | Park | AwaitAsyncCallback`) stays as-is at the proto-message level — the outcome `oneof` shape, the `Error.error_class` field, the `Park.reason` enum (`PARK_REASON_AWAIT_CALLBACK | PARK_REASON_SNOOZE`), and the `AwaitAsyncCallback` envelope all keep their shapes. The bundled-executor restructuring below changes the *values* those executors emit into the existing `error_class` field — not the wire shape. Operators of the bundled executors see different `error_class` strings on the wire; external executors that consumed the old strings keep working (the wire field is opaque to rimsky beyond format validation).
- **Frame scheduling.** The frame in/next semantics on subscriptions, frame-creation sites, frame timeout, etc. all stay as-is. The signal taxonomy is orthogonal to frame mechanics.
- **Wait-set ledger mechanics.** The wait-set row shape stays; the insertion *predicate* changes (filter-evaluation at walk time, per the cascade-fire semantics change), but the row format and the drain-on-settled-state rule stay.
- **Claim engine, claim-tree, claim-producer protocol, claim-scope semantics.** Untouched.
- **R5: relative `source_file` paths in the CLI.** Excluded; ships as a separate micro-spec.
- **Backwards-compat shim.** None; pre-v1 covers it. Operators upgrade by editing templates to the new shape; the validator catches old shapes with precise migration messages.

## Design changes

The following design-doc mutations execute-plan will apply alongside the code changes.

### Concept creations

- **Create `concepts/signal.md`** with:
  - Definition: the unified emission shape `{type, payload}` for any node-run transition.
  - Purpose: collapse the historically parallel vocabularies (`last_outcome`, `transition_reason`, `SubscriptionEntry`'s structured fields) into one type-path-plus-payload contract that drives cascade-fire, audit, and subscription uniformly.
  - Boundaries: owns the canonical type-path taxonomy (5 top-level kinds enumerated above), the payload schema per type, the CEL filter language for `when:` predicates, the audit-emit pathway to `rimsky_events`, the cascade-walker filter-evaluation pathway. Does NOT own subscription edge construction (that's `concept:node-subscription`), wait-set ledger mechanics (`concept:wait-set`), or policy resolution (`concept:error-policy`). Adjacent: `concept:node-subscription`, `concept:error-policy`, `concept:cascade`, `concept:wait-set`, `concept:event-log`, `concept:executor`.
  - Invariants:
    - Type-paths are canonical and validator-enforced.
    - Every transition that affects a node-run emits exactly one signal.
    - Audit-log emission is unconditional (every signal writes one `rimsky_events` row).
    - Cascade-fire is `subscription edge match && CEL predicate evaluates true` — no separate sender-side gate.
    - Wildcard syntax is trailing-`*` only.
    - CEL is the filter language; exact-type subscriptions parse-check field references against the resolved payload schema; prefix-type subscriptions bind `payload` as `dyn`.
    - `terminal/park/*` leaves are the closed two-value set determined by `ParkReason`; extending the set requires a proto-level change first. `AwaitAsyncCallback` is a transient (`transient/await_async`), not a park.

### Concept reshapes

- **Mutate `concepts/node-subscription.md`** in place:
  - Replace the "What it is" section's "Four topic kinds" framing with the new shape: subscriptions take `type:` (canonical type-path or trailing-`*` prefix) + optional `when:` (CEL predicate over the signal payload). Sender-side filters (`node:` / `instance:`) and frame modifier (`frame: in | next`) carry forward unchanged.
  - Update the Boundaries section: it currently lists "The topic taxonomy (`state` / `attribute` / `event`)" — that's stale on two axes (missing the `message` kind added 2026-05-15, and the whole taxonomy moves under `concept:signal`). Replace with: owns the per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)` — replacing the existing exact-key `SubscriptionEdgeMap` in `code:graph/node/subscription_edges.go` with a per-sender radix tree or equivalent prefix-bucket structure to support efficient prefix-match lookup); the auto-subscribe rule from substitution refs; the consumer-side mapping from signal type-path to receiver wait-set rows. Does NOT own the signal taxonomy itself or payload schemas (those live in `concept:signal`).
  - Update Invariants to remove the state-only filter rules (`When`, `Outcome`, `ErrorClass`, `Reason`) which retire entirely; add: subscription `type:` and `when:` are validated at registration against the canonical taxonomy and payload schema. **Preserve the 2026-05-19 invariant**: self-subscription stays first-class in both `frame: in` and `frame: next` shapes; restate the invariant in the new vocabulary as `{ node: <self-type>, type: terminal/success, when: payload.changed, frame: <in|next> }` (replacing the old `outcome: fresh_changed` filter with the equivalent CEL predicate on the renamed payload field).
  - Append a Notes entry: `2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. SubscriptionEntry's structured filter fields (When/Outcome/ErrorClass/Reason/Name/Kind/Sender/SenderKind/Target) retire; replaced by type-path + CEL when:. Inverse-edge map shape changes from exact-key to prefix-keyed (radix tree). Auto-subscribe rule preserved. Self-subscription invariant preserved (restated in new vocabulary).`

- **Mutate `concepts/error-policy.md`** in place:
  - Update the "What it is" section: action vocabulary is now `pass | give_up | retry | discard_claims_then_retry` (4 values). `error_types:` is the unified surface for both executor Error and runtime acquisition failure (synthetic-class `acquire/*`).
  - Replace the "Three-name relationship" section to drop the historical `discard_then_retry` / `resume_then_retry` references.
  - Update Boundaries: owns the 4-value ErrorPolicy vocabulary, the per-class policy chain entry point (executor-Error + acquisition-failure), the retry-counter cap. Does NOT own: signal type-path taxonomy (`concept:signal`), cascade-fire (`concept:cascade`), terminal-resolution stitching (`concept:terminal-resolution`).
  - Replace Invariants section's `discard_then_retry` / `resume_then_retry` reference with the renamed `discard_claims_then_retry`. Add: `acquire/<reason>` is a reserved class-name prefix for runtime acquisition failures; operators may declare `error_types:` keys under this prefix.
  - Update Aliases section to retire the `discard_then_retry` / `resume_then_retry` historical names.
  - Append a Notes entry: `2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Vocabulary tightened to 4 values (resume_then_retry deleted; discard_then_retry renamed to discard_claims_then_retry). Acquisition failure folded into the error_types: surface via synthetic-class prefix acquire/*. Policy resolution decoupled into (signal, dispatch_disposition, color) tuple; preset names map at registration. on_executor_errored.error{error_class} remap retires (concept:lifecycle-handler retires entirely).`

- **Mutate `concepts/cascade.md`** in place:
  - Update Invariants: replace `Cascade fires iff last_outcome == fresh_changed (not the raw Success.changed)` with: `Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL when: predicate evaluates true.`
  - **Retract** (delete and replace with a retraction note) the invariant `Cascade does not propagate from parked or failed`. The retraction note reads: "Retracted 2026-05-23 — under the subscriber-driven cascade-fire model introduced by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design, propagation is determined by subscriber matches against the emitted signal, not by sender color. Settled-color is informational. The functional equivalent (downstream nodes not auto-firing on a failed sender) is now expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all. The matching retraction lives on `concepts/parked-state.md`."
  - Update the "Common pitfalls" section: remove the bullet that talks about `lifecycle-handler resolves never_propagate` (lifecycle-handler retires) and the bullet that treats `last_outcome` as a dispatch gate (`last_outcome` retires). Replace with a single new pitfall: "Treating `terminal/error/*` subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, a subscriber filtering on `terminal/error/*` fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly."
  - Append a Notes entry: `2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Cascade-fire predicate becomes subscriber match (concept:signal); last_outcome column retires. Filter evaluation moves to walk-time (CEL predicates against signal payload); the pessimistic-invalidate rule (insert wait-set rows regardless of filter) retires. The "cascade does not propagate from parked or failed" invariant retracts — propagation is subscriber-driven, not sender-color-driven (the matching retraction lives on concepts/parked-state.md). Common pitfalls refreshed to remove lifecycle-handler and last_outcome references.`

- **Mutate `concepts/terminal-resolution.md`** in place:
  - The current doc describes a five-stage flow: (1) wire→internal terminal kind, (2) dispatch on terminal kind, (3) lifecycle handler, (4) error policy chain, (5) claim-handle resolution. Stage 3 disappears with `concept:lifecycle-handler`'s retirement. Reshape the flow to four stages: (1) wire→internal terminal kind, (2) dispatch on terminal kind, (3) resolution (produces the `Resolution{signal, dispatch_disposition, color, ...}` tuple — runs the operator's `error_types:` chain when the terminal kind is `Errored` or when the synthetic `acquire/*` class is in play; for `Success`/`Park`/`AwaitAsyncCallback`/`Infra` the resolution is fixed by the terminal kind), (4) claim-handle resolution. The runtime emits the signal to both the cascade walker and the audit log; the dispatch_disposition determines whether the dispatch re-enqueues, parks, or ends; the color settles the run row.
  - Add: the same four-stage spine handles executor Error and runtime acquisition failure uniformly (acquisition-failure routes through the error_types chain via synthetic-class `acquire/*`).
  - Update the kind→verb table: lifecycle-handler resolves drop (the lifecycle-handler concept retires). The table reduces to columns: terminal kind → emitted signal → producer verb on each acquired claim. Add a row for `AwaitAsyncCallback` showing it emits `transient/await_async` and is not a settling terminal (no producer verb on first pass; the callback's eventual terminal drives verb emission).
  - Append a Notes entry: `2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Resolution shape becomes (signal, dispatch_disposition, color). Acquisition failure folds into the same spine via synthetic acquire/* error class. concept:lifecycle-handler retires; on_executor_complete and on_executor_errored slots delete. Five-stage flow collapses to four (lifecycle-handler stage absorbed into resolution).`

- **Mutate `concepts/parked-state.md`** in place:
  - Update the resume-context section: park terminals emit signals `terminal/park/snooze` and `terminal/park/await_callback` (the two `ParkReason` enum values). The freeform `parked_reason_label` is a payload field on both signals (no longer a separate column-form distinction). Explicitly note that `AwaitAsyncCallback` is NOT a park (the node stays `running` during the callback wait); it emits `transient/await_async` and is covered under `concept:signal`'s transient subtree.
  - **Retract** the invariant `Cascade does not propagate from parked` (currently at `concepts/parked-state.md:50`). Replace with a retraction note: "Retracted 2026-05-23 — under the subscriber-driven cascade-fire model introduced by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design, propagation is determined by subscriber matches against the emitted signal, not by sender color. Parked nodes emit `terminal/park/*` signals; subscribers decide whether to react. The matching retraction lives on `concepts/cascade.md`."
  - Append a Notes entry: `2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Park terminals emit signals (terminal/park/snooze, terminal/park/await_callback — one per ParkReason value). parked_reason_label moves to signal payload. AwaitAsyncCallback is not a park (node stays running) — it emits transient/await_async; see concept:signal. The "Cascade does not propagate from parked" invariant retracts (matching retraction on concepts/cascade.md).`

- **Touch `concepts/wait-set.md`** (light): note in a new Notes entry that wait-set insertion is now gated by walk-time CEL filter evaluation; the pessimistic-invalidate rule retires. Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design.

- **Touch `concepts/executor.md`** (light): correct the inherited drift in the existing doc that calls the third outcome variant "Snooze" — it is actually `Park` with inner `ParkReason ∈ {AWAIT_CALLBACK, SNOOZE}` per `proto:executor.proto`. After the correction, note that executor terminal vocabulary is the 4-variant `StreamClose.outcome` (`Success | Error | Park | AwaitAsyncCallback`); operator-decided retry is via the operator's `error_types:` chain on `Error`, not an executor wire surface. Executors handle internal retry silently or via `Park{reason: SNOOZE}`. Append Notes entry referencing spec.

- **Touch `concepts/terminal-resolution.md`** (light, in addition to the reshape below): correct the inherited drift in the "Vocabulary note" subsection that lists outcomes as `Success | Error | Snooze | AwaitAsyncCallback`; the correct wire shape is `Success | Error | Park | AwaitAsyncCallback`.

- **Touch `concepts/event-log.md`** (light): note that the node-run-transition subset of `rimsky_events.kind` now carries canonical signal type-paths (e.g., `terminal/error/http/timeout`) rather than free-form strings; for those rows `payload` carries the signal payload per its type's schema. Other audit kinds (state_transition, lock_*, work_*, auth.*, etc.) continue to use free-form text. Append Notes entry referencing spec.

- **Touch `concepts/lineage-record.md` and `concepts/lineage.md`** (light): note that lineage rows replace the `last_outcome` projection with a `settling_signal_type` field carrying the canonical signal type-path of the settling resolution (`terminal/success`, `terminal/error/<class>`, `terminal/park/<reason>`, `terminal/infra/<reason>`). The new field is strictly more expressive than `last_outcome`; existing lineage consumers gain detail. Append Notes entry referencing spec.

- **Touch `concepts/message.md`** (light): note that under `concept:signal`'s field-naming convention, the message envelope's `payload` field is exposed to CEL subscription `when:` predicates as `payload.message_payload` (rather than `payload.payload`) to avoid colliding with the signal envelope's outer `payload` field. The substitution surface (`{{trigger.message.payload.X}}`) is **not** renamed — substitution does not have the envelope-collision problem since it goes through the explicit `trigger.message.` namespace prefix. This deliberate asymmetry keeps substitution backward-compatible and confines the rename to where it's structurally required. Append Notes entry referencing spec.

- **Mutate `concepts/invalidate.md`** in place: today's doc declares three **template-configurable** emit sites — operator API (`POST /admin/instances/.../invalidate`), error-types policy invalidate (`error_types:` block), and lifecycle-handler `invalidate:` slot. The latter two no longer exist under this spec (the `invalidate` action stays retired per 2026-05-14; `concept:lifecycle-handler` retires entirely under this spec). Collapse the template-configurable enumeration to one: the operator-API emit site. **Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged and stay documented.** Append a Notes entry: `2026-05-23 — Template-configurable emit-site enumeration collapses from three to one (operator-API). Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged. The error_types policy invalidate site and the lifecycle-handler invalidate slot stop existing under spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design (the action was retired 2026-05-14; concept:lifecycle-handler retires entirely).`

### Concept retirements

- **Retire `concepts/lifecycle-handler.md`** — move to `concepts/_retired/lifecycle-handler.md` with a retirement note: the three slots (`on_executor_complete`, `on_executor_errored`, `on_acquire_unavailable`) collapse into the unified `concept:error-policy` (acquisition failure as synthetic-class) and direct signal emission (executor `Success` / `Park` / `AwaitAsyncCallback` emit fixed signals with no operator-policy chain). The `by_changed | always_propagate | never_propagate` resolves retire (cascade-fire is now subscriber-driven; the sender cannot suppress downstream firing). The `pass` resolve at lifecycle-handler level retires (redundant with `error_types: { <class>: { policy: [{ action: pass }] } }`). Concrete deletions this retirement entails:
  - **Fields on `code:foundation/spec/template.go`'s `TemplateNodeDef` struct:** delete `OnExecutorComplete *OnExecutorCompleteHandler`, `OnExecutorErrored *OnExecutorTerminalHandler`, `OnAcquireUnavailable *OnAcquireUnavailableHandler`.
  - **Type definitions on `code:foundation/spec/template.go`:** delete the `OnExecutorCompleteHandler`, `OnExecutorTerminalHandler`, and `OnAcquireUnavailableHandler` structs (lines ~225-280 in current state). They become dead types when the fields are removed.
  - **Validators on `code:graph/node/template_validator.go`:** delete `validateOnExecutorComplete` (~line 409), `validateOnExecutorTerminal` (~line 436), and `validateOnAcquireUnavailable` (~line 368). Delete the three call sites in the per-node validator loop (currently at ~lines 181-183).
  - **Runtime:** restructure `code:runtime/runner_lifecycle.go::handleAcquireUnavailable` to emit a `terminal/error/acquire/unavailable` signal and invoke `applyErrorPolicy` with synthetic class `acquire/unavailable`, replacing the existing switch on `h.Resolve` (lines 56+). Delete the `applyTerminalPass` shortcut at `code:runtime/runner_terminal_handlers.go` (the lifecycle-handler `pass` path); `pass` becomes purely an ErrorPolicy chain action invoked through the unified resolution path.
  - **Tests:** all `*_test.go` files exercising the three handler types update or delete in parallel.
- **Retire `concepts/last-outcome.md`** — move to `concepts/_retired/last-outcome.md` with a retirement note: the column retires alongside the cascade-fire-gate semantic. Signal-payload fields (`changed` on `terminal/success`, `discarded_claims` on `transient/retry`) carry the granularity that mattered.
- **Partially retire `concepts/transition-reason.md`** — the runtime type `TransitionReason` and the `NextState` validation surface in `code:foundation/cascade/state.go` **stay** (they cover non-signal state-machine transitions like `dispatch_claimed`, `pure_cascade`, `infra_reenqueue` etc., which the signal taxonomy does not subsume). What retires is the enum's *audit-write role* — signal-bearing transitions write the signal type-path into `rimsky_events.kind`; non-signal transitions continue to write `TransitionReason.Kind` as the audit kind (part of the un-taxonomized audit-kind set left open by `tension:events-kind-no-enum`). Rewrite the doc in place to reflect the scoped responsibility: the concept's "Purpose" section narrows from "answer 'why did the state machine move?' for audit consumers" to "validate legal state-machine transitions in `NextState` and provide a kind string for non-signal audit rows." Do NOT move the doc to `_retired/`; this is a reshape, not a full retirement. Append a Notes entry: `2026-05-23 — Scope narrowed per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. The enum stays for state-machine validation in NextState; the audit-write role retires for signal-bearing transitions (which use signal type-paths in rimsky_events.kind). Non-signal transitions continue to write TransitionReason.Kind as the audit kind.`

### Tensions

- **Update `tensions/events-kind-no-enum.md`** in place: append a Notes section documenting partial coverage. Status stays `open`. Wording: "2026-05-23 — Partially addressed by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Node-run-transition `kind` values are now standardized under the signal type-path taxonomy (`concept:signal`): `terminal/*`, `transient/*`, `attribute/*`, `event/*`, `message/*`, validated at registration. Non-signal audit kinds (`state_transition`, `lock_acquired`, `work_started`, `attributes_substituted`, `auth.*`, etc.) remain free-form `TEXT`; a separate spec would need to taxonomize them. Tension does not move to `_resolved/`."
- This spec creates no new tensions. The two adjacent resolved-historical tensions (`_resolved/transition-reason-vs-last-outcome.md`, `_resolved/error-action-count-drift.md`) are referenced in Background as becoming-moot rather than as direct resolutions — both were resolved-by-documentation in prior specs; the moot-by-collapse outcome here is a stronger form of resolution but the historical resolution records stay in place.

### Proto changes

- **`proto:executor_observability.proto::ObservabilityCapabilities`**: add `repeated string declared_error_classes = 8;` (next available tag after the existing `declared_events = 7;`). Field carries the executor's declared error-class vocabulary, parallel to `declared_events`. Used by the validator's range-check of operator `error_types:` keys (silent-skip when unreachable, per the existing pattern).
- Companion code: add `ExecutorDeclaredErrorClasses func(name string) ([]string, bool)` to `code:graph/node/template_validator.go::ValidatorHooks`, mirroring the existing `ExecutorDeclaredEvents` field.

### Concept-catalog TOC

`concepts.md` auto-regenerates after the above mutations; the planner runs the TOC refresh as part of execute-plan. Expected delta:

- New: `signal` entry.
- Retired: `lifecycle-handler`, `last-outcome` move to the Retired section.
- Updated one-liners: `node-subscription`, `error-policy`, `cascade`, `terminal-resolution`, `parked-state`, `event-log`, `executor`, `wait-set`, `invalidate`, `lineage-record`, `lineage`, `message`, `transition-reason` (scope narrowed, not retired).

## Phasing guidance for the planner

This spec is large. Natural phase boundaries the planner may choose to honor:

- **Phase 1.** Introduce `concept:signal` infrastructure: the type-path taxonomy validator, CEL integration (`github.com/google/cel-go` dependency add + env setup + payload-schema registration), signal-envelope construction shared by all emission sites, and the per-sender prefix-keyed edge-map data structure. The audit log starts writing canonical type-paths (`rimsky_events.kind` shifts to canonical form). No subscribers yet exercise the new walker path.
- **Phase 2.** Reshape `concept:node-subscription` to consume signals: subscription validator accepts new `type:` + `when:` fields; cascade walker evaluates CEL predicates at walk time. The pessimistic-invalidate rule retires. **Must include in the same phase:** delete the existing sender-side `last_outcome == fresh_changed` cascade-fire gates so cascade-fire is fully subscriber-driven. The two literal `if lastOutcome == cascade.LastOutcomeFreshChanged` gate sites are `code:runtime/runner_terminal.go:362` (gate around `cascadeSubscribersStaleInTx`) and `code:runtime/runner_terminal.go:420` (gate around `fanoutRecalculate`). Both must be removed in this phase — otherwise cascade is double-gated and signals matching the new filter that didn't match the old gate would not fire. Other `cascadeSubscribersStaleInTx` call sites (`code:runtime/runner_error_policy.go:220` for retry; `code:runtime/state_propagation.go:372` for the cross-scope parent-settlement bridge; `code:runtime/subgraph_dispatch.go` for sub-graph internal cascade) already fire unconditionally; under the new model they keep that shape but the receiver-side filter evaluation that this phase adds becomes the gate. The `last_outcome` column itself can outlive this phase as long as no read site gates cascade-fire on it. **Preserve the self-subscription invariant** added 2026-05-19 to `concept:node-subscription`: self-subscription stays first-class in both `frame: in` and `frame: next` shapes; the cascade walker's insert-then-drain-in-same-tx pattern and the recently-landed `applyTerminalComplete` in-tx phase flip continue to make this work.
- **Phase 3.** Decouple `PolicyAction` into the 3-tuple resolution shape: `code:graph/node/policy.go::Evaluate` produces `Resolution` tuples; `code:runtime/runner_error_policy.go::applyErrorPolicy` consumes them. ErrorPolicy vocabulary tightens to 4 names; `resume_then_retry` deletes; `discard_then_retry` renames to `discard_claims_then_retry`. The `step` switch at `code:graph/node/policy.go:60-89` gains a `case "pass":` branch (today `pass` falls through to `default → give_up("unknown_action_type")`, which the validator rejection has masked — but the runtime branch needs to exist for the new first-class action). **Ordering constraint:** must precede Phase 4 — Phase 4's acquisition-as-`error_types: { acquire/unavailable: ... }` config has to route through the tightened policy vocabulary, not the legacy 6-action set.
- **Phase 4.** Retire `concept:lifecycle-handler`: delete `OnExecutorComplete` and `OnExecutorErrored` fields from `TemplateNodeDef`; delete the `pass` resolve of `on_executor_errored`; fold acquisition-failure into `error_types:` via synthetic-class `acquire/*`. Restructure `code:runtime/runner_lifecycle.go::handleAcquireUnavailable` to emit through the error-policy chain. Delete `validateOnAcquireUnavailable` and the `OnAcquireUnavailableHandler` shape from `code:foundation/spec/template.go` and `code:graph/node/template_validator.go`. The `ReasonPolicyInvalidate` value and its `NextState` branch (`code:foundation/cascade/state.go:75` and the matching switch arm) become unreachable once the `invalidate` action retires per the validator — delete both as part of this phase's dead-code hygiene.
- **Phase 5.** Retire `concept:last-outcome` and `concept:transition-reason`. The `isTerminal` → `isSettled` rename rides here. (Most of the cascade-fire work happens in Phase 2; this phase finishes the column-and-enum cleanup.) Enumerated work:
  - **`last_outcome` column drop.** Schema migration removes the column from `rimsky_node_runs`. Reader sites to update or remove: cascade-fire gates (already moved in Phase 2), `code:control/cli/client.go`, `code:control/cli/backfill.go`, `code:control/controlapi/backfills.go`, `code:control/controlapi/nodes.go` (operator-reset path), `code:runtime/runner_terminal.go::377-389` (lineage emit path), plus the lineage record schema projection in `code:foundation/persistence/postgres/lineage*.go` and TS-side admin readers. For the lineage projection specifically, the spec adopts the rule "lineage records carry the run's settling signal type-path instead of `last_outcome`" — the lineage row gets a `settling_signal_type` TEXT column populated from the emitted `terminal/*` signal at the resolution site. The signal type-path is strictly more expressive than `last_outcome`, so existing lineage consumers gain detail rather than losing it. Update the existing `concept:lineage-record` and `concept:lineage` docs (touch-level) to reflect.
  - **`TransitionReason` partial retirement (audit role only; state-machine role stays).** The enum at `code:foundation/cascade/state.go` carries ~18 values, several of which (`dispatch_claimed`, `invalidate_received`, `operator_invalidate`, `operator_reset`, `pure_cascade`, `child_transitioned`, `subgraph_internal_cascade_fired`, `infra_reenqueue`, `handler_resume`, `park_timeout`, `acquire_pass`, `dispatch_impossible`, `policy_invalidate`) describe non-signal triggers — they have no place in the signal type-path taxonomy. The `NextState` switch using these reasons is load-bearing for transition validation (notably the `dispatch_claimed` rejection guarding against double-execute). The enum and the `NextState` function therefore **stay** as a runtime state-machine validation surface; what retires is the enum's *audit-write role*. Today's audit rows write `TransitionReason.Kind` as the `rimsky_events.kind` value for state transitions; under this spec, signal-bearing transitions write the signal type-path instead. Non-signal state transitions (e.g., `dispatch_claimed` for `stale → running`) continue to use their `TransitionReason.Kind` string as the audit `kind` — these are part of the un-taxonomized audit-kind set that `tension:events-kind-no-enum` leaves open (per the Background section). The `concept:transition-reason` doc moves to `_retired/` only with respect to the audit-vocabulary role; the runtime type and the per-state validation switch remain. Rewrite the concept doc's retirement note to reflect this scoped retirement.
- **Phase 6.** Restructure bundled-executor error vocabularies: http-node, claude-agent, postgres-stores, stub emit hierarchical `/`-containing error classes. Add `declared_error_classes` to `proto:executor_observability.proto::ObservabilityCapabilities`; bundled executors populate it; validator's `error_types:` key range-check goes live. **Ordering constraint:** the proto regeneration (`make proto-gen`) precedes the bundled-executor work and the validator extension; that's a single sub-step at the start of the phase.

**Hard ordering constraints across phases:**
- Phase 2 must include the `last_outcome` cascade-fire-gate deletion (or move that deletion to Phase 2 explicitly), not defer it to Phase 5.
- Phase 3 must precede Phase 4 (vocabulary tightening before acquire-as-error_types).
- The proto regen at the start of Phase 6 must precede any code change that depends on the new proto field.
- Phases 1 and 6 are independent of Phases 2–5 (Phase 1 is foundational; Phase 6 is per-executor leaves) but Phase 6's validator extension depends on Phase 1's validator infrastructure.

Within the constraints, the planner has latitude. Each phase ends with build + test green (per `.claude/rules/rules.md`'s "verify the build" requirement) — interface changes touch the Go and TS sides and the proto regeneration, so the full sweep matters.
