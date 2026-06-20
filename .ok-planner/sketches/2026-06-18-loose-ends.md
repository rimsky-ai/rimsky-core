# Loose ends — 2026-06-18

Accumulating list of discovered remnants, confusions, and architectural threads worth pulling but not currently blocking. Each entry: what was found, why it's suspect, the current best read, and what would need to happen to resolve it.

---

## 1. Synthetic `terminal/success` cascade after parked-resume and upstream-refresh propagation

**Status — RESOLVED 2026-06-18.** Downward synthetic-signal walk deleted from both call sites; `walkCascadeForInvalidatedNode` renamed to `pullUpstreamRefreshesForNode` and file moved to `code:lib/runtime/upstream_refresh_pull.go`; `invalidationCascadeSignal` constant deleted; previously-failing test at `code:test/scenarios/parked_resume_spurious_cascade_test.go::TestParkedResumeDoesNotSpuriouslyCascadeSuccessSubscriberOnError` converted to regression protection. **Sub-thread STILL OPEN**: verification B under "What would need to happen" #3 — whether the parked-resume call site at `code:lib/runtime/wake_parked.go#105` is wholly redundant once the downward walk is gone (only the upward `pullForceRefreshUpstreams` pass remains; if parked nodes don't typically declare `force_upstream_refresh: true` edges, the call site can be deleted entirely).

**Surfaced**: 2026-06-18 follow-up to a sweep agent's misdiagnosis. The agent claimed "only `terminal/success` walks the cascade" — that claim is wrong (the cascade walker is signal-blind per `story:cascade-signal-blind`), but pulling the thread surfaced a real remnant in the same area.

### What's there

`code:lib/runtime/cascade_invalidate.go` defines:

- `walkCascadeForInvalidatedNode(senderNodeID, ...)` — runs an **upward** `pullForceRefreshUpstreams` pass for the node, then a **downward** cascade walk over the node's subscribers using a hardcoded synthetic signal.
- `invalidationCascadeSignal` — package-level constant: `{ Type: "terminal/success", Payload: { changed: true, attributes_delta: {}, change_summary: "invalidation_cascade" } }`.
- `stalemarkAndEnqueueInFrame(target, ...)` — marks a node stale, appends a state-transition event, then calls `walkCascadeForInvalidatedNode` on the same node.

The function is called from exactly two sites:
- `code:lib/runtime/wake_parked.go#105` — after a parked node resumes (state set to stale, reason `ReasonHandlerResume`).
- `code:lib/runtime/cascade_invalidate.go#105` — recursive, from `stalemarkAndEnqueueInFrame`, which is itself called from `code:lib/runtime/runner_terminal.go#551` when the main cascade walker processes a `force_upstream_refresh: true` edge.

### Origin

Introduced by `e70f59ac feat: signal taxonomy + policy decoupling reshape` (2026-05-24) as a bridge during the move to signal-blind cascade. The walker `cascadeSubscribersStaleInTx` matches subscription edges by `(sender_node_type, sig.Type)` at `code:lib/runtime/runner_terminal.go#357` and evaluates CEL predicates against the signal payload at `#371` — so any code path that wants to drive the walker without an actual signal has to invent one. The synthetic `terminal/success` was the chosen bridge.

Sibling synthetic mechanisms introduced in or near that era have since been retired:
- `7d71ef32 feat: empty-message wake trigger` — retired the synthetic-envelope mechanism (visible as `// @decision: synthetic-envelope-mechanism-retired` markers in `code:lib/runtime/message_delivery.go`).
- `fedc4f38 feat: typed-message schema layer` — retired the standalone "operator-invalidate" verb.

The synthetic-`terminal/success` mechanism is the next sibling of those, never picked off. The function name `walkCascadeForInvalidatedNode`, the file name `cascade_invalidate.go`, and the payload tag `"invalidation_cascade"` all reference the retired `concept:invalidate` vocabulary (`.ok-planner/design/concepts/_retired/invalidate.md`).

### Why suspect — strong evidence

The function does two separable things in sequence; the upward part is legitimate, the downward part is suspect.

1. **Upward `pullForceRefreshUpstreams` is justified and tested.** `story:upstream-pull-on-invalidate` describes "the cascade walker also invalidates the named upstream so it re-runs in the same frame before the receiver dispatches." `code:test/scenarios/per_run_attributes/hard_dep_test.go::TestPerRunAttributes_HardDepPullsUpstream_DirectInvalidateOfReceiver` exercises exactly this — C invalidates → B pulled fresh → C reads B's new value. The test's load-bearing observable is delivered entirely via the upward `pullForceRefreshUpstreams` pass.

2. **Downward synthetic-signal cascade has no story.** Neither `story:upstream-pull-on-invalidate` nor any story for the parked-resume site justifies walking the stale-marked node's OTHER downstream subscribers at this point. The hard_dep test exercises the function but does not exercise the downward walk (B's only relevant subscriber C is matched by the upward pass, not the downward synthetic cascade).

3. **The synthetic signal is over-narrow for any "pre-stale all subscribers" intent.** It matches only subscribers of type `terminal/success` or `terminal/*`. Subscribers matching `terminal/error/*`, `attribute/...`, or other types are not pre-staled by this walk. If the intent were "pre-stale all subscribers of the invalidated node," the type-keyed walker is the wrong tool.

4. **The pre-stale work is shadowed by the real-signal walk.** When the stale-marked node eventually re-dispatches and emits its actual signal, the normal cascade walker fires the appropriate subscribers via `emitSignalInTx` → `cascadeSubscribersStaleInTx` with the REAL signal. Subscribers that the synthetic walk pre-staled would be re-matched by the real walk; subscribers that didn't match the synthetic would be matched by the real walk anyway.

### Verification status

**A. Spurious dispatch when the eventual real signal is non-success — CONFIRMED (2026-06-18).** Verified by `code:test/scenarios/parked_resume_spurious_cascade_test.go::TestParkedResumeDoesNotSpuriouslyCascadeSuccessSubscriberOnError`. Test setup: worker node X scripted to park then re-scripted to `terminal/error/stub/boom` after wake; downstream Y subscribed only to exact `terminal/success` on X, no `force_upstream_refresh`. Result: Y dispatched spuriously — observable as a `terminal/success` row in Y's event ledger carrying the planted `change_summary: "must-not-run"` marker. The mechanism matches the hypothesis exactly: synthetic walk on X's parked-resume inserts a wait-set row gating Y on X's run; `code:lib/runtime/runner_terminal.go#589`'s `MarkDrainedBySender` drains by sender run regardless of topic_kind when X's real `terminal/error` arrives; Y becomes dispatch-eligible and runs. The test is in the corpus as a failing test — documents the bug now; converts to regression protection once the downward walk is removed.

**B. Parked-resume site may not need the upward pass either — STILL OPEN.** Once the downward walk is gone, the only thing `walkCascadeForInvalidatedNode` does at the parked-resume site is the upward `pullForceRefreshUpstreams` pass for the resumed node. If parked nodes typically don't have `force_upstream_refresh: true` edges, the call site has no reason to exist. If they do, the call might be better placed at re-dispatch time rather than at resume time. A focused scenario test would resolve this once the deletion in (A) lands.

### Best read

The downward synthetic-signal cascade is residue from the signal-taxonomy reshape — a bridge added during one move and never converted or removed. The upward `pullForceRefreshUpstreams` portion is real. The two are separable concerns currently bundled in one function whose name (`walkCascadeForInvalidatedNode`) references retired vocabulary.

### What would need to happen

