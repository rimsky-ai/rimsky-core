# Signal taxonomy + scheduler-action decoupling

**Provenance.** A consumer project's migration to rimsky surfaced a cluster
of related issues with the current subscription/policy/error model that
resolve cleanly under one unification. Full per-finding investigation
(with rimsky `file:line` citations) is preserved at
`file:../../../zonebase/.ok-planner/sketches/2026-05-22-cycle5-rimsky-verification.md`
and `file:../../../zonebase/.ok-planner/sketches/2026-05-22-rimsky-upstream-changes.md`
in the zonebase repo. Rimsky reviewers can read those for the detailed
case studies; this sketch is the rimsky-relative design.

This is a sketch — not a spec. Implementation is for a future plan cycle
to scope after the design is settled.

## 1. Context

Rimsky today carries three coupled-but-distinct concerns inside the
single notion of a "policy action":

- **What signal does this state transition emit** for cascade subscribers?
- **What does the dispatcher do next** — extend this dispatch (retry,
  park) or let the run end?
- **What color is the node left in** — informational for lineage and
  dashboards?

The existing `code:foundation/spec/policy.go::PolicyAction` vocabulary
(`retry`, `discard_then_retry`, `resume_then_retry`, `give_up`, `pass`)
conflates these dimensions. Different actions imply different (signal,
scheduler-action, color) triples, with the coupling implicit and
sometimes contradictory:

- `pass` per docstring routes to `fresh+passed` "with no cascade-fire."
  But cascade-fire is supposed to be driven by `SubscriptionEntry` on
  receivers, not by emitters — so the "no cascade-fire" clause is
  inconsistent with the 2026-05-14 subscription-cascade resolution.
- `resume_then_retry` is a behavioral alias for `discard_then_retry`
  (per the 2026-04-30 stores cleanup) but the docstring carries forward
  the historical name.
- `invalidate` was retired in 2026-05-14 but the validator still has a
  special-case rejection branch (`code:graph/node/template_validator.go:357`).
- Subscription filtering on `SubscriptionEntry` uses N×M structured
  fields (`When`, `Outcome`, `ErrorClass`, `Reason`, message-envelope
  fields, attribute `Name`, event `Name`) — fixed vocabulary, no
  payload predicates, no way to filter on attribute values.
- Several state transitions are not subscribable at all: retries,
  on-error policy resolutions, heartbeat misses. Internal evaluator
  state (e.g. `RetryCounter`) is tracked but never emitted as a
  cascade signal.

These limitations are reachable through routine template-authoring:
projects that want fine-grained repair cascades, observability of
retry attempts, or filtering on payload contents hit them quickly.

## 2. The unification: every state transition is a signal

Replace the ad hoc per-topic-kind narrowing structure on
`SubscriptionEntry` with a uniform signal model:

```
Signal {
  type:    string  // hierarchical path
  payload: object  // structured per type
}
```

**Type-path taxonomy** (canonical, validator-enforced):

```
terminal/{success|error|park|infra}[/{class_or_reason}]
transient/{retry|on_error_resolved|heartbeat_missed|policy_resolved}[/...]
lifecycle/{node_created|node_canceled|instance_terminated|...}
attribute/{key}/changed
event/{name}
message/{kind}/{sender_kind}/{target}
```

**Subscription:**

```yaml
subscribes:
  - type: terminal/error/adapter_validation_failed     # exact match
  - type: terminal/error/*                              # prefix wildcard
  - type: terminal/*
    when: payload.duration_ms > 60000                   # type + payload predicate
  - type: transient/retry/*
    when: payload.attempt == payload.cap                # "final retry"
```

**Filter cost model:**

- Type-only match: `strings.HasPrefix` — free.
- Type + simple payload predicate (numeric compare, equality, `exists`):
  O(1–3 ops), cheap.
- Type + complex predicate: paid for by the subscriber who opted in.

Most filters in practice will be type-only; the cost scales with what
the subscriber actually asks for.

**Filter expression language: CEL.** Google's CEL has a Go implementation
(`github.com/google/cel-go`), is well-specified, sandboxed by default,
and is the Kubernetes-ecosystem default for sandboxed predicates against
structured data. The dependency add is real but the language is the
right shape for our use case (boolean predicates over typed structured
data, no side effects, no I/O). Alternatives considered: JMESPath
(too restricted — no arithmetic), JSONLogic (too verbose), custom DSL
extending rimsky's substitution grammar (more work, less standard).

