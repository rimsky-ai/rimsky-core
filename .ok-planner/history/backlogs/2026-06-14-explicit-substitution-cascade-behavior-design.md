# Explicit substitution cascade behavior

**Date:** 2026-06-14
**Source sketch:** `.ok-planner/sketches/2026-06-13-explicit-substitution-cascade-behavior.md`
**Surfaced by:** GitHub issue #18 (the implicit-ungated-edge footgun from substitution reads).

## Context

A substitution ref `{{nodes.X.attribute.Y}}` (or the event-ref / whole-pull variants) in a node's attribute schema auto-creates an ungated subscription edge from X to the receiver on `attribute/Y/changed`. The reactivity surface that today's `concept:node-subscription` describes as decoupled into three primitives — read access (substitution grammar), cascade coupling (subscriptions), eligibility gating (`when:` predicates) — re-couples in practice: every read silently widens the cascade with an ungated implicit edge.

A separate per-attribute-field flag `hard_dep: true` on the same attribute schema entry covers the proactive-upstream-pull case. Two distinct cascade behaviors live in two distinct grammar surfaces, with different defaults and different levels of visibility.

This spec consolidates both surfaces into the `subscribes:` block as two orthogonal required booleans per subscription entry, drops the implicit-edge generation from substitution refs, adds a registration-time coverage check, and retires the `hard_dep:` attribute-field flag. Pre-v1, the change is a loud-failure registration break with no compatibility shim — bundled templates and tests are migrated as part of the work with today-equivalent flag values.

The change retires a recorded Non-goals position in `concept:attribute` (the position that "the configuration surface is exactly `hard_dep: true`" and that `pull_only: true` does not exist). The Non-goals position assumed the implicit auto-subscribe was always desirable; GitHub issue #18 is the load-bearing counter-evidence.

## User outcomes

> **STORY-read-without-waking** — As a template author, I can read an upstream node's attribute via substitution while declaring that the read does not fire my receiver on the sender's change, so my receiver's wake-up is governed only by its other explicit subscriptions.
> **Acceptance:** An author writes a template where receiver A reads `{{nodes.X.attribute.Y}}` and carries a subscription entry naming X's `attribute/Y/changed` (or `attribute/*`) with `wake_on_change: false`. After deploy: when X settles `attribute/Y/changed` and A's other subscriptions don't match, A does not dispatch. When A is dispatched via its other subscriptions and X is in the same frame, A's substitution context includes X's value at dispatch.
> **Falsifier:** A's other subscriptions don't match, X emits `attribute/Y/changed`, A dispatches anyway — meaning the cascade is firing A on the suppressed edge.
> **Proof:** all-of-the-above — an example template exhibiting the gated subscription plus context-gathering reads, and an executable proof that walks the two scenarios (X changes alone → A does not fire; A's gate matches → A fires and reads X's value).

> **STORY-pull-upstream-fresh-on-read** — As a template author, I can declare on a subscription that the sender be brought current before my receiver dispatches, so the receiver's substitution context contains the sender's freshest value at dispatch.
> **Acceptance:** An author writes a template where receiver A subscribes to X with `force_upstream_refresh: true` and reads `{{nodes.X.attribute.Y}}`. After deploy: when A is invalidated and X has not been independently invalidated, A's substitution context at dispatch contains X's freshest value — observable by a downstream node reading the value forwarded through A, or by the operator inspecting A's post-run attribute ledger entry against X's earlier-recorded value.
> **Falsifier:** A's substitution context at dispatch contains a stale value for X (matching X's prior run rather than a value produced this frame), or A's dispatch fails because X's value is absent — both observable by comparing the value A read against X's attribute-ledger state.
> **Proof:** all-of-the-above — an example template exhibiting `force_upstream_refresh: true`, plus an executable proof asserting that A's substitution context at dispatch carries a value X produced after A was invalidated (and that the value differs from X's pre-invalidation value).

> **STORY-uncovered-read-rejected** — As a template author, I get a registration error when a substitution ref has no covering subscription, naming the ref and showing the subscription entry that would cover it.
> **Acceptance:** An author writes a template where node A reads `{{nodes.X.attribute.Y}}` (or `{{nodes.X.event.Y}}`, or the whole-pull `{{nodes.X.attribute}}`) but A's `subscribes:` block contains no entry whose `node:` plus `type:` would deliver that signal. The template-registration endpoint returns a registration error whose body names the uncovered ref (the receiver, the ref text, the schema path the ref appears in) and includes a copy-pasteable subscription entry the author could add.
> **Falsifier:** the template registers despite the uncovered ref (silent acceptance with deferred runtime failure), or registration fails with a generic error that doesn't name the specific uncovered ref or doesn't show a copy-pasteable fix.
> **Proof:** all-of-the-above — example templates exhibiting each uncovered shape (attribute field ref, event ref, whole-pull), plus an executable proof asserting the registration response body shape and content.

## Architecture and mechanism

### Template DSL surface

Every `subscribes:` entry carries two new required boolean fields: `wake_on_change` and `force_upstream_refresh`. Registration rejects entries missing either. The shape:

```yaml
subscribes:
  - node: check-adapter-config
    type: attribute/status/changed
    when: payload.value == 'needs_work'
    wake_on_change: true
    force_upstream_refresh: false
  - node: check-gis-endpoints
    type: attribute/*
    wake_on_change: false
    force_upstream_refresh: false
```

