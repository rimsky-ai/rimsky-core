# Stores Protocol Cleanup — Store-Internal-Vocabulary Excision

**Status:** Spec, ready for review.
**Supersedes:** Sections of `docs/specs/2026-04-27-stores-redesign-v3-design.md`
(see §6 "Spec sections superseded"). The v3 spec remains authoritative for
everything else.
**Driver:** `docs/history/v3-completion.md` Issues 1 + 3, paired into one cycle
because both excise store-internal vocabulary from rimsky's
protocol/grammar surface.

---

## 1. Background

The v3 spec landed the rimsky↔store wire protocol as five runtime verbs
plus a startup `Capabilities()` handshake. Two structural leaks of
store-internal vocabulary survived into v3 unintentionally and contradict
v3 §3.3 ("Store-internal capabilities"):

1. **Open's "pool-empty signal" is in-band on success bytes.** v3 §4.7
   defines an all-zero-length `ClaimResult` (address + payload + region all
   empty) as the store's signal that "no item is available right now."
   The signal is in-band with success values, the term "pool" is
   store-implementation vocabulary (postgres-store concept), and a
   store bug that returns zero-length bytes for a real claim silently
   evaporates the dispatch with no operator-visible signal.

2. **`policy_override` and `claim_resolutions` carry pick-policy
   vocabulary across the boundary.** The `CommitRequest` /
   `AbandonRequest` proto fields carry strings like `release_to_back`,
   `release_to_head`, `delete`. The template grammar's
   `claim_resolutions[<alias>] = { on_commit, on_give_up }` block is the
   operator-author entry point for these strings. The rimsky-side
   `auto_terminal.go::fireResolutionVerb` enumerates the action vocabulary.
   None of this should exist on the rimsky side under v3 §3.3.

This cycle excises both leaks with one set of coordinated edits to the
proto, the rimsky source, the template grammar, the store impls, the
spec text, and the glossary.

For the discovery / trade-off discussion that motivated this cycle, see
`docs/history/v3-completion.md` Issues 1 + 3.

## 2. Goals and non-goals

### 2.1 Goals

- The rimsky↔store wire protocol carries no store-internal vocabulary.
  Specifically: no `policy_override` field, no enumerated
  pick-policy action strings, no implicit "all-empty bytes" signal.
- The rimsky-side template grammar carries no store-internal vocabulary.
  Specifically: no `claim_resolutions` map, no
  `on_commit` / `on_give_up` action strings.
- Store disposition (what Commit/Abandon mean on the store's
  state) is governed entirely by per-store config (e.g. the
  postgres reference store's `pick_policies[*].on_commit_default` /
  `on_give_up_default`). Operators express disposition there, not in
  rimsky-side templates.
- Store implementations and the wire surface get strictly simpler: the
  protocol shrinks from 5 + 1 verbs to 4 + 1 verbs (no `Delete`).
- The v3 spec's invariants and atomicity model carry over unchanged.
  Specifically: invariants 10, 13, 15, 20 hold; the rimsky-side
  acquisition tx still calls `Open` between lock-holder INSERT and
  commit; the store's tx is still decoupled.

### 2.2 Non-goals

- **Executor-driven regional-delete.** The pre-cleanup capability
  "regional rw claim's success path is `Delete` rather than `Commit`"
  (driven today by template `on_commit: delete` → `Store.Delete`) is
  removed in this cycle and not replaced. If a future use case
  motivates it, a follow-up cycle adds an executor-protocol mechanism
  (e.g. a `Discarded` terminal event or a `region_action` field on
  `Complete`). Out of scope here.
- **Store-author guide body rewrite.** `docs/store-author-guide.md`
  retains its v3 banner; the body underneath is still v2 reference
  material. Rewriting the body coherently is its own writing-quality
  cycle, deferred until after this excision lands so the rewrite
  doesn't immediately drift.
- **Rename or restructure of v3 features that don't touch store-internal
  vocabulary.** Selectors, regions, claim_id, capabilities, atomicity
  model, frame engine, scheduler — all unchanged.
- **Backwards-compatibility shims.** The platform is pre-v1 (per
  `.claude/rules/rules.md`). The proto change is breaking by intent;
  no compat layer.

## 3. The cleanup

Four coordinated changes. All four land together — none of them is
useful in isolation.

### 3.1 `claim_resolutions` template grammar — DELETE

Today (v3, in `core/node/template.go::Template.Nodes[*].ClaimResolutions`):

