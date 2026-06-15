# Explicit substitution cascade behavior — Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-14-explicit-substitution-cascade-behavior-design.md`
**Goal:** Consolidate cascade-shape configuration onto subscription entries via two required boolean flags (`wake_on_change`, `force_upstream_refresh`); retire implicit edge generation from substitution refs; retire the legacy `hard_dep:` attribute-field flag; add a registration-time coverage check that rejects substitution refs without a matching subscription. Bundled templates and tests are migrated to the new surface with today-equivalent flag values, preserving every existing runtime behavior.
**Architecture:** Subscription cascade-shape declaration moves from two surfaces (`subscribes:` for explicit edges, attribute-field `hard_dep:` for proactive upstream pull) to one surface (`subscribes:` with two orthogonal booleans). The cascade walker continues to consult two edge maps (subscription map keyed by sender; receiver-keyed map for upstream-refresh) — only the receiver-keyed map's input source changes from attribute-field flags to subscription-block flags. Registration adds a static coverage check that walks every substitution ref and rejects templates with uncovered refs.
**Tech Stack:** Go (rimsky-core). YAML template parsing. JSON Schema attribute validation. Testcontainers for scenario tests. Postgres + SQLite persistence backends. No new dependencies.

---

## Pass 1: DSL surface, full migration sweep, and runtime semantics

**Goal:** Add the two boolean flag fields to `SubscriptionEntry`; sweep every Go file constructing a `SubscriptionEntry` to populate the two flags with today-equivalent values; migrate every legacy attribute-field `hard_dep: true` to the new subscription-block shape; add explicit subscription entries wherever a current template relies on an implicit edge generated from a substitution ref; gate the cascade walker's stale-mark on `wake_on_change`; repurpose the hard-dep edge builder to read from subscription-block flags; retire the implicit edge generation in `BuildSubscriptionEdges` and update its two callers; refresh doc-comments that name the retired mechanism. At the end of this pass the tree builds, every existing test passes (behavior preserved because every migrated entry carries today-equivalent flag values), and no implicit edge is generated anywhere from any substitution ref. The pass is large but its intermediate states pass through transient breakage; only the pass's final state is required to be green.

**Scope:** Tasks 1–11.

**Falsifier:** the two flag fields are absent from `SubscriptionEntry`, OR `BuildSubscriptionEdges` still emits an implicit edge from any substitution ref, OR the cascade walker still stale-marks receivers regardless of `wake_on_change`, OR `BuildHardDepEdges` still reads `hard_dep:` from attribute schemas (not from subscription flags), OR the callers in `lib/graph/scheduler/pure_cascade.go` or `lib/runtime/subscription_loaders.go` still pass a substitution-refs argument to `BuildSubscriptionEdges`, OR `go test ./... -count=1` is not fully green at end of pass.

### Task 1: Extend `SubscriptionEntry` with the two cascade-shape flag fields

**Files:** `lib/foundation/spec/subscription.go`

**Steps:**

1. Read `lib/foundation/spec/subscription.go` and locate the `SubscriptionEntry` struct (currently at lines 17-54). Note the existing YAML/JSON tag style — lowercase, underscored — used by the existing `Node`, `Instance`, `Type`, `When`, `Frame`, `ResolvesViaCallingNode` fields.

2. Add two new pointer-bool fields after the existing `Frame` field (which currently ends at line 42), keeping `ResolvesViaCallingNode` as the last field in the struct:

   ```go
   // WakeOnChange governs whether a matching emission from the sender
   // dispatches the receiver. true: the cascade walker inserts a wait-
   // set row AND stale-marks the receiver. false: wait-set row only;
   // the receiver is not stale-marked from this edge (it dispatches only
   // when other subscriptions fire it; its substitution context still
   // sees the sender's data if the sender settled in this frame).
   //
   // Required field — no default. Registration rejects entries without
   // an explicit value. See decision:cascade-flags-required-no-defaults.
   //
   //	@concept: cascade
   //	@concept: node-subscription
   WakeOnChange *bool `yaml:"wake_on_change" json:"wake_on_change"`

   // ForceUpstreamRefresh governs whether the receiver's invalidation
   // drags the sender into the same frame for re-evaluation. true: when
   // this receiver is invalidated, the cascade walker also invalidates
   // the sender so it re-runs in the same frame before the receiver
   // dispatches. false: no pull; the receiver dispatches with whatever
   // sender state happens to be in this frame.
   //
   // Required field — no default. Registration rejects entries without
   // an explicit value. A cross-cutting subscription (Instance=true)
   // cannot carry ForceUpstreamRefresh=true; the combination is rejected
   // at registration. See decision:cross-cutting-no-force-upstream-refresh.
   //
   //	@concept: cascade
   //	@concept: node-subscription
   ForceUpstreamRefresh *bool `yaml:"force_upstream_refresh" json:"force_upstream_refresh"`
   ```

   The fields are pointer-to-bool so unset (nil) is distinguishable from `false`. Task 12 (in Pass 2) wires the validator to reject nil; until Pass 2 lands, parsing accepts entries without the fields, which lets the migration sweep in Tasks 3–5 land before the rejection rule fires.

3. Run `go build ./lib/foundation/spec/...` and confirm it compiles.

4. Run `go test ./lib/foundation/spec/...` and confirm existing tests still pass.

### Task 2: Add convenience helpers for constructing today-equivalent flag values

**Files:** `lib/foundation/spec/subscription.go`

**Steps:**

1. Add two exported helpers at the bottom of `subscription.go`. Both take a `bool` argument and return a `*bool` — symmetric signatures so the sweep can use either flag value at any call site:

   ```go
   // BoolPtr is the canonical helper for SubscriptionEntry's pointer-bool
   // fields. Exported so test fixtures across the tree can construct
   // WakeOnChange and ForceUpstreamRefresh inline without local *bool
   // hoist variables. Kept in this file (not in a generic ptr-helper
   // package) so its referent is unambiguous in cold reads.
   //
   // Today-equivalent migration shape — preserves pre-2026-06 behavior
   // verbatim — is WakeOnChange: BoolPtr(true), ForceUpstreamRefresh:
   // BoolPtr(false). See plan task 3.
   func BoolPtr(v bool) *bool { return &v }
   ```

   One helper covers both fields. Inlining lookups (`spec.BoolPtr(true)`, `spec.BoolPtr(false)`) is the call-site idiom.

2. Run `go build ./lib/foundation/spec/...` and confirm it compiles.

### Task 3: Sweep every Go file constructing a `SubscriptionEntry` — add the two flag fields with today-equivalent values

**Files:** every file matching `rg -l 'SubscriptionEntry\{' --type go test/ lib/`. As of this plan-writing, the matched count is **51 files**; verify the actual count when executing — additional files may have appeared.

**Steps:**

1. Run `rg -l 'SubscriptionEntry\{' --type go test/ lib/ > /tmp/sweep_list.txt` and read the file. This is the authoritative sweep target.