1. **Split the function — superseded by (2).** The deletion in (2) made the split moot: the function reduces to just the upstream-refresh pull, so there is no second concern to extract.
2. **Delete the downward synthetic-signal walk from both call sites — DONE (2026-06-18).** The synthetic-signal cascade and the `invalidationCascadeSignal` constant are gone; `walkCascadeForInvalidatedNode` was renamed to `pullUpstreamRefreshesForNode` and the file moved to `code:lib/runtime/upstream_refresh_pull.go`. The wake_parked.go call site now invokes the renamed function unchanged in role (upstream-refresh pull only). The recursive call from `stalemarkAndEnqueueInFrame` is preserved — it now serves chained upstream-refresh propagation when an upstream itself has `force_upstream_refresh: true` edges. The previously-failing test at `code:test/scenarios/parked_resume_spurious_cascade_test.go::TestParkedResumeDoesNotSpuriouslyCascadeSuccessSubscriberOnError` now passes — converted from a documented-bug record into regression protection. Verified: `go test ./test/scenarios/ -run TestParkedResumeDoesNotSpuriouslyCascadeSuccessSubscriberOnError -count=3 -race` clean; full `go test ./...` clean (modulo pre-existing testcontainer flakes under Docker load and a pre-existing `TestSubstitutionDocstringMatchesResolver` failure unrelated to this change); race-sensitive `lib/runtime/...` + `lib/foundation/persistence/postgres/...` + `lib/graph/scheduler/...` under `-race -count=3` clean.
3. **Reconsider the parked-resume call site (B above).** Once (2) lands, the only remaining work the function does at the parked-resume site is the upward `pullForceRefreshUpstreams` pass for the resumed node. A focused scenario test should establish whether anything depends on that pass running at resume time rather than at re-dispatch time; if not, the call site itself goes away.
4. **Retire the residual naming — DONE for the function/file/constant (2026-06-18).** Function renamed `walkCascadeForInvalidatedNode` → `pullUpstreamRefreshesForNode`, file renamed `cascade_invalidate.go` → `upstream_refresh_pull.go`, constant `invalidationCascadeSignal` deleted (had no remaining consumer). Remaining residue: the synthetic payload tag `"invalidation_cascade"` is gone with the constant; the only call-out left to retired vocabulary lives in test assertion messages that historically reference the old name, kept intentionally as a "what this test protects against" cue.

### Same shape as

The original fan-out finding — a general primitive operationalized one specific way during one reshape, with the wiring outliving the reason that made it specific.

---

## 2. Message is the only trigger (cluster B)

**Surfaced**: 2026-06-18 sweep agent reports (concept-vs-code asymmetry hunt + special-case branch hunt).

### What's there

`concept:frame` describes a frame as one cascade resolution, with operator-, publisher-, and cascade-emitted triggers all converging on the same delivery path. The schema and code disagree across four interlocking sites:

- **Frame requires `MessageRow`.** `code:lib/graph/frame/producer.go::EnqueueFrame#16` requires `(instanceID, triggeringMessageID)` (NOT NULL). `concept:message-emitter-node` synthesizes a full `persistence.MessageRow` (idempotency UUID, JSON-marshaled attrs, sender `instance:<id>`) at `code:lib/runtime/runner_emit_message.go::emitCascadeMessageInTx#89` before opening a frame.
- **`messages.*` and `nodes.*` substitution grammars run on parallel pipelines.** `concept:message` documents them as sugar ("the only difference is a registration-time check that `<type>` is declared in `messages:`"). The code has: separate parser (`code:lib/graph/node/subscription_edges.go::parseMessageDirective#514` vs `parseSubstitutionDirective#653`), separate extractor (`ExtractMessageRefsFromTemplate` vs `ExtractSubstitutionRefsFromTemplate`), separate validator loop, separate resolver arm (`code:lib/graph/attribute/substitution.go::resolveMessagesValue#218` carries a `RegistryDeclaredTypes` gate that `resolveNodesValue` lacks), and an extra `"*"` super-wildcard fallback in `coverageMatch`'s `case "message"` that `case "attribute"` doesn't have (`code:lib/graph/node/template_validator.go::coverageMatch#734`).
- **`EmitsMessage != ""` welds message-emit onto the uniform pipeline as a kind-of-its-own.** `code:lib/runtime/runner_dispatch.go::dispatch#103` short-circuits with a synthetic `terminalKindComplete` before any executor RPC. `code:lib/runtime/runner_terminal.go::applyTerminalComplete#132` injects an `emitCascadeMessageInTx` side-effect into the otherwise uniform "release locks, write attrs, emit signal" trunk. `code:lib/graph/scheduler/pure_cascade.go::ProcessPureCascade#44` lumps `isEmitMessage(def)` with `hasClaimStore(def)` into a separate `enqueueNativeClaimOnly` path. A panic guards against the combinatoric case `EmitsMessage != "" && IsSubgraphEntryAbsorbed`.
- **`sender_kind` ingress is asymmetric.** `proto:message.proto::sender_kind` accepts three values (`operator | publisher | instance`). `code:lib/runtime/message_delivery.go::EnqueueMessage#36` accepts all three uniformly. `code:lib/control/controlapi/messages.go::handleCreateMessage#126` rejects `instance` at the HTTP boundary and special-cases `publisher` with a subscription-validation gate. `dedupSenderKind#40` silently collapses anything non-publisher into `operator|anonymous`. `code:lib/runtime/runner_emit_message.go::emitCascadeMessageInTx#54` is the sole producer of `instance` and hardcodes it.

### Why suspect

Frame-as-cascade-unit is the general primitive `concept:frame` describes, but the schema makes a `MessageRow` the only legal trigger — so anything wanting its own cascade unit (operator "rerun this subgraph", a periodic invalidate, a claim-release wave) must masquerade as a message and pay its idempotency/payload/audit cost. The two substitution grammars are documented as one thing with one difference; the code is two pipelines that have to be kept in sync by hand. The `EmitsMessage` welds bolt special-case logic onto otherwise kind-agnostic stages; the panic guarding the combinatoric case is the smell of compounding bolt-ons. `sender_kind` advertises a uniform ledger; the ingress paths advertise three different routes.

### Walked 2026-06-18 — decisions

Walked all four sub-findings in conversation against the message-schema and empty-message-wake specs in `.ok-planner/history/specs/`. Resulting verdicts:

1. **Frame requires `MessageRow`** — RETIRED as not-a-defect. The non-null `triggering_message_id` is the design's load-bearing invariant per the message-schema spec (`2026-06-14-message-schema-layer-design`) and `concept:frame`: every frame's origin must be answerable from the database via a real message row, and no internal-runtime path is allowed to create a frame. The original complaint that cascade-units have to "masquerade as messages" inverts the design — under the model the spec landed, operator-rerun, periodic recheck, and claim-release waves ARE messages (typed via a template-declared type, or empty-bodied for whole-instance wake), not masquerades. The `*shared.UUID` form I cited in the prior re-evaluation as "schema shifting toward nullable" is actually inside `FrameListFilter` (a query parameter where `nil` = no filter on this field), not a schema relaxation. Producer path remains non-pointer at `code:lib/graph/frame/producer.go::EnqueueFrame#17`; schema remains non-null. No remediation needed.

2. **`messages.*` and `nodes.*` parallel pipelines** — REAL. Remediation: the messages-form directive becomes lexical sugar resolved at template registration time. One parser, one reference type, one resolver function, one registry lookup branched only on the kind-check (is this name declared as a message-virtual node or as a real node-type?). The shared `ctx.Deps[<type>]` resolver at the bottom stays; the parallel parse / extract / validate surfaces above it go away. Template-author wire form standardizes on `{{nodes.<type>.attribute.<field>}}`; the messages-form gets rewritten to the nodes-form at registration with a registration-time kind-check, or fails registration with a clear error citing the missing declaration.

