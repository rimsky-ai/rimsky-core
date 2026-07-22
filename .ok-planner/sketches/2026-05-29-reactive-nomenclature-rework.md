# Reactive nomenclature rework: from pub-sub vocabulary to invalidate-then-pull vocabulary

**Date:** 2026-05-29
**Type:** Pre-spec sketch (nomenclature / naming rework). Pure rename + doc-clarity; **no behavior change.**
**Motivated by:** a concrete agent misfire (see "Why") surfaced during the
2026-05-29 console-upstream brainstorm forensics.

## Why

Rimsky's reactive engine is **invalidate-then-pull**. When an upstream node's
executor emits, the emission is persisted to a ledger; downstream readers are
marked **stale** and rescheduled; when they re-run, they **pull the latest**
persisted value via substitution. N emissions of the same name collapse to
**one** downstream re-run (wait-set `ON CONFLICT DO NOTHING` on
`(frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)` +
a shared cascade `visited` set; named-events "never create a new frame").
Nothing is delivered along the cascade edge.

But the **vocabulary is pub-sub delivery vocabulary**:

- A non-terminal emission is a "named **event**" that **carries a payload**
  (`concept:named-event`: "a non-terminal executor emission carrying a name
  and an inert payload").
- A downstream node **subscribes** to the event and is a "**subscriber**"
  (`concept:node-subscription`).
- The concept doc states "subscriptions remain **push**: an upstream
  transition causes the receiver to **fire** via the cascade."

"Event," "subscribe," "subscriber," "push," "carries a payload" is the
standard event-bus contract — N events delivered to N subscribers, each
carrying its own payload. That is the **opposite** of what rimsky does. The
names model a delivery system; the engine is a recompute system.

**This has already caused a wrong design — it is not hypothetical.** While
planning a second consumer (`rimsky-github-bot`), an agent wrote
`sketch:2026-05-28-event-trigger-payload-binding` whose load-bearing premise
was *"the downstream subscriber's wait-set fires N independent dispatches, one
per emission."* The code proves the opposite — one dispatch, latest-only;
`code:test/scenarios/on_event_test.go::TestOnEventMultipleEmissionsLatestWins`
asserts that three `progress` emissions yield one dispatch seeing `step:3`.
The agent grounded the cheap, directly-readable claim (latest-only payload
access via `LatestByName`) but **assumed** the cardinality — and the pub-sub
naming actively *confirmed* the wrong assumption instead of catching it. The
bot's plan was paused awaiting a feature that should not exist.

The durable lesson: **misleading nomenclature manufactures wrong premises.**
"Verify, don't assume" is necessary but insufficient when the words themselves
model the wrong mechanism. The fix is to make the names describe what the
engine actually does.

## The mental model the names should convey

A node **watches** other nodes. When a watched node produces a new **response**
(its output), or when an **instance receives a message**, the watcher is
**invalidated** and rescheduled; on re-run it **pulls the latest** values via
substitution. Responses and messages carry **bodies**. Nothing is pushed;
everything is pulled on recompute.

## Proposed renames (direction, not final)

| Today (pub-sub) | Proposed (reactive) | Rationale |
|---|---|---|
| named-**event** / "emit event" | **response** (a node's non-terminal output) | A node produces responses others read; "event" implies a delivered notification. |
| node **subscribes** / subscriber / DSL `subscribes:` | node **watches** / watcher / DSL `watches:` | "Watch" conveys observe-and-recompute; "subscribe" implies delivery. |
| **payload** (event / message body) | **body** | Conventional (HTTP); precise about "the content of a response/message." |
| message "sent to a node" | **an instance receives a message**, which substitutes attributes and invalidates nodes within it | States the real unit (the instance) and the real effect (substitute + invalidate), not delivery-to-a-node. |
| `{{trigger.message.payload}}` / "**trigger message**" | just **the message** the node reads (`message.body` via the node's watch on its addressed message) — drop the `trigger.` wrapper entirely | **There should be no such thing as a "trigger message" — it's redundant.** The `trigger.` substitution namespace has exactly one member (`trigger.message`), so the wrapper carries no information; `trigger.message` could be just `message` and lose nothing. A node reads the message addressed to it; there is no separate "trigger" concept. Confirmed redundant during the 2026-05-29 backfill-override investigation (the mechanism it gates is the backfill `partition_request_override` path, which is being fixed in `spec:2026-05-29-console-upstream-auth-audit-and-fixes` keeping the directive spelling; the rename to drop `trigger.` happens here). |

## Open questions / subtleties — the real work

1. **"response" collides with the terminal result.** A node's *terminal*
   emission is also conceptually its "response." Non-terminal emissions
   (today "named-events") and the terminal result must stay distinct. Decide
   the pairing: "response" for non-terminal + a distinct term for the terminal
   verdict, or a different word for non-terminal entirely. `concept:signal`
   and `concept:terminal-resolution` already occupy nearby ground.

2. **"watch" is already in use (~117 source hits).** `rimsky watch` (the
   instance status/watch CLI), `publisher-subscription`'s `sensor-watch`
   alias, and likely more. `subscribe → watch` collides. Either disambiguate
   (node-watch vs. CLI watch vs. sensor-watch) or choose another verb
   (observe / track). Needs a collision audit before committing.

3. **"subscribe" spans three concepts, and they should not all rename.**
   - `concept:node-subscription` (node↔node reactive coupling) — the prime
     rename target; this is the invalidate-then-pull relationship.
   - `concept:publisher-subscription` (publisher↔instance binding) — touched
     by the "instances receive messages" reframing.
   - `concept:lifecycle-subscriber` (the gRPC lifecycle protocol) — this one
     is a **genuine push protocol** (rimsky calls back to the subscriber).
     "Subscriber" may be *correct* here; renaming it would be wrong. Decide
     per-concept, not globally.

4. **`event/` is a signal type-path prefix** (`concept:signal` taxonomy, ~30
   literal sites). `event → response` means `event/<name>` →
   `response/<name>` across the taxonomy, the substitution auto-subscribe
   edges (`{{nodes.X.event.Y}}` → `{{nodes.X.response.Y}}`), the template
   validators, and their tests.

5. **`payload → body` is ~1700 sites and not uniform.** Proto fields
   (`message.payload`, `Park.payload`, claim-producer `payload`), persistence
   columns (`payload_inline`, `payload_handle`), substitution directives
   (`{{trigger.message.payload}}` → `{{trigger.message.body}}`), and the
   inertness language. Some "payload"s are **not**
   response/message bodies (claim-scope `payload`, `Park.payload`); decide
   whether the rename is uniform or scoped to response/message bodies only.

6. **"and maybe more."** Audit the whole reactive surface for delivery-flavored
   words: "emit," "fire," "dispatch." Leave the already-correct reactive terms
   (`cascade`, `invalidate`, `stale`) alone.

## Blast radius (source counts, `gen/` excluded)

- `subscrib*` ~538 · `payload`/`Payload` ~1696 · `NamedEvent`/`named_event`
  ~143 · `declared_events` ~33 · `event/` type-paths ~30 · `watch` ~117
  (existing — collision surface).
- Surfaces: template DSL keyword (`subscribes:`), the `concept:signal`
  taxonomy + type-paths, proto v1 (`.proto` sources + regenerate), persistence
  migrations (column renames), control-api substitution directives, the CLI,
  the executor SDK (`expected-attributes-schema.ts`), conformance, and tests.
- Concept slugs in scope: `named-event`, `node-subscription`,
  `publisher-subscription` (partial), `message`, `signal`, `event-log`;
  explicitly NOT `lifecycle-subscriber` (push is correct there).

## Explicit non-goals

- **No behavior change.** The engine stays invalidate-then-pull. This is
  rename + doc-clarity only.
- **Not the doc-accuracy fix.** Clarifying the *current* concept docs to state
  the cardinality and pull semantics honestly is happening now, in
  `spec:2026-05-29-console-upstream-auth-audit-and-fixes`, so the next agent
  isn't misled before this rework lands. This sketch is the deeper rename.
- **Not a fold-in.** Too large and cross-cutting to ride the console-upstream
  spec; it gets its own brainstorm.

## Relation to other work

- `sketch:2026-05-28-event-trigger-payload-binding` is the misfire that
  motivated this. Its #1 (complete `{{trigger.message.payload}}` for
  serial_queue message-triggered dispatches) is folded into the
  console-upstream spec; its #2 (per-emission event payload) was dropped as
  premise-false.
- Once brainstormed, this rework resolves the tension
  `tension:event-vocabulary-implies-delivery` (recorded in the console-upstream
  spec).
- Pre-v1: per project rules, take the clean path — rename freely, no compat
  shim, drop/recreate columns rather than threading migrations.

---

## Brainstorm attempt 2026-06-08 (paused pending ok-planner updates)

A brainstorm attempt was started and paused before producing a final spec. The ok-planner toolchain has shape conflicts the rework would fight at every step — the `affirm` skill doesn't bootstrap `design/stories/` and `design/decisions/`; the standard concept-mutation workflow appends `## Notes` entries that the rework's "no historical notes" principle would have to immediately purge; the brainstorm's "no retired-state mention" pre-v1 principle conflicts with the workflow's frontmatter-alias retention default. Resuming should happen after ok-planner is updated.

The substance below captures what was resolved so a fresh brainstorm picks up from a known baseline.

### Resolved direction (locked during the paused attempt)

**Asymmetric rename.** Only the reactive-cascade side renames. The messaging side (`concept:message`, `concept:publisher-subscription`, `concept:lifecycle-subscriber`) keeps its pub-sub vocabulary — the words are honest there. The trap collides with the messaging side precisely because the same vocabulary describes both; only the reactive side is being misnamed.

**Six open questions resolved:**

1. **Non-terminal-emission noun:** keep `event/<name>`. "Events happen" is everyday English; the trap is in the coupling vocabulary around event, not in the noun. `NamedEvent` proto type and `event/<name>` signal type-path stay.

2. **Coupling verb:** rename DSL keyword `subscribes:` → `watches:`. Retire the `sensor-watch` alias on `concept:publisher-subscription`. The CLI `rimsky watch` overload is by-design — every "watch" in the system means observe-and-react.

3. **Concept slugs:** `concept:node-subscription` → `concept:node-watch`. `concept:publisher-subscription` and `concept:lifecycle-subscriber` unchanged.

4. **`event/` type-path:** moot under (1).

5. **`payload → body` scope (asymmetric):**
   - **Rename to `body`:** `NamedEvent.payload` proto field; signal-payload inner fields `event_payload` (in `EventPayload`) and `error_payload` (in **both** `TerminalErrorPayload` and `TransientRetryPayload`); `concept:named-event` and `concept:inertness` body language; persistence columns `rimsky_node_events.payload_inline` / `.payload_handle` / `.payload_handle_backend` → `body_*` (reactive-side named-event ledger; both postgres and sqlite migrations 001).
   - **Keep `payload`:** `Message.payload`; `{{message.payload}}` substitution directive; signal-envelope outer `payload` (CEL accessor); `Park.payload`; claim-scope `payload`; `rimsky_node_runs.parked_payload_*`; `rimsky_messages.payload`; `rimsky_events.payload`.

6. **Verb audit:** `emit` keeps; `dispatch` keeps; `fire` per-site audited (keep on messaging side and receiver-side; rephrase on upstream→watchers).

**Substitution-grammar:** drop the `trigger.` wrapper entirely. `{{trigger.message.<path>}}` → `{{message.<path>}}`. The `trigger.` namespace had exactly one member and was mildly delivery-flavored.

### User-stated principles (load-bearing on resume)

- **Pre-v1 total clean.** No migration hints anywhere — runtime, error messages, validator output, code comments, documentation. A template author submitting a retired keyword gets the generic JSON unknown-field error and figures it out from the spec.
- **No frontmatter `aliases:` for retired slugs.** `concept:node-watch` ships with `aliases: []`.
- **No `## Notes` audit trail on touched concepts.** Backward-looking development history serves no purpose for cold reads of concept docs — the audit trail lives in git and `.ok-planner/history/`. On every concept this rework touches, drop the `## Notes` section entirely; before dropping, fold any unique current-state substance into the body (Definition / Purpose / Boundaries / Invariants). Untouched concepts unaffected — broader project-wide Notes cleanup is a separate spec.

### User-outcome story (locked)

> **STORY-reactive-mental-model-correct** — As a template-designing user (and the LLM assisting me), I can read rimsky's reactive concept docs and DSL and form the correct mental model — observe-and-react / invalidate-then-pull — without conflating it with rimsky's separate cross-frame push messaging system (`concept:message`).
>
> **Acceptance:** A reader (human or LLM) coming cold to `concept:named-event`, `concept:node-watch`, and a representative template's `watches:` block reasons about the system as observe-and-react: they expect one watcher dispatch per frame regardless of emission count, they expect the watcher to pull the latest persisted body via substitution, and they do not propose features (such as per-emission body binding) that the engine cannot deliver.
>
> **Falsifier:** A reader (or LLM agent) reading the post-rework concept docs and DSL still proposes a per-emission delivery or payload-binding design — the misconception class that `sketch:2026-05-28-event-trigger-payload-binding` died to.
>
> **Proof:** Demo + example + executable check (`make lint-vocabulary` passes on the post-rework tree — primary falsifying gate).

The proposed `STORY-retired-vocabulary-actionable-errors` second story was rejected under "pre-v1 total clean" — no actionable-error hints; the generic JSON unknown-field error is sufficient.

### Thoroughness mechanism (locked)

- **Site inventory before rename starts** — exhaustive enumeration organized by surface (proto, DSL keyword, persistence columns, concept docs, code annotations, error messages, observability event names, examples, walkthroughs, test fixtures, executor SDK, conformance). Input to write-plan; work list execute-plan walks to zero.
- **`make lint-vocabulary` regression guard** — token scan over reactive-side surfaces; fails the build if legacy reactive-side vocabulary tokens (`subscribes:`, `concept:node-subscription`, `event_payload`, `error_payload`, `{{trigger.`, "subscriber-cascade", "auto-subscribe") survive. Messaging-side surfaces allowlisted. Becomes part of `make lint`.
- **Mechanical for tokens; hand for prose** — sed/structured-edit for high-volume targeted tokens (proto field names, DSL keyword tokens, concept slug refs in code, `@concept:` annotations, substitution-directive prefix drop). Hand-rewrite concept doc bodies where the framing shifts ("subscribers receive" → "watchers pull on invalidation"), validator error messages, and code comments.

### Concept mutation list (15 concepts touched)

- `concept:node-subscription` → **rename to** `concept:node-watch` (slug + body)
- `concept:named-event` — body rewrite (lead with happens-at-node framing; payload → body)
- `concept:signal` — inner field renames `event_payload`/`error_payload` (both `TerminalErrorPayload` and `TransientRetryPayload` carriers); receiver-side vocabulary; cross-ref repointing
- `concept:inertness` — named-event payload → body; inertness prose
- `concept:publisher-subscription` — retire `sensor-watch` alias; Naming-note cross-ref repoint and DSL keyword update
- `concept:wait-set` — cross-ref repoint to `concept:node-watch`
- `concept:cascade` — receiver-side cascade language (subscription-edge → watch-edge; subscription-driven → watcher-driven; subscribers → watchers). The literal phrase "subscriber-cascade" only appears in historical Notes (now purged).
- `concept:attribute` — substitution prose (drop `trigger.`, both braced AND unbraced grammar forms); auto-subscribe rule → auto-watch rule; cross-ref repoint; Non-goals' `auto-subscribe` mention
- `concept:message` — drop `trigger.` wrapper in substitution prose; Adjacent cross-ref repoint
- `concept:node` — live DSL keyword `subscribes:` → `watches:`; cross-ref repoint
- `concept:discovery-cache` — Boundaries / Adjacent cross-refs repoint
- `concept:error-policy` — live DSL example in retirement-note prose: `subscribes:` → `watches:`
- `concept:fan-out` — drop `trigger.` wrapper in the live `partition_request_override` invariant
- `concept:parked-state` — receiver-side reactive prose (the Retracted block is historical content; Notes-purge covers it)
- `concept:transition-reason` — live invariant: "cascade-fire gate (now subscriber-driven)" → watcher-driven

### Open ambiguities to resolve in the fresh brainstorm

Each surfaced during the spec-review pass:

- **Lint allowlist seam.** Which surfaces does `lint-vocabulary` exclude? At minimum: messaging-side files (`lib/control/controlapi/messages.go`; `concept:message` / `:publisher-subscription` / `:lifecycle-subscriber`). Untouched concept docs may still carry old vocabulary in surviving Notes until broader cleanup — needs an allowlist strategy.
- **`concept:signal` receiver-side rename rule.** The live body uses `subscrib*` in ~8 places — some reactive-side, some receiver-agnostic. A deterministic rule (e.g., "every `subscrib*` token in the live body except in references to `concept:publisher-subscription` and `concept:lifecycle-subscriber`") is needed for mechanical application.
- **Compound terms.** "self-subscription" → "self-watch"? General rule for compounds.
- **Verify under the principles.** The JSON-decode actionable-error question (whether `DisallowUnknownFields()` produces an acceptable error) is resolved under "pre-v1 total clean" — generic error is acceptable. Worth re-confirming on resume.

### ok-planner toolchain updates needed before resuming

1. `affirm` skill bootstraps `design/stories/` and `design/decisions/` directories in every run.
2. Concept-mutation workflow supports producing concept body updates without appending `## Notes` entries (or makes Notes-appending opt-in).
3. Spec template / write-plan supports specs that touch concepts only (no story / decision catalog landing directives when the spec policy-drops them).
4. Possibly: convention for marking concept Notes "purged-on-touch" vs "preserved" — so future mutating skills know not to re-append.

### Work items from the paused brainstorm

The brainstorm surfaced 14 specific work items. Dispositions:

**Resolved during the brainstorm — no further work needed on resume:**

- **A1: payload spill-column rename ambiguity** — resolved. Rename `rimsky_node_events.payload_*` → `body_*` (postgres + sqlite migration 001); keep `parked_payload_*`, `rimsky_messages.payload`, `rimsky_events.payload`, claim-scope payload, blob-orphans. (See Resolved direction → Q5.)
- **A2: actionable-error story for retired keywords** — resolved by dropping the second story entirely. Pre-v1 total clean: generic JSON unknown-field error suffices.
- **C1: missing `design/stories/` and `design/decisions/` catalogs** — resolved by the parallel `plan:2026-06-08-design-corpus-bootstrap` (Pass 3) which creates these catalogs before this rework's plan would run.
- **B6: `concept:publisher-subscription` stale Notes prose** — resolved under the no-`## Notes` principle (the stale entries disappear when the section is dropped).
- **Notes audit and disposition** — resolved as the load-bearing "no `## Notes` audit trail on touched concepts" principle.

**Identified for the eventual spec — concept-mutation list items, substance captured in the 15-concept list above:**

- **B1:** add `concept:fan-out` mutation (drop `trigger.` wrapper in the live `partition_request_override` Invariant).
- **B2:** add `concept:parked-state` mutation (receiver-side reactive prose; the Retracted block disappears via Notes-purge).
- **B3:** add `concept:transition-reason` mutation (live invariant: "cascade-fire gate (now subscriber-driven)" → watcher-driven).
- **B5:** rewrite `concept:cascade` directive to enumerate the actual live receiver-side terminology (subscription-edge → watch-edge; subscription-driven → watcher-driven; subscribers → watchers). The literal phrase "subscriber-cascade" only appears in historical Notes (purged).

**Open ambiguities to revisit on resume — enumerated above under "Open ambiguities":**

- **A3:** `lint-vocabulary` allowlist seam vs surviving Notes on untouched concepts.
- **B4:** `concept:attribute` directive precision — covers line-23 cross-ref, line-35 `auto-subscribe` mention, line-42 unbraced grammar form.
- **B7:** `concept:signal` deterministic rename rule for receiver-side `subscrib*` (~8 ambiguous live sites; some reactive-cascade, some receiver-agnostic).
- **B8:** "self-subscription" → "self-watch" compound-term rule; deterministic rule for compounds in the `node-watch` body rewrite.

### Files affected during the paused attempt

- Spec at `.ok-planner/specs/2026-06-08-reactive-vocabulary-rework-design.md` was created and is being deleted as part of the abort.
- This sketch was updated to capture the resolved direction and work items.