2. For each file in the sweep list, find every `spec.SubscriptionEntry{...}` (or `node.SubscriptionEntry{...}` — verify the import alias by reading) construction. The constructions may be:
   - Direct struct literals in slice initializers
   - Items passed to scenario-test helpers such as `scenario.WithSubscribes(...)` or `node.SubscribesItem{...}`
   - Multi-line struct literals where fields span several lines

   For each entry, add the two fields:

   ```go
   spec.SubscriptionEntry{
     Node: "...",
     Type: "...",
     // ...existing fields...
     WakeOnChange:         spec.BoolPtr(true),   // today-equivalent: receiver fires on sender's match
     ForceUpstreamRefresh: spec.BoolPtr(false),  // today-equivalent: no proactive upstream pull
   }
   ```

   The today-equivalent for every existing entry is `wake_on_change: true, force_upstream_refresh: false`. This preserves the implicit-edge-style cascade fire behavior the entries had before this work — every subscription matched today fires the receiver, none pulls upstreams proactively. The asymmetry is correct: today's explicit `subscribes:` entries already cause cascade fire (`wake_on_change: true`); today's separate `hard_dep:` attribute fields are what trigger upstream pull, and those are migrated in Task 4, not here.

3. After each file's edits, run `go build ./...` to catch any compilation regression early. (Verification is incremental during the sweep; the cross-tree run is in step 4.)

4. After all files are edited, run `go build ./...`. The tree compiles.

5. Run `go vet ./...`. Clean.

6. Confirm field-presence at every construction site via a multi-line regex sweep:

   ```bash
   rg -nU --multiline 'SubscriptionEntry\{[^}]+\}' --type go test/ lib/ |
     awk '/SubscriptionEntry\{/,/\}/ {block = block "\n" $0; if (/\}/) { if (!(block ~ /WakeOnChange/ && block ~ /ForceUpstreamRefresh/)) print "MISSED: " FILENAME ": " block; block=""}}'
   ```

   Empty `MISSED:` output confirms every struct-literal construction sets both fields. If matches print, return to step 2 for the named files.

7. Run `go test ./...` across the tree. **All tests pass at end of Task 3** — Task 3 added pointer-bool fields and populated them with today-equivalent values; no behavior has changed and `hard_dep:` flags still sit on attribute fields. The four hard-dep tests start failing at end of Task 4 (when their templates strip the legacy flag but the runtime still reads from attribute fields) and recover at Task 8. If any test fails after Task 3, the sweep missed an entry — return to step 2.

8. Run `make lint`. Clean.

### Task 4: Migrate every legacy `hard_dep` attribute-field flag to the new subscription-block shape

**Files:** the four files identified by `rg -l '("hard_dep"|prop\["hard_dep"\])' --type go test/ lib/`:

- `test/scenarios/per_run_attributes/hard_dep_test.go`
- `test/scenarios/multi_hard_dep_test.go`
- `lib/runtime/hard_dep_cascade_test.go`
- `lib/graph/node/hard_dep_edges_test.go`

(The Go runtime files that read `hard_dep:` — `lib/graph/node/hard_dep_edges.go` and its consumers in `lib/runtime/` — are not edited here; their input-source change lives in Task 8.)

**Steps:**