3. **`EmitsMessage` welded onto dispatch+terminal+scheduler** — REAL. The design choice "emit-message is a special node-kind, not a per-node capability" stands (the message-schema spec considered the per-node-capability alternative and rejected it on aggregation-naturalness and graph-object-visibility grounds). What changes is only the implementation: emit-message becomes a built-in in-process executor — a utility node whose effect is "build envelope from my resolved attributes, insert into the ledger, return terminal/success." The in-proc executor pattern at `code:lib/runtime/executor/builtin/attribute_passthrough/` is the template. Once emit-message is registered alongside it:
   - The dispatch carve-out at `code:lib/runtime/runner_dispatch.go#103` deletes
   - The terminal-resolution side-effect at `code:lib/runtime/runner_terminal.go#167` deletes
   - The scheduler branch in `code:lib/graph/scheduler/pure_cascade.go::ProcessPureCascade` (lumping emit-message with claim-acquiring nodes) deletes
   - The combinatoric panic guard at `code:lib/runtime/runner_terminal.go#132` deletes

   The `emits_message:` template-DSL field stays as the declaration surface; only the runtime wiring changes.

4. **`sender_kind` ingress asymmetry** — REAL. Remediation: drop `sender_kind` from the wire envelope entirely. The auth IS the discriminator — operator API key (identity-based, broad permissions) vs publisher-subscription capability (capability-based, narrow permissions, includes message-type validation as a side effect of the subscription check) vs internal cascade-emit (no wire ingress, runtime-managed). The HTTP handler stamps the persistence row's `sender_kind` based on which auth path the request came in through. The persistence column keeps the three values for audit/observability; `instance` stays as a runtime-internal marker stamped by the cascade-emit path on rows it writes directly to the ledger, never appearing on the wire. Open: one URL with auth-based dispatch, or two URLs (one per sender kind, with the route making the kind explicit) — both eliminate the wire redundancy; the choice is between uniform-URL or explicit-route.

### Bottom line

Three real, one retired. The three each have a clear remediation shape; the wire and the runtime trunks both narrow significantly when they land. None of the three is blocked on design work — the design intent is settled across the message-schema and empty-message-wake specs in `.ok-planner/history/specs/`; what's needed is the implementation sweep.

### Implementation — DONE 2026-06-19

Sub-finding 4 (`sender_kind` wire envelope):
- `sender_kind` field dropped from `code:lib/control/controlapi/messages.go::postMessageRequest` (the wire envelope).
- HTTP handler derives sender_kind from the request shape: `publisher_subscription_id` set → "publisher"; otherwise → "operator". Wire validation of sender_kind removed.
- Persistence column keeps three values (operator | publisher | instance) for audit/observability; `instance` stays as a runtime-internal marker stamped by `code:lib/runtime/runner_emit_message.go::emitCascadeMessage` on rows the cascade-emit path writes directly to the ledger, never on the wire.
- Four bundled sensors (`code:lib/services/sensors/sensor-{http,webhook,object-store,cron}/sensor.go`) stopped sending sender_kind in their POST envelopes.
- MCP tool schema for `message_send` lost the sender_kind property.
- Tests updated to drop the field from POST bodies; tests asserting body's sender_kind rewritten to assert `publisher_subscription_id` presence. Two test functions retired (the cases they covered — "you said publisher but forgot the ID" and "you sent an invalid sender_kind enum value" — no longer exist).

Sub-finding 3 (`EmitsMessage` welded onto dispatch+terminal+scheduler):
- New built-in in-process executor at `code:lib/runtime/executor/builtin/emit_message/`; alias `rimsky.emit_message`; in-proc URL `inproc://emit_message`.
- `HandlerContext` extended with `EmitCascadeMessage func(ctx, body) (id, replayed, err)` callback. `HandlerContextFactory` signature takes ctx (added) so per-dispatch info attached via `executor.DispatchExtras` can drive the callback binding.
- `runner_dispatch.go` attaches `DispatchExtras` (instance_id, node_id, frame_id, emit-message-type) to ctx before every executor call; the supervisor's factory reads them and binds the callback when emit-message-type is non-empty.
- Carve-outs deleted: `runner_dispatch.go` short-circuit (was at ~line 103); `runner_terminal.go` emit-side-effect (was at ~line 167) and combinatoric panic guard (was at ~line 132); `pure_cascade.go` `isEmitMessage` branch in `ProcessPureCascade` and the `EmitMessageDispatchName` executor-name assignment.
- New canonicalization at `code:lib/graph/node/kind_resolver.go::CanonicalizeEmitMessageSugar` — at template registration (post-validation), translates `emits_message: T` to also set `executor: rimsky.emit_message` (the `emits_message` field stays as the message-type lookup). The validator's "executor and emits_message mutually exclusive" check still rejects author-supplied combinations because validation runs before canonicalization.
- SQL `@emit-message` fallback in `AffirmNodeRunRow` (sqlite + postgres) dropped — `n.executor` is now always the source of truth.
- `emitCascadeMessageInTx` refactored to take `body []byte`; non-tx wrapper `emitCascadeMessage` added for the executor callback.
- `EmitMessageDispatchName` constant and the supervisor's special-case `accepted = append(accepted, EmitMessageDispatchName)` deleted.

Sub-finding 2 (`messages.*` and `nodes.*` parallel pipelines):
- Resolver unified: `code:lib/graph/attribute/substitution.go::resolveSubstitutionValue` handles both `nodes.X.attribute.Y` and `messages.X.Y` prefixes. Switch case `"nodes", "messages":` routes both to it. The two prior resolver functions deleted.
- Parser unified: ONE `parseSubstitutionDirective` recognizing both prefixes, producing one ref shape. The old `parseMessageDirective` deleted.
- Extractor unified: ONE `parseSubstitutionRefsFromAttributes` doing one walk over attributes/stores/locks/fan_out and producing the unified ref list. The two old scan loops collapsed to one.
- Ref types unified: `substitutionRef` now carries `Prefix`, `TypeName`, `FieldPath`, `TopicKind`, `RefLiteral`, `AttributeProperty`. `messageRef` is a type alias for `substitutionRef`. The validator's two passes now consume filtered views (by prefix) of the same underlying list; each pass keeps its appropriate kind-check.
- `ExtractMessageRefsFromTemplate` and `ExtractSubstitutionRefsFromTemplate` retained as filter functions over the unified extract — they call the same `parseSubstitutionRefsFromAttributes` and filter by `Prefix`. One walk, one parser, one ref shape underneath.
- `BuildSubscriptionEdges` signature simplified: dropped the unused `substitutionRefs` parameter (the body had `_ = substitutionRefs`); takes only `messageRefs` now. Three callers (message_delivery, subscription_loaders, pure_cascade) + two test-support helpers stopped extracting the dead-code substitution-refs list.
- Validator field accesses renamed for the new ref shape: `ref.MessageType` and `ref.SenderNodeType` → `ref.TypeName`; `ref.Field` and `ref.Name` → `ref.FieldPath`.

Sub-finding 1 stays retired as discussed above; no code change.

### Same shape as

The original fan-out finding (general primitive operationalized through one specific surface when the concept describes it as general). The frame piece is the deepest reshape — touches the foundational primitive.

---

## 3. SplitScope (sub-claims) wired only via fan-out (cluster C)

