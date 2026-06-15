# Completion report — Explicit substitution cascade behavior

**Plan:** `.ok-planner/plans/2026-06-14-explicit-substitution-cascade-behavior.md`
**Spec:** `.ok-planner/specs/2026-06-14-explicit-substitution-cascade-behavior-design.md`
**Auditor:** completion-auditor subagent (Opus 4.7, 1M context)
**Date:** 2026-06-14

The plan consolidated cascade-shape configuration onto subscription entries via two required boolean flags (`wake_on_change`, `force_upstream_refresh`), retired implicit edge generation from substitution refs, retired the legacy `hard_dep:` attribute-field flag, and added a registration-time coverage check rejecting substitution refs without a matching subscription. Three stories' proofs deliver against the real assembled testcontainers stack and the in-process validator. Every spec-named TD is accounted for below; two divergences are necessitated additions the spec did not name (see Section 3).

---

## 1. Proof walkthrough

### STORY-read-without-waking
**Restatement:** Template author can read an upstream's attribute via substitution while declaring on the covering subscription that the read does not fire the receiver on the sender's change.

**Proof artifact:** `/Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade/test/scenarios/explicit_attribute_context_read_test.go`

**What the artifact exhibits:** Boots the testcontainers stack, deploys a template with `trigger → {gate-sender, context-sender} → receiver` shape. The receiver carries two subscriptions: a gated `wake_on_change: true` on `gate-sender` (CEL `when: payload.value == 'needs_work'`) and a `wake_on_change: false` on `context-sender`. Scenario 1 invalidates `context-sender` alone, waits 2s and asserts the receiver's observed-execute count is unchanged from boot — meaning `context-sender`'s emit lands a wait-set row but does NOT stale-mark the receiver. Scenario 2 invalidates `trigger`, which cascades both senders into one frame; `gate-sender`'s `value: "needs_work"` payload satisfies the receiver's CEL gate so the receiver dispatches once, and its `req.GetAttributes()` carries BOTH senders' scenario-2 values — proving the wait-set insert was NOT incorrectly gated on `wake_on_change`. The `context-sender`'s 500ms delay pins the ordering deterministically (gate-sender stale-marks the receiver first, context-sender's drained wait-set row carries through).

**Invocation:** `cd /Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade && go test ./test/scenarios/ -run TestStoryReadWithoutWaking -count=1 -timeout=5m`

**Status:** EXHIBITS WORKING — `@story: explicit-attribute-context-read` annotation at file-top and on the test function pairs the proof to its story; both scenarios verified.

---

### STORY-pull-upstream-fresh-on-read
**Restatement:** Template author can declare on a subscription that the sender be brought current before the receiver dispatches, so the receiver's substitution context contains the sender's freshest value.

**Proof artifact:** `/Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade/test/scenarios/per_run_attributes/hard_dep_test.go`

**What the artifact exhibits:** Boots the testcontainers stack with an `A → B; A → C; B → C` topology. Receiver C subscribes to B with `force_upstream_refresh: true` and reads `{{nodes.b.attribute.b_value}}`. The first frame settles all three; C reads B's first-fire value `"from-b-1"`. The test then re-primes the stubs, invalidates **A** (not B), and waits for C's latest attribute row to reflect both A's and B's second-fire values. The pre/post comparison (`"from-b-1"` → `"from-b-2"`) catches a stale read, not just any read — a missing upstream-refresh pull would leave B at its prior value when C dispatches.

**Invocation:** `cd /Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade && go test ./test/scenarios/per_run_attributes/ -run TestPerRunAttributes_HardDepPullsUpstream -count=1 -timeout=5m`

**Status:** EXHIBITS WORKING — `@story: upstream-pull-on-invalidate` annotation pins the proof; pre/post value assertion (lines 137, 162) catches the spec's named falsifier (stale value matching upstream's prior run).

---

### STORY-uncovered-read-rejected
**Restatement:** Template author gets a registration error when a substitution ref has no covering subscription, naming the ref and showing the subscription entry that would cover it.

**Proof artifacts (paired):**
- `/Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade/lib/graph/node/template_validator_substitution_coverage_test.go` (validator unit half)
- `/Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade/test/scenarios/registration_rejects_uncovered_substitution_test.go` (control-API HTTP half)