```yaml
nodes:
  - id: my-acquirer
    stores:
      - name: topics-ring
        alias: topic
        read: ["@review-queue"]
    claim_resolutions:
      topic:
        on_commit: release_to_back
        on_give_up: release_to_back
```

After: the entire `claim_resolutions` map is gone from the template
grammar. The Go struct `node.ClaimResolution` is deleted. The
control-api JSON shape `claimResolutionJSON` is deleted. The template
validator no longer accepts the field; templates that still declare it
fail deploy with `unknown field claim_resolutions`. No grace period.

**Why this is sufficient.** Store disposition was the only thing
the field carried. The store already has its own per-policy config
where disposition lives (e.g. the postgres reference store's
`pick_policies[<selector>].on_commit_default` /
`on_give_up_default`). The smoke fixture
(`test/smoke/fixtures/template.yml`) already documents that templates
"use store defaults" — no production templates rely on the override.

### 3.2 `policy_override` wire field — DELETE

Today (v3, in `proto/v1/store_service.proto`):

```proto
message CommitRequest  { string claim_id = 1; bytes region = 2; bytes address = 3; string policy_override = 4; }
message AbandonRequest { string claim_id = 1; bytes region = 2; bytes address = 3; string policy_override = 4; }
```

After:

```proto
message CommitRequest  { string claim_id = 1; bytes region = 2; bytes address = 3; }
message AbandonRequest { string claim_id = 1; bytes region = 2; bytes address = 3; }
```

The `core/store.Store` Go interface methods drop their last parameter:

```go
// Before
Commit(ctx context.Context, claimID ClaimID, region, address []byte, policyOverride string) error
Abandon(ctx context.Context, claimID ClaimID, region, address []byte, policyOverride string) error

// After
Commit(ctx context.Context, claimID ClaimID, region, address []byte) error
Abandon(ctx context.Context, claimID ClaimID, region, address []byte) error
```

The rimsky-side gRPC client (`core/store/remote/client.go`), the bridge
HTTP handler (`stores/internal/bridge/bridge.go`), the standard
store impls (`stores/postgres/store/store.go`,
`stores/filesystem/store/store.go`, `stores/stub/store/store.go`) and
the test fakes (`core/store/storetest/fake.go`) all lose the parameter.
The postgres store's `applyPickAction` drops its `policyOverride`
argument; per-policy config defaults are the only governing input.

The `ClaimSpec` docstring in `core/store/types.go` (lines 39-45)
references `policyOverride` and `claim_resolutions` to describe the
semantic context of the type; that prose is rewritten to match the
post-cleanup model (store disposition is governed by per-store
config, not by a `claim_resolutions` block on the rimsky side).

### 3.3 `Delete` wire verb — DELETE

Today (v3): `StoreService` declares 5 runtime verbs `Open / Commit /
Abandon / Delete / Release` plus 1 startup verb `Capabilities`.

After: 4 runtime verbs `Open / Commit / Abandon / Release` plus 1
startup verb `Capabilities`. The `Delete` RPC, `DeleteRequest`, and
`DeleteResponse` proto messages are removed. The `Store.Delete` Go
interface method is removed. The standard impls' `Delete` methods are
removed:
- `stores/filesystem/store/store.go::Delete` and the two filesystem
  test cases (`TestDeleteRemovesTarget`,
  `TestDeleteEmptyRegionIsNoop`) are deleted.
- `stores/postgres/store/store.go::Delete` (no-op) is deleted.
- `stores/stub/store/store.go::Delete` (call-recorder) is deleted.
- `core/store/storetest/fake.go::Delete` is deleted.

The rimsky-side `auto_terminal.go::fireResolutionVerb` no longer has a
`Delete` branch (it had one for the `delete` resolution action; with
§3.1 that action is gone).

CLAUDE.md's standing reference to "5+1-verb gRPC protocol" updates to
"4+1-verb."

### 3.4 `OpenResponse` outcome — oneof

Today (v3, `proto/v1/store_service.proto::OpenResponse`):

```proto
message OpenResponse {
  bytes address = 1;
  bytes payload = 2;
  bytes region  = 3;
}
```

The all-empty case is in-band: `len(address) == 0 && len(payload) == 0
&& len(region) == 0` is the store's signal "no item available."

After:

```proto
message OpenResponse {
  oneof result {
    Acquired    acquired    = 1;
    Unavailable unavailable = 2;
  }
}

message Acquired {
  bytes address = 1;
  bytes payload = 2;
  bytes region  = 3;
}

message Unavailable {}
```

The `oneof` makes the two outcomes structurally distinct. A store
can no longer accidentally return `Unavailable` while populating
bytes (that combination is unrepresentable). A store that wants to
return `Acquired` with empty bytes (e.g. a side-effect-only claim) is
syntactically distinguishable from "nothing to give right now."

A non-nil gRPC status code remains the third path (store-side
fault; rimsky surfaces it to the operator via supervisor logging /
metrics; dispatch row stays unclaimed for retry).

## 4. Rimsky-side updates

The rimsky source changes follow mechanically from §3:

### 4.1 `core/store/interface.go::Store`

```go
// Before (v3, abridged)
type Store interface {
    Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (ClaimResult, error)
    Commit(ctx context.Context, claimID ClaimID, region, address []byte, policyOverride string) error
    Abandon(ctx context.Context, claimID ClaimID, region, address []byte, policyOverride string) error
    Delete(ctx context.Context, claimID ClaimID, region []byte) error
    Release(ctx context.Context, claimID ClaimID, region, address []byte) error
}

// After
type Store interface {
    Open(ctx context.Context, claimID ClaimID, spec ClaimSpec) (OpenOutcome, error)
    Commit(ctx context.Context, claimID ClaimID, region, address []byte) error
    Abandon(ctx context.Context, claimID ClaimID, region, address []byte) error
    Release(ctx context.Context, claimID ClaimID, region, address []byte) error
}
```

`OpenOutcome` is the Go-side discriminator that mirrors the wire oneof:

```go
type OpenOutcome struct {
    // Available is true when the store returned Acquired{...}.
    // False when it returned Unavailable{}.
    Available bool
    // ClaimResult is populated when Available == true. Its three
    // fields remain opaque json.RawMessage bytes per invariant 20.
    Result ClaimResult
}
```

(Alternative shape considered: a sealed interface with two impls. The
plain struct is simpler for the four-call-site rimsky surface and the
store adapters in `core/store/remote/`. Sealed-interface form has
no callsite advantage here.)

The proto-to-struct mapping inside `core/store/remote/client.go::Open`
is the canonical adapter:

```go
resp, err := c.grpc.Open(ctx, req)
if err != nil {
    return store.OpenOutcome{}, err
}
if u := resp.GetUnavailable(); u != nil {
    return store.OpenOutcome{Available: false}, nil
}
acq := resp.GetAcquired() // non-nil branch by elimination; defensive nil-check
if acq == nil {
    return store.OpenOutcome{}, fmt.Errorf("Open: response carries neither Acquired nor Unavailable")
}
return store.OpenOutcome{
    Available: true,
    Result: store.ClaimResult{
        Address: acq.Address,
        Payload: acq.Payload,
        Region:  acq.Region,
    },
}, nil
```

The HTTP+JSON bridge handler in `stores/internal/bridge/bridge.go` does
the corresponding switch over the canonical proto3-JSON discriminator
shape (`{"acquired": {...}}` vs. `{"unavailable": {}}`). See §5.4 for
the encoder swap that the bridge requires for the oneof to round-trip
correctly.

### 4.2 `core/supervisor/runner_acquire.go::acquireClaim`

The all-empty-bytes check goes away. The flow becomes:

```go
outcome, err := s.Open(ctx, claimID, spec)
if err != nil {
    return AcquiredLock{}, false, fmt.Errorf("acquireClaim: Open: %w", err)
}
if !outcome.Available {
    // The store has no claim to give right now. The dispatch tx
    // rolls back; the next scheduler tick may retry.
    return AcquiredLock{}, false, nil
}
cr := outcome.Result
// proceed with INSERT + dispatch as today
```

The function signature and outer call shape are unchanged. The error
surface (`AcquiredLock{}, false, nil` for "not available";
`AcquiredLock{}, false, err` for "store fault") matches what
the supervisor already does for the verify-before-run / state-machine
bail paths, so callers don't need to learn a new convention.

### 4.3 `core/supervisor/auto_terminal.go`