## 3. The decoupling: three orthogonal dimensions

A policy resolution today is one named action. Under the new model
it's a 3-tuple of independent dimensions plus optional sub-options:

```
PolicyResolution {
  signal:    SignalSpec       // type-path + payload — what subscribers react to
  scheduler: SchedulerAction  // {Retry, ParkAsync, ParkScheduled} OR absent
  color:     NodeColor        // {fresh, stale, running, passed, failed, parked}

  // Retry sub-options:
  retry_discard_claims: bool
  retry_delay_ms:       int

  // ParkScheduled sub-option:
  wake_at:              time.Time
}
```

**SchedulerAction is the "do you want to extend this dispatch"
question.** Three answers:

| SchedulerAction | Meaning |
|---|---|
| `Retry` | Re-enqueue this dispatch (with optional Abandon on claims, optional backoff). Bounded by `max_retries_without_progress`. |
| `ParkAsync` | Pause this dispatch awaiting an external callback. |
| `ParkScheduled` | Pause this dispatch with a wake timer. |

**Absence** of a SchedulerAction = "this dispatch is finished; node
settles in its color; cascade walker continues from the emitted
signal." There is no fourth "terminal" action — that was the confusion
in the prior framing. The default is "dispatch ends"; the actions are
explicit extensions.

**Color is informational.** Per `code:runtime/state_propagation.go:187-193`,
rimsky already groups `fresh`, `failed`, `parked` together as
`isTerminal` ("settled, not currently running"). The dispatcher's
eligibility logic re-evaluates inputs from cascade triggers regardless
of which of those colors the node is in. So a node in `failed` color
whose hard-dep just got invalidated re-dispatches normally. Color
labels what the last run did; it doesn't gate future runs.

(Note: `isTerminal` should rename to `isSettled` — "terminal" carries
unintended finality.)

**Signal emission is independent of both.** Every policy resolution
emits its signal to the cascade; subscribers decide what to do with
it. The docstring's "no cascade-fire" semantic for `pass` evaporates
under this model — there's no per-action decision about whether to fire
the cascade; the cascade is always available, and subscribers earn
their reactions by declaring filters.

## 4. The deterministic spine: scheduler-action table

The full action vocabulary collapses to a small table of canonical
(signal, scheduler, color) tuples:

| Preset name | signal emitted | scheduler | color | sub-options |
|---|---|---|---|---|
| `retry` | `transient/retry/{n}/{class}` | `Retry` | (next run sets it) | discard_claims=false, delay=backoff |
| `discard_claims_then_retry` | `transient/retry/{n}/{class}/discard` | `Retry` | (next run sets it) | discard_claims=true, delay=backoff |
| `give_up` | `terminal/error/{class}` | (none) | `failed` | — |
| `pass` (Z-pattern: swallow-and-rerun) | `terminal/error/{class}` | (none) | `fresh` | — |
| `pass_record` (silently treat error as success) | `terminal/error/{class}` | (none) | `passed` | — |
| `park_async` | `terminal/park/{reason}` | `ParkAsync` | `parked` | session_token, callback_url |
| `park_snooze` | `terminal/park/{reason}` | `ParkScheduled` | `parked` | wake_at |

This table IS the rimsky public documentation surface. Operators
read it and know exactly what each action does. New behaviors are
new rows; the underlying machinery doesn't grow.

**Two API surfaces, one model:**

- **Named presets** (operator-facing) — template authors pick from
  the vocabulary above. Maps to the canonical tuple at registration.
- **Free tuple composition** (latent in implementation) — the runtime
  internally operates on the tuple; presets are just validated
  combinations. Future use cases that need unusual combinations add
  new presets without new code paths.

The validator constrains preset names against the canonical list and
rejects unknown actions at registration (closing the silent-degrade
gap noted at `code:graph/node/template_validator.go::validateErrorTypes`,
which today accepts any string).

**`max_retries_without_progress`** caps the `Retry`-action chain.
When the counter exhausts, the resolved action escalates to `give_up`
(or operator-configured fallback). Counts increment on `Retry`-family
resolutions; `pass`/`pass_record` count if and only if they re-trigger
the node (configurable per error class).

## 5. Action vocabulary cleanup