**Status — RESOLVED 2026-06-19.** Both threads of the cluster closed in one sweep. The "SplitScope-as-general-primitive-trapped-behind-fan-out" framing was pressure-tested away: the only theoretically reachable second consumers (multi-sub-claim acquisition without consuming them, delegation-with-sub-claim, single-partition selection) collapse into existing affordances — narrowed claims acquired via selector-grammar substitution per `decision:substitution-grammar-closed`, fan-out-as-subgraph-entry for delegation, atomicity-of-N which only fan-out needs. The validator's call-site `code:lib/runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared` is defensible as-is; the only architectural residue that survived (the validator's error-message wording) folded into the rename below. The `stores` → `claim-producers` rename then landed across every remaining surface — Go symbols/types/helpers, the scheduler predicate, HTTP routes, the proto field, internal directory layout, Docker image names, and documentation. Verified: `make build-all`, `make lint`, and `make test-all` all clean (exit 0) across all four Go modules. Archived `.ok-planner/` records intentionally left untouched per the project-records rule.

**Surfaced**: 2026-06-18 sweep agent reports.

### What's there

- `concept:claim` describes sub-claims as first-class claims that "resolve identically to those over a regular claim." `concept:claim-producer` describes SplitScope as an optional producer capability.
- Persistence supports sub-claims as a general primitive: self-referential `ParentClaimHandleID` on `table:rimsky_claim_handle`.
- The protocol verb is general: `proto:claim_producer.proto::ClaimProducer.AcquireSubClaims`.
- The only template surface that triggers SplitScope is `fan_out:`. `code:lib/runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared#23-48` is the only call site, gated on `nodeDef.FanOut != nil`. The template validator at `code:lib/graph/node/template_validator_holds.go#103-111` emits the message `fan_out requires store ... supports_split_scope`, framing the protocol verb as a fan-out feature.
- Half-finished `stores` → `claim-producers` rename reinforces the smell. Top-level YAML `stores:` is rejected (`code:lib/control/config/stores.go::LoadRimskyConfigYAML#237`), but the rename hasn't propagated:
  - Per-node template field: `field:lib/foundation/spec/template.go::TemplateNodeDef.Stores` (yaml/json tag `"stores"`).
  - HTTP discovery surface: `route:GET /v1/observability/stores` and `route:GET /v1/observability/stores/{name}` (`code:lib/control/observability/handler.go::registerRoutes#38-39`).
  - Scheduler predicate: `code:lib/graph/scheduler/pure_cascade.go::hasClaimStore#244` — load-bearing as the cascade scheduler's "is this a claim-acquiring node" check, expressed in retired-alias terms.
  - Per-package `StoreEntry` types in `code:lib/control/config/stores.go::StoreEntry#54` and friends.

### Why suspect

Delegation-with-sub-claim and non-dispatch sub-claim derivation for substitution are both unreachable from any template, despite every persistence and protocol piece supporting them. Template authors can't compose patterns the underlying machinery already supports. The `stores` rename being half-done means operators see `claim_producers:` in one place and `stores` in another for the same noun; the scheduler's `hasClaimStore` predicate makes the divergence load-bearing, not just a naming inconsistency.

### Remediation shape

- **Surface SplitScope in template forms beyond `fan_out:` — RETIRED as not-a-defect (2026-06-19).** Pressure-tested in conversation: no second consumer survives scrutiny. "Narrow without dispatch" is an unbound iterator (acquired sub-claims that nothing reads or dispatches over are not a pattern). "Delegation-with-sub-claim" collapses into either fan-out-as-subgraph-entry or a delegate declaring its own narrower claim via selector-grammar substitution. Single-partition selection is covered by the producer's selector grammar at acquisition time per `decision:substitution-grammar-closed`. SplitScope's actual value-add over selector grammar is atomicity-of-N per invariant 10, and only fan-out needs that atomicity. The validator's call-site naming (`acquireFanOutIfDeclared`) is correct: fan-out IS its sole producer of sub-claims.
- **Finish the `stores` → `claim-producers` rename — DONE (2026-06-19).** Eradicated old vocabulary from every non-historical surface:
  - Validator: hook field `StoreAdvertisesSplitScope` → `ClaimProducerAdvertisesSplitScope` (5 sites); error message `fan_out requires store ...` → `... claim_producer ...`.
  - Template DSL: `field:lib/foundation/spec/template.go::TemplateNodeDef.Stores` → `.ClaimProducers` (yaml/json tag `stores` → `claim_producers`); every read across `lib/graph/node/`, `lib/graph/scheduler/`, `lib/runtime/`, test harness, scenario fixtures, and example YAML templates.
  - Types: `NodeStoreRef` → `NodeClaimProducerRef`, `StoreEntry` → `ClaimProducerEntry`, `RemoteStoresConfig` → `RemoteClaimProducersConfig`, `proto:executor.proto::StoreHandle` → `ClaimProducerHandle`.
  - Helpers: `validateStoreEntry`, `probeStoreEntry`, `mergeStoresOnAbsorb`, `WithStores`, `withStores`, `storeRefToJSON`, `buildStoreHandles`, `ListStores`, `SetStore` → claim-producer variants.
  - Scheduler predicate: `code:lib/graph/scheduler/pure_cascade.go::hasClaimStore` → `acquiresClaims`.
  - HTTP routes: `route:GET /v1/observability/stores`, `route:GET /v1/observability/stores/{name}` → `/claim-producers`, `/claim-producers/{name}`; `handleListStores` → `handleListClaimProducers`, `handleGetStore` → `handleGetClaimProducer`; response JSON key + observability `Deps` field renamed.
  - File: `lib/control/config/stores.go` → `lib/control/config/claim_producers.go`.
  - Deprecated-key detector: field renamed `RetiredStoresKey` while keeping `yaml:"stores"` intact so the rejection of legacy top-level `stores:` config still fires (`code:lib/control/config/claim_producers.go#223`).
  - Directories: `lib/services/stores/` → `lib/services/claim_producers/`, `test/support/stores/` → `test/support/claim_producers/`, `test/scenarios/stores/` → `test/scenarios/claim_producers/`, `lib/services/test/scenarios/stores/` → `lib/services/test/scenarios/claim_producers/`, `test/scenarios/claim_stores/` → `test/scenarios/claim_handle_aggregate/`.
  - Docker images: `rimsky-store-filesystem`, `rimsky-store-postgres` → `rimsky-claim-producer-filesystem`, `rimsky-claim-producer-postgres` (Makefile, `file:.github/workflows/ci.yml`, in-repo test harness consts at `code:lib/services/test/harness/store_filesystem.go` and `code:lib/services/test/harness/store_postgres.go`).
  - Proto: `proto:executor.proto::ExecuteRequest.stores` map field → `claim_producers`; `make proto-gen` re-ran clean. Source comments referencing the retired invariant-tag form were also scrubbed during the same pass (separate sub-thread surfaced and resolved while walking the rename).
  - Documentation: `file:CLAUDE.md`, `file:RELEASING.md`, `file:feature-index.md`, `file:.golangci.yml` depguard `files:`/`pkg:` allowlists and comment prose. Archived records under `.ok-planner/{archive,history,plans,specs}/` intentionally left untouched (committed point-in-time records per `.ok-planner/CLAUDE.md`).

### Same shape as

Originally framed as "the original fan-out finding (exact instance of the pattern — a general capability trapped behind one specific trigger)." On closer look, only half of that framing held: SplitScope's protocol-and-persistence shape IS general, but fan-out's atomicity-of-N requirement IS the specific reason that generality matters, and no other template-author affordance needs it. The pattern that DID hold across the cluster was the half-finished vocabulary sweep — a rename started at the top-level config layer that never propagated to the per-node template DSL, the HTTP discovery surface, the scheduler predicate, the proto field, the directory layout, or the image names. That's the shape worth pattern-matching on for future sweep audits: not "general primitive trapped behind specific trigger," but "rename started at one layer and stalled before it touched its load-bearing companions."