```go
// Before — selectResolutionAction picks a string from
// node.ClaimResolution; fireResolutionVerb routes that string to
// Commit / Abandon / Delete with policy_override.
// After — both helpers are deleted. CheckAndFireResolution becomes:

func CheckAndFireResolution(
    ctx context.Context, args RunArgs, tx pgx.Tx,
    lockHolderID shared.UUID,
) error {
    // ... [lock row, list claim-holders, compute aggregate outcome] ...
    s, _ := args.StoreRegistry.Get(storeName)
    claimID := store.ClaimID(lockHolderID.String())
    var verbErr error
    if anyFailed {
        verbErr = s.Abandon(ctx, claimID, region, address)
    } else {
        verbErr = s.Commit(ctx, claimID, region, address)
    }
    if verbErr != nil {
        return fmt.Errorf("CheckAndFireResolution: store verb: %w", verbErr)
    }
    return args.LockHolders.DeleteByID(ctx, tx, lockHolderID, args.SupervisorID)
}
```

The `alias string` and `claimResolutions map[string]node.ClaimResolution`
parameters disappear from the function signature. The two callers are:

- `core/supervisor/runner_terminal.go::releaseClaim` (line 511) — drops
  the `acq.NodeDef`-derived resolution lookup and just calls
  `CheckAndFireResolution(ctx, args, tx, lockHolderID)`.
- `core/supervisor/runner_terminal.go::releaseInheritedClaimsInTx`
  (line 589) — drops `ia.Alias` and the `resolutions` map argument from
  the call.

`core/supervisor/runner_held_claims.go::resolutionForAlias` (line 228)
and `::resolutionForAcquirerNode` (line 239) become dead with the new
signature; both are deleted. The `core/supervisor/terminal_outcome.go`
file does not call `CheckAndFireResolution`; it only contains a
package-level comment that references `claim_resolutions` (line 4).
That comment is rewritten to drop the term.

### 4.4 `core/supervisor/runner_terminal.go::releaseClaim`

The `verbAction` argument is gone. The function becomes a thin
"success → Commit; failure → Abandon" dispatcher with the same
guards (claimant-supervisor-ID, idempotency-by-claim_id) as today.

### 4.5 `core/node/template.go` and validators

- `node.ClaimResolution` struct: deleted.
- `Node.ClaimResolutions` field: deleted.
- `core/node/template_validator.go` — the deploy-time per-field walker
  branch that handled `ClaimResolutions` is deleted.
- `core/node/inheritance.go::ValidateInheritance` (lines 142-182) — the
  separate held-claim validation block that errored when a held claim's
  acquirer didn't declare `claim_resolutions[<alias>]` with both
  `on_commit` and `on_give_up` is deleted. The package doc-comment at
  lines 11-22 that describes the validator's contract gets the
  `claim_resolutions` mention scrubbed accordingly. Held-claim
  acquirers no longer need any per-alias declaration; the supervisor's
  success-vs-failure binary drives the store verb directly.

### 4.6 `core/controlapi/templates.go`

- `claimResolutionJSON` struct: deleted.
- `nodeJSON.ClaimResolutions` field: deleted.
- The cross-translation block that converted JSON → `node.ClaimResolution`:
  deleted.

### 4.7 `core/store/remote/client.go` and test fakes

The gRPC client adapter drops the `policyOverride` parameter from its
`Commit` / `Abandon` methods, drops its `Delete` method entirely, and
maps the `OpenResponse` oneof to `OpenOutcome` per the snippet in §4.1.
Behavior on `UnimplementedDeleteRequest` for an old store:
irrelevant — the client never calls `Delete` anymore, and the proto
change rebuilds both ends together.

The unit-test fake at `core/store/storetest/fake.go` is updated in
lockstep:

- `OpenFunc` (line 26) is retyped from
  `func(claimID, spec) (ClaimResult, error)` to
  `func(claimID, spec) (OpenOutcome, error)`. Tests that previously
  returned an all-empty `ClaimResult{}` to simulate the pool-empty
  signal now return `OpenOutcome{Available: false}` instead.