**What the artifacts exhibit:** The validator unit test file carries three test functions exercising each uncovered-ref shape (per-field attribute `{{nodes.foo.attribute.bar}}`, whole-pull `{{nodes.foo.attribute}}` with only a per-field subscription, and event `{{nodes.foo.event.something_happened}}`). For each, `ValidateTemplate` is invoked directly and `res.StructuredErrors` is asserted to contain a single matching `substitution_ref_uncovered` entry with all six required fields (`kind`, `receiver_node_type`, `ref`, `attribute_property`, `suggested_subscribes_entry`, `suggested_subscribes_note`). The shared helper `assertSuggestedEntryShape` pins the `suggested_subscribes_entry` to a flat 4-key drop-in JSON object (no embedded `_note`) and asserts `suggested_subscribes_note` is a top-level sibling mentioning both flag names. The control-API scenario test boots the real testcontainers stack and submits each shape via `POST /v1/templates`, asserting HTTP 400 plus the same structured shape on the wire — proving the response-rendering site emits the discriminator correctly.

**Invocation:**
- Validator unit: `cd /Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade && go test ./lib/graph/node/ -run TestSubstitutionCoverage -count=1`
- Scenario: `cd /Users/patrick/Documents/projects/research/rimsky/rimsky-core-cascade && go test ./test/scenarios/ -run TestRegistrationRejectsUncoveredSubstitution -count=1 -timeout=5m`

**Status:** EXHIBITS WORKING — both files carry `@story: uncovered-substitution-rejected` annotations on every test function; the whole-pull case explicitly exercises `decision:coverage-wildcard-asymmetry`.

---

## 2. Technical decisions kept

### TD-cascade-flags-on-subscribes
**Restatement:** The two cascade-behavior flags live on `SubscriptionEntry`, not per-ref and not on a separate block.
**Embodiment:** `lib/foundation/spec/subscription.go:56` (`WakeOnChange *bool`) and `lib/foundation/spec/subscription.go:72` (`ForceUpstreamRefresh *bool`) on the `SubscriptionEntry` struct.

### TD-cascade-flags-required-no-defaults
**Restatement:** Both `wake_on_change` and `force_upstream_refresh` are required; registration rejects entries missing either flag.
**Embodiment:** `lib/graph/node/template_validator.go:634-645` — `validateSubscribes` emits `wake_on_change is required` / `force_upstream_refresh is required` errors on missing flags. Defense-in-depth at `lib/graph/node/subscription_edges.go:778-783` (edge builder refuses to coerce nil to false).

### TD-substitution-grammar-closed
**Restatement:** Substitution grammar gains no new tokens; cascade-shape declaration lives on subscriptions, not on reads.
**Embodiment:** `lib/graph/attribute/substitution.go:16-23` — the package doc enumerates five live source kinds; the grammar's `parseSubstitutionDirective` (`lib/graph/node/subscription_edges.go:678-718`) accepts only the established `nodes.X.attribute[.Y]` and `nodes.X.event.Y` shapes. No flag token added.

### TD-substitution-ref-coverage-required
**Restatement:** Every substitution ref in a node's attribute schema must be matched by at least one `subscribes:` entry; uncovered refs are rejected at registration.
**Embodiment:** `lib/graph/node/template_validator.go:966-1013` — `validateSubstitutionRefCoverage` walks every receiver's parsed refs and emits a structured entry per uncovered ref. Wired into `ValidateTemplate` at `lib/graph/node/template_validator.go:345`.

### TD-coverage-wildcard-asymmetry
**Restatement:** Wildcard `attribute/*` covers per-field reads; a per-field subscription does NOT cover a whole-pull read.
**Embodiment:** `lib/graph/node/template_validator.go:1034-1063` — `coverageMatch` returns `false` for whole-pull (`Name == ""`) when only a per-field entry is present, and accepts both exact and wildcard for per-field reads. Pinned by `TestSubstitutionCoverage_WholePullRefUncovered` at `lib/graph/node/template_validator_substitution_coverage_test.go:91-132`.

### TD-cross-cutting-no-force-refresh
**Restatement:** A subscription with `instance: true` and `force_upstream_refresh: true` is rejected at registration.
**Embodiment:** `lib/graph/node/template_validator.go:674-680` — `validateSubscribes` rejects the combination with a message naming both fields. Defense-in-depth at `lib/graph/node/hard_dep_edges.go:102-104` (`hardDepSendersOf` skips cross-cutting entries).

### TD-uncovered-substitution-error-shape
**Restatement:** Structured `substitution_ref_uncovered` entry with `kind`, `receiver_node_type`, `ref`, `attribute_property`, `suggested_subscribes_entry` (flat copy-pasteable), `suggested_subscribes_note` (sibling).
**Embodiment:** `lib/graph/node/template_validator.go:995-1010` — emits the entry with all six fields; `suggested_subscribes_entry` is a flat 4-key map; the note is a top-level sibling string.