---

## 4. SettleChildren — honest parallelism wearing a misleading disguise (cluster D)

**Surfaced**: 2026-06-18 sweep agent reports. **Re-examined and resolved 2026-06-19** with concept docs and the full settlement chain open; original framing didn't survive verification. Presentational refactor below was completed in-session: code split, enum narrowed, concept docs aligned. Build clean for the touched packages and tests passing; the full-tree `make lint` failure is pre-existing from in-flight item-5 work (`publisher-subscription.target_node` removal), not introduced by this refactor.

### What the original framing got wrong

1. **"Six sites reinvent kind discrimination"** — overstated. Of the six, one is genuine kind-distinct logic (`code:lib/runtime/runner_terminal.go::cascadeSubscribersStaleInTx` main carve-out, where main-scope cascades match all receivers and non-main cascades constrain to in-flight runs). The other five are invariant guards (`settleCarryVerbatim`'s `ParentRunID == nil` reject enforces the documented carry-verbatim-is-delegation-only invariant), structural-absence handling (you can't propagate to a non-existent parent), or plain field readers (`resolveAcqScopeInTx` is a two-field getter, not kind-aware logic). And `concept:run-scope` explicitly says **"Kind is derivable, not stored"** — the structural-discrimination pattern is the documented design, not residue. The earlier "add a `Kind` column" remediation reverses a documented decision; a derived helper `RunScope.Kind() RunScopeKind` would suffice for the one site that benefits, but the wholesale "every site needs this" framing is wrong.

2. **"Two parallel settlement implementations as residue"** — misread. `settleCarryVerbatim` and `settleClaimChainAggregate` handle structurally distinct invocation patterns (delegation's one-child attribute carry vs fan-out's many-children claim-chain settlement). The parallelism is load-bearing.

3. **"`Aggregate` switch isn't exhaustive over the aggregation policy enum"** — misread. Carry-verbatim doesn't participate in state-propagation aggregation by construction (it closes the subgraph scope synchronously inside settlement, never entering state propagation). `Aggregate` is correctly exhaustive over the kinds that DO participate.

### What's actually there

`code:lib/runtime/child_execution.go::SettleChildren#154` is a single entry point that dispatches on `Policy.Kind` to two unrelated settlement operations:

- `Policy.Kind == carry_verbatim` → `settleCarryVerbatim` (delegation: one child, attribute writeback to parent run, close subgraph scope, fire cascade).
- `Policy.Kind` anything else → `settleClaimChainAggregate` (fan-out: per-child counter bookkeeping on the parent claim, possibly cancel siblings, settle parent claim's terminal once all children resolve).

`ChildSettlementInput` is a tagged union — half the fields meaningful only for delegation calls (`ExitRunID`, `ExitNodeID`, `ExitNodeAlias`, `Writeback`), the other half only for fan-out calls (`ParentClaimHandleID`, `ChildClaimHandleID`, `ChildOutcome`, `ChildProducerMetadata`). The `Policy` field is the discriminator, but the dispatch is the only consumer — neither sub-function reads `in.Policy` after the dispatch.

`AggregationKindCarryVerbatim` is the routing tag that makes this work. It's a runtime artifact, not an author-facing aggregation policy:

- Authors write `delegate: <graph>` for delegation — no policy knob; runtime stamps `Policy.Kind = CarryVerbatim` on its own.
- Authors write `fan_out: { error_policy: ... }` for fan-out — the policy is an author choice of `strict | threshold | best_effort | first`.

The five-value aggregation policy enum is really **two settlement modes** plus **one of those modes (fan-out aggregate) has a four-value author-configurable policy**. `concept:child-execution`'s "one settlement path with aggregation policy" papers over the distinction.

### Why suspect

1. **The presentation lies about the underlying structure.** "Two settlement modes implicit in invocation pattern" reads as "one primitive with a policy option" through the `SettleChildren` + tagged-union packaging. A future maintainer reading the code has to figure out which half of the input struct they're supposed to fill, and what the relationship is between the dispatcher's `Policy.Kind` switch and the rest of the runtime's policy reads.

2. **`carry_verbatim` lives in the wrong enum.** It's not an aggregation policy from the author's perspective — it's a settlement-mode marker stamped by the runtime to drive the dispatch in `SettleChildren`. Concept doc lists five "kinds"; only four are author-facing.

### Remediation — done

Presentational refactor — runtime behavior stays put.

**Code:**
- `SettleChildren` split into `SettleFromDelegate` (delegation path) and `SettleFromFanoutChild` (fan-out per-child path) in `code:lib/runtime/child_execution.go`. Bodies are what `settleCarryVerbatim` and `settleClaimChainAggregate` already were, promoted to public.
- `ChildSettlementInput` split into `DelegateSettlementInput` (Writeback + Exit* fields) and `FanoutChildSettlementInput` (ParentClaimHandleID + Child* fields). No `Policy` field in either — the dispatch was the only consumer.
- Production call sites updated: `applyTerminalCompleteSubgraphExit` (`code:lib/runtime/subgraph_dispatch.go`) calls `SettleFromDelegate`; `ResolveClaimHandleTerminal` (`code:lib/runtime/terminal_decision.go`) calls `SettleFromFanoutChild`. Test caller in `code:test/scenarios/subgraph/exit_carry_rule_test.go` updated.
- `AggregationKindCarryVerbatim` removed from `code:lib/foundation/spec/aggregation_policy.go`. `AggregationPolicy.Validate()` simplified to the four-kind family. Persisted policy values on claim-handle and run-tree rows were already four-valued in practice (delegation never writes a claim or stamps a run-tree policy).

**Concept docs** (updated in-session at user direction):
- `concept:child-execution` — Definition rewritten to describe two settlement modes (carry, aggregate) instead of "one settlement path"; Purpose reframed accordingly; Boundaries attributes the aggregation policy to `concept:fan-out`; Invariants reworded to reference the carry mode rather than the carry-verbatim policy kind.
- `concept:fan-out` — Definition rewritten to name the aggregate settlement mode and the four-value aggregation policy family explicitly; clarifies fan-out is the policy's only consumer.
- `concept:delegation` — Definition rewritten to name the carry settlement mode; closing line clarifies delegation does not involve an aggregation policy.
- `concepts.md` TOC entries for `delegation` and `fan-out` realigned with the updated bodies (one-line summaries).

### Separately related, not part of this refactor

`code:lib/runtime/run_tree.go::Aggregate` (per-child run states, called from state propagation on every child transition) and `code:lib/runtime/child_execution.go::aggregateParentOutcome` (claim-handle counters, called once at final settlement) duplicate the same four-kind policy semantics over different input shapes at different lifecycle moments. Real algorithmic duplication. The cleanest unification would be a "children summary" abstraction both callers compute on demand, but the lifecycle gap (every-transition vs final-only) may justify keeping them separate. Question left open.

### Same shape as

`concept:child-execution`'s "one settlement path" was an aspirational unification papered over two distinct invocation patterns. The disguise — single entry point + tagged-union input + policy-kind dispatch — kept the parallelism honest at runtime but invisible in the source. Refactor strips the disguise.

---

## 5. Publisher-subscription `target_node` is dead routing

**Status — RESOLVED 2026-06-19.** Field ripped from the wire, persistence, validator, sensors, and conformance, and the proto comment's misleading "used by rimsky for subscription routing only" claim is gone with it. Delivery now does what the runtime always actually did: route by `messages.type` against node-subscription edges, no routing decision derived from publisher-subscription metadata. Concept doc `concept:publisher-subscription` updated to drop the `target_node` invariant + the "(target_node, message_type)" inline-routing claim. Pre-v1 schema migration `014-drop-publisher-target-node.sql` added in both backends. Bundled-sensor state DBs (cron, http, webhook, object-store) drop the column via `ALTER TABLE … DROP COLUMN IF EXISTS target_node` at bootstrap. One incidental bug fixed in the same pass: `code:lib/protocols/conformance/claimproducer/serialization9b.go::checkSerialization9b` had its error message reworded last session in a way that dropped the "9b" identifier the operator-facing assertion at `code:lib/services/test/scenarios/conformance_9b/probe_test.go#42` was matching against; restored the slug as plain prose ("violates invariant 9b") so the test passes again without re-introducing the retired invariant-tag form. Verified: `go build ./...`, `make lint`, and `go test ./...` clean across root + foundation + protocols (services testcontainer failures pre-existing and image-build-dependent, unrelated).

**Surfaced**: 2026-06-18 sweep agent reports (singleton, not part of any cluster).

### What's there

- `concept:publisher-subscription` describes the subscription as carrying inline routing fields (`target_node`, `message_type`), with `target_node` load-bearing for delivery.
- `target_node` is NOT NULL at validation: `code:lib/graph/node/template_validator.go::ValidatePublisherSpec#1967`.
- Persisted on the row; shipped over the wire via `proto:publisher.proto::SubscribeRequest#5`.
- At delivery, `code:lib/runtime/message_delivery.go::cascadeMessageVirtualNodeSettleInTx#199` resolves receivers solely from `edges.Match(msg.Type, "terminal/success")` against `concept:node-subscription` edges. Nothing reads `target_node`.

### Why suspect

A routing field is required of every publisher subscription while the runtime routes purely by `message.type` against node-subscription edges. Authors are forced to pin a single receiver up front; one publisher subscription cannot feed multiple receivers, even though the subscription-walk-as-virtual-node delivery model already supports it.

### Remediation shape

Drop `target_node` from `proto:publisher.proto::SubscribeRequest`, the persistence row, and the validator. Delivery already routes by `message.type` + edge matching, which handles fan-out to multiple receivers naturally. Operators who want a single-receiver constraint express it via a node subscription's `when:` predicate.

---

## 6. Lifecycle-subscriber excludes publishers

**Status — RESOLVED 2026-06-19.** Root cause was a slot/protocol conflation in `code:lib/control/controlapi/lifecycle.go::peersReferencedBySpec`: the peer-walker enumerated by template slot (claim-producer + executor slots on each node), which structurally excluded any service referenced only in the template's `publishers:` block — including sensors, which are a `concept:publisher` subclass. The slot a service appears in is orthogonal to which protocols it implements; a service is what its protocol list declares it to be, not what slot the template happens to use it in. Fixed by extending the walk to also union service names from `spec.Publishers`. `code:lib/runtime/lifecycle_fanout.go::FanOutRunScopeEvent` inherits the corrected list via its `peersForSpec` callback — no changes needed there. The protocol-aware filter that decides "is this name actually a lifecycle subscriber?" already lived downstream at `code:lib/runtime/lifecycle_fanout.go#49` (`lifecycleSubs.Get(name)`), so the walker correctly stays slot-agnostic and just returns "every service this template references anywhere." Regression locked down by `code:lib/control/controlapi/lifecycle_test.go::TestPeersReferencedBySpec_IncludesPublishers`. Verified: `go test ./lib/control/controlapi/... -count=1` clean.

**Surfaced**: 2026-06-18 sweep agent reports.

### What was there

- `concept:lifecycle-subscriber` describes lifecycle as an opt-in peer protocol — any peer service can declare it in its protocol list. The doc's "Adjacent" list under-committed (named only `claim-producer`, not `publisher` / `executor` / `sensor`), but the "What it is" is generic ("opt-in per service").
- `peersReferencedBySpec` enumerated peers by walking each node's `ClaimProducers` and `Executor` slots only — never `spec.Publishers`. A sensor declared in the publishers block, even one that subscribed to `lifecycle-subscriber`, was silently never called.

### Why it was a bug

The slot a service appears in (claim-producer slot / executor slot / publishers block) describes how the template *uses* the service. The protocol list describes what the service *can do*. The walker conflated the two: it asked "which slots is this service in?" when the right question is "is this service referenced anywhere in this template?" The downstream subscriber-registry check (`lifecycleSubs.Get(name)`) already provides the protocol-aware filter, so the walker had no business pre-filtering by slot.

### Concept-doc alignment (done in-session at user direction)

`concept:lifecycle-subscriber` updated to remove the claim-producer-archetypes-everything framing:

- "What it is" generalized: the secondary-protocol pairing now names all three peer kinds (`concept:claim-producer`, `concept:executor`, `concept:publisher`) and explicitly states slot-vs-protocol orthogonality.
- "Purpose" recast: claim-producer DDL-on-deploy demoted from "the archetype" to "one archetype," with executor cache-warming and publisher substrate-provisioning called out as equally legitimate.
- "Adjacent" list grew to include `executor`, `publisher`, and `sensor` alongside `claim-producer`.

---

## 7. Write-semantics 4-value enum collapses to 2 at the conflict gate

**Status — RESOLVED 2026-06-19.** Neither of the original remediation forks was right. The four values are genuinely needed — by the producer's capability advertisement, by the operator's `write_semantics_allowed` narrowing, by the conformance suite (each value has a distinct contract), and by telemetry. The **gate** is the one consumer whose question is binary ("does this producer support reader×writer concurrency on this scope?" — i.e., MVCC pass-through, yes or no). The byte-equal-scope uniformity invariant means holder and candidate at the gate share one realized value; the gate's input is one semantics, not two. The fix was to align the gate's *surface* with what it actually consumes — narrow the function, not the enum.

### Code changes

- `code:lib/foundation/locks/conflict.go` — `ModeCoexists` narrowed to `(intentA, intentB, sem)`. The cross-semantics branch (`if syncA != syncB { return true }`) is gone — its input is no longer expressible. `isSync` renamed to `mvccPassThrough` (the actually-asked question); silent fallback on unknown values replaced with a `panic` (the supervisor rejects `UNKNOWN` upstream — reaching the default means a fifth enum value was added without updating this switch, which is a contract bug that should fail loud).
- `code:lib/foundation/locks/conflict_test.go` — `TestModeCoexistsMatrix` rewritten as per-value (intentA × intentB) sub-matrix; `TestModeCoexistsCrossQuadrant` **deleted** (its input was in undefined territory per byte-equal-scope uniformity, and is now uncompileable under the narrowed signature); `TestModeCoexistsSymmetric` narrowed to iterate `intent × intent × sem` instead of the cross-sem product.
- `code:lib/runtime/runner_acquire_claims.go#179` — call site updated to pass `(spec.Intent, holderIntent, holderRWS)`; no more double-pass of `holderRWS`.

### Concept-doc updates

`concept:write-semantics` per-value section rewritten. The cross-semantics coexistence claims ("the coexistence predicate returns false for sync↔sync, sync↔staged_async, sync↔blocking_async") are gone — those described a region the byte-equal-scope uniformity invariant says cannot occur. Each value now describes its own (holder-intent × candidate-intent) sub-matrix, with a leading sentence stating the no-cross-value rule explicitly. The stale `(invariant 9b)` numeric reference in the Invariants section was also dropped.

### Verified

- `go build ./...` exit 0.
- `go test ./...` — all packages pass.
- `make lint` clean.

---

## 8. Terminal-decision Outcome × Cause product flattened to a single enum

**Status — RESOLVED 2026-06-19.** Sketch's "Outcome × Cause as a product type" framing overstated what `concept:terminal-resolution` actually claims — the doc doesn't elevate `Cause` to a load-bearing axis; it's a forensics/lineage field. But the underlying asymmetry was real: the type permitted six combinations (2 outcomes × 3 causes), only four of which were semantically meaningful. `Commit + SiblingCancel` and `Commit + DescendantCancel` were nonsense the type allowed; `Natural` was a "no special cause" placeholder doubling as a real cause value.

The clean fix wasn't a Go-faked sum type (the sketch's proposed remediation). It was flattening to a single four-value enum:

- `OutcomeCommit`
- `OutcomeAbandon`
- `OutcomeAbandonSiblingCancel`
- `OutcomeAbandonDescendantCancel`

The `Natural` value disappeared as a wart. The two prior types (`AggregateOutcome`, `TerminalCause`) collapsed into one (`TerminalOutcome`). The `Cause` field was deleted from `TerminalDecision`. A `(o TerminalOutcome) IsAbandon() bool` helper handles the common "any abandon?" check. A `(o TerminalOutcome) CauseString() string` helper preserves the wire-level cause strings ("natural" / "sibling_cancel" / "descendant_cancel") that the lineage ledger and event payload consumers expect.

### Code changes

- `code:lib/runtime/terminal_decision.go` — types replaced. `TerminalDecision.Outcome` typed `TerminalOutcome`; `Cause` field removed.
- `code:lib/runtime/terminal_decision_forensics.go` — `terminalOutcomeKey` collapsed to a single switch on outcome; `emitTerminalForensics`'s `if Abandon { switch Cause }` collapsed into outcome-driven branches; `outcomeVerbName` uses `IsAbandon()`.
- `code:lib/runtime/terminal_decision_cancel.go` — `cancelInFlightSiblings` and `cancelDescendantClaims` set the appropriate `OutcomeAbandonSiblingCancel` / `OutcomeAbandonDescendantCancel` directly; no separate `Cause:` field needed.
- `code:lib/runtime/child_execution.go`, `code:lib/runtime/auto_terminal.go`, `code:lib/runtime/runner_terminal_release.go`, `code:lib/runtime/runner_acquire_postcommit.go` — constant renames; `== AggregateAbandon` comparisons that meant "any abandon" became `.IsAbandon()`.
- Tests swept: `code:lib/runtime/terminal_decision_test.go` (rewrote the cause-cases tests as one outcome-cases test + a new `IsAbandon`/`CauseString` test), `code:lib/runtime/auto_terminal_test.go`, `code:lib/runtime/lineage_writer_test.go`, `code:lib/runtime/runner_subclaim_test.go`, `code:test/scenarios/orphan_reaper_terminal_race_test.go`, `code:test/scenarios/forensics/fanout_post_mortem_test.go`, `code:test/scenarios/lineage/claim_abandon_lineage_test.go`, `code:test/scenarios/lineage/force_cancelled_lineage_test.go`.

### Wire shape preserved

The forensics ledger and event payload still write `cause = "natural" | "sibling_cancel" | "descendant_cancel"` for abandons (Commit gets no cause). External consumers of the lineage ledger / event log don't see a wire-shape change.

### Verified

- `go build ./...` exit 0.
- `go test ./lib/runtime/... ./test/scenarios/lineage/... ./test/scenarios/forensics/... ./test/scenarios/claim_handle_aggregate/... ./test/scenarios/ -run TestOrphan` — all pass.
- `gofmt -l` clean on every touched file.

### Concept-doc updates

None needed. `concept:terminal-resolution` doesn't talk about `Cause` as a load-bearing axis (it's described as a forensics/lineage field), so the doc remains accurate after the refactor.

---

## 9. Empty-string sentinels treated as first-class values

**Status — RESOLVED 2026-06-19.** Five of the six sites in the sketch's enumeration were swept; the sixth (`messageVirtualNodeSettleSignal`'s `msg.Type == ""` → `"empty-wake"`) is the deliberate root-message-invocation convention per `decision:empty-message-as-root-trigger` and stays. Two additional defensive blank-checks in `parentSettlementSignal` were eliminated as part of the sweep — they were dead code under the invariant that `AggregateResult.ParentSettlingSignalType` is non-blank when `IsSettled`.

The general principle: stamp the canonical default at the **write site**, not patch blank at the **read site**. Two clean patterns covered everything — (1) closed-set field gets a typed enum + canonical default stamped at construction; (2) open-set "no value yet" field uses a pointer through the conversion so nil-ness survives. Where blank was a filter against a legitimate other case (the empty-wake message), the inline `==""` check was replaced with a typed predicate so the reason for the skip is named.

### Code changes

**Stage 1 — `AggregationKind` typed enum + write-time defaulting.**
- `code:lib/foundation/spec/aggregation_policy.go`: `type AggregationKind string` introduced; the four `AggregationKind*` constants are now typed; `AggregationPolicy.Kind` typed `AggregationKind`. `Validate()` still rejects blank with `"kind is required"` for explicit caller-supplied policies.
- `code:lib/graph/node/kind_resolver.go::CanonicalizeAggregationPolicyDefault`: new canonicalizer that stamps `AggregationKindStrict` on every fan-out node's `ErrorPolicy.Kind` when the author left it blank. Wired into both template-registration sites at `code:lib/control/controlapi/templates.go#221` and `#374`.
- `code:lib/runtime/run_tree.go::CreateRootRun` / `CreateChildRun`: stamp `AggregationKindStrict` on the persisted run-tree row when the caller passed a blank policy (delegation parents have no `FanOut.ErrorPolicy`, so they reach the row creators with a zero-value policy; stamping there is what makes downstream reads honest).
- `code:lib/runtime/run_tree.go::Aggregate`: the `if kind == "" { kind = "strict" }` patch deleted; the switch reads `policy.Kind` verbatim. The `default:` arm remains as defense against future programmer error.
- `code:lib/runtime/runner_subclaim.go::AcquireSubClaims`: the `if Kind != ""` gate at the policy-persistence call deleted; `AggregationKindStrict` defaulted at the call boundary instead, so every sub-claim's parent gets a persisted policy. Partition-key + JSON validation moved to a first pre-pass so the empty-partition test no longer panics on the nil `ClaimHandles` table it never used to reach.
- `code:lib/runtime/child_execution.go::aggregateParentOutcome`: the `|| policy.Kind == ""` arm of the unmarshal-error-or-blank fallback deleted; the function trusts the persisted policy now that the write side guarantees it.