- Rename `discard_then_retry` → `discard_claims_then_retry`. The
  current name is opaque about WHAT gets discarded; "claims" is the
  load-bearing word.
- Drop `resume_then_retry` entirely. It's a confirmed alias for
  `discard_then_retry` since the 2026-04-30 stores cleanup; the
  docstring at `code:foundation/spec/policy.go:30-36` admits as much.
  Pre-v1, no compat obligation.
- Drop the `invalidate` validator special-case at
  `code:graph/node/template_validator.go:357` (retired in 2026-05-14;
  the migration message has served its purpose).
- Redefine `pass` to mean the Z-pattern semantic (`fresh` color, full
  cascade fire). Retire the docstring's "passed+no-cascade" semantic.
  If a project genuinely wants "swallow this error and call the node
  passed," that becomes `pass_record` (explicit, named).

## 6. Bundled executor error vocabulary

Rimsky's bundled executors emit error classes today as flat opaque
strings (`http_unexpected_status`, `http_request_failed`,
`http_response_parse_failed`, etc. for http-node). Under the unified
model these become structured type-paths matching the canonical
taxonomy:

**http-node** (`code:executors/http-node/server.go`):

- `http/network_error` — DNS, connection refused, TLS handshake fail
- `http/timeout` — request timeout
- `http/request_invalid/{body_class}` — non-`expect_status` response.
  The executor reads the response body's `error_class` field (or a
  node-configurable field name; default `error_class`) and emits it
  as the leaf segment. Falls back to `http/request_invalid/_unspecified`
  if the body isn't JSON or doesn't contain the field.
- `http/server_error/{status}` — 5xx without a body class
- `http/expectation_mismatch` — status outside `expect_status` with
  no body class and not 5xx
- `http/response_unparseable` — body wasn't valid JSON when one was
  expected

This subsumes what the zonebase verification report called "R1"
(extract `error_class` from response body) — it falls out of the
broader executor-vocab rationalization, no special-case implementation.

**claude-agent** (`code:executors/claude-agent/`):

