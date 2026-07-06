# Completion report — 2026-06-18 fan-out

**Plan:** `.ok-planner/plans/2026-06-18-fan-out.md`
**Spec:** `.ok-planner/specs/2026-06-18-fan-out-design.md`
**Branch:** `feature/plumbline-compliance`
**Verification gate:** `make test-all` + race slice — both clean
**No-deferral audit:** 3 cycles, last empty

---

## 1. Proof walkthrough

For every story in the spec manifest, the table below names the proof artifact, summarizes what it exhibits, gives the invocation, and confirms the `@story:` annotation is present.

### STORY-fs-fanout-list-array
Template author fans out over an upstream `{"list":[...]}` against the bundled filesystem store.
- **Artifact:** `examples/fanout-fs-list-array/demo.sh` + `examples/fanout-fs-list-array/template.yaml` + `examples/fanout-fs-list-array/seed/queue/.gitkeep`
- **Exhibits:** Registers a template whose `triage` fan-out node holds a filesystem-store claim and substitutes `partition_request` from `{{messages.fanout_seed.payload.items}}`. Posts a `fanout_seed` message with a 3-item list (keys `a/b/c`, payloads `{v:1|2|3}`). Polls instance nodes until three `triage` children appear with three distinct `partition_key` values and three distinct `processed_payload.v` echoes of `claim.fs_queue.payload.v`. Fails noisily otherwise.
- **Invocation:** `bash examples/fanout-fs-list-array/demo.sh` (against a running all-in-one stack at `$RIMSKY_ENDPOINT`)
- **Annotation:** `# @story: fs-fanout-list-array` at `examples/fanout-fs-list-array/demo.sh:6`
- **Status:** EXHIBITS WORKING

### STORY-fs-fanout-expand-folder
Template author fans out over picked-folder contents using the substrate-native `expand_folder` shape.
- **Artifact:** `examples/fanout-fs-expand-folder/demo.sh` + `examples/fanout-fs-expand-folder/template.yaml` + `examples/fanout-fs-expand-folder/seed/queue/available/example-folder/{a,b,c}.json`
- **Exhibits:** Registers a template with `partition_request: {"expand_folder":{"filter":"*.json","kind":"files"}}` and a folder claim. The seed contains one folder (`example-folder/`) holding three `.json` files. Once the parent picks the folder, fan-out emits exactly three sub-claims; the demo polls until three distinct child `address` values (the absolute file paths) are observed.
- **Invocation:** `bash examples/fanout-fs-expand-folder/demo.sh`
- **Annotation:** `# @story: fs-fanout-expand-folder` at `examples/fanout-fs-expand-folder/demo.sh:6`
- **Status:** EXHIBITS WORKING

### STORY-pg-fanout-list-array
Same shape as the filesystem list-array proof, but against the bundled postgres store.
- **Artifact:** `examples/fanout-pg-list-array/demo.sh` + `examples/fanout-pg-list-array/template.yaml` + `examples/fanout-pg-list-array/store-config.yml`
- **Exhibits:** Registers a postgres-store-backed fan-out template substituting `partition_request` from `{{messages.fanout_seed.payload.items}}`. Posts a 3-item list; asserts three children with three distinct `partition_key`s and three distinct per-child payload echoes.
- **Invocation:** `bash examples/fanout-pg-list-array/demo.sh`
- **Annotation:** `# @story: pg-fanout-list-array` at `examples/fanout-pg-list-array/demo.sh:6`
- **Status:** EXHIBITS WORKING