`hard_dep: true` retires from the attribute schema. The substitution grammar (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Y}}`, whole-pull `{{nodes.X.attribute}}`) is unchanged. All cascade-shape declaration lives in `subscribes:`.

### Semantics of the two flags

`wake_on_change` governs whether a matching emission from the sender dispatches the receiver. When `true`, the cascade walker inserts a wait-set row for the receiver on the sender's run AND stale-marks the receiver. When `false`, the wait-set row is inserted but the receiver is NOT stale-marked from this edge. The wait-set row carries the sender's data into the receiver's substitution context if the receiver dispatches via some other subscription in the same frame.

`force_upstream_refresh` governs whether the receiver's invalidation drags the sender into the same frame. When `true`, the cascade walker invalidates the sender as a side effect of invalidating the receiver, the sender re-runs in the same frame, and the receiver's substitution context reads the sender's post-evaluation value. When `false`, no pull; the receiver dispatches with whatever sender state happens to be in this frame (none, if the sender wasn't otherwise invalidated).

The four cells:

| | `force_upstream_refresh: false` | `force_upstream_refresh: true` |
|---|---|---|
| **`wake_on_change: true`** | Fire on sender's change; read whatever value is current | Fire on sender's change AND drag the sender in whenever I'm dispatched by any other path |
| **`wake_on_change: false`** | Context-gathering read: drain into my context if the sender is in this frame, else the read is absent | Pull sender so it's fresh when I dispatch; my wake-up comes from a different subscription |

### Registration validator

The template-registration validator runs three new checks alongside its existing validation pipeline.

**Substitution-ref coverage.** Walk every node's attribute schema. For every `source:` directive, parse out the substitution refs and classify by shape:

| Ref shape | Implied required signal | Coverage rule |
|---|---|---|
| `{{nodes.X.attribute.Y}}` | `attribute/Y/changed` from X | Covered by either `{ node: X, type: attribute/Y/changed }` or `{ node: X, type: attribute/* }` |
| `{{nodes.X.attribute}}` (whole-pull) | `attribute/*` from X | Covered only by `{ node: X, type: attribute/* }` |
| `{{nodes.X.event.Y}}` | `event/Y` from X | Covered by `{ node: X, type: event/Y }` |

Any ref with no covering subscription is a registration error. A wildcard subscription covers per-field reads of the same sender; a per-field subscription does not cover a whole-pull read of the same sender.

**Cross-cutting incoherent combination.** A subscription entry with `instance: true` (cross-cutting) and `force_upstream_refresh: true` is rejected. Cross-cutting subscriptions are sender-agnostic; there is no specific upstream to refresh.

**Both flags required.** Any subscription entry missing either `wake_on_change` or `force_upstream_refresh` is rejected at parse time. No defaults are applied.

### Error response shape

Today's `template_validation_failed` response carries a `validation_errors` array of `{path, msg}` entries built at `code:lib/control/controlapi/templates.go` line 221-224. The substitution-ref coverage check produces a richer structured entry, added strictly additively alongside the existing `{path, msg}` shape — existing entries keep their two fields; the new entry adds five more fields that consumers key on by the presence of a discriminator field. No existing entry changes shape; no consumer of today's `{path, msg}` entries breaks.

One new error kind:

- **`substitution_ref_uncovered`** — fields: `kind` (the discriminator, set to `"substitution_ref_uncovered"`), `receiver_node_type`, `ref` (the literal text from the schema), `attribute_property` (the schema path the ref appears in), `suggested_subscribes_entry` (a copy-pasteable subscription JSON object with both flags set to `false`), `suggested_subscribes_note` (a one-sentence explanation of the implication of each flag value, separate from `suggested_subscribes_entry` so the suggested entry itself is valid drop-in YAML/JSON).

Example body:

```json
{
  "kind": "substitution_ref_uncovered",
  "receiver_node_type": "resolver",
  "ref": "{{nodes.checker.attribute.status}}",
  "attribute_property": "attributes.schema.properties.prompt",
  "suggested_subscribes_entry": {
    "node": "checker",
    "type": "attribute/status/changed",
    "wake_on_change": false,
    "force_upstream_refresh": false
  },
  "suggested_subscribes_note": "set wake_on_change: true if this ref should also fire this receiver; set force_upstream_refresh: true if checker should be re-evaluated when this receiver is invalidated"
}
```

Both the `{path, msg}` shape (used by Check 2 above when `wake_on_change` or `force_upstream_refresh` is missing on a subscription entry, and used by Check 3 for the cross-cutting incoherent combination) and the new `substitution_ref_uncovered` shape (used by Check 1) appear in the same `validation_errors` array. Consumers distinguish them by checking for the `kind` field's presence. Structured (not just text) so both human authors and LLM agents writing templates can consume the fix-suggestion programmatically.

### Runtime mechanism

**`wake_on_change` honored at terminal cascade.** The cascade walker at `code:lib/runtime/runner_terminal.go::cascadeSubscribersStaleInTx` walks each subscription edge matching the sender's emitted signal. Two steps per matching edge: wait-set row insert (so the substitution-context builder can find the sender's data) and receiver stale-mark (so the receiver eventually dispatches). The stale-mark is gated on `wake_on_change`:

- `wake_on_change: true` → both steps as today.
- `wake_on_change: false` → wait-set insert only; the stale-mark step is skipped.

If the receiver dispatches via some other subscription in the same frame, the substitution-context builder at `code:lib/runtime/substitution_context.go::BuildAttributeDeps` reads the drained wait-set row and the receiver's `{{nodes.X.attribute.Y}}` resolves to the sender's value. If the receiver dispatches and the sender isn't in this frame, the substitution engine returns `ErrMissingSource` for that ref — and the existing fallback/lenient/optional routing kicks in. Whether dispatch proceeds (with a fallback literal, a `null`, an omitted optional field) or fails with `template_resolution_failed` (for a strict required field with no fallback) is governed by the substitution grammar exactly as today.

**`force_upstream_refresh` honored via the receiver-keyed map, repurposed.** The runtime machinery at `code:lib/graph/node/hard_dep_edges.go::BuildHardDepEdges` and `code:lib/runtime/runner_terminal.go::pullHardDepUpstreams` doesn't change. Only the builder's input source moves: instead of walking attribute schemas for `hard_dep: true` properties, it walks every node's `subscribes:` entries for entries with `force_upstream_refresh: true` and takes the sender from `node:`. Cycle detection, fan-out-target rejection, and same-receiver-to-same-sender de-duplication carry over verbatim.

**Implicit edge generation retires.** Today the subscription-edge map at `code:lib/graph/node/subscription_edges.go::BuildSubscriptionEdges` is fed both by the explicit `subscribes:` block (lines 398-405) and by an "Implicit subscriptions from substitution refs" loop (lines 406-410) that consumes the output of `code:lib/graph/node/subscription_edges.go::ExtractSubstitutionRefsFromTemplate` (which itself walks every node's attribute schema via `parseSubstitutionRefsFromAttributes` and parses `{{...}}` directives). The implicit-edge loop and its substitution-ref input plumbing come out entirely. The subscription-edge map is built from `subscribes:` entries only. The two callers that pass substitution refs as a separate parameter to `BuildSubscriptionEdges` today — `code:lib/graph/scheduler/pure_cascade.go` (lines 193-194) and `code:lib/runtime/subscription_loaders.go::subscriptionEdgesForTemplate` (lines 75-76) — drop the substitution-ref argument when the signature changes.

**Substitution-context builder unchanged.** `BuildAttributeDeps` reads drained wait-set rows per `concept:wait-set` and doesn't care which flag caused the wait-set row to land. Logic untouched.

**Cascade invariant precision.** `concept:cascade`'s invariant — *"Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL `when:` predicate evaluates true"* — gets a precise extension distinguishing the two steps. A cascade walk inserts a wait-set row iff the match-and-when test passes; the receiver is additionally stale-marked iff the matching subscription's `wake_on_change` is `true`.

### Migration scope

**Code surfaces touched:**
- `code:lib/foundation/spec/subscription.go::SubscriptionEntry` — add `WakeOnChange` and `ForceUpstreamRefresh` boolean fields with `wake_on_change` and `force_upstream_refresh` YAML/JSON tags.
- `code:lib/graph/node/template_validator.go` — add the substitution-ref coverage check (the existing `code:lib/graph/node/template_validator.go::ValidateTemplate` already calls `ExtractSubstitutionRefsFromTemplate` at line 316; the coverage check is the new validation step) and the cross-cutting incoherent-combination check inside `validateSubscribes` (line 636).
- `code:lib/graph/node/subscription_edges.go` — `BuildSubscriptionEdges` (line 390) drops its "Implicit subscriptions from substitution refs" loop (lines 406-410); `ExtractSubstitutionRefsFromTemplate` (line 437) and its internal helpers (`parseSubstitutionRefsFromAttributes` line 516, `substitutionRef` struct line 417, `substitutionDirectiveRe` line 426) remain in the file as the parsing primitive the new validator coverage check consumes — only the edge-emission consumer of those parsed refs retires. The function signature of `BuildSubscriptionEdges` loses the `substitutionRefs` parameter (or accepts a nil map and ignores it during the transition).
- `code:lib/graph/scheduler/pure_cascade.go` — the call to `ExtractSubstitutionRefsFromTemplate` plus `BuildSubscriptionEdges` at lines 193-194 drops the substitution-refs argument and is reviewed for any other implicit-edge assumption.
- `code:lib/runtime/subscription_loaders.go::subscriptionEdgesForTemplate` — the same call pair at lines 75-76 drops the substitution-refs argument; the loader otherwise consumes the explicit `subscribes:` flags from the new `SubscriptionEntry` fields.
- `code:lib/graph/attribute/substitution.go` — the file's package doc at line 21 references substitution-ref auto-subscribe; update the doc text to remove the auto-subscribe mention. The file itself contains no edge-emission code (the edge emission lives in `subscription_edges.go`); only the doc comment changes here.
- `code:lib/graph/node/hard_dep_edges.go` — `BuildHardDepEdges` and `hardDepSendersOf` move their input source from attribute-property `hard_dep: true` flags to `subscribes:` entries with `force_upstream_refresh: true`. The map type, cycle detection, and fan-out-target rejection carry over.
- `code:lib/runtime/runner_terminal.go::cascadeSubscribersStaleInTx` — gate the stale-mark on `wake_on_change`; the wait-set insert continues unconditionally for any matching edge. The `pullHardDepUpstreams` consumer of the receiver-keyed map is unchanged.
- Doc-comment-only surfaces mentioning auto-subscribe / implicit-subscription that get refreshed to match the new model: `code:lib/foundation/spec/template.go` (line 138), `code:lib/foundation/signal/taxonomy.go` (line 65), `code:lib/runtime/runner_terminal.go` (line 1079), and additional in-file comments in `subscription_edges.go` (lines 7, 304, 432, 507, 521, 551, 584, 587, 608, 720). No behavior change at these sites — just doc-text updates so the comments match the post-spec mechanism.

**Test surfaces touched:**
- `code:test/scenarios/multi_hard_dep_test.go`, `code:test/scenarios/per_run_attributes/hard_dep_test.go`, `code:test/scenarios/parked_lifecycle_test.go` — rewrite to use `subscribes:` entries with `force_upstream_refresh: true` instead of `hard_dep: true` on attribute fields.
- `code:lib/graph/node/hard_dep_edges_test.go`, `code:lib/runtime/hard_dep_cascade_test.go` — same rewrite; edge-map builder and cascade walker behavior tests carry over with the new input source.
- New tests serving as proofs for the three stories (listed in the stories' Proof lines and in the Manifest).

**Bundled-scenario-test sweep:** every `subscribes:` entry constructed in any `test/scenarios/` or `lib/services/test/` Go test gets the two flags added with today-equivalent values (`wake_on_change: true, force_upstream_refresh: false`). Mechanical work; preserves today's behavior verbatim.

**Concept docs touched:** `concept:attribute`, `concept:node-subscription`, `concept:cascade` — see `## Design changes` for the verbatim mutations.

**No special handling for legacy `hard_dep: true`.** A template uploaded post-change carrying `hard_dep: true` on an attribute field gets whatever the existing attribute-schema validator does with unknown properties — silent ignore if the validator is permissive, generic error if strict. No special-case rejector with a migration redirect. Pre-v1, "break freely" applies; the project doesn't owe consumers a migration helper.

## Technical decisions

**TD-cascade-flags-on-subscribes** — The two cascade-behavior flags live on `subscribes:` entries.
**Choice:** Both flags are fields of `SubscriptionEntry`.
**Rationale:** A single subscription edge can serve multiple substitution reads; placing flags per-ref creates a "which value wins?" ambiguity. The subscription is the edge.
**Alternatives:** Per-substitution-ref flags (rejected: ambiguity across multiple reads). Separate `cascade_deps:` block (rejected: third surface for the same concept).

**TD-cascade-flags-required-no-defaults** — Both `wake_on_change` and `force_upstream_refresh` are required on every `subscribes:` entry.
**Choice:** Registration rejects entries missing either flag. No defaults applied.
**Rationale:** Call-site clarity. Reading any subscription entry tells the reader the full cascade behavior with no document-memorization required. Forces template authors (human or LLM agent) to think about the cascade behavior at every edge.
**Alternatives:** Default `wake_on_change: true, force_upstream_refresh: false` matching today's implicit behavior (rejected: invisible behavior; reading a subscription entry doesn't tell the full contract).

**TD-substitution-grammar-closed** — The substitution grammar gains no new tokens.
**Choice:** The closed-enumeration discipline per `concept:attribute`'s invariants is preserved. No inline cascade flags in `{{...}}`.
**Rationale:** Cascade-shape declaration belongs on the cascade-edge surface (`subscribes:`), not on the read surface.

**TD-substitution-ref-coverage-required** — Every substitution ref must be matched by at least one `subscribes:` entry.
**Choice:** Registration walks every node's attribute schema, parses out substitution refs, and rejects the template if any ref lacks a covering subscription. Applies to `{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Y}}`, and whole-pull `{{nodes.X.attribute}}`.
**Rationale:** The "no orphan reads" guarantee survives by static validation rather than by silent edge generation. Cascade edges become exactly what the author wrote.

**TD-coverage-wildcard-asymmetry** — Wildcard subscriptions cover per-field reads; per-field subscriptions do not cover whole-pull reads.
**Choice:** An `attribute/*` subscription covers both `{{nodes.X.attribute.Y}}` and `{{nodes.X.attribute}}`. An `attribute/Y/changed` subscription covers only the per-field read for Y; a whole-pull `{{nodes.X.attribute}}` requires the wildcard.
**Rationale:** A per-field reader watches one field; a whole-pull reader needs to know about every field. The asymmetry is intentional.

**TD-cross-cutting-no-force-refresh** — A cross-cutting subscription cannot carry `force_upstream_refresh: true`.
**Choice:** A subscription with `instance: true` and `force_upstream_refresh: true` is rejected at registration.
**Rationale:** Cross-cutting subscriptions are sender-agnostic; there is no specific upstream to refresh.

**TD-uncovered-substitution-error-shape** — Registration error for an uncovered substitution ref is a structured envelope entry, added additively alongside the existing `{path, msg}` shape.
**Choice:** The `validation_errors` array carries an entry of kind `substitution_ref_uncovered` with `kind` (discriminator), `receiver_node_type`, `ref`, `attribute_property`, `suggested_subscribes_entry` (a copy-pasteable subscription JSON object with both flags set to `false`), and `suggested_subscribes_note` (a separate one-sentence explanation field). Existing `{path, msg}` entries are unchanged; the new shape is strictly additive. Consumers distinguish entries by the presence of the `kind` field.
**Rationale:** Programmatic fix-suggestion. Both human authors and LLM agents consume registration errors; a structured envelope lets them apply the fix mechanically. Keeping the suggested entry as a valid drop-in JSON object (no embedded `_note` field) preserves its copy-pasteability.
**Alternatives:** Adopt the structured shape uniformly across every existing `validation_errors` entry (rejected: wider scope of changes touching every existing error-emit site; not necessary for this spec's user outcomes).

**TD-validation-errors-additive-not-uniform** — The new structured-entry shape is added alongside the existing `{path, msg}` shape, not as a replacement.
**Choice:** Existing `validation_errors` entries (built at `code:lib/control/controlapi/templates.go` line 221-224) keep their `{path, msg}` shape. The new `substitution_ref_uncovered` kind is the only entry with the richer shape. Other new checks in this spec (Check 2: both-flags-required; Check 3: cross-cutting incoherent combination) use the existing `{path, msg}` shape rather than the richer one — they don't need copy-pasteable fix suggestions because the fix is mechanically obvious (add the missing flag; drop the incoherent combination).
**Rationale:** Scope discipline. Generalizing the structured shape across all existing entries is a wider change that touches every error-emit site in the validator; the user outcomes only require the new shape for the substitution-coverage error.

**TD-no-hard-dep-special-case** — Legacy `hard_dep: true` on attribute fields gets no special-case treatment.
**Choice:** The cascade walker and edge builder stop reading `hard_dep:` from attribute fields. The attribute-schema validator gains no special-case rejector for the retired field; whatever the existing JSON Schema validation does with unknown properties applies. No migration-redirect error.
**Rationale:** Pre-v1, the project doesn't owe consumers a migration helper. "Erase completely" reads as "no special-case code naming the retired field."
**Note on error-path separation:** this decision applies *only* to the legacy attribute-field `hard_dep:` flag. Distinct from the `subscribes:`-entry coverage check (Check 1, which produces the structured `substitution_ref_uncovered` envelope) and from the both-flags-required check (Check 2, which produces a `{path, msg}` entry naming the missing flag). A template carrying both shapes of legacy issue — a `hard_dep:` field on an attribute property AND a `subscribes:` entry missing flags — surfaces a generic schema-validator response for the first and a structured `{path, msg}` for the second. The asymmetry is intentional: the retired flag gets no helper; the new flag-coverage rule gets a precise pointer.
**Alternatives:** Add an explicit `hard_dep_field_removed` registration error directing the author to the new shape (rejected: commemorates the retired field; user-stated preference is to erase rather than commemorate).

**TD-wake-on-change-wait-set-only** — `wake_on_change: false` skips the receiver stale-mark.
**Choice:** At cascade walk, a matching subscription edge always inserts a wait-set row for the receiver on the sender's run. The receiver is additionally stale-marked iff the edge's `wake_on_change` is `true`.
**Rationale:** Decouples context-gathering reads from cascade dispatch. The receiver's wake-up is governed by its other subscriptions; its substitution context still receives the sender's data if the sender happens to settle in this frame.

**TD-force-upstream-refresh-via-receiver-keyed-map** — Runtime reuses the existing hard-dep edge map, sourced from subscription flags.
**Choice:** The receiver-keyed map of upstream node-types to proactively invalidate is built at registration from `subscribes:` entries with `force_upstream_refresh: true`. The cascade-walker consumption path (`pullHardDepUpstreams` and its caller in the invalidate cascade) is unchanged. Cycle detection at registration; fan-out-target rejection; same-receiver-to-same-sender de-duplication carry over.
**Rationale:** The runtime machinery exists and is correct; only the input source changes.

**TD-implicit-edge-generation-retired** — Substitution refs no longer contribute to the subscription-edge map.
**Choice:** The subscription-edge map at `code:lib/graph/node/subscription_edges.go` is fed by the explicit `subscribes:` block only. The substitution-ref auto-subscribe inference path retires.
**Rationale:** Cascade edges are exactly what the author wrote; nothing inferred.

**TD-substitution-context-builder-unchanged** — `BuildAttributeDeps` reads drained wait-set rows; its logic is untouched.
**Choice:** The substitution-context builder at `code:lib/runtime/substitution_context.go::BuildAttributeDeps` continues to read drained wait-set rows per `concept:wait-set`.
**Rationale:** The builder doesn't care which flag caused the wait-set row; the row's presence is what it keys on.

**TD-substitution-grammar-fallback-unchanged** — The existing fallback / lenient / optional routing for unresolved refs is unchanged.
**Choice:** When a substitution ref returns `ErrMissingSource` because its sender wasn't in this frame, the existing routing — `| "literal"` fallback, `?` lenient marker, optional-field omission, strict-required escalation to `template_resolution_failed` — continues to govern the dispatch outcome.
**Rationale:** Authors who want graceful degradation continue to declare a fallback or lenient marker on the ref, exactly as today. The new model removes the implicit edge that was masking the case; it does not change what happens after.

**TD-migration-fills-flags-today-equivalent** — Every existing `subscribes:` entry in the codebase gets the two flags added with today-equivalent values.
**Choice:** Existing `subscribes:` entries get `wake_on_change: true, force_upstream_refresh: false` added at migration time. No behavior change at runtime.
**Rationale:** Preserves today's behavior on every subscription verbatim. The migration's correctness is established by every existing test continuing to pass.

**TD-migration-hard-dep-becomes-force-refresh** — Every `hard_dep: true` field on an attribute schema becomes a `force_upstream_refresh: true` subscription entry.
**Choice:** Existing `hard_dep: true` fields are removed and the corresponding receiver's `subscribes:` entry for that sender gets `force_upstream_refresh: true`. If no existing subscription names the sender, a new entry is added with `wake_on_change: true, force_upstream_refresh: true` (preserving today's combined wake+refresh behavior).
**Rationale:** Today's hard-dep tests already exercise the proactive-invalidation runtime behavior; the rewrite preserves the runtime behavior exactly.

**TD-migration-implicit-edges-become-explicit** — Every implicit edge created today by a substitution ref without a matching explicit subscription gets an explicit covering subscription added.
**Choice:** Templates whose substitution refs rely on today's implicit auto-subscribe get explicit `subscribes:` entries added at migration time with today-equivalent flags (`wake_on_change: true, force_upstream_refresh: false`).
**Rationale:** Preserves today's behavior on every implicit edge verbatim. After migration, no template relies on implicit edges; the registration coverage check from this spec onward catches new instances at registration.

## Design changes

- **Concept: mutate `concepts/attribute.md` in place.** Rewrite the relevant Non-goals entry (the one currently naming `force_fresh: true`, `pull_only: true`, and `trigger_if_missing: true` as decided against, and naming `hard_dep: true` as the sole configuration surface) with the new text:

  > `force_fresh: true` (always-re-execute) and `trigger_if_missing: true` (lazy upstream initialization). These flags do not exist. Authors expressing "re-execute on every dispatch" structure templates around explicit invalidation rather than a per-read flag, and authors expressing "lazy upstream initialization" use the cascade's proactive-upstream-pull mechanism declared on the receiver's subscription.

  Remove the Invariants line currently stating *"Per-field `source:` admits an opt-in `hard_dep: true` flag on `nodes.<X>.attribute.<Y>` reads. When set, the cascade walker proactively invalidates the upstream so its value is available in the current frame. Hard-dep cycles are rejected at template registration by the hard-dep edge builder."* — no replacement reference to the retired flag.

  Add a short Boundaries note: *"Cascade-shape configuration tied to a substitution ref is declared in the receiver's `subscribes:` block via the per-edge `wake_on_change` and `force_upstream_refresh` flags. The substitution grammar carries no cascade-shape tokens."*

- **Concept: mutate `concepts/node-subscription.md` in place.** Replace the "What it is" section with:

  > A node-subscription declares `type:` (a canonical signal type-path, exact or trailing-`*` prefix per `concept:signal`) plus an optional `when:` CEL predicate over the signal payload, plus two required cascade-shape booleans: `wake_on_change` and `force_upstream_refresh`. Sender-side filters (`node:` selects a specific upstream node-type, `instance: true` is cross-cutting) and the frame modifier (`frame: in | next`) apply. Subscriptions are declared per node under `subscribes:` in the template DSL.
  >
  > `wake_on_change` governs whether a matching emission from the sender dispatches the receiver: `true` stale-marks the receiver and inserts a wait-set row gating its dispatch on the sender; `false` inserts only the wait-set row so the receiver's substitution context can read the sender's data if it dispatches via another edge, without firing the receiver itself.
  >
  > `force_upstream_refresh` governs whether the receiver's invalidation drags the sender into the same frame: `true` invalidates the sender so it re-runs in the same frame before the receiver dispatches; `false` leaves the sender wherever it is. A cross-cutting subscription (`instance: true`) cannot carry `force_upstream_refresh: true` — the combination is incoherent and rejected at registration.
  >
  > Every substitution ref in a node's attribute schema (`{{nodes.X.attribute.Y}}`, `{{nodes.X.event.Y}}`, or the whole-pull `{{nodes.X.attribute}}`) is matched at registration against the receiver's `subscribes:` block; a ref with no covering entry is rejected.

  Replace the Owns section's first two bullets with:

  > - The per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)`; a per-sender radix tree / prefix-bucket structure computed at template registration), populated exclusively from `subscribes:` entries.
  > - The two-flag cascade-shape contract (`wake_on_change`, `force_upstream_refresh`) on every subscription entry.
  > - The registration-time coverage check that matches every substitution ref against the receiver's `subscribes:` block.

  Replace the Invariants line currently stating *"Substitution refs auto-subscribe — no orphan reads."* with:

  > Every substitution ref in a node's attribute schema is matched by at least one `subscribes:` entry whose sender and type would deliver the corresponding signal. Templates with uncovered refs are rejected at registration.

  Add to Invariants:

  > Every `subscribes:` entry carries the `wake_on_change` and `force_upstream_refresh` boolean fields; entries missing either field are rejected at registration. A cross-cutting subscription (`instance: true`) cannot carry `force_upstream_refresh: true`.

- **Concept: mutate `concepts/cascade.md` in place.** Replace the Invariants line currently stating *"Cascade fires iff a subscription edge matches the emitted signal's type AND the subscriber's CEL `when:` predicate evaluates true."* with:

  > A cascade walk inserts a wait-set row for the receiver iff a subscription edge matches the emitted signal's type AND the subscriber's CEL `when:` predicate evaluates true. The receiver is additionally stale-marked iff the matching subscription's `wake_on_change` field is `true`.

  Replace the Boundaries paragraph currently stating *"The cascade walker consults two edge maps — the subscription-edge map and the hard-dep edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); hard-dep edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams that a receiver declares `hard_dep: true` on."* with:

  > The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); upstream-refresh edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams that a receiver names in a `subscribes:` entry with `force_upstream_refresh: true`.

- **Story: mutate `stories/multi-hard-dep-rendezvous.md` in place.** Replace the Role section with:

  > As a template author declaring two or more subscriptions with `force_upstream_refresh: true` on one node, I can rely on each upstream running once and the receiver dispatching once with all force-refreshed upstreams settled — so the shape rendezvouses instead of livelocking.

  Replace the Capability section with:

  > The upstream-refresh pull carries a settled-this-frame guard: when a later-settling upstream's cascade walk re-visits the receiver, an upstream that already settled in the frame (a run row in the frame but no in-flight row) is not re-affirmed — its value is already in the receiver's drained wait-set, so there is nothing to gate on and nothing to re-run. A still-running or just-woken upstream falls through to the normal gate-insert path, so the guard protects frame termination without weakening the rendezvous (see `concept:cascade`, `decision:hard-dep-settled-guard`).

  Replace the Acceptance section with:

  > A node with two `force_upstream_refresh: true` upstreams that settle independently in the same frame: each upstream runs once; the receiver runs once, after both; the frame terminates.

  Falsifier, Business value, and Proof sections stand as-is.

- **Decision: mutate `decisions/hard-dep-settled-guard.md` in place.** Replace the Choice section with:

  > The upstream-refresh pull carries a settled-this-frame guard: an upstream that already has a run row in the frame but no in-flight run is not re-affirmed on receiver re-visits — its value is already in the receiver's drained wait-set. The in-flight probe runs first, so a still-running or just-woken upstream falls through to the normal gate-insert path; the guard protects frame termination without weakening the rendezvous (see `story:multi-hard-dep-rendezvous`, `concept:cascade`).

  Replace the Rationale section with:

  > Without the guard, two `force_upstream_refresh: true` upstreams settling independently in one frame would mutually re-seed and the frame would never terminate; the deterministic two-upstream-refresh scenario stands as the regression pin.

- **Story: create `stories/explicit-attribute-context-read.md`** capturing this spec's STORY-read-without-waking verbatim — role, capability, business value, Acceptance, Falsifier, Proof.

- **Story: create `stories/upstream-pull-on-invalidate.md`** capturing this spec's STORY-pull-upstream-fresh-on-read verbatim — role, capability, business value, Acceptance, Falsifier, Proof.

- **Story: create `stories/uncovered-substitution-rejected.md`** capturing this spec's STORY-uncovered-read-rejected verbatim — role, capability, business value, Acceptance, Falsifier, Proof.

- **Decision: create `decisions/cascade-flags-on-subscribes.md`** capturing TD-cascade-flags-on-subscribes — Choice: the cascade-behavior flags `wake_on_change` and `force_upstream_refresh` are fields of the subscription-entry shape, not of the substitution-ref shape and not of a separate block. Rationale: a single subscription edge can serve multiple substitution reads; placing flags per-ref creates a "which value wins?" ambiguity. The subscription is the edge. Alternatives considered: per-substitution-ref inline flags (rejected: ambiguity across multiple reads of the same sender); a separate `cascade_deps:` block (rejected: third surface for the same concept).

- **Decision: create `decisions/cascade-flags-required-no-defaults.md`** capturing TD-cascade-flags-required-no-defaults — Choice: both flags are required fields on every subscription entry; registration rejects entries missing either. No defaults applied. Rationale: call-site clarity — reading any subscription entry tells the reader the full cascade behavior with no document-memorization. Forces template authors to think about the cascade behavior at every edge.

- **Decision: create `decisions/substitution-grammar-closed.md`** capturing TD-substitution-grammar-closed — Choice: the substitution grammar gains no new tokens. The closed-enumeration discipline is preserved; cascade-shape declaration lives on the cascade-edge surface, not on the read surface. Rationale: separation of read access from cascade coupling — the substitution grammar carries data references only.

- **Decision: create `decisions/substitution-ref-coverage-required.md`** capturing TD-substitution-ref-coverage-required — Choice: every substitution ref in a node's attribute schema must be matched by at least one subscription entry whose sender and type would deliver the corresponding signal; registration rejects templates with uncovered refs. Applies to per-field attribute reads, event reads, and whole-pull reads. Rationale: the "no orphan reads" guarantee survives by static validation rather than by silent edge generation; cascade edges become exactly what the author wrote.

- **Decision: create `decisions/coverage-wildcard-asymmetry.md`** capturing TD-coverage-wildcard-asymmetry — Choice: a wildcard `attribute/*` subscription covers both per-field and whole-pull reads of the same sender; a per-field subscription covers only the matching per-field read and does not cover a whole-pull read. Rationale: a per-field reader watches one field; a whole-pull reader needs to know about every field, so requires the wildcard. The asymmetry is intentional.

- **Decision: create `decisions/cross-cutting-no-force-upstream-refresh.md`** capturing TD-cross-cutting-no-force-refresh — Choice: a cross-cutting subscription (`instance: true`) carrying `force_upstream_refresh: true` is rejected at registration. Rationale: cross-cutting subscriptions are sender-agnostic; there is no specific upstream for the refresh to invalidate.

- **Decision: create `decisions/uncovered-substitution-error-shape.md`** capturing TD-uncovered-substitution-error-shape — Choice: registration rejection for an uncovered substitution ref returns a structured `validation_errors` entry of kind `substitution_ref_uncovered` carrying the receiver node-type, the literal ref text, the schema property path the ref appears in, a copy-pasteable subscription-entry suggestion with both flags set to `false`, and a separate one-sentence note explaining the flag implications. Rationale: programmatic fix-suggestion. Both human authors and LLM agents writing templates consume registration errors; a structured envelope lets them apply the fix mechanically. Keeping the suggested entry as a valid drop-in JSON object preserves its copy-pasteability.

- **Decision: create `decisions/validation-errors-additive-not-uniform.md`** capturing TD-validation-errors-additive-not-uniform — Choice: the new structured-entry shape for substitution-coverage rejections is added strictly additively to the `validation_errors` array; existing `{path, msg}` entries keep their two-field shape. Other registration checks introduced in the same scope (missing flags, cross-cutting incoherent combinations) use the existing `{path, msg}` shape rather than the richer one because the fix is mechanically obvious without a structured suggestion. Consumers distinguish entries by the presence of the discriminator field on the richer shape. Rationale: scope discipline. Generalizing the structured shape across every existing error-emit site is a wider change with no user-outcome justification in the current scope.

- **Decision: create `decisions/hard-dep-field-no-special-case.md`** capturing TD-no-hard-dep-special-case — Choice: the cascade walker and edge builder stop reading the legacy attribute-field `hard_dep:` flag. The attribute-schema validator gains no special-case rejector for the retired field; whatever the existing JSON Schema validation does with unknown properties applies. No migration-redirect error is generated. Rationale: pre-v1, the project does not owe consumers a migration helper. The cleanest interpretation of "erase the retired flag" is "no special-case code naming it." Alternatives considered: explicit `hard_dep_field_removed` registration error with migration redirect (rejected: commemorates the retired field).

- **Decision: create `decisions/wake-on-change-wait-set-only.md`** capturing TD-wake-on-change-wait-set-only — Choice: at cascade walk time, a matching subscription edge always inserts a wait-set row for the receiver on the sender's run. The receiver is additionally stale-marked iff the edge's `wake_on_change` is `true`. Rationale: decouples context-gathering reads from cascade dispatch. The receiver's wake-up is governed by its other subscriptions; its substitution context still receives the sender's data if the sender happens to settle in the same frame.

- **Decision: create `decisions/force-upstream-refresh-via-receiver-keyed-map.md`** capturing TD-force-upstream-refresh-via-receiver-keyed-map — Choice: the receiver-keyed map of upstream node-types to proactively invalidate at receiver invalidation is built at registration from `subscribes:` entries with `force_upstream_refresh: true`. The cascade walker's consumption path is unchanged. Cycle detection at registration; fan-out-target rejection; same-receiver-to-same-sender de-duplication carry over from the prior attribute-field-sourced map. Rationale: the runtime machinery exists and is correct; only the input source changes.

- **Decision: create `decisions/implicit-edge-generation-retired.md`** capturing TD-implicit-edge-generation-retired — Choice: substitution refs do not contribute to the subscription-edge map. The map is fed by the explicit subscription block only. Rationale: cascade edges are exactly what the author wrote; nothing inferred.

- **Decision: create `decisions/substitution-context-builder-unchanged.md`** capturing TD-substitution-context-builder-unchanged — Choice: the substitution-context builder continues to read drained wait-set rows per `concept:wait-set`, with no changes to its logic. Rationale: the builder doesn't care which flag caused the wait-set row to land; the row's presence is what it keys on. The two-flag distinction is implicit in whether the row exists at all.

- **Decision: create `decisions/substitution-grammar-fallback-unchanged.md`** capturing TD-substitution-grammar-fallback-unchanged — Choice: the existing fallback / lenient / optional routing for unresolved substitution refs continues to govern dispatch outcomes when a `wake_on_change: false` edge's sender isn't in scope. Authors declare `| "literal"` fallbacks or `?` lenient markers as they do today. Rationale: the new model removes the implicit edge that was masking the case; it does not change what happens when a ref can't resolve. The existing graceful-degradation grammar finally matters under the author's explicit control.

- **Decision: create `decisions/migration-fills-flags-today-equivalent.md`** capturing TD-migration-fills-flags-today-equivalent — Choice: every existing subscription entry in the codebase receives `wake_on_change: true` and `force_upstream_refresh: false` at migration time. Rationale: preserves today's behavior on every subscription verbatim; the migration's correctness is established by every existing test continuing to pass.

- **Decision: create `decisions/migration-hard-dep-becomes-force-refresh.md`** capturing TD-migration-hard-dep-becomes-force-refresh — Choice: every legacy attribute-field `hard_dep: true` flag is removed at migration time and the corresponding receiver's subscription entry for that sender carries `force_upstream_refresh: true`; if no existing subscription names the sender, a new entry is added with `wake_on_change: true` and `force_upstream_refresh: true`. Rationale: preserves today's hard-dep runtime behavior exactly; existing hard-dep tests carry over as the regression coverage for the new flag.

- **Decision: create `decisions/migration-implicit-edges-become-explicit.md`** capturing TD-migration-implicit-edges-become-explicit — Choice: every implicit edge created today by a substitution ref without a matching explicit subscription is replaced at migration time by an explicit subscription entry with today-equivalent flags (`wake_on_change: true`, `force_upstream_refresh: false`). Rationale: preserves today's behavior on every implicit edge verbatim. After migration, no template relies on implicit edges; the registration coverage check catches new instances at registration.

## Manifest

### Stories
- **STORY-read-without-waking** — context-gathering read without firing the receiver (Proof: all-of-the-above — example template + executable scenario test)
- **STORY-pull-upstream-fresh-on-read** — receiver pulls sender into the same frame on invalidation (Proof: all-of-the-above — example template + executable scenario test)
- **STORY-uncovered-read-rejected** — registration rejects substitution refs without covering subscriptions (Proof: all-of-the-above — example templates + executable validator tests + control-API scenario test)

### Technical decisions
- **TD-cascade-flags-on-subscribes** — flags live on subscription entries
- **TD-cascade-flags-required-no-defaults** — both flags required; no defaults
- **TD-substitution-grammar-closed** — substitution grammar gains no new tokens
- **TD-substitution-ref-coverage-required** — every substitution ref must be covered
- **TD-coverage-wildcard-asymmetry** — wildcard covers per-field; per-field does not cover whole-pull
- **TD-cross-cutting-no-force-refresh** — incoherent combination rejected
- **TD-uncovered-substitution-error-shape** — structured `validation_errors` entry of kind `substitution_ref_uncovered`
- **TD-validation-errors-additive-not-uniform** — the new structured entry is additive alongside the existing `{path, msg}` shape
- **TD-no-hard-dep-special-case** — legacy attribute-field flag gets no special-case treatment
- **TD-wake-on-change-wait-set-only** — `wake_on_change: false` inserts wait-set row but skips stale-mark
- **TD-force-upstream-refresh-via-receiver-keyed-map** — runtime reuses hard-dep edge map with subscription-flag input
- **TD-implicit-edge-generation-retired** — substitution refs don't feed the edge map
- **TD-substitution-context-builder-unchanged** — drained-wait-set query is untouched
- **TD-substitution-grammar-fallback-unchanged** — fallback/lenient/optional routing continues to govern dispatch outcomes
- **TD-migration-fills-flags-today-equivalent** — existing entries get today-equivalent flag values
- **TD-migration-hard-dep-becomes-force-refresh** — existing `hard_dep:` fields become `force_upstream_refresh: true` subscriptions
- **TD-migration-implicit-edges-become-explicit** — implicit edges become explicit subscriptions at migration

### Design changes
- Concept: mutate `concepts/attribute.md` (Non-goals entry rewrite; Invariants line removed; Boundaries note added)
- Concept: mutate `concepts/node-subscription.md` ("What it is" + Owns + Invariants rewrites)
- Concept: mutate `concepts/cascade.md` (Invariants line precision; Boundaries paragraph update)
- Story: mutate `stories/multi-hard-dep-rendezvous.md` (Role + Capability + Acceptance rewrites, swapping `hard_dep: true` for `force_upstream_refresh: true`)
- Decision: mutate `decisions/hard-dep-settled-guard.md` (Choice + Rationale rewrites, swapping `hard_dep: true` for `force_upstream_refresh: true`)
- Story: create `stories/explicit-attribute-context-read.md`
- Story: create `stories/upstream-pull-on-invalidate.md`
- Story: create `stories/uncovered-substitution-rejected.md`
- Decision: create `decisions/cascade-flags-on-subscribes.md`
- Decision: create `decisions/cascade-flags-required-no-defaults.md`
- Decision: create `decisions/substitution-grammar-closed.md`
- Decision: create `decisions/substitution-ref-coverage-required.md`
- Decision: create `decisions/coverage-wildcard-asymmetry.md`
- Decision: create `decisions/cross-cutting-no-force-upstream-refresh.md`
- Decision: create `decisions/uncovered-substitution-error-shape.md`
- Decision: create `decisions/validation-errors-additive-not-uniform.md`
- Decision: create `decisions/hard-dep-field-no-special-case.md`
- Decision: create `decisions/wake-on-change-wait-set-only.md`
- Decision: create `decisions/force-upstream-refresh-via-receiver-keyed-map.md`
- Decision: create `decisions/implicit-edge-generation-retired.md`
- Decision: create `decisions/substitution-context-builder-unchanged.md`
- Decision: create `decisions/substitution-grammar-fallback-unchanged.md`
- Decision: create `decisions/migration-fills-flags-today-equivalent.md`
- Decision: create `decisions/migration-hard-dep-becomes-force-refresh.md`
- Decision: create `decisions/migration-implicit-edges-become-explicit.md`