- The package-doc paragraph at line 25 (which references "an all-empty
  `ClaimResult` to simulate the pool-empty signal") is rewritten to
  describe the `Unavailable` outcome.
- The fake's `Commit` / `Abandon` callbacks drop the `policyOverride`
  argument from their signatures; the fake's `Delete` method is
  deleted.

## 5. Store-side updates

### 5.1 `stores/postgres/store/store.go`

- `Commit` / `Abandon` lose the `policyOverride` parameter. Both call
  `applyPickAction(ctx, claimID, successPath bool)` (no override
  argument).
- `applyPickAction` reads the action from
  `pp.OnCommitDefault` (success) or `pp.OnGiveUpDefault` (failure).
  The configured-defaults path is the only path; the override branch
  goes away.
- `Delete` method: deleted.
- The reference deploy config (`deploy/store-postgres.yml`) already
  declares per-policy defaults; no operator-config change required.

### 5.2 `stores/filesystem/store/store.go`

- `Commit` / `Abandon` lose the `policyOverride` parameter.
- `Delete` method: deleted.
- The two test cases for `Delete` (`TestDeleteRemovesTarget`,
  `TestDeleteEmptyRegionIsNoop`): deleted.

### 5.3 `stores/stub/store/store.go`

- `Commit` / `Abandon` lose the `policyOverride` parameter.
- `Delete` method: deleted.
- The "delete" call recording in `Call`: deleted.

### 5.4 Bridge handler (`stores/internal/bridge/bridge.go`)

- `policy_override` field deserialization: deleted.
- `Delete` route handler and the `POST /v1/delete` route: deleted.
- `Open` response wrapping uses the new oneof shape.
- **`writeJSON` (line 170) switches from `encoding/json` to
  `google.golang.org/protobuf/encoding/protojson.Marshal`.** The
  current `writeJSON` carries a comment (lines 159-169) explicitly
  flagging that the introduction of a `oneof` field is the trigger to
  make this swap — `encoding/json` does not produce the canonical
  proto3-JSON discriminator shape (`{"acquired": {...}}` /
  `{"unavailable": {}}`) and HTTP+JSON bridge clients that decode with
  `protojson.Unmarshal` will fail to parse the response. Inbound
  requests do not have any oneofs after this cleanup, so the
  `decodeOptional` / `json.Unmarshal` request-side helpers do not
  change.

### 5.5 Capabilities response — unchanged

Stores do not advertise pick-policy support over `Capabilities()`;
store-internal capabilities remain store-internal (per v3
§3.3). The `CapabilityStruct.write_semantics` field is the only
declared capability; that stays.

## 6. Spec sections superseded

This document supersedes the following sections of v3:

- **§4.1 ("The runtime verbs (5 + 1)")** — re-titled "The runtime
  verbs (4 + 1)"; the `Delete` row drops out of the table; verb
  count revised throughout.
- **§4.5 (`policy_override`)** — entire section deleted.
- **§4.7 (`ClaimResult`, "Pool-empty signal")** — the third paragraph
  ("Pool-empty signal") is replaced with: "The store signals
  acquisition outcome via the `OpenResponse` oneof (`Acquired` |
  `Unavailable`); see the revised §4.7 above." The `ClaimResult`
  type and the second paragraph ("All three fields are opaque bytes
  per invariant 20...") are retained verbatim.
- **§4.10 invariant 13.1 ("auto-terminal routing table")** — the
  routing-table form ("commit | abandon | delete | release_to_back |
  release_to_head") collapses to "success → Commit; failure →
  Abandon." Invariant 13 itself (auto-terminal at holding-subgraph
  completion, aggregate-outcome-driven) is unchanged.
- **§5.1 ("gRPC")** — the `service StoreService { ... }` proto
  block (v3 spec lines 204-213) is re-rendered with the `Delete`
  RPC line removed. The post-revision shape is the appendix of this
  document (§11).
- **§5.2 ("HTTP+JSON bridge")** — the route table (v3 spec
  lines 222-228) drops the `POST /v1/delete` row.
- **§7.8 obligation #3 ("All terminal verbs MUST be idempotent in
  `claim_id`")** — the parenthetical list `(Commit / Abandon /
  Release / Delete)` becomes `(Commit / Abandon / Release)`. The
  obligation itself is unchanged in substance.

All other v3 sections carry over unmodified.

## 7. Glossary

`docs/glossary.md` gets a new section "Store-internal vocabulary
(not part of rimsky's protocol surface)" appended after the rimsky-
vocabulary entries. The "pick policy" entry moves under that section
with a one-liner: "An items-table queue convention some stores
implement (e.g. the postgres reference store-service). Not part of
rimsky's protocol surface; appears only in store-service-specific
docs and config." The terms `release_to_back`, `release_to_head`,
and the items-table-`delete` action go in the same section as
brief one-liners pointing at `docs/store-author-guide.md` and the
postgres reference's config.

The rimsky-vocabulary entries (`claim`, `region`, `selector`,
`store`, `named lock`, etc.) keep their existing prose.

## 8. Test migration

### 8.1 Tests that exercise `claim_resolutions` directly

These get rewritten or deleted:

- `core/node/template_validator_test.go` — drop the cases that walk
  `ClaimResolutions`. Add a case that asserts a template carrying
  the field fails deploy with the unknown-field error.
- `core/controlapi/app_test.go` — drop the `claim_resolutions` JSON
  cases.
- `core/scenario/harness_test.go`, `core/scenario/harness.go`,
  `core/scenario/harness_util.go` — drop the
  `WithClaimResolution(...)` builder helpers.
- `core/supervisor/auto_terminal_test.go` — the
  `TestCheckAndFireResolution_*` cases collapse to two: success →
  Commit; any-failed → Abandon. Existing
  `TestCheckAndFireResolution_AnyFailedFiresGiveUp` becomes the
  Abandon case; existing commit-fires case stays as-is, just
  without the resolution-map argument.
- `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go::TestAutoTerminalAggregateCommitEndToEnd` —
  drop the resolution-vocabulary parts; assert the wire path fires
  `Commit` (not "release_to_back" / "delete").
- `test/scenarios/frame_resolution/held_claim_resolution_at_frame_end_test.go` —
  drop the resolution-vocabulary parts; assert success → Commit.

### 8.2 Tests that exercise `policy_override` store behavior

`stores/stub/store/store_test.go` and the postgres store tests that
call `Commit / Abandon` with a non-empty `policyOverride` argument
get rewritten to call without it; the postgres test cases that need
to exercise the configured defaults stay (with their setup adjusting
the `pick_policies[*].on_commit_default` config value rather than
passing an override argument).

### 8.3 Tests that exercise `Delete`

`stores/filesystem/store/store_test.go::TestDeleteRemovesTarget` and
`TestDeleteEmptyRegionIsNoop` get deleted. The integration scenarios
under `test/scenarios/stores/` that exercised regional-delete
through the rimsky stack are dropped or rewritten; the resulting
coverage is "the store's `Delete` capability is excised; verify
no rimsky path attempts to call it."

### 8.4 Tests that exercise the `Open` outcome

The two scenarios that drove the all-empty-bytes signal
(`test/scenarios/stores/...` and the unit-level coverage in
`stores/postgres/store/store_test.go`,
`stores/stub/store/store_test.go`) get rewritten to drive the new
`Unavailable{}` variant. Store-side: a configured pick policy
with an empty items table now returns `Unavailable{}` from
`openPickPolicy` (postgres) or the stub's empty-FIFO path. Rimsky-
side: `runner_acquire` returns `(AcquiredLock{}, false, nil)` and
the dispatch tx rolls back, exactly as today's all-empty-bytes path
does.

### 8.5 Smoke fixtures

Two files carry `claim_resolutions` content in the smoke fixture set
and both are updated:

- **`test/smoke/fixtures/template.yml` (lines 95-98)** — deletes its
  `claim_resolutions:` block (4 lines: header + comment + one entry
  resolving to `release_to_back / release_to_back`). The YAML's
  current shape is documentary only; the deployed body is the Go
  smoke test.
- **`test/smoke/stores_redesign_smoke_test.go` (lines 592-597)** —
  the deployed body removes the
  `"claim_resolutions": map[string]any{...}` literal entirely. The
  test's surrounding assertions (acquirer-driven flow, queue
  semantics, success-path Commit / Abandon outcomes) stay; only the
  override-vocabulary disappears. The package-level comment at line
  683 ("the acquirer's `claim_resolutions` block governs ...") is
  rewritten to describe the per-store-config defaults model.

Behavior is preserved: the postgres store-service's `@review-queue`
policy already declares `on_commit_default: release_to_back` /
`on_give_up_default: release_to_back` in `deploy/store-postgres.yml`.
No other smoke-fixture changes.

## 9. Documentation cascade

Beyond the spec itself, documentation that references the excised
vocabulary is updated:

- **CLAUDE.md** — the "5+1-verb gRPC protocol" reference becomes
  "4+1-verb"; the gotcha note about `RIMSKY_STORES_CONFIG` and the
  store-service surface drops the parenthetical "(`release_to_back`,
  `release_to_head`, `delete`)" examples; the "auto-terminal" gotcha
  note re-states the routing as "success → Commit; failure →
  Abandon" with no action-string vocabulary.
- **`core/store/doc.go`** — package-level doc comment (lines 41-49)
  enumerates "Five protocol verbs (spec §4.1)" with `Delete` as one
  of them and references `policyOverride` arguments on Commit /
  Abandon. Updated to "Four protocol verbs (spec §4.1)" with the
  `Delete` entry removed and the `policyOverride` mentions scrubbed.
- **`docs/architecture.md` §1.2** — the postgres reference store
  description rewords "supports regional access AND store-side
  pick policies" to "supports regional access AND items-table queue
  semantics implemented store-internally." Verb count updated
  in any place that says "5 + 1."
- **`docs/operator-guide.md`** — the timing-constraint discussion
  (`visibility_timeout > 5 × heartbeat_interval`) is relabeled as
  guidance for operators of *the postgres reference store-service*
  specifically, not as a rimsky-level constraint.
- **`docs/store-author-guide.md`** — the v3 banner stays. The body
  rewrite is **out of scope** (per §2.2). The banner's section list
  drops `Delete` and `policy_override` references. Operator-author
  prose freely uses "pick policy" / "release_to_back" / etc., since
  this guide is the store-author-facing surface where that
  vocabulary belongs.
- **`docs/glossary.md`** — see §7.
- **CHANGELOG.md** — single combined entry under `## Unreleased`
  describing the cycle:

  > **Stores Protocol Cleanup — store-internal-vocabulary excision.**
  > Drops `policy_override` from `CommitRequest` / `AbandonRequest`,
  > deletes the `Delete` wire verb, replaces `OpenResponse`'s
  > implicit all-empty-bytes pool-empty signal with an explicit
  > `oneof Acquired | Unavailable` discriminator, and removes the
  > `claim_resolutions` template grammar. The wire surface is now
  > 4 runtime verbs + 1 startup handshake; store disposition
  > (commit-vs-release-vs-delete on the store's own state) is
  > governed entirely by per-store config. Spec:
  > `docs/specs/2026-04-30-stores-protocol-cleanup-design.md`.
  > Supersedes v3 §4.1 / §4.5 / §4.7 / §4.10 invariant 13.1 / §7.8
  > obligation #3.

- **`docs/history/v3-completion.md`** — Issues 1 and 3 marked as resolved
  by this cycle; the doc retains the Issue 2 (frame-engine
  multi-source observation) and the lower-priority follow-ups.

## 10. Out of scope (deferred)

- **Executor-driven regional-delete.** A future cycle may add an
  executor terminal event (`Discarded` or `Complete{region_action:
  delete}`) that invokes a re-introduced `Delete` verb. Today's
  template-driven version goes away; nothing replaces it in this
  cycle.
- **Store-author guide body rewrite.** Banner stays current; body is
  v2 reference material. Out of scope per §2.2.
- **mTLS / transport credentials, store-side conformance probe,
  bridge handler error-class mapping** — explicitly deferred per v3
  §15. Not affected by this cycle.

## 11. Appendix — full revised proto

For reference, the complete `proto/v1/store_service.proto` after
the cleanup:

```proto
syntax = "proto3";
package rimsky.v1;
option go_package = "github.com/fallguyconsulting/rimsky/proto/v1/gen;genv1";

service StoreService {
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse);
  rpc Open(OpenRequest) returns (OpenResponse);
  rpc Commit(CommitRequest) returns (CommitResponse);
  rpc Abandon(AbandonRequest) returns (AbandonResponse);
  rpc Release(ReleaseRequest) returns (ReleaseResponse);
}

message CapabilityStruct {
  string write_semantics = 1;
}
message CapabilitiesRequest {}
message CapabilitiesResponse { CapabilityStruct capabilities = 1; }

message OpenRequest {
  string claim_id   = 1;
  string store_name = 2;
  string selector   = 3;
  string intent     = 4;  // "r" | "rw"
  string alias      = 5;  // template-side identifier; store ignores
}

message OpenResponse {
  oneof result {
    Acquired    acquired    = 1;
    Unavailable unavailable = 2;
  }
}
message Acquired {
  bytes address = 1;
  bytes payload = 2;
  bytes region  = 3;
}
message Unavailable {}

message CommitRequest  { string claim_id = 1; bytes region = 2; bytes address = 3; }
message CommitResponse {}

message AbandonRequest  { string claim_id = 1; bytes region = 2; bytes address = 3; }
message AbandonResponse {}

message ReleaseRequest  { string claim_id = 1; bytes region = 2; bytes address = 3; }
message ReleaseResponse {}
```