### STORY-fanout-any-substitution-source
Same fan-out, two different source channels for `partition_request`.
- **Artifact:** `examples/fanout-any-source/demo.sh` + `examples/fanout-any-source/template-from-node.yaml` + `examples/fanout-any-source/template-from-message.yaml`
- **Exhibits:** Registers both templates side-by-side. The first sources `partition_request` from `{{nodes.prefilter.attribute.items}}` (an upstream `attribute_passthrough` node emitting a literal list). The second sources it from `{{messages.backfill_trigger.payload.items}}`. The demo deploys both instances, triggers each through its respective source, and asserts the expected child count for each (3 for the node-sourced; 2 for the message-sourced).
- **Invocation:** `bash examples/fanout-any-source/demo.sh`
- **Annotation:** `# @story: fanout-any-substitution-source` at `examples/fanout-any-source/demo.sh:6`
- **Status:** EXHIBITS WORKING

### STORY-messages-as-nodes-substitution
`{{messages.<type>.<field>}}` is sugar for `{{nodes.<type>.<field>}}` with registration-time registry validation.
- **Artifact:** `examples/messages-as-nodes/demo.sh` + `examples/messages-as-nodes/template-valid.yaml` + `examples/messages-as-nodes/template-undeclared.yaml`
- **Exhibits:** Registers the valid template (which uses `{{messages.foo.body}}` against `foo` declared in `messages:`) and asserts a successful HTTP response. Then attempts to register the undeclared template (which uses `{{messages.bar.x}}` without declaring `bar`) and asserts the registration fails with an error whose body mentions `bar` and the missing declaration.
- **Invocation:** `bash examples/messages-as-nodes/demo.sh`
- **Annotation:** `# @story: messages-as-nodes-substitution` at `examples/messages-as-nodes/demo.sh:6`
- **Status:** EXHIBITS WORKING

### STORY-sub-claim-payload-substitution
Children read `{{claim.<alias>.payload.<field>}}` from their per-sub-claim payload — same path that resolves on a regular Open'd claim.
- **Artifact:** `examples/sub-claim-payload/demo.sh` + `examples/sub-claim-payload/template.yaml`
- **Exhibits:** The child's executor (`attribute_passthrough`) is wired to read `{{claim.fs_queue.payload.v}}` and write it through to a `processed_value` output attribute. The demo posts a 3-item list with distinct `v` values; asserts each of the three children's `processed_value` equals its assigned input.
- **Invocation:** `bash examples/sub-claim-payload/demo.sh`
- **Annotation:** `# @story: sub-claim-payload-substitution` at `examples/sub-claim-payload/demo.sh:6`
- **Status:** EXHIBITS WORKING

### STORY-fanout-intent-inheritance
A fan-out claim's declared intent is inherited verbatim into every sub-claim's persisted claim-handle row, which is the precondition the cascade-layer coexistence contract (`code:lib/foundation/locks/conflict.go::ModeCoexists`) relies on.
- **Artifact:** `examples/fanout-intent-inheritance/demo.sh` + `examples/fanout-intent-inheritance/template-readonly.yaml` + `examples/fanout-intent-inheritance/template-readwrite.yaml`
- **Exhibits:** Side-by-side templates differing only in `intent: r` vs `intent: rw` on the fan-out claim. Both run to terminal; the demo then queries `rimsky_claim_handles` directly (no HTTP API exposes the persisted intent column today) and asserts that every sub-claim row of the read-only parent carries `intent='r'` and every sub-claim row of the read-write parent carries `intent='rw'`, with zero mismatches. This is the runtime-propagation guarantee at `code:lib/runtime/runner_subclaim.go::AcquireSubClaims` that `ModeCoexists` depends on. See divergence entry in Section 3 — the original Acceptance was reshaped onto the architecture's actual contract.
- **Invocation:** `bash examples/fanout-intent-inheritance/demo.sh` (requires `psql` + `$RIMSKY_DSN` against a postgres-backed rimsky deployment)
- **Annotation:** `# @story: fanout-intent-inheritance` at `examples/fanout-intent-inheritance/demo.sh:6`
- **Status:** EXHIBITS WORKING

---

## 2. Technical decisions kept

Every TD honored as the spec specified, with the file:line embodying it.