### TD-validation-errors-additive-not-uniform
**Restatement:** Structured shape is added alongside `{path, msg}`; existing entries unchanged.
**Embodiment:** `lib/control/controlapi/templates.go:231-235` — flat `entries` slice appends legacy `{path, msg}` first, structured entries second (deterministic order). Identical pattern at `lib/control/controlapi/templates.go:439-443` for the pipeline-rejection path.

### TD-no-hard-dep-special-case
**Restatement:** Legacy `hard_dep:` flag retires with no special-case rejector.
**Embodiment:** `lib/graph/node/hard_dep_edges.go:87-116` (`hardDepSendersOf`) walks `Subscribes` only; no path in the validator or edge builder reads `hard_dep` from attribute schemas. `rg "\"hard_dep\"" lib/ test/ --type go` confirms zero hits at attribute-schema sites.

### TD-wake-on-change-wait-set-only
**Restatement:** `wake_on_change: false` inserts a wait-set row but skips the receiver stale-mark.
**Embodiment:** `lib/runtime/runner_terminal.go:905-906` — `if !edge.WakeOnChange { skipAffirm = true }` short-circuits the affirm/stale-mark while leaving the wait-set insert (`lib/runtime/runner_terminal.go:997-1005`) unconditional. The `frame:next` branch at `lib/runtime/runner_terminal.go:743-745` gates the new-frame enqueue on `WakeOnChange`.

### TD-force-upstream-refresh-via-receiver-keyed-map
**Restatement:** Receiver-keyed map (`HardDepEdgeMap`) consumes subscription flags; cascade-walker consumption path unchanged.
**Embodiment:** `lib/graph/node/hard_dep_edges.go:45-78` — `BuildHardDepEdges` walks `Subscribes` for `ForceUpstreamRefresh == true`; cycle detection, fan-out-target rejection, and dedup carry over unchanged. Runtime consumption at `lib/runtime/runner_terminal.go:1051-1136` (`pullForceRefreshUpstreams`, renamed from `pullHardDepUpstreams`).

### TD-implicit-edge-generation-retired
**Restatement:** Subscription-edge map fed only by explicit `subscribes:` block.
**Embodiment:** `lib/graph/node/subscription_edges.go:428-442` — `BuildSubscriptionEdges` walks `n.Subscribes` only; the function no longer accepts a `substitutionRefs` parameter. Callers at `lib/graph/scheduler/pure_cascade.go:193` and `lib/runtime/subscription_loaders.go:76` (and a third site at `lib/runtime/message_delivery.go:313, 442`) pass only `tmpl.Spec`.

### TD-substitution-context-builder-unchanged
**Restatement:** `BuildAttributeDeps` reads drained wait-set rows; logic untouched.
**Embodiment:** `lib/runtime/substitution_context.go::BuildAttributeDeps` is not in the diff (zero modifications). The wait-set row inserted at `lib/runtime/runner_terminal.go:997-1005` is the source the builder keys on, exactly as before.

### TD-substitution-grammar-fallback-unchanged
**Restatement:** Existing fallback/lenient/optional routing for unresolved refs is unchanged.
**Embodiment:** `lib/graph/attribute/substitution.go` (diff is a doc-comment edit only at lines 16-23). The dispatch routing for `ErrMissingSource` (`| "literal"` fallback, `?` lenient marker, optional-field omission, strict-required → `template_resolution_failed`) is untouched.

### TD-migration-fills-flags-today-equivalent
**Restatement:** Every existing `SubscriptionEntry` in the codebase gets `wake_on_change: true, force_upstream_refresh: false`.
**Embodiment:** Sweep verified — `rg WakeOnChange test/ lib/ --type go` returns >300 hits across 84 test files; the Task 11 multi-line audit (per plan) reports empty output for the missed-fields sweep. The full test suite passes per Pass 4 verification gate.

### TD-migration-hard-dep-becomes-force-refresh
**Restatement:** Every legacy `hard_dep: true` becomes a `force_upstream_refresh: true` subscription.
**Embodiment:** The four touched test files (`test/scenarios/per_run_attributes/hard_dep_test.go`, `test/scenarios/multi_hard_dep_test.go`, `lib/runtime/hard_dep_cascade_test.go`, `lib/graph/node/hard_dep_edges_test.go`) carry no `"hard_dep": true` strings and no `prop["hard_dep"] = true` assignments per `rg`. Example: `test/scenarios/per_run_attributes/hard_dep_test.go:98` carries `Node: "b", Type: "attribute/b_value/changed", WakeOnChange: BoolPtr(true), ForceUpstreamRefresh: BoolPtr(true)` — the migrated shape.