1. For each file, locate every attribute-schema property carrying `hard_dep: true`. Two construction styles appear in the codebase:

   - JSON-string style: `"hard_dep": true` embedded in a Go string-formatted template.
   - Go-map style: `prop["hard_dep"] = true` in code that builds the attribute schema programmatically (this is `lib/graph/node/hard_dep_edges_test.go`'s style — see line ~21 of that file).

   For each occurrence:

   a. Identify the sender — the `{{nodes.<X>.attribute.<Y>}}` ref this hard-dep targets (parse the property's `source:`).

   b. Remove the `hard_dep` flag from the attribute property.

   c. Locate the receiver node's `Subscribes:` block. If a subscription entry already names this sender with `Type: "attribute/<Y>/changed"` or `Type: "attribute/*"`, change its `ForceUpstreamRefresh` value to `spec.BoolPtr(true)`. If no such subscription entry exists, add one:

   ```go
   spec.SubscriptionEntry{
     Node:                 "<X>",
     Type:                 "attribute/<Y>/changed",
     WakeOnChange:         spec.BoolPtr(true),
     ForceUpstreamRefresh: spec.BoolPtr(true),
   }
   ```

   The added entry preserves combined "fire-on-change + pull-upstream" behavior, which is what the old `hard_dep` flag delivered combined with whatever implicit cascade the substitution ref induced.

2. After each file's edits, run `go build ./...` and confirm it compiles.

3. After all files are edited, run `rg -n 'hard_dep' --type go test/ lib/`. The remaining occurrences should be exactly:
   - `lib/graph/node/hard_dep_edges.go` (the runtime side — Task 8 repurposes its source)
   - Test struct-name references (e.g., `TestBuildHardDepEdges_SimpleHardDep`)
   - Doc-comments referencing the retired flag (Task 10 refreshes)
   - Filename-self references (e.g., `parked_lifecycle_test.go` doc-comment pointing at `hard_dep_cascade_test.go`)

   No attribute-schema property anywhere carries the flag.

4. Run `go test ./...`. The four hard-dep tests will fail because the runtime still reads `hard_dep:` from attribute fields (Task 8 has not landed yet). Other tests pass. The transient failure resolves at Task 8.

### Task 5: Sweep every template that reads a substitution ref without a covering explicit subscription

**Files:** every file constructing a template with substitution directives in attribute schemas. Discovered by:

```bash
rg -n '\{\{nodes\.[a-zA-Z_][a-zA-Z0-9_-]*\.(attribute|event)' --type go test/ lib/
```

**Steps:**

1. Read `lib/graph/node/subscription_edges.go::ExtractSubstitutionRefsFromTemplate` (line 437) and `parseSubstitutionRefsFromAttributes` (line 516) to understand what counts as a substitution-implied edge under the current implicit-subscribe rule. The parser skips `claim.*` and `params.*` directives (those don't auto-subscribe today and remain non-cascading under this spec); it also skips refs that name the receiver itself.

2. Run the rg above and enumerate the substitution-ref occurrences per file. For each occurrence, identify the receiver node and the sender node.

3. For each (receiver, sender) pair, inspect the receiver's `Subscribes:` block. If the block lacks an entry whose `Node:` matches the sender and `Type:` would deliver the implied signal:

   - For `{{nodes.X.attribute.Y}}`: add `{Node: "X", Type: "attribute/Y/changed", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}`.
   - For `{{nodes.X.attribute}}` (whole-pull, no field): add `{Node: "X", Type: "attribute/*", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}`.
   - For `{{nodes.X.event.Y}}`: add `{Node: "X", Type: "event/Y", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}`.

   `WakeOnChange: true` is the today-equivalent — the implicit edge today fires the receiver on the sender's change, and the migrated explicit entry preserves that.

4. Re-run the rg from step 1 and re-check the same sample. For each match, spot-verify the receiver's `Subscribes:` block carries a covering entry. (Pass 2 Task 13 mechanizes this verification at registration; this manual sweep is the migration step.)

5. Run `go test ./...`. Same state as end of Task 4 — only the four hard-dep tests fail.

### Task 6: Extend `SubscriptionEdge` with the two flag values and propagate them through `edgeFromSubscription`

**Files:** `lib/graph/node/subscription_edges.go`

**Steps:**

1. Read `lib/graph/node/subscription_edges.go::SubscriptionEdge` (currently lines 37-43). The struct carries `ReceiverNodeType`, `TypePattern`, `WhenExpr`, `SubscriptionScope`, `Frame`. Extend with two bool fields:

   ```go
   type SubscriptionEdge struct {
     ReceiverNodeType     string
     TypePattern          signal.TypePath
     WhenExpr             *signal.CompiledPredicate
     SubscriptionScope    string
     Frame                string
     WakeOnChange         bool   // unwrapped from SubscriptionEntry.WakeOnChange at edge construction
     ForceUpstreamRefresh bool   // unwrapped from SubscriptionEntry.ForceUpstreamRefresh at edge construction
   }
   ```

2. Locate `edgeFromSubscription` (the constructor used in `BuildSubscriptionEdges` around line 399). Read the function to find its current signature, then extend it to copy and validate the new fields:

   ```go
   func edgeFromSubscription(s spec.SubscriptionEntry, receiverType string) (SubscriptionEdge, error) {
     // ... existing parsing of Type/When/Frame ...
     if s.WakeOnChange == nil {
       return SubscriptionEdge{}, fmt.Errorf("subscription on %q to %q missing wake_on_change", receiverType, s.Type)
     }
     if s.ForceUpstreamRefresh == nil {
       return SubscriptionEdge{}, fmt.Errorf("subscription on %q to %q missing force_upstream_refresh", receiverType, s.Type)
     }
     return SubscriptionEdge{
       ReceiverNodeType:     receiverType,
       // ... existing fields ...
       WakeOnChange:         *s.WakeOnChange,
       ForceUpstreamRefresh: *s.ForceUpstreamRefresh,
     }, nil
   }
   ```

   The nil-checks are defense-in-depth — Pass 2's validator catches missing flags first, but the edge-builder refuses to silently coerce nil to false.

3. Run `go build ./...` and confirm it compiles.

4. Run `go test ./lib/graph/node/... -count=1`. The unit tests against the edge map should pass — every migrated entry has both flags set (Tasks 3-4), so the nil-check never fires.

### Task 7: Gate `cascadeSubscribersStaleInTx`'s receiver stale-mark on `wake_on_change`

**Files:** `lib/runtime/runner_terminal.go`

**Steps:**

1. Read `lib/runtime/runner_terminal.go::cascadeSubscribersStaleInTx`. The function is around line 561; its main per-edge loop runs through ~line 950. Two regions of the loop carry "wake-up" effects:

   - The `case node.FrameNext:` branch at lines 731-766, which calls `frame.EnqueueOrCoalesce` to queue a follow-on frame.
   - The `default:` (FrameIn) branch, where the settled-this-frame guard at lines 856-877 (with the self-edge bypass sub-check at line 868) skips the receiver-affirm under a different condition; the actual stale-mark of the receiver run row happens after the guard's `skipAffirm`/affirm decision.

2. Gate the wake-up effect on `edge.WakeOnChange` in both branches:

   - In the `case node.FrameNext:` branch: wrap the `frame.EnqueueOrCoalesce` call and the subsequent wake-parked-receiver logic in `if edge.WakeOnChange { ... }`. The new-frame enqueue is the wake-up effect for the FrameNext case.
   - In the `default:` branch: wrap the affirm-and-mark-stale path in `if edge.WakeOnChange { ... }`. The wait-set insert (`InsertWaitSetRow` or the equivalent in this function — verify by reading) MUST stay outside the gate, so the receiver's substitution context still picks up the sender's drained value when the receiver eventually dispatches via some other edge.

   **Load-bearing property — the wait-set insert is unconditional.** Do NOT make the wait-set insert conditional on `wake_on_change`. STORY-read-without-waking depends on the receiver's substitution context containing the sender's data even when `wake_on_change: false` — and that data flows through the wait-set drain. A cheaper shape that gates the wait-set insert would break the receiver's `{{nodes.X.attribute.Y}}` read entirely.

3. The settled-this-frame guard at lines 856-877 is orthogonal to `wake_on_change` and stays as-is. The guard prevents re-affirming a receiver that already settled in this frame (preventing mutual re-seeding in two-upstream-refresh shapes); it does not interact with the new flag.

4. Run `go build ./lib/runtime/... ./lib/graph/...` and confirm it compiles.

5. Run `go test ./lib/runtime/... -count=1`. Cascade-related tests still pass (every existing template has `WakeOnChange: true`, so the new gate is a no-op for migrated tests).

### Task 8: Repurpose `BuildHardDepEdges` to read from subscription-block flags

**Files:** `lib/graph/node/hard_dep_edges.go`, `lib/graph/node/hard_dep_edges_test.go`

**Steps:**

1. Read `lib/graph/node/hard_dep_edges.go::BuildHardDepEdges` (line 50) and `hardDepSendersOf` (line 88). The current implementation walks every node's `attributes:` schema looking for properties with `hard_dep: true` and `source:` referencing `{{nodes.X.attribute.Y}}`; produces `HardDepEdgeMap` keyed by receiver node-type with a list of upstream node-types.

2. Rewrite `hardDepSendersOf` to walk the node's `Subscribes:` entries instead of attribute properties:

   ```go
   // hardDepSendersOf returns the upstream node-types named by
   // subscription entries on n that carry force_upstream_refresh: true.
   // Source moved from attribute-field hard_dep: true flag to the
   // subscription-block force_upstream_refresh flag under spec
   // 2026-06-14-explicit-substitution-cascade-behavior.
   //
   //	@concept: cascade
   func hardDepSendersOf(n TemplateNodeDef) []string {
     out := []string{}
     seen := map[string]bool{}
     for _, s := range n.Subscribes {
       if s.ForceUpstreamRefresh == nil || !*s.ForceUpstreamRefresh {
         continue
       }
       // Cross-cutting subscriptions (Instance=true) cannot carry
       // force_upstream_refresh; Pass 2 Task 12's validator rejects
       // that combination. Skip defensively so this builder doesn't
       // panic on a malformed input that slipped past validation.
       if s.Instance {
         continue
       }
       // Self-edges are skipped — a node doesn't pull itself.
       if s.Node == "" || s.Node == n.Type {
         continue
       }
       if seen[s.Node] {
         continue
       }
       seen[s.Node] = true
       out = append(out, s.Node)
     }
     return out
   }
   ```

3. The `BuildHardDepEdges` outer function (cycle detection + fan-out-target rejection at lines 53-78) operates on the receiver→sender map output and stays unchanged — only the input source moved.

4. Rewrite `lib/graph/node/hard_dep_edges_test.go`. Read the file (~5 tests including `TestBuildHardDepEdges_SimpleHardDep`, `_NoHardDep`, `_SelfReferenceIgnored`, `_CycleDetected`). For each test, swap the template construction from attribute-field `hard_dep: true` to subscription-block shape:

   ```go
   // Pre:
   prop["hard_dep"] = true

   // Post:
   n.Subscribes = append(n.Subscribes, spec.SubscriptionEntry{
     Node:                 "<upstream-type>",
     Type:                 "attribute/<field>/changed",
     WakeOnChange:         spec.BoolPtr(true),
     ForceUpstreamRefresh: spec.BoolPtr(true),
   })
   ```

   The expected `HardDepEdgeMap` output is unchanged across the migration.

5. Run `go test ./lib/graph/node/... -run HardDep -count=1`. Tests pass.

6. Run `go test ./lib/runtime/... -run HardDep -count=1`. `hard_dep_cascade_test.go`'s tests now pass (their templates were migrated in Task 4 and the runtime now reads from the subscription flags). One of the previously-failing four tests recovers.

7. Run `go test ./test/scenarios/per_run_attributes/ -count=1 -timeout=5m`. Recovers.

8. Run `go test ./test/scenarios/ -run MultiHardDep -count=1 -timeout=5m`. Recovers.

### Task 9: Drop the implicit-edge loop from `BuildSubscriptionEdges`, drop the substitution-refs parameter, update callers

**Files:** `lib/graph/node/subscription_edges.go`, `lib/graph/scheduler/pure_cascade.go`, `lib/runtime/subscription_loaders.go`

**Steps:**

1. Read `lib/graph/node/subscription_edges.go::BuildSubscriptionEdges` (line 390). The function accepts a `substitutionRefs map[string][]substitutionRef` parameter and emits both explicit-subscription edges (lines 398-405) and an "Implicit subscriptions from substitution refs" loop (lines 406-410).

2. Drop the implicit-edge loop (lines 406-410). Drop the `substitutionRefs` parameter from the function signature. The function now only walks `n.Subscribes` and emits one edge per entry.

3. The supporting helpers split into two groups:

   - **Retained:** `ExtractSubstitutionRefsFromTemplate` (line 437), `parseSubstitutionRefsFromAttributes` (line 516), `substitutionRef` struct (line 417), `substitutionDirectiveRe` (line 426). Pass 2 Task 13's coverage check consumes these to enumerate substitution refs per receiver.
   - **Retired:** `edgeFromSubstitutionRef` (find it — it's the helper the dropped loop called). No remaining caller. Delete it.

4. Update the two callers to drop the now-unused substitution-refs argument:

   - `lib/graph/scheduler/pure_cascade.go` lines 193-194: the call pair currently is

     ```go
     subs := nodepkg.ExtractSubstitutionRefsFromTemplate(row.Spec)
     edges, err := nodepkg.BuildSubscriptionEdges(row.Spec, subs)
     ```

     Read the surrounding function. If `subs` is used only as the second argument to `BuildSubscriptionEdges`, drop both lines and replace with `edges, err := nodepkg.BuildSubscriptionEdges(row.Spec)`. If `subs` is used elsewhere in the function, retain the `ExtractSubstitutionRefsFromTemplate` call and drop only the second argument.

   - `lib/runtime/subscription_loaders.go::subscriptionEdgesForTemplate` (lines 75-76): identical shape, identical fix.

5. Update `subscription_edges.go`'s package doc-comment (lines 5-22) to remove the "substitution-ref auto-subscribe inference" mention. The new wording: *"Computed at template registration by the validator (`template_validator.go::validateSubscribes`) from the template's explicit `subscribes:` block."*

6. Run `go build ./...` and `make lint`. Clean.

### Task 10: Refresh doc-comments in code files that name the retired implicit-subscribe / `hard_dep:` mechanism

**Files:** `lib/foundation/spec/template.go`, `lib/foundation/signal/taxonomy.go`, `lib/runtime/runner_terminal.go`, `lib/graph/attribute/substitution.go`, `lib/graph/node/subscription_edges.go` (multiple sites).

**Steps:**

1. Run `rg -n 'auto-subscribe|auto.subscription|implicit subscription|substitution-ref auto' --type go` and enumerate every remaining doc-comment mention. Expected sites (line numbers may have shifted slightly during prior tasks — re-locate by content):

   - `lib/foundation/spec/template.go` (~line 138)
   - `lib/foundation/signal/taxonomy.go` (~line 65)
   - `lib/runtime/runner_terminal.go` (~line 1079)
   - Additional in-file comments in `lib/graph/node/subscription_edges.go` (~lines 7, 304, 432, 507, 521, 551, 584, 587, 608, 720)

2. For each mention, rewrite the comment to describe the new model: explicit `subscribes:` block as the sole edge-emission source; registration-time coverage check validates every substitution ref is matched by a subscription. Do not write "previously the implicit-edge inference did X" — describe the new state directly.

3. In `lib/graph/attribute/substitution.go` line 21 the package doc mentions auto-subscribe. Rewrite the comment to remove that mention; the file's job (resolving substitution refs at dispatch) does not change.

4. Run `go build ./...` and `make lint`. Clean.

### Task 11: Verify Pass 1 falsifier — every entry migrated, every implicit edge gone, every runtime gate landed, tree fully green

**Files:** none (verification only)

**Steps:**

1. Run `rg -n 'WakeOnChange|ForceUpstreamRefresh' lib/foundation/spec/subscription.go` and confirm both fields are present on `SubscriptionEntry`.

2. Run the multi-line field-presence sweep:

   ```bash
   rg -nU --multiline 'SubscriptionEntry\{[^}]+\}' --type go test/ lib/ |
     awk '/SubscriptionEntry\{/,/\}/ {block = block "\n" $0; if (/\}/) { if (!(block ~ /WakeOnChange/ && block ~ /ForceUpstreamRefresh/)) print "MISSED: " FILENAME ": " block; block=""}}'
   ```

   Empty output. Every struct-literal construction sets both fields.

3. Run `rg -n '"hard_dep"\s*:\s*true' --type go test/ lib/`. Empty result.

4. Run `rg -n 'prop\["hard_dep"\]\s*=\s*true' --type go test/ lib/`. Empty result. No attribute-schema property in any test carries the retired flag.

5. Run `rg -n '\{\{nodes\.[a-zA-Z_][a-zA-Z0-9_-]*\.(attribute|event)' --type go test/ lib/` and spot-check ten random matches: for each, verify the receiver's `Subscribes:` block names the sender with an appropriate signal type.

6. Run `go build ./... && make lint`. Both clean.

7. Run `go test ./... -count=1`. **Every test passes.** Tree is fully green at end of pass.

8. Run `go test ./test/scenarios/... -count=3 -race -timeout=30m`. No flakiness introduced by the cascade walker's gate change.

---

## Pass 2: Validator hardening — coverage check, cross-cutting incoherence rejection, structured error envelope

**Goal:** Tighten the template registration validator. Reject subscription entries missing either flag. Reject the cross-cutting + `force_upstream_refresh: true` incoherent combination. Add the substitution-ref coverage check — every substitution ref must be matched by at least one subscription entry. Emit a structured `substitution_ref_uncovered` error entry alongside the existing `{path, msg}` shape in the `validation_errors` array (additive, not replacing). After this pass, registration rejects any template authored against the legacy implicit-edge model or the legacy `hard_dep:` field surface.

**Scope:** Tasks 12–15.

**Falsifier:** `validateSubscribes` does not reject entries missing `wake_on_change` or `force_upstream_refresh`, OR the cross-cutting + `force_upstream_refresh: true` combination is accepted at registration, OR a template with a substitution ref of any shape (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Y}}`, `{{nodes.X.attribute}}`) but no covering `subscribes:` entry registers without error, OR the rejection response body for an uncovered ref does not contain the structured `substitution_ref_uncovered` entry with all six named fields (`kind`, `receiver_node_type`, `ref`, `attribute_property`, `suggested_subscribes_entry`, `suggested_subscribes_note`).

### Task 12: Reject subscription entries missing either flag and reject cross-cutting + force_upstream_refresh combination

**Files:** `lib/graph/node/template_validator.go`

**Steps:**

1. Read `lib/graph/node/template_validator.go::validateSubscribes` (line 636). The function performs per-entry shape validation. Add three new checks at the start of the per-entry loop:

   ```go
   if s.WakeOnChange == nil {
     res.Errors = append(res.Errors, ValidationError{
       Path: fmt.Sprintf("nodes[%s].subscribes[%d].wake_on_change", n.Type, i),
       Msg:  "wake_on_change is required (true or false); no default applies",
     })
     continue
   }
   if s.ForceUpstreamRefresh == nil {
     res.Errors = append(res.Errors, ValidationError{
       Path: fmt.Sprintf("nodes[%s].subscribes[%d].force_upstream_refresh", n.Type, i),
       Msg:  "force_upstream_refresh is required (true or false); no default applies",
     })
     continue
   }
   if s.Instance && *s.ForceUpstreamRefresh {
     res.Errors = append(res.Errors, ValidationError{
       Path: fmt.Sprintf("nodes[%s].subscribes[%d]", n.Type, i),
       Msg:  "force_upstream_refresh: true cannot be combined with instance: true (cross-cutting subscriptions are sender-agnostic; there is no specific upstream to refresh)",
     })
   }
   ```

   These errors use the existing `{path, msg}` shape (per TD-validation-errors-additive-not-uniform — only the substitution-ref coverage error gets the richer shape).

2. Run `go test ./lib/graph/node/... -run TestValidate -count=1` and confirm existing validator tests still pass (every migrated template has both flags set).

3. Add three new test cases to `lib/graph/node/template_validator_test.go` exercising the new rejections:
   - Entry missing `wake_on_change` → assert a `{path, msg}` entry whose `path` ends in `.wake_on_change` and `msg` says required.
   - Entry missing `force_upstream_refresh` → assert a `{path, msg}` entry whose `path` ends in `.force_upstream_refresh`.
   - `instance: true` + `force_upstream_refresh: true` → assert a `{path, msg}` entry whose `msg` mentions both fields.

4. Run `go test ./lib/graph/node/... -run TestValidate -count=1`. The three new tests pass.

### Task 13: Implement the substitution-ref coverage check

**Files:** `lib/graph/node/template_validator.go`, `lib/graph/node/subscription_edges.go`

**Steps:**

1. Read `lib/graph/node/template_validator.go::ValidateTemplate` around line 316 — the function already calls `ExtractSubstitutionRefsFromTemplate`. Use the return value as input to the new coverage check.

2. Extend the parsed `substitutionRef` struct in `subscription_edges.go` (currently line 417) to capture the literal ref text and the schema path. The current struct carries `SenderNodeType`, `TopicKind` ("attribute"|"event"), `Name`. Add:

   ```go
   type substitutionRef struct {
     SenderNodeType   string
     TopicKind        string  // "attribute" | "event"
     Name             string  // attribute key or event name; "" indicates whole-pull
     RefLiteral       string  // the exact "{{nodes.X.attribute.Y}}" text
     AttributeProperty string // the schema property path the ref appears in
   }
   ```

   Update `parseSubstitutionRefsFromAttributes` (line 516) to populate both new fields during parsing.

3. Add a new function `validateSubstitutionRefCoverage` placed near `validateSubscribes` in `template_validator.go`:

   ```go
   // validateSubstitutionRefCoverage walks every substitution ref per
   // receiver and rejects refs that no subscribes: entry matches.
   //
   // Coverage rules (per decision:coverage-wildcard-asymmetry):
   //   {{nodes.X.attribute.Y}}   <- attribute/Y/changed OR attribute/*
   //   {{nodes.X.attribute}}     <- attribute/* only (wildcard required)
   //   {{nodes.X.event.Y}}       <- event/Y
   //
   //	@concept: node-subscription
   //	@concept: attribute
   func validateSubstitutionRefCoverage(tmpl *TemplateSpec, refs map[string][]substitutionRef, res *ValidationResult) {
     // Build per-receiver index of subscribes entries keyed by (sender, type).
     // For each receiver's refs, iterate and check the implied signal-type
     // is covered. Emit a structured substitution_ref_uncovered entry
     // per uncovered ref.
   }
   ```

4. The implied signal type per ref shape:

   - `TopicKind == "attribute"` and `Name != ""` → required type `attribute/<Name>/changed`, covered by exact `attribute/<Name>/changed` or by wildcard `attribute/*`.
   - `TopicKind == "attribute"` and `Name == ""` (whole-pull) → required type `attribute/*`, covered ONLY by exact `attribute/*` (not by any per-field `attribute/Y/changed`).
   - `TopicKind == "event"` → required type `event/<Name>`, covered by exact `event/<Name>`.

5. The existing `ValidationError` type carries `Path` and `Msg` only. Extend the validator's result type with a sibling slice for structured entries. Read the surrounding types to choose the smaller diff — likely a new `StructuredErrors []map[string]any` slice on `ValidationResult`. Emit one entry per uncovered ref:

   ```go
   res.StructuredErrors = append(res.StructuredErrors, map[string]any{
     "kind":               "substitution_ref_uncovered",
     "receiver_node_type": receiverType,
     "ref":                ref.RefLiteral,
     "attribute_property": ref.AttributeProperty,
     "suggested_subscribes_entry": map[string]any{
       "node":                   ref.SenderNodeType,
       "type":                   suggestedType,  // "attribute/<Name>/changed" or "attribute/*" or "event/<Name>"
       "wake_on_change":         false,
       "force_upstream_refresh": false,
     },
     "suggested_subscribes_note": fmt.Sprintf(
       "set wake_on_change: true if this ref should also fire this receiver; set force_upstream_refresh: true if %s should be re-evaluated when this receiver is invalidated",
       ref.SenderNodeType,
     ),
   })
   ```

   The `suggested_subscribes_entry` is a flat JSON object the author can copy-paste verbatim into their template. The note is a sibling field, not embedded inside the entry — per TD-uncovered-substitution-error-shape, the entry stays drop-in valid.

6. Wire the new check into `ValidateTemplate`'s top-level flow alongside the existing `refs := ExtractSubstitutionRefsFromTemplate(*spec)` call (line 316):

   ```go
   refs := ExtractSubstitutionRefsFromTemplate(*spec)
   validateSubstitutionRefCoverage(spec, refs, &res)
   ```

7. Run `go test ./lib/graph/node/... -run TestValidate -count=1`. Pre-existing tests pass because Pass 1 added explicit covering subscriptions to every template with substitution refs.

### Task 14: Render the structured error entry in the registration HTTP response

**Files:** `lib/control/controlapi/templates.go`

**Steps:**

1. Read `lib/control/controlapi/templates.go` around lines 215-235 — the existing handler that emits `validation_errors` from `res.Errors`.

2. Extend the response-building code to include the structured-error entries alongside the existing `{path, msg}` entries. The output is one flat array; entries with the structured shape contain the `kind` field as their discriminator; entries with the legacy shape contain `path` + `msg`. Example:

   ```go
   if !res.Ok() {
     entries := make([]map[string]any, 0, len(res.Errors)+len(res.StructuredErrors))
     for _, e := range res.Errors {
       entries = append(entries, map[string]any{"path": e.Path, "msg": e.Msg})
     }
     for _, e := range res.StructuredErrors {
       entries = append(entries, e)
     }
     writeJSON(w, http.StatusBadRequest, map[string]any{
       "error":               shared.ErrTemplateValidation.Error(),
       "validation_errors":   entries,
       "validation_warnings": staticWarningsToFindings(res.Warnings),
     })
     return
   }
   ```

   (Adapt to the actual `res.StructuredErrors` accessor Task 13 introduced.)

3. **Load-bearing property — order is deterministic.** The array is built by appending `res.Errors` first (in iteration order, which is insertion order for a slice) and `res.StructuredErrors` second (same). No map iteration on the final array. The order matters because Task 19's scenario test asserts specific entries appear in specific positions.

4. Run `go test ./lib/control/controlapi/... -count=1` and confirm existing template-registration tests still pass.

### Task 15: Verify Pass 2 falsifier

**Files:** none (verification only)

**Steps:**

1. Run `go test ./... -count=1`. All tests pass.

2. Manually construct an in-process test (in a scratch file you delete after) that submits each of the following templates and confirms the expected error response:
   - Template with a `subscribes:` entry missing `wake_on_change` → `{path, msg}` entry whose `path` ends in `.wake_on_change`.
   - Template with `instance: true` and `force_upstream_refresh: true` → `{path, msg}` entry whose `msg` mentions both fields.
   - Template with `source: "{{nodes.foo.attribute.bar}}"` but no `subscribes:` entry naming `foo` → structured entry with `kind: "substitution_ref_uncovered"`, `ref: "{{nodes.foo.attribute.bar}}"`, `suggested_subscribes_entry.type: "attribute/bar/changed"`.
   - Template with `source: "{{nodes.foo.attribute}}"` and a per-field `attribute/bar/changed` subscription on `foo` → structured entry (whole-pull not covered by per-field; asymmetry rule).

3. Delete the scratch test. The four scenarios above are expressed as durable validator unit tests in Pass 4 Task 19.

4. Run `make lint`. Clean.

---

## Pass 3: Doc comments, design-doc mutations, design-doc creations

**Goal:** Apply every design-doc mutation enumerated in the spec's `## Design changes` section — three concept mutations, two existing-artifact mutations, three new story files, seventeen new decision files. No code changes in this pass — purely durable design artifacts and concept body text.

**Scope:** Tasks 16–17.

**Falsifier:** any of the 25 design-change bullets in the spec's `## Design changes` section is unapplied — a concept file's body still contains the pre-spec text the spec directed to be rewritten, a new story or decision file is missing from `.ok-planner/design/`, an existing artifact's body wasn't updated, or a new artifact's body contains forward-looking or backward-looking phrasing that violates the current-state-only rule.

### Task 16: Apply concept and existing-artifact mutations

**Files:**
- `.ok-planner/design/concepts/attribute.md`
- `.ok-planner/design/concepts/node-subscription.md`
- `.ok-planner/design/concepts/cascade.md`
- `.ok-planner/design/stories/multi-hard-dep-rendezvous.md`
- `.ok-planner/design/decisions/hard-dep-settled-guard.md`

**Steps:**

1. For each of the five files above, open the spec at `.ok-planner/specs/2026-06-14-explicit-substitution-cascade-behavior-design.md` and locate the relevant `## Design changes` bullet. Each bullet enumerates the exact new body text for each section the spec mutates.

2. Apply the bullets verbatim. The spec's body text for each mutation is self-contained (path-free, no `code:` citations, no `(was X)` lines, no `## Notes` / `## History` sections).

   For `concepts/attribute.md`:
   - Rewrite the Non-goals entry currently naming `pull_only:` and `hard_dep:` per the spec's design-changes bullet.
   - Remove the Invariants line about `hard_dep:`.
   - Add the Boundaries note about cascade-shape configuration living in `subscribes:`.

   For `concepts/node-subscription.md`:
   - Rewrite "What it is" per spec.
   - Rewrite the Owns section's first two bullets per spec.
   - Replace the Invariants line about auto-subscribe per spec.
   - Add the new Invariants line about both flags being required.

   For `concepts/cascade.md`:
   - Rewrite the Invariants "fires iff" line per spec.
   - Rewrite the Boundaries paragraph about edge maps per spec.

   For `stories/multi-hard-dep-rendezvous.md`:
   - Rewrite Role, Capability, and Acceptance per spec.
   - Falsifier, Business value, and Proof sections unchanged.

   For `decisions/hard-dep-settled-guard.md`:
   - Rewrite Choice and Rationale per spec.

3. After each file is edited, re-read it and verify: no `## Notes` section, no dated audit entries, no "previously was X", no path citations, no `code:` references. Slug citations (`concept:cascade`, `story:multi-hard-dep-rendezvous`, etc.) are allowed.

4. Run `rg -n 'hard_dep' .ok-planner/design/` and confirm the only remaining references are intentional concept-noun-style mentions (e.g., the `decision:hard-dep-settled-guard` slug itself) — every load-bearing prose mention of the legacy flag is gone.

### Task 17: Create the three new story files and seventeen new decision files

**Files:**
- `.ok-planner/design/stories/explicit-attribute-context-read.md` (new)
- `.ok-planner/design/stories/upstream-pull-on-invalidate.md` (new)
- `.ok-planner/design/stories/uncovered-substitution-rejected.md` (new)
- `.ok-planner/design/decisions/cascade-flags-on-subscribes.md` (new)
- `.ok-planner/design/decisions/cascade-flags-required-no-defaults.md` (new)
- `.ok-planner/design/decisions/substitution-grammar-closed.md` (new)
- `.ok-planner/design/decisions/substitution-ref-coverage-required.md` (new)
- `.ok-planner/design/decisions/coverage-wildcard-asymmetry.md` (new)
- `.ok-planner/design/decisions/cross-cutting-no-force-upstream-refresh.md` (new)
- `.ok-planner/design/decisions/uncovered-substitution-error-shape.md` (new)
- `.ok-planner/design/decisions/validation-errors-additive-not-uniform.md` (new)
- `.ok-planner/design/decisions/hard-dep-field-no-special-case.md` (new)
- `.ok-planner/design/decisions/wake-on-change-wait-set-only.md` (new)
- `.ok-planner/design/decisions/force-upstream-refresh-via-receiver-keyed-map.md` (new)
- `.ok-planner/design/decisions/implicit-edge-generation-retired.md` (new)
- `.ok-planner/design/decisions/substitution-context-builder-unchanged.md` (new)
- `.ok-planner/design/decisions/substitution-grammar-fallback-unchanged.md` (new)
- `.ok-planner/design/decisions/migration-fills-flags-today-equivalent.md` (new)
- `.ok-planner/design/decisions/migration-hard-dep-becomes-force-refresh.md` (new)
- `.ok-planner/design/decisions/migration-implicit-edges-become-explicit.md` (new)

**Steps:**

1. Read `.ok-planner/design/stories/cascade-signal-blind.md` as a shape reference for new story files (frontmatter, section order, prose style). Read `.ok-planner/design/decisions/cascade-inside-settlement.md` as a shape reference for new decision files. Match the prevailing voice and section structure.

2. For each new story file, create the file with:
   - YAML frontmatter: `story: <slug>`, `status: as-is`
   - `# <Title>` heading (short noun phrase naming the user-outcome)
   - `## Role` section
   - `## Capability` section
   - `## Business value` section
   - `## Acceptance` section
   - `## Falsifier` section
   - `## Proof` section

   Body text comes from the spec's User outcomes section. Each story's STORY-`<slug>` block has Acceptance, Falsifier, and Proof; transcribe them into the new story file. The spec's Role line ("As a template author...") splits across `## Role` (the "As a..." phrasing) and `## Capability` (the "I can..." phrasing); use the same prose. Add a `## Business value` section per the prevailing format — for these three stories, a short statement of what the explicit-control affordance gives template authors.

3. For each new decision file, create the file with:
   - YAML frontmatter: `decision: <slug>`, `status: as-is`
   - `# <Title>` heading
   - `## Choice` section
   - `## Rationale` section
   - `## Alternatives` section (only when the spec recorded alternatives; omit otherwise — see the prevailing pattern in the existing decisions directory)

   Body text comes from the spec's `## Design changes` bullets for each `decisions/<slug>.md` creation. Each bullet contains the Choice and Rationale text verbatim; transcribe.

4. Self-containment check on every new file: re-read it and verify no file paths, no `code:` citations, no external-doc references, no quoted code, no "Owns / Does NOT own" sections naming paths. Slug citations (`concept:cascade`, `story:explicit-attribute-context-read`, `decision:cascade-flags-on-subscribes`) are allowed.

5. Run `ls .ok-planner/design/stories/ | wc -l` and `ls .ok-planner/design/decisions/ | wc -l` to confirm the file counts increased by exactly 3 and 17 respectively.

6. The TOC files (`.ok-planner/design/concepts.md`, `stories.md`, `decisions.md`) are auto-generated and carry "(auto-generated)" headers. If the project has a regenerator script, run it; otherwise leave the TOCs as they are — `execute-plan`'s closing flow regenerates them.

---

## Pass 4: Acceptance — STORY-read-without-waking, STORY-pull-upstream-fresh-on-read, STORY-uncovered-read-rejected (acceptance pass — STORY-read-without-waking, STORY-pull-upstream-fresh-on-read, STORY-uncovered-read-rejected)

**Goal:** Deliver the three user-outcome stories' proof artifacts. Each story exhibits its acceptance through the real assembled product driven by the existing testcontainers-based scenario harness. Wiring for all three stories landed in Passes 1-2; this pass authors the proof artifacts and binds each to its story.

**Scope:** Tasks 18–21.

**Falsifier:**

- STORY-read-without-waking falsifier (per spec): the receiver fires on the sender's `attribute/<Y>/changed` despite the proof template setting `wake_on_change: false` on the matching subscription — meaning the cascade walker's gate from Pass 1 Task 7 is broken or absent.
- STORY-pull-upstream-fresh-on-read falsifier (per spec): the receiver's substitution context at dispatch contains a stale value for the pulled upstream (matching the upstream's pre-frame state, not a value produced this frame), or the dispatch fails because the upstream's value is absent — meaning the hard-dep edge map's input-source migration from Pass 1 Task 8 is broken or absent.
- STORY-uncovered-read-rejected falsifier (per spec): a template registers despite carrying a substitution ref with no covering subscription (silent acceptance with deferred runtime failure), OR registration fails but the response body lacks the structured `substitution_ref_uncovered` entry with all six fields (`kind`, `receiver_node_type`, `ref`, `attribute_property`, `suggested_subscribes_entry`, `suggested_subscribes_note`) plus a valid drop-in `suggested_subscribes_entry` JSON object.

The proof artifacts boot the assembled product via the existing testcontainers harness used by `test/scenarios/`, exercising the real cascade walker and the real control-API surface. In-process construction of the cascade walker would not satisfy stories 1 and 2; in-process construction of the validator would not satisfy story 3.

### Task 18: Proof artifact for STORY-read-without-waking

**Files:** `test/scenarios/explicit_attribute_context_read_test.go` (new)

**Story:** STORY-read-without-waking
**Proof form (from spec):** all-of-the-above — an example template exhibiting the gated subscription plus context-gathering reads, and an executable proof that walks the two scenarios (X changes alone → A does not fire; A's gate matches → A fires and reads X's value).

**Steps:**

1. Read `test/scenarios/cascade_signal_blind_e2e_test.go` as a shape reference for how scenario tests boot the testcontainers stack, register a template, drive a sender, and inspect downstream behavior via the event log.

2. Create `test/scenarios/explicit_attribute_context_read_test.go` with a single test function:

   ```go
   // TestStoryReadWithoutWaking exhibits STORY-read-without-waking
   // (spec 2026-06-14-explicit-substitution-cascade-behavior).
   //
   // Template shape mirrors the GitHub issue #18 author's scenario:
   //   - receiver A has one gated explicit subscription (when:
   //     payload.value == 'needs_work') to one sender (gate-sender G)
   //   - receiver A reads {{nodes.X.attribute.Y}} from a different
   //     sender X with wake_on_change=false on the covering subscription
   //
   // Two assertions:
   //   1. Frame where X's attribute/Y changes alone (G's gate not matching):
   //      A does NOT dispatch.
   //   2. Frame where G's gate matches AND X is in the frame:
   //      A dispatches once, A's substitution context contains X's value.
   //
   //	@story: explicit-attribute-context-read
   func TestStoryReadWithoutWaking(t *testing.T) { ... }
   ```

3. Construct the template with three node types (`gate-sender`, `context-sender`, `receiver`). Receiver's subscribes block:

   ```go
   Subscribes: []spec.SubscriptionEntry{
     {
       Node:                 "gate-sender",
       Type:                 "attribute/status/changed",
       When:                 "payload.value == 'needs_work'",
       WakeOnChange:         spec.BoolPtr(true),
       ForceUpstreamRefresh: spec.BoolPtr(false),
     },
     {
       Node:                 "context-sender",
       Type:                 "attribute/data/changed",
       WakeOnChange:         spec.BoolPtr(false),  // story 1 the point
       ForceUpstreamRefresh: spec.BoolPtr(false),
     },
   }
   ```

   Receiver's attribute schema reads `{{nodes.gate-sender.attribute.status}}` and `{{nodes.context-sender.attribute.data}}`.

4. Drive scenario 1 — invalidate only `context-sender` (use the operator-API invalidate endpoint per the prevailing scenario-test pattern; consult `test/scenarios/cascade_signal_blind_e2e_test.go` for the helper). Wait for the frame to terminate. Assert via the event log that:
   - `context-sender` produced a `terminal/success` event.
   - `receiver` produced no `work_started` event in this frame.

5. Drive scenario 2 — invalidate `gate-sender` so it produces `attribute/status/changed` with payload `value: "needs_work"` (configure the http-node executor target accordingly). Also invalidate `context-sender` so it produces `attribute/data/changed`. Wait for the frame to terminate. Assert via the event log that:
   - `receiver` produced exactly one `work_started` and `terminal/success` event.
   - `receiver`'s post-run attribute ledger contains a value reflecting both senders' contributions (read the receiver's attribute row via the persistence layer and assert it carries the substituted data from `context-sender`).

6. Use a real executor that resolves the receiver's substituted attributes. The bundled `http-node` is suitable; configure it to return a small payload that echoes the substituted inputs so the assertions in step 5 can verify the receiver actually read `context-sender`'s data. **Do not stub the executor.** STORY-read-without-waking's Acceptance hinges on the receiver actually reading X's value, which means the executor must be the real cascade-resolved component.

7. Add a top-of-file `@story:` annotation so the completion auditor pairs test-to-story.

8. Run `go test ./test/scenarios/ -run TestStoryReadWithoutWaking -count=1 -timeout=5m`. Test passes.

### Task 19: Proof artifact for STORY-uncovered-read-rejected

**Files:**
- `lib/graph/node/template_validator_substitution_coverage_test.go` (new)
- `test/scenarios/registration_rejects_uncovered_substitution_test.go` (new)

**Story:** STORY-uncovered-read-rejected
**Proof form (from spec):** all-of-the-above — example templates exhibiting each uncovered shape (attribute field ref, event ref, whole-pull), plus an executable proof asserting the registration response body shape and content.

**Steps:**

1. **Validator unit tests** (`lib/graph/node/template_validator_substitution_coverage_test.go`):

   Read `lib/graph/node/template_validator_test.go` for the existing validator unit-test pattern. Create three test functions exhibiting each uncovered ref shape:

   - `TestSubstitutionCoverage_PerFieldAttributeRefUncovered` — template constructs a receiver with `source: "{{nodes.foo.attribute.bar}}"` and a `subscribes:` block that names neither `attribute/bar/changed` nor `attribute/*` from `foo`. Assert `ValidateTemplate` emits a structured entry with `kind: "substitution_ref_uncovered"`, `receiver_node_type` matching the receiver, `ref` containing `nodes.foo.attribute.bar`, `attribute_property` matching the schema path, and `suggested_subscribes_entry.type == "attribute/bar/changed"`.

   - `TestSubstitutionCoverage_WholePullRefUncovered` — receiver reads `{{nodes.foo.attribute}}` (whole-pull, no field) with a `subscribes:` block carrying `attribute/bar/changed` from `foo` (per-field, not wildcard). The asymmetry rule (decision:coverage-wildcard-asymmetry) means this does not satisfy the whole-pull read. Assert the structured entry names `attribute/*` in `suggested_subscribes_entry.type`.

   - `TestSubstitutionCoverage_EventRefUncovered` — receiver reads `{{nodes.foo.event.something_happened}}` with no `event/something_happened` subscription from `foo`. Assert the structured entry names `event/something_happened` in `suggested_subscribes_entry.type`.

   For each test, assert `suggested_subscribes_entry` is a flat JSON object with `node`, `type`, `wake_on_change: false`, `force_upstream_refresh: false` and no embedded `_note` field. Assert `suggested_subscribes_note` is a sibling field with the explanatory text containing both flag names.

2. **Control-API scenario test** (`test/scenarios/registration_rejects_uncovered_substitution_test.go`):

   Use the testcontainers harness (same shape as `test/scenarios/cascade_signal_blind_e2e_test.go` and other testcontainers-based scenario tests). Do **not** use an in-process server — the proof must exercise the real HTTP boundary the operator interacts with, per TD-uncovered-substitution-error-shape's rationale (programmatic fix-suggestion delivered through the operator's actual surface).

   The test boots the rimsky stack, then for each of the three uncovered-ref templates (per-field, whole-pull, event ref) submits a `POST /v1/templates` and asserts:
   - HTTP 400 response.
   - Response body contains `validation_errors` array.
   - The array contains exactly one entry with `kind: "substitution_ref_uncovered"`.
   - The structured entry's full field set matches the per-template expected payload (ref text, suggested entry type, etc.).
   - `suggested_subscribes_entry` deserializes as a valid drop-in JSON object with four keys (`node`, `type`, `wake_on_change`, `force_upstream_refresh`).
   - `suggested_subscribes_note` is present, non-empty, and contains both flag names.

3. Run `go test ./lib/graph/node/ -run TestSubstitutionCoverage -count=1`. Validator unit tests pass.

4. Run `go test ./test/scenarios/ -run TestRegistrationRejectsUncoveredSubstitution -count=1 -timeout=5m`. Scenario test passes.

### Task 20: Bless the rewritten hard-dep scenario test as the proof artifact for STORY-pull-upstream-fresh-on-read

**Files:** `test/scenarios/per_run_attributes/hard_dep_test.go`

**Story:** STORY-pull-upstream-fresh-on-read
**Proof form (from spec):** all-of-the-above — an example template exhibiting `force_upstream_refresh: true`, plus an executable proof asserting that A's substitution context at dispatch carries a value X produced after A was invalidated (and that the value differs from X's pre-invalidation value).

**Steps:**

1. Read `test/scenarios/per_run_attributes/hard_dep_test.go` (rewritten in Pass 1 Task 4 to use `force_upstream_refresh: true` subscriptions). Identify the test function whose scenario most closely matches story 2's Acceptance — receiver A is invalidated while sender X has not been independently invalidated, A's substitution context at dispatch contains X's freshest value.

2. Add a top-of-function story-slug annotation:

   ```go
   // <existing function name> serves as the canonical proof for
   // STORY-pull-upstream-fresh-on-read (spec
   // 2026-06-14-explicit-substitution-cascade-behavior).
   //
   //	@story: upstream-pull-on-invalidate
   ```

3. Verify the test asserts X's pre/post comparison — read X's pre-invalidation attribute-ledger row's value; trigger A's invalidation; after the frame terminates, read A's attribute-ledger row and assert the substituted value differs from X's pre-invalidation value and matches X's newest run's contribution. If the existing assertion only checks A's run completes (not the pre/post value difference), extend it. STORY-pull-upstream-fresh-on-read's Falsifier names "a stale value (matching X's prior run rather than a value produced this frame)" as the failure condition — the assertion must catch a stale read, not just any read.

4. Run `go test ./test/scenarios/per_run_attributes/ -count=1 -timeout=5m`. Test passes.

### Task 21: Verify Pass 4 falsifier — all three story proofs deliver

**Files:** none (verification only)

**Steps:**

1. Run `go test ./test/scenarios/ -count=1 -timeout=15m`. Every scenario test passes, including the three new acceptance proofs and every migrated existing test.

2. Run `go test ./... -count=1 -timeout=15m` across the whole tree. Every test passes.

3. Run `go test ./test/scenarios/ -count=3 -race -timeout=30m` to confirm no flakiness was introduced by the cascade walker's gate changes from Pass 1 Task 7 or by the new proof tests.

4. Run `make lint`. Clean.

5. Spot-check (still in-pass — driven by `cat`/`rg`, not by hand): confirm each of the three new test files names its story slug in a top-of-file comment, references the spec, and asserts the story's user-observable outcome (not an internal helper).

---

## Manual checks after completion

None. Every property in this spec — the new flags' presence on subscription entries, the validator's coverage check and incoherent-combination rejection, the cascade walker's gate behavior, the hard-dep edge map's input-source migration, the implicit-edge generation's retirement, the structured error envelope's shape, every design-doc mutation and creation, and every user-outcome story's proof — is verifiable by a command and exercised by the test suite. No visual or human-judgment verification is required.