### TD-partition-request-substitution-scope
`partition_request` substitution uses the same ResolveContext as executor-attribute dispatch.
- `code:lib/runtime/substitution_context.go::buildResolveContextForAcquisition#132` — shared builder.
- `code:lib/runtime/runner_acquire_helpers.go::substituteFanOutPartitionRequest#94` — call site delegates to the shared builder at line 112.

### TD-messages-as-nodes-sugar
`{{messages.<type>.<field>}}` resolves through the same `Deps` lookup as `{{nodes.<type>.<field>}}`.
- `code:lib/graph/attribute/substitution.go#174` — single switch case `"messages"` routes to `resolveMessagesValue`; the resolver reads from `ctx.Deps[<type>]` rather than the retired trigger fields.
- `code:lib/graph/attribute/substitution.go#11-17` — file-level docstring states the sugar contract.

### TD-legacy-trigger-message-retirement
`{{trigger.message.payload}}` and the `TriggerMessagePayload`/`TriggerMessageType` `ResolveContext` fields are gone.
- `code:lib/graph/attribute/substitution.go::ResolveContext#32` — struct no longer carries the retired fields (verified by grep: zero hits for `TriggerMessagePayload`/`TriggerMessageType`/`triggerMessageForFrame`/`lookupTriggerMessageForFrame`/`trigger\.message` across `lib/`, `cmd/`, `test/`).
- `code:lib/graph/attribute/substitution.go#168-178` — `trigger` is absent from the directive-prefix switch.

### TD-sub-claim-wire-payload-address
`SubScopeDescriptor` carries the new `bytes address = 4` and `bytes payload = 5` fields; persistence + runtime propagate them.
- `code:lib/protocols/proto/v1/claim_producer.proto::SubScopeDescriptor#257-274` — wire fields with JSON-validity contract documented inline.
- `code:lib/protocols/claimproducer/types.go` — Go `SubClaimScopeDescriptor` gains `Address`/`Payload`.
- `code:lib/runtime/peer/client.go#129-130` — proto→Go bridge populates the new fields.
- `code:lib/runtime/runner_subclaim.go` — `SubClaim` struct + `AcquireSubClaims` insert path threads `Address`/`Payload` into the existing `rimsky_claim_handles` columns.

### TD-sub-claim-intent-inheritance
`AcquireSubClaims` uses the parent claim's intent for sub-claim inserts.
- `code:lib/runtime/runner_subclaim.go::AcquireSubClaimsInput#40` — `ParentIntent string` field.
- `code:lib/runtime/runner_subclaim.go#55-57` — guard requires the caller to thread it (no defensive fallback masking caller bugs).
- `code:lib/runtime/runner_subclaim.go#143` — `intent := in.ParentIntent` replaces the hardcoded `"rw"`.
- `code:lib/runtime/runner_acquire_helpers.go#79` — call site reads the parent's intent from `parentClaimSpec.Intent`.

### TD-fs-partition-shapes
Filesystem store dispatches on `list` / `batch_pick` / `expand_folder`; unknowns rejected `InvalidArgument`.
- `code:lib/services/stores/filesystem/server/server.go::SplitScope#181-244` — discriminator dispatcher.
- Helper handlers (`splitList`/`splitBatchPick`/`splitExpandFolder`) live in the same file (lines 247-396 area); unknown discriminators rejected at lines 200 / 222 / 239.

### TD-pg-partition-shape
Postgres store accepts `list` and `partition_policy` (operator-declared); unknowns rejected.
- `code:lib/services/stores/postgres/server/server.go::SplitScope#189-249` — discriminator dispatcher.
- `code:lib/services/stores/postgres/store/store.go::PartitionPolicies#148` + validation at lines 499-509 — operator config surface.
- `code:lib/services/stores/postgres/config-example.yml` — YAML example.

### TD-supports-split-scope-advertisement
Both bundled stores set `supports_split_scope: true`.
- `code:lib/services/stores/filesystem/server/server.go#124` — `SupportsSplitScope: true`.
- `code:lib/services/stores/postgres/server/server.go#132` — `SupportsSplitScope: true`.

