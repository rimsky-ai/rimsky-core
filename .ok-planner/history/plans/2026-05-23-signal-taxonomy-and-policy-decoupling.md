# Signal Taxonomy and Policy Decoupling — Implementation Plan

**Spec:** .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
**Goal:** Replace `SubscriptionEntry`'s N×M structured-field filtering with a hierarchical signal type-path taxonomy + CEL `when:` predicates, decouple the conflated `PolicyAction` into a `(signal, dispatch_disposition, color)` resolution tuple, retire `concept:lifecycle-handler` / `concept:last-outcome` / (partially) `concept:transition-reason`, fold runtime acquisition failure into the unified `error_types:` surface, and restructure bundled-executor error vocabularies into hierarchical paths.
**Architecture:** A new `foundation/signal/` package owns the canonical type-path taxonomy, payload schemas, signal-envelope construction, and CEL filter language. Every node-run transition emits a `Signal{type, payload}` to both the cascade walker (which evaluates subscriber CEL predicates to gate wait-set inserts) and the audit log (which writes one `rimsky_events` row per signal). Operator-side ErrorPolicy collapses to four named actions (`pass | give_up | retry | discard_claims_then_retry`); runtime acquisition failure routes through the same `error_types:` chain via synthetic class `acquire/unavailable`.
**Tech Stack:** Go (root + foundation + protocols modules tied via `go.work`), `github.com/google/cel-go` (new dep on root `go.mod`), `jackc/pgx/v5` for Postgres, `modernc.org/sqlite`, TypeScript + vitest for the claude-agent executor, protoc + buf for proto regeneration via `make proto-gen`.

---

## Pass overview and ordering constraints

