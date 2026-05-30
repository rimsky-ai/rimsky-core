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
   inertness language (`@blessed-invariant 21`). Some "payload"s are **not**
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