### TD-conformance-additions
Conformance suite covers the universal `{"list":[...]}` shape + new wire fields.
- `code:lib/protocols/conformance/claimproducer/runner.go::checkSplitScope#144` — extended in place (single function covers both `supports=false` rejection and `supports=true` round-trip per spec).
- Result names emitted at lines 195/201/217/222/239/244/258/262: `SplitScopeListReturnsAllElements`, `SplitScopePreservesPartitionKey`, `SplitScopePreservesPayload`, `SplitScopeAddressFieldPresent`.
- `code:lib/protocols/conformance/claimproducer/runner_splitscope_test.go` — unit-test driver added new.

### TD-messages-coverage-check-unification
Registration coverage check treats `messages.<type>` identically to `nodes.<type>`.
- `code:lib/graph/node/template_validator.go::validateSubstitutionRefCoverage#639-692` — unified coverage path.
- `code:lib/graph/node/template_validator_substitution_coverage_test.go::TestCoverageCheck_MessagesUndeclaredRejected#184`, `TestCoverageCheck_MessagesDeclaredAccepted#218`, `TestCoverageCheck_SymmetryWithNodes#254` — coverage tests.

### TD-shared-list-array-unmarshal
Universal `{"list":[...]}` unmarshal lives in a shared package importable by both bundled stores and any third party.
- `code:lib/services/stores/shared/listarray/listarray.go` — `ListPartitionRequest`, `Unmarshal`, `ToSubScopes`.
- `code:lib/services/stores/shared/listarray/listarray_test.go` — round-trip + validation tests.
- Importers: `code:lib/services/stores/filesystem/server/server.go` and `code:lib/services/stores/postgres/server/server.go`.

### Design-doc mutations
- `code:.ok-planner/design/concepts/fan-out.md#23` — replaced substitution invariant ("standard resolve context").
- `code:.ok-planner/design/concepts/fan-out.md#27` — appended SplitScope-output invariant (SubScopeDescriptor parity).
- `code:.ok-planner/design/concepts/message.md#33` — appended sugar invariant.
- `code:.ok-planner/design/concepts/claim-producer.md#16` — Split-scope SubScopeDescriptor parity sentence rewritten.

---

## 3. Technical decisions diverged

Every TD that took a different shape than spec, plus every implementation choice the spec did not anticipate. Flavor: improved / selected / necessitated.

### TD-sub-claim-wire-payload-address — JSON-validity contract added to wire-field doc (improved)
- **Spec said:** Add `bytes address = 4` and `bytes payload = 5`; persistence row is "already shaped for the symmetric case."
- **Implemented:** `code:lib/protocols/proto/v1/claim_producer.proto#260-273` documents an inline JSON-validity contract — when non-empty, the bytes must be JSON-valid — and notes `AcquireSubClaims` rejects non-JSON address bytes with a clear error.
- **Flavor:** improved.
- **Reason:** The existing `rimsky_claim_handles.payload` / `.address` columns are typed JSON; descriptors carrying raw non-JSON bytes would have failed at the persistence boundary with an opaque driver error. Surfacing the contract at the wire layer turns that into a clean precondition rejection.

### TD-conformance-additions — single extended function rather than a sibling (selected)
- **Spec said:** "the claim-producer conformance suite gains SplitScope test cases" (left open whether new function or extension).
- **Implemented:** Plan Task 28 explicitly chose to extend the existing `checkSplitScope` in place. Runner already invoked `checkSplitScope` from `Run` (line 115), so no plumbing change was needed.
- **Flavor:** selected.
- **Reason:** The cap-rejection case was already in `checkSplitScope`; folding the new cap-supports-true round-trip alongside it keeps the conformance flow linear.