- `agent/rate_limited`
- `agent/context_exceeded`
- `agent/tool_use_failed/{tool}`
- `agent/refused` (model refused the request)
- `agent/schema_violation` (structured output didn't match)
- `agent/timeout`

**postgres-stores** (`code:stores/postgres/`):

- `pg/claim_unavailable`
- `pg/connection_lost`
- `pg/swap_failed`
- `pg/verifier_check_failed/{check_kind}`

The hierarchical structure gives template authors clean subscription
surfaces: `type: http/timeout` for one node's reaction; `type: http/*`
for a generic "any HTTP problem" handler; `type: agent/rate_limited`
+ `when: payload.retry_after_seconds > 60` for a long-rate-limit
escalation.

## 7. Validator tightening

- Range-check `error_types.<class>.action` against the canonical preset
  vocabulary at `code:graph/node/template_validator.go::validateErrorTypes`.
  Reject unknown values with a precise error naming the offending field
  and listing the valid set.
- Range-check subscription `type:` against the declared canonical
  taxonomy. Reject paths outside the taxonomy with a clear error.
- Parse and statically validate CEL `when:` predicates at registration
  time. Reject syntactically invalid expressions; warn on expressions
  that reference fields not declared in the relevant signal's payload
  schema.

## 8. Migration shim and backwards-compat

Rimsky is pre-v1 (per `file:.claude/rules/rules.md`) so breaking
changes are OK. But this is a substantive surface change and a shim
keeps the transition cheap:

- The existing `SubscriptionEntry` struct fields (`When`, `ErrorClass`,
  `Reason`, etc.) canonicalize at registration to the new
  `(type, when_expression)` form. Old YAML keeps working.
- The existing PolicyAction names map to the new canonical-preset
  vocabulary. `resume_then_retry` is removed entirely (no shim — the
  docstring already calls it dead).
- The new `type:` and `when:` subscription form is purely additive;
  old templates don't use them.
- The bundled-executor error-vocab changes are observable wire
  behavior. Templates that subscribe to the OLD flat error classes
  (`http_unexpected_status`, `http_request_failed`) get a registration-
  time warning suggesting the new hierarchical form, but old filters
  still match via legacy-alias support during a transition window.

## 9. Rough work breakdown (LOC by area, not time)

For the rimsky maintainer's scoping:

- `code:foundation/spec/subscription.go` — extend `SubscriptionEntry`
  with `Type` and `When` (CEL expression); keep old fields with
  shim semantics. ~30 LOC + tests.
- `code:foundation/spec/policy.go` — refactor `PolicyAction` /
  `ResolvedAction` to the new 3-tuple shape; document canonical
  preset table. ~50 LOC + tests.
- `code:graph/node/policy.go::Evaluate` (and `step` evaluator) —
  rewrite around the new resolution shape; implement preset-to-tuple
  mapping. ~80 LOC + tests.
- `code:graph/node/subscription_edges.go` + cascade walker — match
  on type-path prefix + optional CEL predicate. ~100 LOC + tests.
- `code:graph/node/template_validator.go` — range-check actions and
  subscription types; CEL parse-check. ~50 LOC + tests.
- CEL integration (`github.com/google/cel-go` dependency, env
  setup, signal-payload schema registration). ~100 LOC + tests.
- Signal emission for previously-invisible transitions (retry,
  on_error_resolved, heartbeat_missed, lifecycle/*). Implemented
  lazily: emit only when a subscriber exists. ~80 LOC + tests across
  runtime/.
- Bundled-executor error-vocab updates: ~30 LOC each across http-node,
  claude-agent, postgres-stores. ~100 LOC total + tests.
- Migration shim for legacy `SubscriptionEntry` fields and action
  names. ~50 LOC + tests.

Ballpark: 600–800 LOC of substantive code + 600–1000 LOC of tests
across the codebase. Touches the foundation/runtime/graph triad and
the bundled executors; not a single-file change.

## 10. R5: source_file relative paths

Independent of the signal-taxonomy work; bundled here only because it
came from the same investigation. Scope:

**Drop the `..`-escape rejection** in `code:control/cli/templates.go::readSourceFile`.
Today the resolver rejects paths that escape the spec file's directory.
Loosen to allow `..` relative paths so projects can use a shared-prompts
layout like:

```
templates/
  onboard.yaml         # source_file: ../shared/header.md
  ingest.yaml
shared/
  header.md
```

Absolute paths can stay rejected (or also be lifted — minor either
way). The CLI runs on the operator's machine; the operator-trust
boundary already covers whatever the CLI can read.

~5 LOC change + a test case for relative-escape paths. Not coupled to
the signal-taxonomy work; can land independently.

## 11. Cross-references

- The consumer-project verification report:
  `file:../../../zonebase/.ok-planner/sketches/2026-05-22-cycle5-rimsky-verification.md`
  (per-finding rimsky `file:line` citations and rationale for each
  decision recorded here)
- The retired upstream-asks list:
  `file:../../../zonebase/.ok-planner/sketches/2026-05-22-rimsky-upstream-changes.md`
  (predecessor sketch with items R2/R3/R4/R6/R7 that were dropped
  when the consumer project's v1 simplification removed their need)
- 2026-05-14 subscription-cascade resolution (existing rimsky spec)
  that retired `invalidate` as a policy action — referenced in §5
- 2026-04-30 stores cleanup (existing rimsky spec) that made
  `resume_then_retry` a behavioral alias for `discard_then_retry` —
  referenced in §5

## Open questions for the rimsky maintainer

- **CEL is one good filter language; are there others worth weighing?**
  JMESPath rejected (too restricted); JSONLogic rejected (too verbose);
  custom DSL rejected (more work, less standard). But the maintainer
  may have other constraints in mind.
- **Wildcards: trailing `*` only, or full-path globs?** Sketch
  recommends trailing-only for predictability; full globs invite
  surprises.
- **Lazy vs eager emission of new signals (retry, on_error_resolved,
  etc.)?** Sketch recommends lazy — check subscription edge map at
  registration; skip emission if nothing's subscribing. Avoids
  cascade-walker thrashing on retries nobody's watching.
- **Backwards-compat shim window length.** Pre-v1 we can break freely,
  but if other consumer projects exist beyond zonebase by the time
  this lands, a shim window during the transition reduces churn.
- **`pass` semantics.** Sketch recommends redefining `pass` to mean
  the Z-pattern semantic (`fresh` color, full cascade), retiring the
  docstring's "passed+no-cascade" version. If maintainer prefers
  keeping the docstring semantic and adding a NEW preset name for
  Z-pattern, that's a documentation call.