Six passes, one per spec phase. Hard ordering constraints (from the spec's "Phasing guidance" section):

- **Pass 2 must include the cascade-fire gate deletion** (the two `if lastOutcome == cascade.LastOutcomeFreshChanged` sites at `runtime/runner_terminal.go:362` and `runtime/runner_terminal.go:420`). Without this, cascade is double-gated and the new subscriber-driven model can't fire.
- **Pass 3 must precede Pass 4.** Pass 4's `acquire/unavailable` config has to route through the tightened 4-action vocabulary, not the legacy 6-action set.
- **Pass 6's proto regen (`make proto-gen`) must precede any code change in that pass that consumes the new field.**
- Passes 1 and 6 are loosely independent of 2–5 (Pass 1 is foundational infrastructure; Pass 6 is per-executor leaves) but Pass 6's validator extension depends on Pass 1's signal-package infrastructure.

The plan honors all constraints by sequencing passes in numeric order; the executor processes them serially.

---

## Pass 1: Signal infrastructure + audit-emission wiring

**Goal:** Introduce the `foundation/signal/` package (type-path taxonomy, payload schemas, CEL filter env, signal-envelope construction, audit-emit helper) and wire every signal-bearing producer (`applyTerminal*`, `applyErrorPolicy` retry path, named-event emit, attribute-change emit, message arrival, heartbeat-missed sweep, `AwaitAsyncCallback` initial emit) to write `rimsky_events` rows with canonical signal type-paths in `kind`. The cascade walker is unaffected in this pass (it still gates on `last_outcome`); subscribers are unaffected (they still use the structured-filter `SubscriptionEntry`). Non-signal audit writes (`lock_acquired`, `work_started`, `auth.*`, etc.) keep their existing shape — see `tension:events-kind-no-enum` partial-coverage note in the spec.

**Scope:** Tasks 1–17
**End state:** working
**Verification:** `go build ./... && go test ./foundation/signal/... ./runtime/... -count=1 && make lint`

### Task 1: Add `cel-go` dependency to the foundation module

**Files:** `foundation/go.mod`, `foundation/go.sum`

**Steps:**
1. From the repo root, run `cd foundation && go get github.com/google/cel-go@latest && go mod tidy && cd ..`.
2. Run `git diff foundation/go.mod` and confirm a single new line under `require` of the form `github.com/google/cel-go vX.Y.Z`. Confirm `foundation/go.sum` has the corresponding checksum lines.
3. The signal package will live under `foundation/signal/` (see Task 2 onward); foundation must be the importing module. The root `go.mod` does not need direct CEL imports — root code (`graph/`, `runtime/`, `control/`) imports `foundation/signal` and gets CEL transitively.

**Verification:** `go build ./...` exits 0 (run from the repo root; go.work ties the modules).

### Task 2: Create `foundation/signal/types.go` with the Signal envelope and TypePath

**Files:** `foundation/signal/types.go` (new)

**Steps:**
1. Create the file with package declaration `package signal`, an `@concept: signal` annotation comment block, and these exported types:
   - `type TypePath string` — the canonical hierarchical path (e.g., `"terminal/error/http/timeout"`).
   - `type Signal struct { Type TypePath; Payload map[string]any }` — the wire envelope.
   - `type TopLevelKind string` constants: `KindTerminal`, `KindTransient`, `KindAttribute`, `KindEvent`, `KindMessage` (values `"terminal"`, `"transient"`, `"attribute"`, `"event"`, `"message"`).
2. Add a helper `func (t TypePath) TopLevel() TopLevelKind` that returns the first slash-delimited segment as a `TopLevelKind`, with validation that the segment is one of the five canonical values (returns empty string on invalid).
3. Add a helper `func (t TypePath) HasPrefix(prefix TypePath) bool` that does `strings.HasPrefix(string(t), string(prefix))` plus a guard that `prefix` either ends with `*` (in which case it strips the trailing `*`) or matches exactly.

**Verification:** `go build ./foundation/signal/...` exits 0.

### Task 3: Create `foundation/signal/taxonomy.go` with the canonical type-path validator

**Files:** `foundation/signal/taxonomy.go` (new), `foundation/signal/taxonomy_test.go` (new)

**Steps:**
1. Create `taxonomy.go` defining:
   - `var canonicalPathPatterns = []string{ ... }` — the enumerated valid type-path patterns from the spec's "Signal type-path taxonomy" section:
     ```
     "terminal/success"
     "terminal/error/*"
     "terminal/park/snooze"
     "terminal/park/await_callback"
     "terminal/infra/*"
     "transient/retry/*"
     "transient/heartbeat_missed"
     "transient/await_async"
     "attribute/*/changed"
     "event/*"
     "message/*"
     ```
   - `func ValidateTypePath(t TypePath) error` — returns nil if `t` matches any of the canonical patterns (exact-match for the no-`*` patterns; prefix-match for patterns ending in `*`, with the `*` allowed to expand to one or more slash-delimited segments). Returns `fmt.Errorf("invalid signal type-path: %q (not in canonical taxonomy)", t)` otherwise.
   - `func ValidateSubscriptionType(t TypePath) error` — same as `ValidateTypePath` but also accepts trailing-`*` patterns on the type itself (a subscription `type:` may be `terminal/error/*` even though that's not an exact-emit shape — it's a pattern to match emit shapes). Reject positional wildcards (e.g., `terminal/*/foo`) with `fmt.Errorf("invalid subscription type: %q (positional wildcards not supported; use trailing-*)", t)`.
2. Write `taxonomy_test.go` covering:
   - `TestValidateTypePath_AcceptsCanonical` — table-driven with one row per canonical pattern (`terminal/success`, `terminal/error/http/timeout`, `transient/retry/3/agent/rate_limited`, `attribute/budget_cents/changed`, `event/discovered`, `message/invalidate/operator/self`). All return nil.
   - `TestValidateTypePath_RejectsUnknown` — `terminal/garbage`, `not_a_kind/foo`, `terminal/error` (no class leaf), `lifecycle/node_created` (the explicitly-not-introduced kind from the spec).
   - `TestValidateSubscriptionType_AcceptsTrailingWildcard` — `terminal/error/*`, `terminal/*`, `event/*`.
   - `TestValidateSubscriptionType_RejectsPositionalWildcard` — `terminal/*/foo`, `*/error/*`.

**Verification:** `go test ./foundation/signal/... -run 'TestValidateTypePath|TestValidateSubscriptionType' -count=1` passes.

### Task 4: Create `foundation/signal/payloads.go` with the per-signal payload-schema declarations

**Files:** `foundation/signal/payloads.go` (new), `foundation/signal/payloads_test.go` (new)

**Steps:**
1. Create `payloads.go` with one exported struct per signal payload schema enumerated in the spec's "Payload schemas" section. Each struct mirrors the spec's field listing exactly. The convention is rimsky-side: proto fields named `payload` get renamed to `error_payload` / `event_payload` / `park_payload` / `message_payload` per the spec's "Field-naming convention" subsection. Structs:
   ```go
   type TerminalSuccessPayload struct {
       Changed         bool          `json:"changed"`
       AttributesDelta map[string]any `json:"attributes_delta"`
       ChangeSummary   string        `json:"change_summary,omitempty"`
   }
   type TerminalErrorPayload struct {
       ErrorClass     string         `json:"error_class"`
       ErrorPayload   map[string]any `json:"error_payload,omitempty"`
       Attempt        int            `json:"attempt"`
       RetriesSoFar   int            `json:"retries_so_far"`
   }
   type TerminalParkSnoozePayload struct {
       ResumeAt           time.Time `json:"resume_at"`
       SessionToken       string    `json:"session_token,omitempty"`
       ParkPayload        []byte    `json:"park_payload,omitempty"`
       ParkedReasonLabel  string    `json:"parked_reason_label,omitempty"`
       ParkedReasonNote   string    `json:"parked_reason_note,omitempty"`
   }
   type TerminalParkAwaitCallbackPayload struct {
       ResumeAt           *time.Time `json:"resume_at,omitempty"`
       SessionToken       string     `json:"session_token,omitempty"`
       ParkPayload        []byte     `json:"park_payload,omitempty"`
       ParkedReasonLabel  string     `json:"parked_reason_label,omitempty"`
       ParkedReasonNote   string     `json:"parked_reason_note,omitempty"`
   }
   type TerminalInfraPayload struct {
       Reason          string         `json:"reason"`
       LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
       Details         map[string]any `json:"details,omitempty"`
   }
   type TransientRetryPayload struct {
       Attempt          int            `json:"attempt"`
       Cap              int            `json:"cap"`
       ErrorClass       string         `json:"error_class"`
       DiscardedClaims  bool           `json:"discarded_claims"`
       DelayMs          int            `json:"delay_ms"`
       ErrorPayload     map[string]any `json:"error_payload,omitempty"`
   }
   type TransientHeartbeatMissedPayload struct {
       LastHeartbeatAt time.Time              `json:"last_heartbeat_at"`
       DispatchID      foundationshared.UUID  `json:"dispatch_id"`
       ThresholdMs     int                    `json:"threshold_ms"`
   }
   type TransientAwaitAsyncPayload struct {
       AsyncAckID  string `json:"async_ack_id"`
       CallbackURL string `json:"callback_url"`
   }
   type AttributeChangedPayload struct {
       Key      string `json:"key"`
       Value    any    `json:"value"`
       OldValue any    `json:"old_value,omitempty"`
   }
   type EventPayload struct {
       Name         string         `json:"name"`
       EventPayload map[string]any `json:"event_payload,omitempty"`
   }
   type MessagePayload struct {
       Kind           string         `json:"kind"`
       SenderKind     string         `json:"sender_kind"`
       Sender         string         `json:"sender"`
       Target         string         `json:"target"`
       MessagePayload map[string]any `json:"message_payload,omitempty"`
   }
   ```
   Import `time` and `foundationshared "github.com/rimsky-ai/rimsky-core/foundation/shared"`.
2. Add a helper `func PayloadSchemaForType(t TypePath) (reflect.Type, bool)` returning the Go reflect.Type of the payload schema matching the exact-type, or the second-return `false` for prefix types (which bind `dyn` per the spec). Map exact types to their structs:
   - `terminal/success` → `TerminalSuccessPayload`
   - exact `terminal/error/<class>` (any leaf) → `TerminalErrorPayload`
   - `terminal/park/snooze` → `TerminalParkSnoozePayload`
   - `terminal/park/await_callback` → `TerminalParkAwaitCallbackPayload`
   - exact `terminal/infra/<reason>` → `TerminalInfraPayload`
   - exact `transient/retry/<n>/<class>` → `TransientRetryPayload`
   - `transient/heartbeat_missed` → `TransientHeartbeatMissedPayload`
   - `transient/await_async` → `TransientAwaitAsyncPayload`
   - exact `attribute/<key>/changed` → `AttributeChangedPayload`
   - exact `event/<name>` → `EventPayload`
   - exact `message/<kind>/<sender_kind>/<target>` → `MessagePayload`
3. Write `payloads_test.go` covering: each struct round-trips through `json.Marshal` + `json.Unmarshal` preserving fields; `PayloadSchemaForType` returns the correct reflect.Type for each canonical exact-path; returns `(nil, false)` for prefix paths like `terminal/*` and `terminal/error/*`.

**Verification:** `go test ./foundation/signal/... -run 'TestPayloads|TestPayloadSchemaForType' -count=1` passes.

### Task 5: Create `foundation/signal/cel.go` with the CEL environment and predicate compilation

**Files:** `foundation/signal/cel.go` (new), `foundation/signal/cel_test.go` (new)

**Steps:**
1. Create `cel.go` exposing:
   - `type CompiledPredicate struct { program cel.Program }` — opaque wrapper over a compiled CEL program.
   - `func CompileWhen(typeSpec TypePath, when string) (*CompiledPredicate, error)` — compiles the CEL `when:` expression given the subscription's `type:` field. If `typeSpec` is an exact type (no trailing `*`), the CEL env binds `payload` as the corresponding payload-schema type (via `cel.Variable("payload", schemaType)`). If `typeSpec` ends in `*` (prefix), the CEL env binds `payload` as `cel.DynType`. Both cases also bind `type` as `cel.StringType`. Empty `when` returns `(nil, nil)` — meaning "no predicate, always match." Syntax errors return `fmt.Errorf("invalid CEL expression %q: %w", when, err)`. Field-reference errors (only emitted for exact-type bindings) return an explicit error naming the missing field.
   - `func (p *CompiledPredicate) Eval(signal Signal) (bool, error)` — evaluates the program against the signal's type+payload, returning the boolean result. If `p` is nil, returns `true, nil`.
2. The CEL env construction uses `cel.NewEnv(opts...)` from `github.com/google/cel-go/cel`. Schema types come from `Task 4`'s `PayloadSchemaForType`. For payload struct fields, use `cel.ObjectType` with the fully-qualified Go type name — see CEL-go's Go-struct-to-CEL example for the registration pattern (the codebase has no prior CEL integration, so introduce a small helper `registerPayloadTypes(env)` here that walks all the payload structs in `payloads.go` and registers them in the env).
3. Write `cel_test.go` covering:
   - `TestCompileWhen_NilOnEmpty` — empty string returns `(nil, nil)`.
   - `TestCompileWhen_AcceptsValidExact` — `type: terminal/error/http/timeout`, `when: "payload.error_class == 'http/timeout'"` compiles + evaluates true against a matching `Signal`.
   - `TestCompileWhen_AcceptsValidPrefix` — `type: terminal/*`, `when: "type.startsWith('terminal/error')"` compiles + evaluates true.
   - `TestCompileWhen_RejectsInvalidSyntax` — `when: "payload.foo &&&"` returns a syntax error.
   - `TestCompileWhen_RejectsUnknownFieldExact` — `type: terminal/success`, `when: "payload.error_class == 'x'"` returns a field-not-in-schema error (because `terminal/success` payload has no `error_class`).
   - `TestCompileWhen_PrefixBindsDyn` — `type: terminal/*`, `when: "payload.error_class == 'x'"` compiles successfully (dyn binding allows unknown fields); evaluates false when the actual signal payload doesn't have that field.

**Verification:** `go test ./foundation/signal/... -run 'TestCompileWhen' -count=1` passes.

### Task 6: Create `foundation/signal/audit.go` with the `EmitSignal` helper

**Files:** `foundation/signal/audit.go` (new), `foundation/signal/audit_test.go` (new)

**Steps:**
1. Open `foundation/persistence/events.go:~24-30` and confirm the actual `EventTable` + `EventAppendInput` shape. As of plan-writing (verify before editing):
   ```go
   type EventTable interface {
       Append(ctx context.Context, in EventAppendInput, tx Tx) error
       // ... other methods
   }
   type EventAppendInput struct {
       InstanceID *shared.UUID    // pointer, nullable
       NodeID     *shared.UUID    // pointer, nullable
       Kind       string
       Payload    map[string]any  // already a map; no marshal needed
       OccurredAt *time.Time      // pointer; nil → server NOW()
   }
   ```
2. Create `audit.go` matching the actual field shapes:
   ```go
   package signal

   import (
       "context"
       "time"

       "github.com/rimsky-ai/rimsky-core/foundation/persistence"
       shared "github.com/rimsky-ai/rimsky-core/foundation/shared"
   )

   func EmitSignal(
       ctx context.Context,
       events persistence.EventTable,
       instanceID, nodeID shared.UUID,
       sig Signal,
       occurredAt time.Time,
       tx persistence.Tx,
   ) error {
       return events.Append(ctx, persistence.EventAppendInput{
           InstanceID: &instanceID,
           NodeID:     &nodeID,
           Kind:       string(sig.Type),
           Payload:    sig.Payload,   // already map[string]any from the Signal envelope
           OccurredAt: &occurredAt,
       }, tx)
   }
   ```
   (No `json.Marshal` step — payload is already `map[string]any`. Pointer-wrap UUIDs and timestamps.)
3. Write `audit_test.go` using the in-memory persistence backend (find via `rg 'package inmemory' foundation/persistence/`), seeding an instance + node, calling `EmitSignal` with a `terminal/success` signal, then reading back the row and asserting `Kind == "terminal/success"` and `Payload` matches the expected `map[string]any` (no JSON round-trip needed since the field is already a map).

**Verification:** `go test ./foundation/signal/... -run 'TestEmitSignal' -count=1` passes.

### Task 7: Wire signal emission at `applyTerminalComplete`

**Files:** `runtime/runner_terminal_handlers.go`, `runtime/runner_terminal.go`

**Steps:**
1. Find `applyTerminalComplete` (currently in `runtime/runner_terminal_handlers.go` or `runtime/runner_terminal.go` — use `rg "func applyTerminalComplete"`). At the audit-write site (where today an event row with `kind="state_transition"` or similar gets written), replace with a call to `signal.EmitSignal(ctx, args.Persist.Events(), instanceID, nodeID, signal.Signal{Type: "terminal/success", Payload: ...}, args.Clock.Now(), tx)`.
2. Construct the `TerminalSuccessPayload` from the terminal event data: `Changed: t.Changed`, `AttributesDelta: t.AttributesDel`, `ChangeSummary: t.ChangeSummary`. Convert to `map[string]any` via a small `payloadToMap(any) map[string]any` helper or via JSON-round-trip.
3. **Do not delete** the existing fixed-string audit write (`kind=state_transition`, `kind=work_completed`, etc., depending on the site) yet. The new signal write is additive at this stage — both rows write for now, with different `kind` values. Pass 5 Task 52 retires the additive fixed-string write. Add a comment `// TODO(signal-taxonomy Pass 5): retire this fixed-string audit write — the kind=terminal/success signal-emit above is the canonical audit row.`
4. Add the import `signalpkg "github.com/rimsky-ai/rimsky-core/foundation/signal"`. (Use the `signalpkg` alias to avoid a collision with any local `signal` symbol.)

**Verification:** Run any existing `applyTerminalComplete` test (find via `rg "applyTerminalComplete" --include='*_test.go'`); confirm it still passes. Then run `rg "kind.*terminal/success" runtime/` and confirm one new write site exists.

### Task 8: Wire signal emission at `applyTerminalError` / `applyErrorPolicy`

**Files:** `runtime/runner_terminal_handlers.go`, `runtime/runner_error_policy.go`

**Steps:**
1. Find `applyTerminalError` and `applyErrorPolicy`. At the resolution site (`applyResolvedAction` or equivalent) where today the verdict gets recorded:
   - For `give_up` resolution: emit `terminal/error/<error_class>` signal with `TerminalErrorPayload`.
   - For `retry` resolution: emit `transient/retry/<attempt>/<error_class>` signal with `TransientRetryPayload`. Construct the type-path by concatenation: `signalpkg.TypePath(fmt.Sprintf("transient/retry/%d/%s", attempt, errorClass))`.
   - For `pass` resolution: emit `terminal/error/<error_class>` (same shape as give_up; color difference is in the run-row settle, not the signal — per the spec's ErrorPolicy table).
2. Same additive-write rule as Task 7: leave existing audit writes in place; add the signal-emit alongside; mark with `// TODO(signal-taxonomy Pass 5)`.
3. For the retry signal's `attempt` and `cap` fields, source from the `EvaluatorState.RetryCounter + 1` (1-indexed per the spec) and `action.Count` respectively (current `PolicyAction.Count` field).

**Verification:** Add a unit test `TestApplyErrorPolicy_EmitsTransientRetrySignal` in `runtime/runner_error_policy_test.go` that exercises a retry resolution and asserts via the in-memory events table that a `kind=transient/retry/1/<class>` row appears with the expected payload fields.

### Task 9: Wire signal emission at `applyTerminalPark`

**Files:** `runtime/runner_terminal_park.go`

**Steps:**
1. Find `applyTerminalPark` (currently in `runtime/runner_terminal_park.go`). Branch on `t.ParkReason`:
   - `PARK_REASON_SNOOZE` → emit `terminal/park/snooze` with `TerminalParkSnoozePayload{ResumeAt: t.ParkResumeAt, SessionToken: t.ParkSessionToken, ParkPayload: t.ParkPayload, ParkedReasonLabel: t.ParkReasonLabel, ParkedReasonNote: t.ParkReasonNote}`.
   - `PARK_REASON_AWAIT_CALLBACK` → emit `terminal/park/await_callback` with the corresponding payload (note `ResumeAt` is `*time.Time` here, set to nil if zero).
2. Same additive-write rule; leave existing audit writes; add `// TODO(signal-taxonomy Pass 5)`.

**Verification:** Add a unit test `TestApplyTerminalPark_EmitsSnoozeSignal` and `TestApplyTerminalPark_EmitsAwaitCallbackSignal` covering both branches.

### Task 10: Wire signal emission at `AwaitAsyncCallback` and `Infra` terminal paths

**Files:** `runtime/runner_dispatch.go`, `runtime/runner_terminal.go`

**Steps:**
1. Find the `case *genv1.StreamClose_AwaitAsync:` branch in `runtime/runner_dispatch.go:352` (was 353). Before returning the `terminalEvent{Kind: terminalKindAsyncAccepted}`, call `signal.EmitSignal` with `transient/await_async` signal carrying `TransientAwaitAsyncPayload{AsyncAckID: oc.AwaitAsync.AsyncAckId, CallbackURL: <from ExecuteRequest>}`. The callback URL is in the dispatch context; pass it through if not already accessible.
2. For the Infra path: find `terminalKindInfra` cases (synthesized at `runner_dispatch.go:~285, ~292` for stream-close failures and `~195, ~200, ~205` for executor-dial failures). In the handler for `terminalKindInfra` (whatever calls `applyTerminalInfra*`), emit `terminal/infra/<reason>` with `TerminalInfraPayload{Reason: t.ErrorClass, LastHeartbeatAt: ...}`. Map the reason from the existing `t.ErrorClass` synthesized value (e.g., `stream_closed_without_terminal` → reason = `stream_closed_without_terminal`).
3. Same additive-write rule.

**Verification:** Add unit tests `TestAwaitAsyncCallback_EmitsTransientSignal` and `TestApplyTerminalInfra_EmitsInfraSignal`.

### Task 11: Wire signal emission at named-event emit and message arrival

**Files:** `runtime/runner_named_events.go`, `runtime/message_delivery.go` (verified to exist at plan-write time)

**Steps:**
1. In `runtime/runner_named_events.go`, at the named-event persist site, additionally call `signal.EmitSignal` with `event/<name>` signal carrying `EventPayload{Name: name, EventPayload: payloadMap}`.
2. In the message-delivery cascade entry point, emit `message/<kind>/<sender_kind>/<target>` signal carrying `MessagePayload` with all envelope fields populated. The type-path is constructed by concatenation: `signalpkg.TypePath(fmt.Sprintf("message/%s/%s/%s", env.Kind, env.SenderKind, env.Target))`.

**Verification:** Unit tests `TestNamedEvent_EmitsEventSignal` and `TestMessageDelivery_EmitsMessageSignal`.

### Task 12: Wire signal emission at attribute writes and heartbeat-missed sweep

**Files:** `runtime/runner_terminal_handlers.go` (attribute-delta site within `applyTerminalComplete`), the heartbeat sweep (find via `rg "heartbeat.*sweep|HeartbeatLost|orphan.*heartbeat" runtime/`)

**Steps:**
1. After `applyTerminalComplete` writes the `attributes_delta`, iterate the changed attribute keys and for each emit one `attribute/<key>/changed` signal with `AttributeChangedPayload{Key, Value, OldValue}`. The old-value lookup comes from the pre-write attribute row; if not readily available, leave `OldValue: nil`.
2. In the heartbeat sweep, when a dispatch is detected as past its threshold, emit `transient/heartbeat_missed` with `TransientHeartbeatMissedPayload{LastHeartbeatAt, DispatchID, ThresholdMs}`. The sweep currently writes to the audit log as `kind="heartbeat_lost"`; that write stays (it's a non-signal admin event) but the additional signal-shaped emit goes alongside.

**Verification:** Unit tests `TestApplyTerminalComplete_EmitsAttributeSignalsPerKey` and `TestHeartbeatSweep_EmitsTransientSignal`.

### Task 13: Create `concepts/signal.md`

**Files:** `.ok-planner/design/concepts/signal.md` (new)

**Steps:**
1. Create the file using the standard concept template (consult any existing concept file like `.ok-planner/design/concepts/cascade.md` for shape — Definition, Purpose, Boundaries, Invariants, Aliases, Notes).
2. Populate from the spec's "Architecture > One signal, three orthogonal dimensions" section + the "Signal type-path taxonomy" section + the "Payload schemas" section + the "Subscription model > CEL environment" section. Include the YAML front-matter:
   ```yaml
   ---
   concept: signal
   status: as-is
   aliases: []
   references:
     - ../../specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md
   ---
   ```
3. The Boundaries section enumerates what `concept:signal` owns:
   - Canonical type-path taxonomy (5 top-level kinds + leaf rules)
   - Payload schema per signal type
   - CEL filter language (env construction, predicate compilation, evaluation)
   - Audit-emit pathway to `rimsky_events`
   - Signal-envelope construction helpers
4. The Invariants section enumerates (verbatim from the spec's "Design changes / Concept creations > concepts/signal.md > Invariants"):
   - Type-paths are canonical and validator-enforced.
   - Every transition that affects a node-run emits exactly one signal.
   - Audit-log emission is unconditional (every signal writes one `rimsky_events` row).
   - Cascade-fire is `subscription edge match && CEL predicate evaluates true` — no separate sender-side gate.
   - Wildcard syntax is trailing-`*` only.
   - CEL is the filter language; exact-type subscriptions parse-check field references against the resolved payload schema; prefix-type subscriptions bind `payload` as `dyn`.
   - `terminal/park/*` leaves are the closed two-value set determined by `ParkReason`; extending the set requires a proto-level change first. `AwaitAsyncCallback` is a transient (`transient/await_async`), not a park.
5. Add an empty Notes section.

**Verification:** `ls .ok-planner/design/concepts/signal.md` succeeds.

### Task 14: Regenerate `concepts.md` TOC

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Read the existing TOC. Find the alphabetically-correct insertion point for `signal` (between `sensor` and `service`).
2. Insert a new bullet:
   ```
   - `signal` — Hierarchical type-path-plus-payload envelope emitted on every node-run transition; drives cascade-fire (via subscriber-side CEL match), audit (`rimsky_events.kind`), and the canonical "what just happened" vocabulary. Introduced 2026-05-23.
   ```

**Verification:** `grep -c '^- \`signal\`' .ok-planner/design/concepts.md` returns 1.

### Task 15: Scenario tests for end-to-end signal emission

**Files:** `test/scenarios/signal_emission_test.go` (new)

**Steps:**
1. Create the file with three tests, each using the existing scenario harness (`scenario.Start(t, scenario.HarnessOpts{})`):
   - `TestSignalEmission_TerminalSuccess` — wire a stub executor to return Success{changed: true}; assert one `rimsky_events` row with `kind="terminal/success"` and payload `{"changed": true, ...}`.
   - `TestSignalEmission_TerminalErrorWithRetryThenGiveUp` — wire stub to return Error{error_class:"foo"}; operator's `error_types: { foo: { policy: [{action: retry, count: 1}, {action: give_up}] } }`; assert sequence of `kind` values: `transient/retry/1/foo` then `terminal/error/foo`.
   - `TestSignalEmission_ParkSnooze` — wire stub to return Park{reason: SNOOZE, resume_at: <time>}; assert `kind="terminal/park/snooze"` with payload.resume_at populated.
2. Use the harness's existing event-log reader (find via `rg "rimsky_events" test/scenarios/` for the read pattern).

**Verification:** `go test ./test/scenarios/ -run TestSignalEmission -count=1` passes. Requires Docker (testcontainers).

### Task 16: Add `@concept: signal` annotation at the EmitSignal helper

**Files:** `foundation/signal/audit.go`

**Steps:**
1. Add a comment block above `func EmitSignal(...)`:
   ```go
   // EmitSignal writes one rimsky_events row per emitted signal. The
   // sole audit-emit pathway for signal-bearing transitions.
   //
   //	@concept: signal
   ```

**Verification:** `rg '@concept: signal' foundation/signal/` returns at least one hit.

### Task 17: Verify Pass 1 end-state

**Files:** none (verification-only task)

**Steps:**
1. Run `go build ./...` — exits 0.
2. Run `go test ./foundation/signal/... ./runtime/... -count=1` — passes.
3. Run `make lint` — exits 0.
4. Run `rg 'EmitSignal' runtime/ | wc -l` — expect at least 8 (one call per signal-emit site wired in Tasks 7-12, plus tests).
5. Run `rg 'kind="terminal/' runtime/` — confirm signal-shaped kinds appear in producer call sites.

**Verification:** All above commands exit successfully and meet expectations.

---

## Pass 2: Subscription consumption (signal-driven cascade)

**Goal:** Reshape `SubscriptionEntry` to use `type:` + `when:` (CEL); replace the structured-filter fields entirely. Rebuild `BuildSubscriptionEdges` to produce a prefix-keyed inverse-edge map. Rewrite `cascadeSubscribersStaleInTx` to consume the new map and evaluate CEL predicates at walk time. Delete the two cascade-fire gates at `runtime/runner_terminal.go:362` and `runtime/runner_terminal.go:420`. Update template validator to accept new fields, range-check `type:`, parse-check `when:`. Update auto-subscribe (substitution-ref → implicit subscription). Update all template fixtures + scenario tests using old fields. Mutate concept docs: `node-subscription`, `cascade`, `wait-set`, `message`.

**Scope:** Tasks 18–32
**End state:** working
**Verification:** `go build ./... && go test ./graph/node/... ./runtime/... ./test/scenarios/... -count=1 && make lint`

### Task 18: Replace structured-filter fields on `SubscriptionEntry`

**Files:** `foundation/spec/subscription.go`

**Steps:**
1. Open the file. Replace the current struct (which has `Node`, `Instance`, `On`, `When`, `Outcome`, `ErrorClass`, `Reason`, `Name`, `Frame`, `Kind`, `Sender`, `SenderKind`, `Target`, `ResolvesViaCallingNode`) with:
   ```go
   type SubscriptionEntry struct {
       Node     string `yaml:"node,omitempty" json:"node,omitempty"`
       Instance bool   `yaml:"instance,omitempty" json:"instance,omitempty"`

       Type string `yaml:"type" json:"type"`
       When string `yaml:"when,omitempty" json:"when,omitempty"`

       Frame string `yaml:"frame,omitempty" json:"frame,omitempty"`

       ResolvesViaCallingNode bool `yaml:"resolves_via_calling_node,omitempty" json:"resolves_via_calling_node,omitempty"`
   }
   ```
2. Delete the `TopicKind*` constants block (`TopicKindState`, `TopicKindAttribute`, `TopicKindEvent`, `TopicKindMessage`) — no longer used.
3. Keep the `MessageSenderKind*` constants (`MessageSenderKindOperator | Publisher | Instance`) — still used as values inside signal payloads and at the messages-endpoint surface; only the per-subscription filter retires.
4. Keep the `SubscriptionScope*` constants (`SubscriptionScopeDirect | Instance`) and the `NodeState*` constants — both used elsewhere.

**Verification:** `go build ./foundation/spec/...` exits 0 (the rest of the build is expected to break here; this is a mid-pass intermediate state — Tasks 19-26 restore it).

### Task 19: Replace `SubscriptionEdge` / `SubscriptionEdgeMap` with prefix-keyed form

**Files:** `graph/node/subscription_edges.go`

**Steps:**
1. Replace the `SubscriptionEdge` struct with:
   ```go
   type SubscriptionEdge struct {
       ReceiverNodeType  string
       TypePattern       signal.TypePath           // exact or trailing-* prefix
       WhenExpr          *signal.CompiledPredicate // nil if no when:
       SubscriptionScope string                    // "direct" | "instance"
       Frame             string                    // "in" | "next"
   }
   ```
   Import `signal "github.com/rimsky-ai/rimsky-core/foundation/signal"`.
2. Delete the `SubscriptionFilter` struct entirely.
3. Replace `SubscriptionEdgeMap` with a per-sender prefix-trie:
   ```go
   type SubscriptionEdgeMap struct {
       bySender map[string]*prefixNode // sender node type -> root prefix node
   }
   type prefixNode struct {
       segment  string
       edges    []SubscriptionEdge       // edges that match exactly at this depth
       wildcard []SubscriptionEdge       // edges that match this prefix and below (trailing-*)
       children map[string]*prefixNode
   }
   ```
   Add methods:
   - `func NewSubscriptionEdgeMap() *SubscriptionEdgeMap` — constructor.
   - `func (m *SubscriptionEdgeMap) Insert(senderNodeType string, edge SubscriptionEdge)` — walks `edge.TypePattern` segments, building the tree; if pattern ends in `*`, attaches to `wildcard`; otherwise to `edges`.
   - `func (m *SubscriptionEdgeMap) Match(senderNodeType string, signalType signal.TypePath) []SubscriptionEdge` — walks segments, collecting `edges` matches at the exact terminal node plus all `wildcard` matches encountered along the path. Cross-cutting (`instance: true`) edges live under the empty sender-key `""` and are appended to the result if the caller wants to also include cross-cutting matches.
4. Rewrite `BuildSubscriptionEdges` to produce the new map structure. Each `SubscriptionEntry` becomes one `SubscriptionEdge`; compile `WhenExpr` via `signal.CompileWhen(entry.Type, entry.When)`; surface compile errors as registration-time errors.

**Verification:** `go build ./graph/node/... ./foundation/signal/...` exits 0.

### Task 20: Update `cascadeSubscribersStaleInTx` to consume signals

**Files:** `runtime/runner_terminal.go`

**Steps:**
1. Find `cascadeSubscribersStaleInTx` (current signature per grounding):
   ```go
   func cascadeSubscribersStaleInTx(
       ctx context.Context, args RunArgs, tx persistence.Tx,
       senderID foundationshared.UUID,
       senderNodeType string,
       senderRunID foundationshared.UUID,
       instanceID foundationshared.UUID,
       senderFrameID foundationshared.UUID,
   )
   ```
   Change the signature to take the emitted signal directly:
   ```go
   func cascadeSubscribersStaleInTx(
       ctx context.Context, args RunArgs, tx persistence.Tx,
       senderID foundationshared.UUID,
       senderNodeType string,
       senderRunID foundationshared.UUID,
       instanceID foundationshared.UUID,
       senderFrameID foundationshared.UUID,
       sig signal.Signal,
   )
   ```
2. Inside, replace the existing edge-loop. Old code accesses the edge map by `edges[senderNodeType]` (and `edges[""]` for cross-cutting) returning `[]SubscriptionEdge`; new code calls `edges.Match(senderNodeType, sig.Type)` returning the prefix-matched + cross-cutting edge list. Update all read-sites at runner_terminal.go:~587, ~588, ~963, ~964 (or wherever the map lookups live — find via `rg 'edges\[' runtime/runner_terminal.go`).
3. For each matched edge, evaluate the CEL predicate:
   ```go
   matched, err := edge.WhenExpr.Eval(sig)
   if err != nil {
       return fmt.Errorf("cascadeSubscribersStaleInTx: CEL eval for %s: %w", edge.ReceiverNodeType, err)
   }
   if !matched {
       continue
   }
   ```
   Then proceed with the existing per-edge logic (FrameNext / FrameIn dispatch, wait-set insert).
4. Remove all references to `edge.TopicKind` and `edge.Filter` in this function — they're gone with the structural change.

**Verification:** `go build ./runtime/...` exits 0.

### Task 21: Update all `cascadeSubscribersStaleInTx` call sites to pass the signal

**Files:** `runtime/runner_terminal.go`, `runtime/runner_error_policy.go`, `runtime/state_propagation.go`, `runtime/subgraph_dispatch.go`, `runtime/cascade_invalidate.go`, `runtime/hard_dep_cascade_export_test.go`

**Steps:**
1. Find all call sites via `rg 'cascadeSubscribersStaleInTx\(' runtime/`. Each call needs the new `signal.Signal` parameter.
2. At each call site, the caller already has the data needed to construct the signal (it's the same data the producer wired up in Pass 1). Extract the signal-construction logic from the producer (`applyTerminalComplete`, etc.) into a small helper if not already; pass the constructed signal directly.
3. The hard-dep cascade test export at `runtime/hard_dep_cascade_export_test.go:27` also needs the signal parameter — update the test export's signature to match.

**Verification:** `go build ./...` exits 0.

### Task 22: Delete the two `last_outcome == fresh_changed` cascade-fire gates

**Files:** `runtime/runner_terminal.go`

**Steps:**
1. Locate both gate sites via `rg 'lastOutcome == cascade.LastOutcomeFreshChanged' runtime/runner_terminal.go` (line numbers may have shifted from the pre-Pass-1 cited `:362`/`:420` after Pass 1's signal-emit additions). For each match, the surrounding block has the shape:
   ```go
   if lastOutcome == cascade.LastOutcomeFreshChanged {
       if err := cascadeSubscribersStaleInTx(ctx, args, tx, ...); err != nil { ... }
   }
   ```
   Remove the `if lastOutcome == cascade.LastOutcomeFreshChanged {` wrapper and the matching closing brace; the inner `cascadeSubscribersStaleInTx` call now runs unconditionally for every settled outcome (the receiver-side filter introduced in Tasks 19-21 is the new gate).
2. The two sites are: the gate around `cascadeSubscribersStaleInTx` and the gate around `fanoutRecalculate`. Both need the same treatment.
3. Confirm with `rg 'lastOutcome == cascade.LastOutcomeFreshChanged' runtime/` — should return zero hits.

**Verification:** `go build ./...` exits 0. `rg 'lastOutcome == cascade.LastOutcomeFreshChanged' runtime/` returns nothing.

### Task 23: Update template validator for new `type:` + `when:` fields

**Files:** `graph/node/template_validator.go`

**Steps:**
1. Find `validateSubscribes` (or the function that validates each `SubscriptionEntry`). Rewrite to:
   - Require non-empty `Type`.
   - Call `signal.ValidateSubscriptionType(entry.Type)` and surface the error with field path.
   - If `Node` is set, validate it references an existing node-type in the template (preserve existing semantic).
   - Validate `Node` xor `Instance` (mutual exclusion — preserve existing semantic).
   - Validate `Frame` is `"in" | "next" | ""` (preserve existing semantic).
   - If `When` is non-empty, call `signal.CompileWhen(entry.Type, entry.When)` and surface compile errors as validation errors with field path.
   - For `terminal/error/*` subscriptions naming a specific sender via `Node`, additionally call `hooks.ExecutorDeclaredErrorClasses(senderNode.Executor)` (added in Task 24 below); if available, cross-check the type leaf against the declared set; silent-skip otherwise. Mirror the existing `validateOnEvent`-replacement pattern at `template_validator.go:230,571`.
2. Remove validation logic for the deleted fields (`On`, `When`-as-state-filter, `Outcome`, `ErrorClass`, `Reason`, `Name`, `Kind`, `Sender`, `SenderKind`, `Target`).
3. The `acquire/unavailable` validator warning belongs with Pass 4 Task 41 (the `handleAcquireUnavailable` rewire), not here — moved to Task 41.5 below.

**Verification:** `go build ./graph/node/...` exits 0. Add a unit test `TestValidateSubscribes_NewShape` covering: accepts well-formed type+when; rejects unknown type-path; rejects malformed CEL; accepts node+type combo; rejects node+instance combination.

### Task 24: Add `ExecutorDeclaredErrorClasses` hook

**Files:** `graph/node/template_validator.go`

**Steps:**
1. Find the `ValidatorHooks` struct (around line 84 per grounding). Add a new field:
   ```go
   // ExecutorDeclaredErrorClasses returns the set of error-class paths
   // the named executor advertises via ObservabilityCapabilities.declared_error_classes,
   // and whether the executor was reachable. Mirrors ExecutorDeclaredEvents.
   //
   // The proto field declared_error_classes is added in Pass 6;
   // until then this hook always returns ([], false).
   //
   //	@concept: signal
   ExecutorDeclaredErrorClasses func(name string) ([]string, bool)
   ```
2. Plumb the new field through any `ValidatorHooks` construction site (`rg 'ValidatorHooks{' --include='*.go'`). The default value is nil; until Pass 6 wires it, the validator's range-check silent-skips per the pattern.

**Verification:** `go build ./...` exits 0.

### Task 25: Update auto-subscribe (substitution-ref → implicit subscription)

**Files:** `graph/node/subscription_edges.go`

**Steps:**
1. Find `ExtractSubstitutionRefsFromTemplate`. The output type changes: instead of producing `(SenderNodeType, TopicKind, Name)` triples, produce `(SenderNodeType, TypePath)` pairs:
   - `{{nodes.X.attribute.Y}}` → `(X, "attribute/Y/changed")`
   - `{{nodes.X.attribute}}` (bare) → `(X, "attribute/*/changed")` (whole-attribute pull)
   - `{{nodes.X.event.Y}}` → `(X, "event/Y")`
2. Update `edgeFromSubstitutionRef` to produce a `SubscriptionEdge` with `TypePattern` set to the above paths, `WhenExpr: nil`, `SubscriptionScope: SubscriptionScopeDirect`, `Frame: "in"`.
3. Update `BuildSubscriptionEdges` to consume the new triple shape.

**Verification:** `go build ./graph/node/...` exits 0.

### Task 26: Update template fixtures and scenario tests using old SubscriptionEntry fields

**Files:** All `*.yaml` template fixtures and `*_test.go` scenario tests that declare `subscribes:` blocks (find via `rg 'subscribes:' --include='*.yaml' --include='*_test.go' --include='*.go' -l`)

**Steps:**
1. For each fixture/test file, rewrite the `subscribes:` declarations from the old shape (e.g., `{ node: X, on: state, when: failed, error_class: foo }`) to the new shape (e.g., `{ node: X, type: terminal/error/foo }`). The translation rules:
   - `{ on: state, when: <node-state> }` → drop the `when` since node-state isn't first-class; subscribe to the relevant signal type-path. (For most existing cases, `on: state, when: failed` → `type: terminal/error/*`; `on: state, when: parked` → `type: terminal/park/*`; `on: state` (any) → `type: terminal/*`.)
   - `{ on: state, when: failed, error_class: foo }` → `type: terminal/error/foo`.
   - `{ on: state, when: parked, reason: snooze }` → `type: terminal/park/snooze`.
   - `{ on: state, outcome: fresh_changed }` → `type: terminal/success, when: payload.changed` (the spec's preserved self-subscription invariant).
   - `{ on: attribute, name: foo }` → `type: attribute/foo/changed`.
   - `{ on: event, name: foo }` → `type: event/foo`.
   - `{ on: message, kind: foo, sender_kind: operator, target: self }` → `type: message/foo/operator/self`.
2. Process each file one at a time; after each, run the corresponding scenario test (via `go test -run <TestName>`) to confirm it passes against the new shape.
3. Some scenario tests assert specific cascade-fire behavior tied to the old structured-filter semantics. Update those assertions to match the new subscriber-driven semantics: e.g., a test asserting "subscriber A fires because outcome matches" becomes "subscriber A fires because CEL matches payload."

**Verification:** `go test ./test/scenarios/ -count=1` exits 0. `rg '\bon: state\b' --include='*.yaml' --include='*.go'` returns nothing (all old fields gone).

### Task 27: Mutate `concepts/node-subscription.md`

**Files:** `.ok-planner/design/concepts/node-subscription.md`

**Steps:**
1. Open the file. In the "What it is" section, replace the "Four topic kinds" framing with: "A node-subscription declares `type:` (a canonical signal type-path, exact or trailing-`*` prefix per `concept:signal`) plus an optional `when:` CEL predicate over the signal payload. Sender-side filters (`node:` selects a specific upstream, `instance: true` is cross-cutting) and the frame modifier (`frame: in | next`) carry forward unchanged. The auto-subscribe rule from substitution refs becomes an implicit `type: attribute/Y/changed` or `type: event/Y` subscription on the reading node."
2. Update the Boundaries section. Replace the current `Owns:` list with:
   ```
   Owns:
   - The per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)`; a per-sender radix tree / prefix-bucket structure).
   - The auto-subscribe rule from substitution refs.
   - The consumer-side mapping from signal type-path to receiver wait-set rows.

   Does NOT own:
   - The signal taxonomy itself or payload schemas (those live in `concept:signal`).
   - The cascade walk itself (lives in `concept:cascade`).
   - The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
   - The eligibility predicate evaluated by `code:foundation/persistence/postgres/nodes.go::ListReadyForDispatch`.
   ```
3. Update the Invariants section: remove the state-only filter rules (`When`, `Outcome`, `ErrorClass`, `Reason` invariants — those fields retire). Add: "Subscription `type:` and `when:` are validated at registration against the canonical taxonomy (`concept:signal`) and the resolved payload schema." Preserve the self-subscription invariant added 2026-05-19 by restating in the new vocabulary: "Self-subscription is first-class in both `frame: in` and `frame: next` shapes; the canonical form is `{ node: <self-type>, type: terminal/success, when: payload.changed, frame: <in|next> }`."
4. Append to Notes:
   ```
   - 2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. SubscriptionEntry's structured filter fields (When/Outcome/ErrorClass/Reason/Name/Kind/Sender/SenderKind/Target) retire; replaced by type-path + CEL when:. Inverse-edge map shape changes from exact-key to prefix-keyed (radix tree). Auto-subscribe rule preserved. Self-subscription invariant preserved (restated in new vocabulary).
   ```

**Verification:** `grep -c 'type-path' .ok-planner/design/concepts/node-subscription.md` returns at least 3.

### Task 28: Mutate `concepts/cascade.md`

**Files:** `.ok-planner/design/concepts/cascade.md`

**Steps:**
1. Update the Invariants section: find the line `Cascade fires iff last_outcome == fresh_changed (not the raw Success.changed)` and replace with `Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL when: predicate evaluates true.`
2. Find the invariant `Cascade does not propagate from parked` or `Cascade does not propagate from parked or failed`. Delete it. Insert in the same location: `> **Retracted 2026-05-23.** Under the subscriber-driven cascade-fire model introduced by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design, propagation is determined by subscriber matches against the emitted signal, not by sender color. Settled-color is informational. The functional equivalent (downstream nodes not auto-firing on a failed sender) is now expressed receiver-side via subscribers' \`when:\` predicates or via not subscribing to \`terminal/error/*\` at all. The matching retraction lives on \`concepts/parked-state.md\`.`
3. Find the Common pitfalls section. Remove the bullet referencing `lifecycle-handler resolves never_propagate` and the bullet treating `last_outcome` as a dispatch gate. Replace those with one new pitfall: `Treating \`terminal/error/*\` subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, a subscriber filtering on \`terminal/error/*\` fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.`
4. Append to Notes:
   ```
   - 2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Cascade-fire predicate becomes subscriber match (concept:signal); last_outcome column retires. Filter evaluation moves to walk-time (CEL predicates against signal payload); the pessimistic-invalidate rule (insert wait-set rows regardless of filter) retires. The "cascade does not propagate from parked or failed" invariant retracts — propagation is subscriber-driven, not sender-color-driven (the matching retraction lives on concepts/parked-state.md). Common pitfalls refreshed to remove lifecycle-handler and last_outcome references.
   ```

**Verification:** `grep -c 'subscriber match' .ok-planner/design/concepts/cascade.md` returns at least 1.

### Task 29: Touch `concepts/wait-set.md`

**Files:** `.ok-planner/design/concepts/wait-set.md`

**Steps:**
1. Append to the Notes section:
   ```
   - 2026-05-23 — Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design: wait-set insertion is now gated by walk-time CEL filter evaluation. The pessimistic-invalidate rule (insert wait-set rows for every subscription edge regardless of filter compatibility) retires. Row shape and the drain-on-settled-state rule are unchanged.
   ```

**Verification:** `grep -c '2026-05-23' .ok-planner/design/concepts/wait-set.md` returns at least 1.

### Task 30: Touch `concepts/message.md`

**Files:** `.ok-planner/design/concepts/message.md`

**Steps:**
1. Append to the Notes section:
   ```
   - 2026-05-23 — Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design: under `concept:signal`'s field-naming convention, the message envelope's `payload` field is exposed to CEL subscription `when:` predicates as `payload.message_payload` (rather than `payload.payload`) to avoid colliding with the signal envelope's outer `payload` field. The substitution surface (`{{trigger.message.payload.X}}`) is NOT renamed — substitution does not have the envelope-collision problem since it goes through the explicit `trigger.message.` namespace prefix. This deliberate asymmetry keeps substitution backward-compatible and confines the rename to where it's structurally required.
   ```

**Verification:** `grep -c 'message_payload' .ok-planner/design/concepts/message.md` returns at least 1.

### Task 31: Add `@concept: signal` annotations at the load-bearing sites

**Files:** `runtime/runner_terminal.go`, `graph/node/subscription_edges.go`

**Steps:**
1. Above `cascadeSubscribersStaleInTx`, add the annotation comment:
   ```go
   // cascadeSubscribersStaleInTx marks subscriber nodes stale + frame_id
   // based on the emitted signal. Subscriber match (signal.TypePath
   // prefix + compiled CEL when: predicate) is the cascade-fire gate.
   //
   //	@concept: signal
   //	@concept: node-subscription
   ```
   (Two concepts only — `wait-set` belongs at the wait-set insert helper site, not here, per the cold-read no-carpet-bombing rule.)
2. Above `BuildSubscriptionEdges`, ensure the `@concept: signal` annotation is present alongside `@concept: node-subscription`.

**Verification:** `rg '@concept: signal' runtime/ graph/node/ | wc -l` returns at least 2.

### Task 32: Verify Pass 2 end-state

**Files:** none (verification-only task)

**Steps:**
1. `go build ./...` — exits 0.
2. `go test ./graph/node/... ./foundation/signal/... ./runtime/... ./test/scenarios/... -count=1` — passes.
3. `make lint` — exits 0.
4. `rg 'lastOutcome == cascade.LastOutcomeFreshChanged' runtime/` — returns nothing.
5. `rg '\bon: state\b' --include='*.yaml' --include='*.go'` — returns nothing.

**Verification:** All commands above pass.

---

## Pass 3: Policy 3-tuple decoupling + ErrorPolicy vocabulary tightening

**Goal:** Refactor `PolicyAction` / `ResolvedAction` to the new `Resolution{Signal, DispatchDisposition, Color, ...}` 3-tuple. Tighten the action vocabulary to four values (`pass | give_up | retry | discard_claims_then_retry`). Delete `resume_then_retry`; rename `discard_then_retry` to `discard_claims_then_retry`; add a `case "pass":` branch in `policy.go::step` (today it falls through to `default → give_up`). Delete the special-case `action: invalidate` rejection in the validator (the new generic range-check subsumes it). Mutate `concept:error-policy`.

**Scope:** Tasks 33–38
**End state:** working
**Verification:** `go build ./... && go test ./graph/node/... ./runtime/... -count=1 && make lint`

### Task 33: Define the `Resolution` type and `DispatchDisposition` / `Color` enums

**Files:** `foundation/spec/policy.go`

**Steps:**
1. Add to `foundation/spec/policy.go`:
   ```go
   type DispatchDisposition string
   const (
       DispositionEnd            DispatchDisposition = "end"
       DispositionRetry          DispatchDisposition = "retry"
       DispositionParkAsync      DispatchDisposition = "park_async"
       DispositionParkScheduled  DispatchDisposition = "park_scheduled"
   )

   type SettledColor string
   const (
       ColorFresh  SettledColor = "fresh"
       ColorFailed SettledColor = "failed"
       ColorParked SettledColor = "parked"
   )

   // Resolution is the unified output of one policy-resolution decision.
   // Signal: emitted to cascade walker + audit log.
   // DispatchDisposition: what becomes of this dispatch.
   // Color: the run-row's settled state (only meaningful when disposition ends or parks).
   //
   //	@concept: error-policy
   //	@concept: signal
   type Resolution struct {
       Signal              signal.Signal       // type + payload to emit
       DispatchDisposition DispatchDisposition // end | retry | park_async | park_scheduled
       Color               SettledColor        // fresh | failed | parked
       RetryDiscardClaims  bool                // only when DispatchDisposition == Retry
       RetryDelayMs        int                 // only when DispatchDisposition == Retry
       WakeAt              time.Time           // only when DispatchDisposition == ParkScheduled
   }
   ```
   Import `signal "github.com/rimsky-ai/rimsky-core/foundation/signal"`.

**Verification:** `go build ./foundation/spec/...` exits 0.

### Task 34: Refactor `PolicyAction` action vocabulary; delete `resume_then_retry`; rename

**Files:** `foundation/spec/policy.go`, `graph/node/policy.go`

**Steps:**
1. In `foundation/spec/policy.go`'s `PolicyAction` struct, update the docstring to enumerate the 4-value vocabulary: `"pass" | "give_up" | "retry" | "discard_claims_then_retry"`. Delete any mention of `discard_then_retry`, `resume_then_retry`, or `invalidate` (those are out).
2. In `graph/node/policy.go::step`, change the switch:
   - Delete the `case "resume_then_retry":` branch entirely (the spec retires it without a shim).
   - Rename `case "discard_then_retry":` to `case "discard_claims_then_retry":`. (The retry branch handles both; it's just the case name.)
   - Delete the `case "invalidate":` branch entirely.
   - Add a new `case "pass":` branch that returns:
     ```go
     return ResolvedAction{
         Kind:     "pass",
         NewState: EvaluatorState{
             ActionIndex: state.ActionIndex + 1,
             RetryCounter: 0,
             CurrentErrorClass: errorClass,
         },
     }
     ```
     (Pass settles the run as fresh; the chain advances so a subsequent same-class error doesn't pass again.)
   - The `default:` branch stays as `give_up("unknown_action_type")`.
3. In the existing retry-branch `case "retry", "discard_then_retry", "resume_then_retry":`, change to `case "retry", "discard_claims_then_retry":` only.

**Verification:** `go build ./...` exits 0 (the runtime in `runner_error_policy.go` may need a parallel update — handled in Task 35).

### Task 35: Refactor `applyErrorPolicy` and `applyResolvedAction` to produce/consume `Resolution`

**Files:** `runtime/runner_error_policy.go`

**Steps:**
1. Find `applyErrorPolicy` (current location per grounding). Where it today builds a `ResolvedAction` from the policy chain, additionally construct a `signal.Signal` based on the resolution kind:
   - `Kind: "retry"` or `"discard_claims_then_retry"` → signal type `transient/retry/<attempt>/<errorClass>`; payload `TransientRetryPayload{...}`.
   - `Kind: "give_up"` → signal type `terminal/error/<errorClass>`; payload `TerminalErrorPayload{...}`.
   - `Kind: "pass"` → signal type `terminal/error/<errorClass>`; payload `TerminalErrorPayload{...}` (same shape as give_up; the color differs at settle-time).
2. Build the full `Resolution` from the resolved action + signal:
   - retry/discard_claims_then_retry → `DispatchDisposition: Retry`, `RetryDiscardClaims: <true/false>`, `RetryDelayMs: <from action.delay>`.
   - give_up → `DispatchDisposition: End`, `Color: Failed`.
   - pass → `DispatchDisposition: End`, `Color: Fresh`.
3. Update `applyResolvedAction` (which currently maps `ResolvedAction.Kind` to state mutations) to instead consume a `Resolution`. Settle the run row's color from `resolution.Color`; map `DispatchDisposition` to the existing re-enqueue/end/park logic.
4. Remove the now-dead retry branch labels (the old code matched `"resume_then_retry"`).
5. Where signal-emit was wired in Pass 1 Task 8, replace the ad-hoc signal construction with the canonical one from the `Resolution`.

**Verification:** `go test ./runtime/... -run 'TestApplyErrorPolicy|TestApplyResolvedAction' -count=1` passes.

### Task 36: Delete the special-case `action: invalidate` validator rejection

**Files:** `graph/node/template_validator.go`

**Steps:**
1. Find `validateErrorTypes` (around line ~360). Delete the entire `if action.Action == "invalidate" { ... continue }` block. The new generic range-check at the same site (added below) catches it.
2. In the same function, add a generic range-check against the canonical ErrorPolicy vocabulary:
   ```go
   validActions := map[string]bool{
       "pass": true, "give_up": true, "retry": true, "discard_claims_then_retry": true,
   }
   if !validActions[action.Action] {
       res.Errors = append(res.Errors, ValidationError{
           Path: fmt.Sprintf("%s.error_types[%s].policy[%d].action", base, className, ai),
           Msg:  fmt.Sprintf("unknown action %q; valid actions are: pass | give_up | retry | discard_claims_then_retry", action.Action),
       })
       continue
   }
   ```
3. Run `rg '"discard_then_retry"|"resume_then_retry"|"invalidate"' graph/ runtime/ foundation/` and update any remaining references (test fixtures may use the old strings).

**Verification:** Unit test `TestValidateErrorTypes_RejectsUnknown` covers: `action: invalidate`, `action: resume_then_retry`, `action: foo` all reject with the new error message. `action: pass | give_up | retry | discard_claims_then_retry` all accept.

### Task 37: Mutate `concepts/error-policy.md`

**Files:** `.ok-planner/design/concepts/error-policy.md`

**Steps:**
1. Update "What it is" section: action vocabulary is now `pass | give_up | retry | discard_claims_then_retry` (4 values). `error_types:` is the unified surface for both executor `Error` and runtime acquisition failure (synthetic-class `acquire/*`).
2. Delete the "Three-name relationship" section's references to `discard_then_retry` / `resume_then_retry`.
3. Update the Boundaries section: owns the 4-value ErrorPolicy vocabulary, the per-class policy chain entry point (executor-Error + acquisition-failure), the retry-counter cap. Does NOT own: signal type-path taxonomy (`concept:signal`), cascade-fire (`concept:cascade`), terminal-resolution stitching (`concept:terminal-resolution`).
4. Update Invariants section: replace `discard_then_retry releases held claim handles before retry; resume_then_retry preserves them` with `discard_claims_then_retry releases held claim handles before retry; the regular retry preserves them by default`. Add: `acquire/<reason>` is a reserved class-name prefix for runtime acquisition failures; operators may declare `error_types:` keys under this prefix.
5. Update Aliases section to retire the `discard_then_retry` / `resume_then_retry` historical names.
6. Append to Notes:
   ```
   - 2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Vocabulary tightened to 4 values (resume_then_retry deleted; discard_then_retry renamed to discard_claims_then_retry; pass added as first-class action with explicit step-switch case). Acquisition failure folded into the error_types: surface via synthetic-class prefix acquire/* (the Pass 4 work). Policy resolution decoupled into (signal, dispatch_disposition, color) tuple; preset names map at registration. on_executor_errored.error{error_class} remap retires (concept:lifecycle-handler retires entirely in Pass 4).
   ```

**Verification:** `grep -c 'discard_claims_then_retry' .ok-planner/design/concepts/error-policy.md` returns at least 2.

### Task 38: Verify Pass 3 end-state

**Files:** none (verification-only task)

**Steps:**
1. `go build ./...` — exits 0.
2. `go test ./graph/node/... ./runtime/... -count=1` — passes.
3. `make lint` — exits 0.
4. `rg '"discard_then_retry"|"resume_then_retry"' graph/ runtime/ foundation/` — returns nothing.

**Verification:** All commands pass.

---

## Pass 4: Retire `concept:lifecycle-handler` + fold acquire-failure into `error_types:`

**Goal:** Delete the three lifecycle-handler slots (`on_executor_complete`, `on_executor_errored`, `on_acquire_unavailable`) from `TemplateNodeDef`; delete the corresponding handler struct types and validators; rewire `handleAcquireUnavailable` to emit a `terminal/error/acquire/unavailable` signal and route through `applyErrorPolicy` via synthetic class `acquire/unavailable`; delete the now-dead `applyTerminalPass` shortcut; delete the dead `ReasonPolicyInvalidate` enum value + its `NextState` branch. Retire `concepts/lifecycle-handler.md` to `_retired/`; mutate `concepts/error-policy.md` (acquire prefix), `concepts/terminal-resolution.md` (5→4 stage flow), `concepts/invalidate.md` (3→1 template-configurable emit sites).

**Scope:** Tasks 39–46 (incl. 41.5)
**End state:** working
**Verification:** `go build ./... && go test ./... -count=1 && make lint`

### Task 39: Delete lifecycle-handler fields from `TemplateNodeDef`

**Files:** `foundation/spec/template.go`

**Steps:**
1. Find the `TemplateNodeDef` struct (line ~150 per grounding). Delete these three fields:
   - `OnExecutorComplete *OnExecutorCompleteHandler`
   - `OnExecutorErrored *OnExecutorTerminalHandler`
   - `OnAcquireUnavailable *OnAcquireUnavailableHandler`
2. Delete the type definitions that those fields referenced: `OnExecutorCompleteHandler`, `OnExecutorTerminalHandler`, `OnAcquireUnavailableHandler` (around lines ~225-280 per grounding).
3. Delete any helper functions on those types (e.g., resolver methods, default-resolves enums like `ResolvePass | ResolveRetry | ResolveError`).

**Verification:** `go build ./foundation/spec/...` may fail (callers reference these types); Tasks 40-42 restore. After all of Pass 4 completes, `go build ./foundation/spec/...` exits 0.

### Task 40: Delete the lifecycle-handler validators

**Files:** `graph/node/template_validator.go`

**Steps:**
1. Delete `validateOnExecutorComplete` (line ~409 per grounding).
2. Delete `validateOnExecutorTerminal` (line ~436 per grounding).
3. Delete `validateOnAcquireUnavailable` (line ~371 per second-pass grounding).
4. Delete the three call sites in the per-node validator loop (lines ~181-183 per grounding).

**Verification:** `go build ./graph/node/...` exits 0 after lifecycle types are gone.

### Task 41: Rewire `handleAcquireUnavailable` through `applyErrorPolicy`

**Files:** `runtime/runner_lifecycle.go`, `runtime/runner_acquire.go`

**Steps:**
1. Find `handleAcquireUnavailable` at `runtime/runner_lifecycle.go:39`. Replace its body entirely:
   - Construct a synthetic `terminalEvent`-like context with `ErrorClass: "acquire/unavailable"`.
   - Construct the `Error` payload from the acquisition-failure context (which claim, which producer, what conflict).
   - **Tx handling.** `handleAcquireUnavailable` runs AFTER the caller's per-candidate acquisition tx rolled back (see `runner_acquire.go:281`). The rewired flow needs a fresh tx to invoke `applyErrorPolicy` (which mutates state). Open a new tx via `args.Persist.Transaction(ctx, func(ctx, tx) error { return applyErrorPolicy(...) })` inside the function — do not assume the caller passes a usable tx.
   - Call into `applyErrorPolicy(ctx, args, acq, ...)` with this synthetic class. The error-policy chain resolves per the operator's `error_types: { "acquire/unavailable": ... }`.
   - If no policy is declared for `acquire/unavailable`, `Evaluate` returns `give_up("unknown_error_class")` (the intentional behavior change per the spec — operator must opt in to retry).
2. Update the caller at `runtime/runner_acquire.go:280-281` if its signature changed (it likely won't, but verify).
3. Delete the resolved-action switch (`pass | retry | error`) that today lives in `handleAcquireUnavailable` (lines ~56+); all of that logic is now in `applyErrorPolicy`.
4. Add a comment block above the rewritten function:
   ```go
   // handleAcquireUnavailable routes pre-dispatch acquisition failure
   // through the operator's error_types: chain via synthetic class
   // "acquire/unavailable". No implicit retry; an operator that wants
   // retry declares it explicitly in error_types:. See the validator
   // warning (Pass 2 Task 23) that surfaces the absence.
   //
   //	@concept: error-policy
   ```

**Verification:** Add a scenario test `TestAcquireUnavailable_RoutesViaErrorTypes`: configure a template with `error_types: { "acquire/unavailable": { policy: [{action: retry, count: 2}, {action: give_up}] } }`, force the producer into a conflict, assert two retry signals followed by a terminal/error/acquire/unavailable signal.

### Task 41.5: Add validator warning for nodes with `stores:` but no `acquire/unavailable` policy

**Files:** `graph/node/template_validator.go`

**Steps:**
1. At the per-node validation pass (after `validateErrorTypes` runs), check if the node declares any `stores:` entries and its `error_types:` does NOT have an `acquire/unavailable` key (exact or via a wildcard pattern that would match).
2. If both conditions hold, append to `res.Warnings` an informational message: `"node uses claim-producers but declares no acquire/unavailable error_types entry; the default behavior is fail-fast, not implicit retry. See spec .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md §ErrorPolicy."`.
3. If `ValidationResult` doesn't have a `Warnings` slice yet, add one — sibling to `Errors`. Find via `rg 'type ValidationResult' graph/node/`.

**Verification:** Add a unit test `TestValidator_WarnsOnMissingAcquireUnavailablePolicy` covering: node with `stores:` and no `acquire/unavailable` → warning emitted; node with `stores:` and `error_types: { "acquire/unavailable": {...} }` → no warning; node without `stores:` → no warning regardless of error_types.

### Task 42: Rewrite `applyTerminalError` to delegate purely to `applyErrorPolicy`; delete `applyTerminalPass`

**Files:** `runtime/runner_terminal_handlers.go`

**Steps:**
1. Find `applyTerminalError` at `runner_terminal_handlers.go:~40` (per the reviewer's grounding). Today it reads `acq.NodeDef.OnExecutorErrored` to branch on the handler's resolve (`pass | error{error_class} | fall-through`). With `OnExecutorErrored` deleted from `TemplateNodeDef` (Task 39), this handler-reading logic is dead.
2. Rewrite `applyTerminalError` to a thin shim that calls `applyErrorPolicy(ctx, args, acq, t.ErrorClass, t.Payload, tx)` directly — no handler lookup, no `pass`/`error` branch, no `applyTerminalPass` call. The full body should be just the `applyErrorPolicy` call plus error wrapping.
3. Delete `applyTerminalPass` at line ~77. With `applyTerminalError`'s `pass` branch gone (step 2), there are no remaining callers; `pass` is now purely an ErrorPolicy chain action handled by `applyErrorPolicy` + `applyResolvedAction` (which Pass 3 wired).
4. Find any other `applyTerminalPass` call site or comment reference via `rg 'applyTerminalPass' runtime/` and remove. There's a known comment block at `runtime/runner_terminal.go:~326-328` listing terminal-handler call sites that references `applyTerminalPass`; update that comment too.

**Verification:** `rg 'applyTerminalPass' runtime/` returns nothing; `rg 'OnExecutorErrored' runtime/` returns nothing; `go build ./... && go test ./runtime/... -count=1` exits 0.

### Task 43: Delete `ReasonPolicyInvalidate` and its `NextState` branch

**Files:** `foundation/cascade/state.go`

**Steps:**
1. Find `var ReasonPolicyInvalidate = TransitionReason{Kind: "policy_invalidate"}` at line ~75 per grounding. Delete the var declaration.
2. Find the corresponding branch in `NextState`'s switch (around line ~236 per grounding) that handles `reason.Kind == "policy_invalidate"`. Delete the branch.
3. Run `rg 'ReasonPolicyInvalidate|policy_invalidate' --include='*.go'` and clean up any remaining references (likely none post-Pass-3 since the action retires).

**Verification:** `rg 'ReasonPolicyInvalidate' --include='*.go'` returns nothing. `go test ./foundation/cascade/... -count=1` passes.

### Task 44: Retire `concepts/lifecycle-handler.md`

**Files:** `.ok-planner/design/concepts/lifecycle-handler.md` → `.ok-planner/design/concepts/_retired/lifecycle-handler.md`

**Steps:**
1. `git mv .ok-planner/design/concepts/lifecycle-handler.md .ok-planner/design/concepts/_retired/lifecycle-handler.md` (`_retired/` already exists in the repo — no `mkdir` needed; the existing dir contains earlier retired concepts like `node-state.md`, `on-event-handler.md`).
2. Edit the moved file: at the top of the file, after the front-matter, insert:
   ```
   > **Retired 2026-05-23** per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`.
   >
   > The three slots (`on_executor_complete`, `on_executor_errored`, `on_acquire_unavailable`) collapsed into the unified `concept:error-policy` (acquisition failure as synthetic-class `acquire/*`) and direct signal emission (executor `Success` / `Park` / `AwaitAsyncCallback` emit fixed signals with no operator-policy chain). The `by_changed | always_propagate | never_propagate` resolves retired (cascade-fire is now subscriber-driven; the sender cannot suppress downstream firing). The `pass` resolve at lifecycle-handler level retired (redundant with `error_types: { <class>: { policy: [{ action: pass }] } }`).
   >
   > Concrete deletions:
   > - Fields on `TemplateNodeDef`: `OnExecutorComplete`, `OnExecutorErrored`, `OnAcquireUnavailable`.
   > - Types: `OnExecutorCompleteHandler`, `OnExecutorTerminalHandler`, `OnAcquireUnavailableHandler`.
   > - Validators: `validateOnExecutorComplete`, `validateOnExecutorTerminal`, `validateOnAcquireUnavailable`.
   > - Runtime: `applyTerminalPass` shortcut; `handleAcquireUnavailable` switch on `h.Resolve` (rewired through `applyErrorPolicy`).
   ```

**Verification:** `ls .ok-planner/design/concepts/_retired/lifecycle-handler.md` succeeds; `ls .ok-planner/design/concepts/lifecycle-handler.md` fails.

### Task 45: Mutate `concepts/terminal-resolution.md` and `concepts/invalidate.md`

**Files:** `.ok-planner/design/concepts/terminal-resolution.md`, `.ok-planner/design/concepts/invalidate.md`

**Steps:**
1. In `terminal-resolution.md`:
   - Reshape the "What it is" section's five-stage flow to four stages: (1) wire→internal terminal kind, (2) dispatch on terminal kind, (3) resolution (produces the `Resolution{signal, dispatch_disposition, color, ...}` tuple — runs the operator's `error_types:` chain when the terminal kind is `Errored` or when the synthetic `acquire/*` class is in play; for `Success`/`Park`/`AwaitAsyncCallback`/`Infra` the resolution is fixed by the terminal kind), (4) claim-handle resolution.
   - Add: "the same four-stage spine handles executor Error and runtime acquisition failure uniformly (acquisition-failure routes through the error_types chain via synthetic-class `acquire/*`)."
   - Update the kind→verb table to drop the lifecycle-handler column; reduce to: terminal kind → emitted signal → producer verb on each acquired claim. Add a row for `AwaitAsyncCallback` showing it emits `transient/await_async` and is not a settling terminal (no producer verb on first pass; the callback's eventual terminal drives verb emission).
   - Correct the inherited drift in the "Vocabulary note" subsection that lists outcomes as `Success | Error | Snooze | AwaitAsyncCallback`; the correct wire shape is `Success | Error | Park | AwaitAsyncCallback`.
   - Append to Notes: `2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Resolution shape becomes (signal, dispatch_disposition, color). Acquisition failure folds into the same spine via synthetic acquire/* error class. concept:lifecycle-handler retires; on_executor_complete and on_executor_errored slots delete. Five-stage flow collapses to four (lifecycle-handler stage absorbed into resolution).`
2. In `invalidate.md`:
   - Find the "three emit sites" enumeration (operator API, error-types policy invalidate, lifecycle-handler `invalidate:` slot). Collapse to one **template-configurable** emit site: operator API. Add a note: "Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged and remain documented."
   - Append to Notes: `2026-05-23 — Template-configurable emit-site enumeration collapses from three to one (operator-API). Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged. The error_types policy invalidate site and the lifecycle-handler invalidate slot stop existing under spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design (the action was retired 2026-05-14; concept:lifecycle-handler retires entirely).`

**Verification:** `grep -c 'four-stage' .ok-planner/design/concepts/terminal-resolution.md` returns at least 1. `grep -c '2026-05-23' .ok-planner/design/concepts/invalidate.md` returns at least 1.

### Task 46: Verify Pass 4 end-state and update `concepts.md` TOC

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Open `concepts.md`. Find the `lifecycle-handler` entry and move it to the "Retired concepts" section at the bottom with a one-line retirement note.
2. Update the one-liner for `error-policy` to reflect the 4-value vocabulary and the unified surface.
3. Update the one-liner for `terminal-resolution` to reflect the four-stage flow.
4. Update the one-liner for `invalidate` to mention the emit-site collapse.
5. Run `go build ./... && go test ./... -count=1 && make lint` and confirm clean.

**Verification:** `grep -A1 'Retired concepts' .ok-planner/design/concepts.md | grep -c 'lifecycle-handler'` returns at least 1.

---

## Pass 5: Retire `last_outcome` + partial-retire `transition-reason` + `isTerminal` → `isSettled` rename

**Goal:** Drop the `last_outcome` column from `rimsky_node_runs` (migration on both Postgres and SQLite). Update reader sites (`control/cli/*.go`, `control/controlapi/*.go`, `runtime/runner_terminal.go` lineage-emit site, lineage writer/projection). Replace the lineage projection of `last_outcome` with a `settling_signal_type` field. Narrow `TransitionReason`'s role: the enum stays (load-bearing for state-machine validation in `NextState`); the audit-write role retires for signal-bearing transitions (which write signal type-paths instead). Rename `isTerminal`/`IsTerminal` → `isSettled`/`IsSettled` throughout. Retire `concepts/last-outcome.md` to `_retired/`. Reshape `concepts/transition-reason.md` in place (not retired). Mutate `concepts/parked-state.md` (retract invariant + add 3 park-flavored signals note). Touch `concepts/lineage-record.md`, `concepts/lineage.md`, `concepts/event-log.md`.

**Scope:** Tasks 46.5–58 (the three 46.x tasks add the `settling_signal_type` column + drop `lastOutcome` from interfaces + rewrite run-tree aggregation and substitution-visibility gate; these must run before the old-column drop in Task 47)
**End state:** working
**Verification:** `go build ./... && go test ./... -count=1 && make lint`

### Task 46.5: Add `settling_signal_type` column to `rimsky_node_runs`

**Files:** `foundation/persistence/postgres/migrations/013-node-runs-settling-signal-type.sql` (new), `foundation/persistence/sqlite/migrations/013-node-runs-settling-signal-type.sql` (new), `foundation/persistence/nodes.go`, `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`

**Steps:**
1. Run `ls foundation/persistence/postgres/migrations/ foundation/persistence/sqlite/migrations/` to confirm the next available number. As of plan-writing, both directories' latest is `012-node-runs-prior-dispatch.sql`, so `013` is next. If other migrations have landed since, bump consistently in both directories. Subsequent Pass 5 migrations (Task 47 below) will use `014`.
2. Postgres migration:
   ```sql
   -- 013-node-runs-settling-signal-type.sql
   -- Add settling_signal_type column to rimsky_node_runs.
   -- Carries the canonical signal type-path of the run's settling resolution
   -- (terminal/success, terminal/error/<class>, terminal/park/<reason>, terminal/infra/<reason>).
   -- Strictly more expressive than the retired last_outcome column.
   ALTER TABLE rimsky_node_runs ADD COLUMN settling_signal_type TEXT;
   ```
3. SQLite migration: same content.
4. Add `SettlingSignalType *string` to the `NodeRunRow` struct in `foundation/persistence/nodes.go` (nil-pointer for not-yet-settled rows).

**Verification:** `go test ./foundation/persistence/... -count=1` passes.

### Task 46.6: Drop `lastOutcome` parameter from persistence interface signatures; add `settling_signal_type` writes

**Files:** `foundation/persistence/nodes.go`, `foundation/persistence/run_tree.go`, all callers (find via `rg 'UpdateState\(|UpdateStateAndOutcome\(' --include='*.go'`), `foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`

**Steps:**
1. In `foundation/persistence/nodes.go:~134`, find `NodeTable.UpdateState(... lastOutcome cascade.LastOutcome ...)`. Drop the `lastOutcome` parameter from the interface signature; replace with `settlingSignalType *string` (nil for non-settling transitions like `stale → running`; non-nil for settling transitions carrying the signal type-path). **Do not rename** the method — keep `UpdateState`.
2. In `foundation/persistence/run_tree.go:~139`, find `RunTreeTable.UpdateStateAndOutcome(... lastOutcome cascade.LastOutcome)`. Same parameter replacement. **Keep the method name as `UpdateStateAndOutcome`** for this revision — renaming a public interface method on a 5+-importer interface alongside the param-drop is two breaking changes at once. A follow-up cleanup can rename to `UpdateState` once this work has settled.
3. Update both interface implementations (`foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`, plus matching `run_tree.go` implementations) to drop the `last_outcome = $N` SQL bind and replace with `settling_signal_type = $N` (writing the new column added in Task 46.5).
4. Update all callers via `rg 'UpdateState\(|UpdateStateAndOutcome\(' --include='*.go'`. Each caller passes the new parameter — for callers in the resolution producers (Pass 1 wired their signal-emit), the signal type-path is already in scope; pass it through.

**Verification:** `go build ./... && go test ./foundation/persistence/... ./runtime/... -count=1` passes.

### Task 46.7: Rewrite run-tree aggregation and substitution-visibility gate to use signal type-paths

**Files:** `runtime/run_tree.go`, `runtime/substitution_context.go`

**Steps:**
1. **Run-tree aggregation in `runtime/run_tree.go`** currently uses `ChildState.LastOutcome` (~line 57) and `aggregateSuccessOutcome` (~line 80) to compute parent state from children. The existing aggregation algorithm (the `strict | threshold | best_effort | first` policy switch per `concept:fan-out`) is NOT changing — only how the result maps to the parent's emitted signal. Under the new model:
   - The aggregator continues to compute a parent outcome from children using the existing policy switch.
   - At parent-settle time, map the computed outcome to a signal type-path using the same rules as elsewhere in the codebase: success outcome → `terminal/success` (with aggregated `payload.changed`); failed outcome → `terminal/error/aggregate/<policy_name>_failed` (e.g., `terminal/error/aggregate/strict_failed`); parked outcome → carry the canonical park signal of the children's park kind. The `aggregate/*` error-class prefix joins the canonical taxonomy; subscribers wanting to filter on "any aggregation failure" use `type: terminal/error/aggregate/*`.
2. Refactor `ChildState` to carry the child's settling signal type-path instead of `LastOutcome`: rename field to `SettlingSignalType signal.TypePath`.
3. Refactor `aggregateSuccessOutcome` (and any sibling aggregators) to consume the new field. The aggregation logic that currently distinguishes `fresh_changed` vs `fresh_unchanged` now checks `signal.Type == "terminal/success"` plus the signal payload's `changed` field (which the aggregator now needs in addition to type — extend the aggregation input shape if necessary).
4. Update `AggregateResult.ParentOutcome` (~line 144) similarly: rename to `ParentSettlingSignalType signal.TypePath`.
5. **Substitution visibility gate in `runtime/substitution_context.go`** currently uses a `settledSuccessOutcomes` set (`{fresh_changed, fresh_unchanged, passed}` per grounding at lines 33-111) keyed on `senderRun.LastOutcome` to decide whether the sender's attributes are visible to downstream substitution. Under the new model, replace with a check on the sender run's settled state (`state == fresh`) plus the settling-signal type-path (one of `terminal/success`, or the `pass`-resolved `terminal/error/*` with color = fresh — match by reading the run's `settling_signal_type` column added in Task 46.5). The set retires; a small helper `isSettledForSubstitution(run NodeRunRow) bool` replaces it.

**Verification:** `go test ./runtime/... -run 'TestRunTree|TestAggregate|TestSubstitution' -count=1` passes.

### Task 47: Postgres + SQLite migration to drop `last_outcome` column

**Files:** `foundation/persistence/postgres/migrations/014-drop-last-outcome.sql` (new), `foundation/persistence/sqlite/migrations/014-drop-last-outcome.sql` (new)

**Steps:**
1. Task 46.5 took `013-node-runs-settling-signal-type.sql`. This task takes `014`. Run `ls foundation/persistence/postgres/migrations/ foundation/persistence/sqlite/migrations/` and confirm `013` is now occupied by Task 46.5 and `014` is the next available; if other migrations landed between plan-writing and execution, bump consistently in both directories.
2. Create the Postgres migration:
   ```sql
   -- 014-drop-last-outcome.sql
   -- Drop the last_outcome column from rimsky_node_runs.
   -- Per spec .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md Phase 5.
   -- Cascade-fire is now subscriber-driven (signal-type-path match), not gated on this column.
   -- The settling_signal_type column (added in migration 013) replaces last_outcome's information role.
   -- Reader sites have been updated in the same plan pass.
   ALTER TABLE rimsky_node_runs DROP COLUMN IF EXISTS last_outcome;
   ```
3. Create the SQLite migration with the same content. `modernc.org/sqlite v1.50+` (per `foundation/go.mod`) bundles SQLite well past 3.35 which supports `DROP COLUMN` natively; if the migration runner errors at apply time on the SQLite side, fall back to the SQLite-canonical `CREATE TABLE new_table AS SELECT ... ; DROP TABLE old ; ALTER TABLE rename` pattern (used by existing migration `004-last-outcome-and-progress.sql` or similar — find the pattern via `rg 'CREATE TABLE.*SELECT' foundation/persistence/sqlite/migrations/`).

**Verification:** Run `go test ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... -count=1` (requires Docker for Postgres testcontainers). Both pass.

### Task 48: Update `last_outcome` reader sites in `control/cli/`

**Files:** `control/cli/client.go`, `control/cli/backfill.go`

**Steps:**
1. In `client.go`, find the `last_outcome` reference (line per grounding). Replace with the run row's settling signal type-path. The Go struct that today carries `LastOutcome string` should be updated to carry `SettlingSignalType string` (matching the lineage projection rename in Task 51). If the field is read from a JSON response, update the JSON tag.
2. In `backfill.go`, same treatment.
3. If the CLI's output formatting prints `last_outcome`, update to print `settling_signal_type` instead.

**Verification:** `go build ./control/cli/...` exits 0. Existing CLI tests pass.

### Task 49: Update `last_outcome` reader sites in `control/controlapi/`

**Files:** `control/controlapi/backfills.go`, `control/controlapi/nodes.go`, `control/controlapi/lineage_test.go`

**Steps:**
1. Same pattern as Task 48 for each file. Find the `last_outcome` reference; replace with `settling_signal_type` field; update the JSON response shape if applicable.
2. In `nodes.go::OperatorReset` (the operator-reset path that reads `last_outcome` per grounding), drop the read entirely if it was used only for an audit-log payload; the reset's audit event already carries enough information without the old field.

**Verification:** `go build ./control/controlapi/... && go test ./control/controlapi/... -count=1` passes.

### Task 50: Update `runtime/runner_terminal.go` lineage emit site

**Files:** `runtime/runner_terminal.go`

**Steps:**
1. Find the lineage-emit site at lines ~377-389 per grounding. Today it writes a `lineage_record` row with `last_outcome` populated. Change to populate `settling_signal_type` with the run's final signal type-path (the signal emitted by the resolution producer).
2. The signal type-path is already available in the calling context (Pass 1 wired it into the resolution); pass it through.

**Verification:** `go test ./runtime/... -run 'TestLineage' -count=1` passes.

### Task 51: Update lineage record's JSON shape with `settling_signal_type`

**Files:** `runtime/lineage_writer.go`, `foundation/persistence/postgres/lineage.go` (or wherever the lineage row struct lives — find via `rg 'rimsky_lineage' foundation/persistence/`)

**Steps:**
1. `rimsky_lineage`'s columns are `id, record_kind, instance_id, frame_id, observed_at, record JSONB, outcome` — `last_outcome` lives **inside** the JSONB `record` column, not as a top-level column. **No schema migration is needed**; this task changes only the JSON-payload field name.
2. Find the Go struct that serializes into `record` at `runtime/lineage_writer.go:~114` (the `LastOutcome string \`json:"last_outcome"\`` field per grounding). Rename the Go field to `SettlingSignalType` and update the JSON tag to `json:"settling_signal_type"`.
3. Update the writer (called from Task 50's emit site) to populate `SettlingSignalType` with the run's settling signal type-path instead of the `LastOutcome` enum string.
4. Find any reader that unmarshals `record` JSON expecting the old field (via `rg '"last_outcome"' --include='*.go'`) and update.

**Verification:** `go test ./runtime/... -run 'TestLineage' -count=1 && go test ./foundation/persistence/... -count=1` passes.

### Task 52: Retire the additive audit writes left in place by Pass 1

**Files:** `runtime/runner_terminal.go`, `runtime/runner_terminal_handlers.go`, `runtime/runner_terminal_park.go`, `runtime/runner_error_policy.go`, `runtime/runner_dispatch.go`, `runtime/runner_named_events.go`, plus any other site marked with `// TODO(signal-taxonomy Pass 5)` from Pass 1 Tasks 7-12.

**Steps:**
1. **Context:** The codebase does NOT write `rimsky_events` rows with `kind = TransitionReason.Kind`. Rather, today's audit writes use a small set of fixed strings: `"attributes_committed"`, `"no_op_commit"`, `"work_completed"`, `"work_started"`, `"state_transition"`, `"park_requested"`, etc. (find via `rg 'Kind:.*"\w' runtime/`). Pass 1 wired NEW `EmitSignal` calls alongside these existing fixed-string writes; Pass 5 retires the fixed-string writes for signal-bearing transitions, leaving the signal-typed row as the sole audit entry.
2. For each `// TODO(signal-taxonomy Pass 5)` comment added in Pass 1 Tasks 7-12, identify the original fixed-string audit write that the comment marked for retirement. Delete that original write. Remove the TODO comment.
3. **Do not** retire fixed-string writes for non-signal transitions (e.g., `dispatch_claimed`, `pure_cascade`, `infra_reenqueue`, `handler_resume`, `park_timeout`). Those remain as free-form audit kinds per the spec's `tension:events-kind-no-enum` partial-coverage note.
4. `TransitionReason` enum and `NextState` validation stay intact. **Do not delete** the type or the per-state validation switch in `NextState`.

**Verification:** `rg '// TODO\(signal-taxonomy Pass 5' runtime/` returns nothing. `go test ./runtime/... ./test/scenarios/... -count=1` passes (test fixtures asserting on the retired fixed-string kinds may need updating to assert on the signal type-paths — update as needed).

### Task 53: Rename `isTerminal` / `IsTerminal` → `isSettled` / `IsSettled`

**Files:** `runtime/state_propagation.go`, `runtime/run_tree.go`, and any other files using these identifiers (find via `rg '\b[iI]sTerminal\b' --include='*.go'`)

**Steps:**
1. In `runtime/state_propagation.go`:
   - Line 255 (`func isTerminal(state cascade.NodeState) bool`) → rename to `isSettled`.
   - Line 187 and 197 (call sites) → update to `isSettled`.
   - Line 152 (`if !result.IsTerminal`) → update to `!result.IsSettled` after Task 53.2 below.
2. In `runtime/run_tree.go`:
   - Line 85 (`func (c ChildState) IsTerminal() bool`) → rename method to `IsSettled`.
   - Line 137 (`IsTerminal bool` field on `AggregateResult`) → rename to `IsSettled`.
   - Update docstrings throughout the file to use "settled" instead of "terminal" for state-machine landings (preserving "terminal" usage in the wire-protocol sense per `concept:terminal-resolution`).
3. Find and update all callers via `rg '\b[iI]sTerminal\b' --include='*.go'`.

**Verification:** `rg '\b[iI]sTerminal\b' --include='*.go'` returns zero matches against the renamed sites. `go build ./... && go test ./runtime/... -count=1` passes.

### Task 54: Retire `concepts/last-outcome.md`

**Files:** `.ok-planner/design/concepts/last-outcome.md` → `.ok-planner/design/concepts/_retired/last-outcome.md`

**Steps:**
1. `git mv .ok-planner/design/concepts/last-outcome.md .ok-planner/design/concepts/_retired/last-outcome.md`.
2. Edit the moved file: at the top, after the front-matter, insert:
   ```
   > **Retired 2026-05-23** per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`.
   >
   > The column retired alongside the cascade-fire-gate semantic (cascade-fire is now subscriber-driven via `concept:signal`). Signal-payload fields (`changed` on `terminal/success`, `discarded_claims` on `transient/retry`) carry the granularity that mattered. The lineage projection's `last_outcome` field was replaced with `settling_signal_type`.
   ```

**Verification:** `ls .ok-planner/design/concepts/_retired/last-outcome.md` succeeds; `ls .ok-planner/design/concepts/last-outcome.md` fails.

### Task 55: Reshape `concepts/transition-reason.md` in place (partial retirement)

**Files:** `.ok-planner/design/concepts/transition-reason.md`

**Steps:**
1. Update the "Purpose" section: narrow from "answer 'why did the state machine move?' for audit consumers" to "validate legal state-machine transitions in `NextState` and provide a kind string for non-signal audit rows."
2. Update Boundaries: still owns the closed enum + `NextState` per-state switch + the audit-event-log payload field carrying the reason FOR NON-SIGNAL TRANSITIONS. Does NOT own the audit kind for signal-bearing transitions (those use signal type-paths from `concept:signal`).
3. Update Invariants: existing invariants stay valid (reason is written at every state transition; absence from the audit row is a defect — but only for non-signal transitions).
4. Append to Notes:
   ```
   - 2026-05-23 — Scope narrowed per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. The enum stays for state-machine validation in NextState; the audit-write role retires for signal-bearing transitions (which use signal type-paths in rimsky_events.kind). Non-signal transitions (dispatch_claimed, pure_cascade, infra_reenqueue, handler_resume, park_timeout, etc.) continue to write TransitionReason.Kind as the audit kind — part of the un-taxonomized audit-kind set left open by tension:events-kind-no-enum.
   ```

**Verification:** `grep -c 'Scope narrowed' .ok-planner/design/concepts/transition-reason.md` returns at least 1.

### Task 56: Mutate `concepts/parked-state.md`

**Files:** `.ok-planner/design/concepts/parked-state.md`

**Steps:**
1. Find the resume-context section. Update: "park terminals emit signals `terminal/park/snooze` and `terminal/park/await_callback` (the two `ParkReason` enum values). The freeform `parked_reason_label` is a payload field on both signals (no longer a separate column-form distinction). `AwaitAsyncCallback` is NOT a park (the node stays `running` during the callback wait); it emits `transient/await_async` and is covered under `concept:signal`'s transient subtree."
2. Find the invariant `Cascade does not propagate from parked` at line ~50. Delete it. Insert a retraction note in the same location:
   > **Retracted 2026-05-23** — under the subscriber-driven cascade-fire model introduced by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design, propagation is determined by subscriber matches against the emitted signal, not by sender color. Parked nodes emit `terminal/park/*` signals; subscribers decide whether to react. The matching retraction lives on `concepts/cascade.md`.
3. Append to Notes:
   ```
   - 2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Park terminals emit signals (terminal/park/snooze, terminal/park/await_callback — one per ParkReason value). parked_reason_label moves to signal payload. AwaitAsyncCallback is not a park (node stays running) — it emits transient/await_async; see concept:signal. The "Cascade does not propagate from parked" invariant retracts (matching retraction on concepts/cascade.md).
   ```

**Verification:** `grep -c '2026-05-23' .ok-planner/design/concepts/parked-state.md` returns at least 1.

### Task 57: Touch `concepts/lineage-record.md`, `concepts/lineage.md`, `concepts/event-log.md`, `concepts/executor.md`

**Files:** Four concept docs

**Steps:**
1. `lineage-record.md`: append to Notes:
   ```
   - 2026-05-23 — Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design: lineage rows replace the `last_outcome` projection with a `settling_signal_type` field carrying the canonical signal type-path of the settling resolution (`terminal/success`, `terminal/error/<class>`, `terminal/park/<reason>`, `terminal/infra/<reason>`). The new field is strictly more expressive than `last_outcome`.
   ```
2. `lineage.md`: append the same Notes entry (slightly reworded for the projection level).
3. `event-log.md`: append:
   ```
   - 2026-05-23 — Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design: the node-run-transition subset of `rimsky_events.kind` now carries canonical signal type-paths (e.g., `terminal/error/http/timeout`) rather than free-form strings; for those rows `payload` carries the signal payload per its type's schema. Other audit kinds (state_transition, lock_*, work_*, auth.*, etc.) continue to use free-form text — see tension:events-kind-no-enum partial-coverage note.
   ```
4. `executor.md`: correct the "Snooze" drift in the inherited doc to "Park" with the inner `ParkReason ∈ {AWAIT_CALLBACK, SNOOZE}`. After correction, append:
   ```
   - 2026-05-23 — Per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design: executor terminal vocabulary is the 4-variant `StreamClose.outcome` (`Success | Error | Park | AwaitAsyncCallback`); operator-decided retry is via the operator's `error_types:` chain on `Error`, not an executor wire surface. Executors handle internal retry silently or via `Park{reason: SNOOZE}`.
   ```

**Verification:** `grep -c '2026-05-23' .ok-planner/design/concepts/{lineage-record,lineage,event-log,executor}.md | grep -v ':0'` confirms all four were updated.

### Task 58: Verify Pass 5 end-state and update `concepts.md` TOC

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Open `concepts.md`. Move `last-outcome` to the "Retired concepts" section. Keep `transition-reason` in the active section but update its one-liner to reflect the narrowed scope ("state-machine validation enum for `NextState`; audit-write role retired for signal-bearing transitions").
2. Update `cascade`, `parked-state`, `lineage-record`, `lineage`, `event-log`, `executor` one-liners to reflect changes.
3. Run `go build ./... && go test ./... -count=1 && make lint && go test ./test/scenarios/... ./foundation/persistence/... -count=1`.

**Verification:** All commands pass. Run `rg '\blast_outcome\b' --include='*.go'` and confirm zero hits (the column and field are gone).

---

## Pass 6: Bundled-executor error vocabularies + `declared_error_classes` proto extension

**Goal:** Add `declared_error_classes = 8` to `proto:executor_observability.proto::ObservabilityCapabilities`. Regenerate protos via `make proto-gen`. Update bundled executors (http-node, claude-agent, postgres-stores, stub-executor) to emit hierarchical `/`-containing error classes per the spec's "Bundled-executor error vocabularies" section. Wire `ExecutorDeclaredErrorClasses` to populate from the new proto field. Update `tension:events-kind-no-enum` with a Notes entry recording partial coverage. Touch `concepts/event-log.md` (done in Pass 5; touch again here only if Pass 6 reveals additional needs).

**Scope:** Tasks 59–69
**End state:** working
**Verification:** `go build ./... && go test ./... -count=1 && make lint && (cd executors/claude-agent && npm test && npm run build)`

### Task 59: Add `declared_error_classes` to `executor_observability.proto`

**Files:** `protocols/proto/v1/executor_observability.proto`

**Steps:**
1. Open the proto file. Find `message ObservabilityCapabilities`. After the `declared_events = 7;` field, add:
   ```proto
   // declared_error_classes is the set of error-class paths this
   // executor may emit on Error.error_class. Patterns ending in `*`
   // indicate prefix-pattern leaves (e.g., `http/server_error/*`);
   // exact strings indicate fixed leaves. The validator's range-check
   // of operator `error_types:` keys accepts a key if it exactly
   // matches a declared plain leaf OR matches a declared `<prefix>/*`
   // pattern by prefix. Empty/absent means "executor does not declare;
   // skip validator range-check for this executor".
   //
   // Per concept:signal hierarchical error_class rule.
   repeated string declared_error_classes = 8;
   ```

**Verification:** `make proto-gen` exits 0 (regenerates `protocols/proto/v1/gen/*`).

### Task 60: Regenerate protos and update Go consumers

**Files:** `protocols/proto/v1/gen/*` (auto-generated)

**Steps:**
1. Run `make proto-gen` from the repo root.
2. Confirm `git diff protocols/proto/v1/gen/` shows the new `DeclaredErrorClasses` field on the generated `ObservabilityCapabilities` Go struct.
3. Confirm `go build ./...` exits 0.

**Verification:** `rg 'DeclaredErrorClasses' protocols/proto/v1/gen/` returns at least one hit.

### Task 61: Wire `declared_error_classes` through the control-layer `ExecutorCapabilities` closure

**Files:** `control/observability/discovery.go`, `control/observability/handshake.go`, `control/config/controlapi.go`, `control/controlapi/app.go`, `control/controlapi/templates.go`

**Steps:**
1. In `control/observability/discovery.go`, find the `ObservabilityCapabilities` struct and add a `DeclaredErrorClasses []string` field alongside `DeclaredEvents`.
2. In `control/observability/handshake.go::executorCapsFromProto` (~lines 67-87), copy the new proto field into the struct: `DeclaredErrorClasses: pb.GetDeclaredErrorClasses()`.
3. **Extend the existing single closure (don't add a parallel one).** Today's pattern at `control/config/controlapi.go:~296` and `control/controlapi/app.go:~74` is one `ExecutorCapabilities func(executorName string) (declaredEvents []string, expectedAttributesSchema []byte, ok bool)` closure that returns multiple capability fields in one call. Extend its signature to return a third capability:
   ```go
   ExecutorCapabilities func(executorName string) (declaredEvents []string, declaredErrorClasses []string, expectedAttributesSchema []byte, ok bool)
   ```
   (Or use a small `ExecutorCapsResult` struct return shape if the parameter count gets unwieldy; verify the existing convention before choosing.)
4. Update the closure's implementation in `control/config/controlapi.go` to populate the new return value from `Discovery.GetExecutor().DeclaredErrorClasses`.
5. In `control/controlapi/templates.go:~121-127`, derive both `hooks.ExecutorDeclaredEvents` AND `hooks.ExecutorDeclaredErrorClasses` from the single closure (mirroring how the existing code derives `ExecutorDeclaredEvents` + `ExecutorExpectedAttributesSchema` from the same call).
6. Update every call site of `ExecutorCapabilities(...)` (find via `rg 'ExecutorCapabilities\(' control/`) to handle the new return value.

**Verification:** `go build ./...` exits 0; `go test ./control/observability/... ./control/controlapi/... -count=1` passes.

### Task 62: Update http-node executor's error-class vocabulary

**Files:** `executors/http-node/server.go`, `executors/http-node/bridge.go`, `executors/http-node/observability.go` (find via `rg 'ObservabilityCapabilities' executors/http-node/`)

**Steps:**
1. In `server.go`: rename emitted error classes per the spec's "http-node" subsection. Each `sendErrored(send, "<old_class>", ...)` call at lines 158, 187, 192, 210, 220, 224, 235, 339, 345 needs a new class. Translation:
   - `invalid_attribute` (lines 158, 187, 192, 339, 345) → `http/attribute_invalid`
   - `http_request_failed` (lines 210, 220) → `http/network_error`
   - `http_unexpected_status` (line 224) → branch: if status is 5xx → `http/server_error/<status>`; else if body has a parseable `error_class` field → `http/request_invalid/<body_class>`; else → `http/expectation_mismatch`.
   - `http_response_parse_failed` (line 235) → `http/response_unparseable`.
   - (No existing site for `http/timeout` — the timeout handling currently bubbles up as `http_request_failed`; split it: if the underlying error is a `context.DeadlineExceeded` or `net.Error.Timeout()`, emit `http/timeout` instead of `http/network_error`.)
2. In `bridge.go:65`: rename `internal_server_error` to `http/internal_error`.
3. In the http-node observability handshake (`observability.go` or equivalent), populate `DeclaredErrorClasses` with: `["http/network_error", "http/timeout", "http/request_invalid/*", "http/server_error/*", "http/expectation_mismatch", "http/response_unparseable", "http/attribute_invalid", "http/internal_error"]`.
4. Update existing http-node tests (`server_test.go`, `bridge_test.go`) that assert on the old class strings.

**Verification:** `go test ./executors/http-node/... -count=1` passes.

### Task 63: Update claude-agent executor's error-class vocabulary

**Files:** `executors/claude-agent/src/server.ts`, `executors/claude-agent/src/agent-run.ts`, `executors/claude-agent/src/http-bridge.ts`, `executors/claude-agent/src/internal-mcp-tools.ts`, `executors/claude-agent/src/internal-mcp-server.ts`

**Steps:**
1. In `server.ts:515` (the `outcome.kind === "blocked"` branch): change `error_class: "executor_blocked"` to `error_class: "agent/blocked"`. Update the matching comment at line 512.
2. In `server.ts:466`: change `error_class: "executor_internal_error"` to `error_class: "agent/internal_error"`.
3. In `http-bridge.ts:241`: same change to `agent/internal_error`.
4. In `agent-run.ts`: update all `errorClass: "<flat_class>"` literals — find via `rg 'errorClass:\s*"' executors/claude-agent/src/agent-run.ts`. Per the second-pass grounding the lines are approximately 204, 246, 309, 388, 498, 687, 750, 901, 916, 925; verify by direct grep before editing. Translation table:
   - `invalid_attribute` → `agent/attribute_invalid`
   - `invalid_attributes_schema` → `agent/attribute_invalid` (or a sub-leaf `agent/attribute_invalid/schema` if more precision is wanted — pick `agent/attribute_invalid` for simplicity)
   - `invalid_cwd_from_store` → `agent/attribute_invalid` (or `agent/cwd_invalid` if more precise)
   - `schema_validation_failed` → `agent/schema_violation`
   - `cli_spawn_failed` → `agent/cli_spawn_failed`
   - `silence_timeout` → `agent/timeout`
   - `subprocess_exit_before_complete` → `agent/subprocess_exit/before_complete`
5. The `report_blocked` MCP tool comments at `internal-mcp-tools.ts:23-26` reference `executor_blocked` in prose — update to `agent/blocked`. Similar in `internal-mcp-server.ts:215`.
6. In the claude-agent observability handshake (if it has one; otherwise add one), populate `declared_error_classes` with: `["agent/blocked", "agent/internal_error", "agent/attribute_invalid", "agent/schema_violation", "agent/cli_spawn_failed", "agent/timeout", "agent/subprocess_exit/*", "agent/rate_limited", "agent/context_exceeded", "agent/tool_use_failed/*", "agent/refused"]`.
7. Update vitest tests (e.g., `agent-run.test.ts:148, 238`) asserting on the old class strings.

**Verification:** `cd executors/claude-agent && npm test && npm run build` exits 0.

### Task 64: Update postgres-stores executor's error-class vocabulary

**Files:** `stores/postgres/server/executor.go`, `stores/postgres/server/executor_test.go`, plus the verifier path (find via `rg 'verifier' stores/postgres/`)

**Steps:**
1. In `stores/postgres/server/executor.go`: rename emissions — find the actual `invalid_attribute` and `verifier_failed` sites via `rg 'invalid_attribute|verifier_failed' stores/postgres/server/executor.go` (per second-pass grounding the `invalid_attribute` sites are around lines 68, 72, 77 and `verifier_failed` is around line 82).
   - `invalid_attribute` → `pg/attribute_invalid`
   - `verifier_failed` → `pg/verifier_check_failed/<check_kind>` (the check_kind requires plumbing the failed check identity through the verifier-result envelope — find the verifier-result type in `stores/postgres/` and add a `FailedCheckKind string` field to it; populate at the verifier's failure site).
2. Add new emission sites per the spec's "postgres-stores" subsection:
   - `pg/claim_unavailable` — find the producer-side conflict site (likely in the claim-acquisition path) and emit there.
   - `pg/connection_lost` — find the transient DB connection failure paths and emit there.
   - `pg/swap_failed` — find the atomic-staging swap path and emit there.
3. Add an observability handshake for postgres-stores if not present (mirroring http-node's pattern); populate `declared_error_classes` with: `["pg/attribute_invalid", "pg/claim_unavailable", "pg/connection_lost", "pg/swap_failed", "pg/verifier_check_failed/*"]`.
4. Update `executor_test.go:167, 246` to assert the new class strings.

**Verification:** `go test ./stores/postgres/... -count=1` passes.

### Task 65: Update stub-executor's error-class vocabulary

**Files:** `executors/stub/stub.go`, `executors/stub/stub_test.go`

**Steps:**
1. In `stub.go`: the stub already emits whatever class is configured (`errorClass` field). Add a convention: prefix the configured class with `stub/` if it doesn't already contain a `/`. Update the test fixture defaults to use `stub/<configured-class>` form.
2. Update `stub_test.go` to assert the new form.

**Verification:** `go test ./executors/stub/... -count=1` passes.

### Task 66: Update `tension:events-kind-no-enum` with partial-coverage note

**Files:** `.ok-planner/design/tensions/events-kind-no-enum.md`

**Steps:**
1. Open the file. Append a Notes section (or extend the existing one) with:
   ```
   ## Notes
   - 2026-05-23 — Partially addressed by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Node-run-transition `kind` values are now standardized under the signal type-path taxonomy (`concept:signal`): `terminal/*`, `transient/*`, `attribute/*`, `event/*`, `message/*`, validated at registration. Non-signal audit kinds (`state_transition`, `lock_acquired`, `work_started`, `attributes_substituted`, `auth.*`, etc.) remain free-form `TEXT`; a separate spec would need to taxonomize them. Tension does not move to `_resolved/`.
   ```
2. Status stays `open` (don't change the front-matter).

**Verification:** `grep -c 'Partially addressed' .ok-planner/design/tensions/events-kind-no-enum.md` returns 1.

### Task 67: Update scenario tests + add executor-vocabulary smoke tests

**Files:** `test/scenarios/bundled_executor_vocab_test.go` (new)

**Steps:**
1. Create the new scenario test exercising each bundled executor's hierarchical vocabulary:
   - `TestHttpNode_EmitsHierarchicalErrorClasses` — run http-node with a deliberately failing target; assert at least one of `http/*` classes appears as the signal type-path leaf.
   - `TestPostgresStores_EmitsHierarchicalErrorClasses` — run postgres-stores with a deliberately invalid attribute; assert `pg/attribute_invalid`.
2. Use the scenario harness (`scenario.Start(t, scenario.HarnessOpts{})`).

**Verification:** `go test ./test/scenarios/ -run 'TestHttpNode_Emits|TestPostgresStores_Emits' -count=1` passes.

### Task 68: Update operator-side validator tests for executor-declared-error-classes range-check

**Files:** `graph/node/template_validator_test.go`

**Steps:**
1. Add tests:
   - `TestValidateErrorTypes_AcceptsDeclaredHttpClass` — mock the validator hook to return `["http/timeout"]`; validate a template with `error_types: { "http/timeout": ... }`; assert no error.
   - `TestValidateErrorTypes_AcceptsDeclaredWildcardClass` — mock the hook to return `["http/server_error/*"]`; validate `error_types: { "http/server_error/500": ... }`; assert no error (prefix match).
   - `TestValidateErrorTypes_AcceptsUndeclaredWhenHookUnavailable` — hook returns `(nil, false)`; validate `error_types: { "foo": ... }`; assert no error (silent-skip).

**Verification:** Tests pass.

### Task 69: Verify Pass 6 end-state

**Files:** none (verification-only task)

**Steps:**
1. `go build ./...` — exits 0.
2. `go test ./... -count=1` — passes.
3. `make lint` — exits 0.
4. `cd executors/claude-agent && npm install && npm test && npm run build` — passes.
5. `rg '"executor_blocked"|"executor_internal_error"|"http_request_failed"|"http_unexpected_status"|"http_response_parse_failed"|"verifier_failed"' executors/ stores/` — returns nothing (all old flat classes renamed).

**Verification:** All commands pass.

---

## Manual checks after completion

The implementation is fully autonomous; these are the operator's optional post-implementation sanity checks:

1. **Conformance binaries.** Run `go run ./cmd/rimsky-executor-conformance --endpoint <bundled-executor> --transport grpc` against each of http-node, claude-agent, and postgres-stores to confirm the new error-class vocabularies pass the conformance suite. The plan does not run this automatically because it requires the operator to bring up each executor binary.
2. **Reference deployment smoke.** Bring up `deploy/docker-compose.yml`; confirm `/health` reaches green on every service. The plan does not run this automatically because it requires Docker on the operator's machine.
3. **CEL filter authoring smoke.** Author a small operator template that uses a `when: payload.error_class.startsWith("http/")` predicate against a deliberately-failing http-node node; confirm the subscriber fires correctly. The plan covers this through unit + scenario tests, but a hand-authored template confirms the end-to-end ergonomics.