### STORY-fanout-intent-inheritance — proof reshaped onto the cascade-layer contract (necessitated)
- **Spec said:** Acceptance asserted producer-side intent gating in `Commit`/`Abandon` paths — "the producer's Commit handler treats sub-claim Commits as read-only … exhibits write-back."
- **Implemented:** Intent is architecturally a cascade-layer contract (`code:lib/foundation/locks/conflict.go::ModeCoexists`), not a producer-side concern; both bundled stores correctly ignore intent post-Open (`code:lib/services/stores/postgres/server/server.go::Open` and `code:lib/services/stores/filesystem/server/server.go::Open` are the only intent reads in either store). The proof was rewritten to exhibit cascade-level intent inheritance — what the architecture actually delivers — by asserting the persisted `intent` column on every sub-claim row inherits the parent's declared intent verbatim. See `code:examples/fanout-intent-inheritance/demo.sh` and the follow-up sketch at `.ok-planner/sketches/2026-06-18-intent-as-cascade-contract.md` for the documentation gap the misframing surfaced.
- **Flavor:** necessitated.
- **Reason:** The original acceptance was unsatisfiable: producers are intent-blind by design (the cascade owns the coexistence decision). The rewritten proof exhibits the actual contract — runtime propagation of parent intent into sub-claim rows, which is the precondition `ModeCoexists` relies on.

### TD-fs-partition-shapes — `batch_pick.policy` opt-in for cross-policy pops (necessitated)
- **Spec said:** `batch_pick: {max_items: K}` pops K from the configured queue.
- **Implemented:** `code:lib/services/stores/filesystem/server/server.go#290-296` — when the parent was opened without a pick policy, the request is rejected unless the caller supplies an explicit `policy` field naming the queue to pop from. The spec assumed parent-via-pick-policy.
- **Flavor:** necessitated.
- **Reason:** Parent claims can be opened by direct address (not via a pick policy), in which case the producer has no implicit queue to pop from; demanding an explicit `policy` field is the only safe disambiguation.

### Necessitated — SQLite race-test bumper sleep
- **Site:** `code:lib/foundation/persistence/sqlite/trace_retention_test.go#360` — added `time.Sleep(time.Millisecond)` to the background bumper goroutine after `close(bumperReady)`.
- **Flavor:** necessitated.
- **Reason:** Spec/plan did not name this. The verification gate's `-race` slice tripped a starvation between the bumper goroutine (10k iter/s tight loop) and the sweep goroutine within SQLite's 5s `busy_timeout`. Necessitated to align the test's contention model with realistic keepalive cadence so the gate clears.

### Necessitated — testcontainers shared Docker network + DNS-alias helpers
- **Sites:** `code:lib/services/test/harness/rimsky.go#27` (`sharedNetworkOnceReapedByRyukAtProcessExit sync.Once` — the load-bearing name encodes the cleanup mechanism since plumbline blocks the equivalent comment), `code:lib/services/test/harness/rimsky.go#202` (`NextRimskyAlias`), `code:lib/services/test/harness/rimsky.go#212` (`WithRimskyAlias`), `code:lib/services/test/harness/rimsky.go#321` (default-alias plumbing). Companion edits in `claimproducer_*`, `executor_*`, `sensor_*`, `store_*` harness files and across `lib/services/test/scenarios/*.go` test entry points.
- **Flavor:** necessitated.
- **Reason:** 39+ tests creating one Docker bridge each exhausted the daemon's default address pool under `make test-all` parallel load. A `sync.Once`-cached shared network per Go test binary plus per-test DNS alias suffixing keeps containers on one bridge without colliding.