**Stage 2 — `SettlingSignalType` pointer preservation through `ChildState`.**
- `code:lib/runtime/run_tree.go::ChildState.SettlingSignalType` retyped `*signalpkg.TypePath` (was `signalpkg.TypePath`, a string alias). The DB column was already nullable (`*string` everywhere); only the runtime conversion was dropping nil-ness.
- `code:lib/runtime/state_propagation.go#118-126` (the row → `ChildState` conversion): nil-ness is preserved through the conversion. Blank no longer collapses into a value at this site.
- `code:lib/runtime/run_tree.go::ChildState.IsSuccess`: asks the real question — `if c.SettlingSignalType == nil` means "settled without a specific signal, treat as success."
- `code:lib/runtime/run_tree.go::aggregateFirst`: the empty-default branch (`if sig == "" { sig = "terminal/success" }`) deleted; the function reads the winner's pointer directly and falls back to the canonical `terminal/success` only when the pointer is nil.
- `code:lib/runtime/state_propagation.go::parentSettlementSignal`: the three defensive `if typ == "" { typ = <default> }` patches deleted. The function now trusts the typed signal it's given — under the invariant that `AggregateResult.ParentSettlingSignalType` is non-blank when `IsSettled`, those patches were dead code.
- `code:lib/foundation/signal/types.go`: `PathPtr(t TypePath) *TypePath` helper added so test constructors can write `signalpkg.PathPtr("terminal/success")` instead of stashing a local + taking its address.
- Test files swept (12 files under `code:lib/runtime/run_tree_test.go`, `code:test/scenarios/run_tree/...`, `code:test/scenarios/fanout/...`, `code:test/scenarios/verifier/...`): every `SettlingSignalType: signalpkg.TypePath("X")` literal became `SettlingSignalType: signalpkg.PathPtr("X")`. One test loop using a bare `string` variable became a loop over the typed `AggregationKind` constants.

**Stage 3 — `lookupGraphName` rename.**
- `code:lib/runtime/runner_acquire.go::lookupGraphName` → `graphContainingNodeType`. The function's `MainGraphName`-on-fallthrough behaviour is correct (the template validator rejects unknown node-types at registration, so the only callers that reach the fallthrough are genuinely-main node-types) — the confusion was the name, not the behaviour. New name makes the contract loud.

**Stage 4 — `IsEmptyWake()` typed predicate.**
- `code:lib/foundation/persistence/messages.go::MessageRow.IsEmptyWake`: new method (`return m.Type == ""`) carrying `@decision:empty-message-as-root-trigger` + `@story:empty-message-wakes-roots` annotations.
- `code:lib/runtime/substitution_context.go::BuildAttributeDeps#105`: the inline `if m.Type == "" { continue }` filter became `if m.IsEmptyWake() { continue }`. The behaviour is identical; the reason for the skip is now named at the predicate, not inferred at the call site.

### What stayed

- `code:lib/runtime/message_delivery.go::messageVirtualNodeSettleSignal` keeps its `if summaryTail == "" { summaryTail = "empty-wake" }` — that's the deliberate `decision:empty-message-as-root-trigger` convention, not a sentinel-as-value.
- `code:lib/foundation/persistence/run_tree.go::MarshalAggregationPolicy`'s zero-value short-circuit (`if Kind == "" && !CancelSiblings && MaxFailures == 0 { return nil, nil }`) kept as defensive optimization for ad-hoc test policies; not load-bearing.

### Verified

- `go build ./...` and `go test ./...` clean across root + `lib/foundation` (54 packages, all `ok`).
- `make lint` clean.
- Race-sensitive `lib/runtime/...` under `-race -count=3` clean.
- Section-D follow-ons (`lineage`, `forensics`, `claim_handle_aggregate`) clean.

---

## 10. Duplicate "attribute-only" scanner

**Status — RESOLVED 2026-06-19.** Already collapsed by section 2's sub-finding-2 substitution-unification sweep; section 10 was a separately-surfaced finding for the same residue. The two scanners (`scanSrc` and `scanSrcAttributeOnly`) and the `TopicKind != "attribute"` guard are gone. The current `code:lib/graph/node/subscription_edges.go::parseSubstitutionRefsFromAttributes` defines one `scan` closure at `#525` used uniformly across all four sources — `n.Attributes.Schema` (walked via `walkSchemaForSourcesWithPath`), `n.ClaimProducers[i].Selector`, `n.Locks[i].Name`, `n.FanOut.PartitionRequest`. The `TopicKind != "attribute"` restriction was the dead guard; it was dropped, not hoisted to the parser — `story:fan-out`'s "not architecturally distinguished" framing carries through. Downstream discrimination (nodes-refs vs messages-refs) happens at the **extractor** level: `ExtractMessageRefsFromTemplate` and `ExtractSubstitutionRefsFromTemplate` are thin filtered views (`filter by Prefix`) over the same underlying walk. No additional code change required.

**Surfaced**: 2026-06-18 sweep agent reports.

---

## 11. `SensorName` / `SensorContext` vestige across validation path

**Status — RESOLVED 2026-06-19.** Full rename of the validation surface from `Sensor*` to `Publisher*` — Go layer, proto layer, conformance, bundled-sensor service binaries. Pre-v1, no compat shim. The proto rename regenerates wire-compatible (field numbers unchanged) but type-renamed bindings; existing clients see no on-wire change but Go consumers see `genv1.PublisherContext` instead of `genv1.SensorContext`.

### Code changes

**Proto (`proto:validation.proto`)**:
- `message SensorContext` → `PublisherContext`; field `sensor_name` → `publisher_name`; kind/resolved_config docstrings updated to say "publisher" not "sensor".
- `ValidateRequest`'s oneof: variant `SensorContext sensor = 5` → `PublisherContext publisher = 5`. Field number 5 preserved, so wire is unchanged; Go-generated wrapper renamed from `ValidateRequest_Sensor` to `ValidateRequest_Publisher`.
- `proto:claim_producer.proto#121` comment listing valid `validation_supported_roles` values: `"sensor"` → `"publisher"`.
- `make proto-gen` regenerated all bindings cleanly.

**Go validation interface (`code:lib/runtime/clientiface/validation.go`)**:
- Interface method `ValidateSensor(...)` → `ValidatePublisher(...)`.
- `ValidateSensorInput` struct → `ValidatePublisherInput`; `SensorName` field → `PublisherName`.

**Go validation runtime (`code:lib/runtime/validation_pipeline.go`, `code:lib/runtime/peer/validation_client.go`)**:
- Type-alias rename; call-site rename.
- Role discriminator string `"sensor"` → `"publisher"` at the `clientAdvertisesRole` check, the `appendFindings` tag, the `projectFindings` tag, and the wire `Role:` field in the RPC.
- Error message `Validate(sensor)` → `Validate(publisher)`.

**Conformance runner (`code:lib/protocols/conformance/validation/runner.go`)**:
- Function `checkSensorHappy` → `checkPublisherHappy`; reported check name `"SensorHappy"` → `"PublisherHappy"`.
- Conformance dispatch case `"sensor"` → `"publisher"` at `#34`.
- Fixture data: `SensorName: "conformance-sensor"` → `PublisherName: "conformance-publisher"`.

**Publisher SDK (`code:lib/protocols/publisherkit/publisher.go`)**:
- `Request.SensorName` field → `PublisherName`.
- Log key `"sensor"` → `"publisher"` in the rejected-message warn at `#75`.

**Bundled sensor binaries** (`code:lib/services/sensors/sensor-{cron,http,object-store,webhook}/sensor.go`):
- `publisherkit.Request{SensorName: "sensor-X"}` → `Request{PublisherName: "sensor-X"}`. The VALUE stays `"sensor-X"` — those are publisher-instance names that happen to be sensor implementations. The field name is what was wrong.

**Tests swept**:
- `code:lib/protocols/publisherkit/publisher_test.go` — 4 literals.
- `code:lib/control/controlapi/validation_pipeline_test.go` — fake validator method rename + counter field rename (`sensor` → `publisher`).

### Verified

- `go build ./...` clean across all four modules.
- `go test ./...` clean across root + foundation + protocols.
- `make lint` clean across all five modules (root + foundation + protocols + services + examples).
- `lib/services/test/scenarios/...` testcontainer failures pre-existing (missing locally-built `rimsky-claim-producer-filesystem` image — documented in CLAUDE.md), unrelated to the rename.