### TD-migration-implicit-edges-become-explicit
**Restatement:** Every implicit edge from a substitution ref gets an explicit covering subscription.
**Embodiment:** Mechanically verified by the fact that the entire test suite passes against the new validator (which rejects uncovered refs); any unmigrated implicit edge would surface as a registration error. The full Pass 4 test run is the load-bearing falsifier.

---

## 3. Technical decisions diverged

### necessitated: walkCascadeForInvalidatedNode pulls invalidated node's own upstream-refresh upstreams
**TD context:** Necessitated by STORY-pull-upstream-fresh-on-read; not anticipated in the spec.
**What the spec said:** The spec described `force_upstream_refresh` as a receiver-keyed map consumed by `pullHardDepUpstreams` from the cascade walker (TD-force-upstream-refresh-via-receiver-keyed-map). The spec implicitly assumed the existing runtime machinery was sufficient.
**What was implemented:** `lib/runtime/cascade_invalidate.go:405-438` — `walkCascadeForInvalidatedNode` now pulls the just-invalidated node's OWN `force_upstream_refresh: true` upstreams before the downstream walk. Without this, an admin invalidate against A would only fire A's upstream-refresh edges when A was reached as a receiver of some OTHER sender's cascade walk — direct invalidation would leave the upstreams stale.
**Flavor:** necessitated.
**Reason:** STORY-pull-upstream-fresh-on-read's Acceptance explicitly names the direct-invalidate path ("when A is invalidated and X has not been independently invalidated"). The existing runtime machinery only pulled upstreams when a node was reached via a sender's cascade walk; the direct-invalidate entry point needed the equivalent pull. The added site carries an `@story: upstream-pull-on-invalidate` annotation.
**Regression pin for the direct-invalidate branch:** `test/scenarios/per_run_attributes/hard_dep_test.go::TestPerRunAttributes_HardDepPullsUpstream_DirectInvalidateOfReceiver` invalidates `c` (the receiver) directly via the admin invalidate API and asserts c's substitution context at re-dispatch carries b's freshest value — the load-bearing observable that exercises the added 33 lines at `lib/runtime/cascade_invalidate.go:405-438`. The sibling test `TestPerRunAttributes_HardDepPullsUpstream` covers the cascaded-invalidate path (invalidate `a`, b is dragged via `cascadeSubscribersStaleInTx::pullForceRefreshUpstreams`).

### necessitated: pullHardDepUpstreams renamed to pullForceRefreshUpstreams
**TD context:** Necessitated by terminology consistency with the spec's "upstream-refresh edge map" / `force_upstream_refresh:` naming.
**What the spec said:** TD-force-upstream-refresh-via-receiver-keyed-map said "the cascade-walker consumption path is unchanged." The spec did not direct a function rename.
**What was implemented:** `lib/runtime/runner_terminal.go:1051` (`pullForceRefreshUpstreams`), with the audit reason at `lib/runtime/cascade_invalidate.go:513` also updated from `"hard_dep_pull"` to `"upstream_refresh_pull"`.
**Flavor:** improved.
**Reason:** Keeping `pullHardDepUpstreams` would have left a misleading name pointing at a retired concept (the legacy `hard_dep:` attribute-field flag); the function's input source and semantics now belong to `force_upstream_refresh`. Cold-read discipline is better served by the rename, even though the body's logic is unchanged.

---

## Coverage check

**Stories exhibited:** 3 / 3 in manifest. Each story has a working proof artifact with file path, invocation, and `@story:` annotation paired to its design-doc story slug.

**Technical decisions:** 17 kept + 0 diverged = 17 / 17 spec-listed TDs.

**Additional implementation choices recorded as necessitated/improved in Section 3:** 2 (the direct-invalidate upstream pull, and the function rename). Both are necessitated/improved per the necessity rule — the first to satisfy STORY-pull-upstream-fresh-on-read's direct-invalidate acceptance branch, the second for terminology coherence with the spec's `force_upstream_refresh` naming.

**Process check:** No GAPs detected. All three story proofs exist, run, and exhibit the user-observable outcome. All seventeen spec-named TDs land in code, traceable to file:line citations. All twenty design-doc artifacts directed by the spec (three concept mutations, two existing-artifact mutations, three new stories, seventeen new decisions) are present in `.ok-planner/design/`. No mismatch flagged.