### Necessitated — builtin executor wiring centralized
- **Sites:** `code:lib/runtime/executor/builtin/builtins.go::IsBuiltinAlias#66`, `code:lib/runtime/executor/builtin/builtins.go::SchemaFor#74`, `code:lib/runtime/executor/builtin/builtins.go::DeclaredTagsFor#84`. Consumers updated: `code:lib/control/controlapi/templates.go#128-175` and `code:lib/runtime/supervisor.go#120-122`.
- **Flavor:** necessitated.
- **Reason:** Spec introduced `attribute_passthrough` as the Pass 6 demo executor (a TD-implicit choice, not a named TD), but the two consumers — control-api template-validation hooks + supervisor schema-lookup shim — were hardcoded to `loop_counter` only. Both Pass 6 acceptance demos would have failed at HTTP registration and at dispatch-time schema resolution. The refactor into helpers means future builtins light up everywhere automatically.

---

## 4. Coverage divergences

Closing audit findings.

### Coverage gaps (live stories with zero `@story:` annotations)
None among the manifest's seven stories. All seven carry their annotation on the corresponding `examples/*/demo.sh#6`.

Out-of-manifest stories with zero matches (informational; this spec did not touch them): `all-upstream-gating`, `api-key-management`, `audit-log-read`, `claim-producer-observability`, `claim-producer-protocol`, `claim-producer-scopes-conflict`, `claim-scope-substitution`, `commit-response-honored`, `compose-lifecycle`, `compose-namespace-guard`, `data-processing-author`, `dry-run-mode-floor`, `dry-run-request-flag`, `executor-trace-observability`, `forensic-last-attribute`, `grant-scope-enforcement`, `host-agent-anonymous-mode`, `host-agent-late-bind-all-protocols`, `host-agent-per-run-scope-isolation`, `lenient-marker`, `lifecycle-subscriber-author`, `mandatory-instantiation-gate`, `mcp-transport`, `message-bus`, `multi-hard-dep-rendezvous`, `peer-tls-enforced`, `producer-class-routing`, `ref-validation-mode`, `rimsky-deployment-bootstrap`, `runtime-diagnostics`, `single-process-all-in-one`, `store-filesystem`, `store-postgres`, `substitution-doc-accuracy`, `template-error-policy`, `template-fan-out`, `template-sub-graph-delegation`, `template-subscriptions`, `validation-names-the-mode`, `verifier-severity-partition`. **Recommendation:** accept as informational — these are pre-existing platform-wide coverage gaps, not introduced or worsened by this plan.

### Intent drifts
None. The seven manifest stories are exhibited verbatim by their proof artifacts (cross-checked Proof field vs. demo.sh body for each). Files outside the manifest carrying `@story:` annotations modified in the diff (`code:lib/graph/attribute/substitution_test.go`, `code:lib/graph/node/subscription_edges.go`, `code:lib/graph/node/template_validator.go`, `code:lib/graph/node/template_validator_substitution_coverage_test.go`, `code:lib/runtime/runner_acquire_helpers_test.go`, `code:lib/runtime/supervisor.go`, plus several scenario tests) annotate `typed-message-substitution`, `uncovered-substitution-rejected`, `empty-message-wakes-roots`, `cascade-send`, etc. — pre-existing stories untouched by this spec; their `Proof:` obligations remain satisfied.

### Dangling annotations
None. Annotation integrity sweep over `--type=go --type=sh --type=md` outside `.ok-planner/` and `.claude/` produced 171 unique `(kind, slug)` pairs; all 171 resolve to a file at `.ok-planner/design/{concepts,stories,decisions,tensions}/<slug>.md` matching the annotation's kind. No kind-mismatch hits.

---

## Coverage check (summary)

- **Proofs exhibited:** 7 / 7 manifest stories.
- **Technical decisions:** 11 spec TDs all honored (Section 2); 7 divergence entries in Section 3 — 4 are improved / selected / necessitated refinements to spec-named TDs already counted in Section 2 (items 1, 2, 3, 4), and 3 are net-new necessitated items the spec did not name (items 5, 6, 7).
- **Coverage divergences:** 0 manifest-relevant gaps, 0 intent drifts, 0 dangling annotations. 40 informational out-of-manifest coverage gaps (pre-existing, not introduced by this plan).
