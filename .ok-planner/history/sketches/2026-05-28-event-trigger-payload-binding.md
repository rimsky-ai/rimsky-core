# Per-dispatch trigger-payload binding for event-subscriber dispatches

**Date:** 2026-05-28
**Touches:** `lib/runtime/runner_dispatch.go`, `lib/graph/attribute/substitution.go`, possibly `lib/foundation/persistence/...` (a small new column or join), `lib/foundation/cascade/...`
**Type:** Pre-spec sketch.

## Why

Discovered while planning the second external consumer (`rimsky-github-bot`) — the same shape of gap as the `emit_named_event` discovery from the `claude-agent-protocol-coverage` sketch the day before. The bot's design fan-outs work via `concept:named-event` + `concept:node-subscription`: one upstream node emits N named events; the downstream subscriber's wait-set fires N independent dispatches, one per emission. Each downstream dispatch needs to see its OWN triggering event's payload — not the latest emission across all of them.

The substitution grammar is the natural place to wire this — a template author writes the per-dispatch value into the agent's resolved attributes via a substitution directive. But the only event-payload directive that exists today, `{{nodes.X.event.Y.<field>}}`, resolves to the MOST RECENT emission, not the per-dispatch one:

- `code:lib/runtime/runner_dispatch.go::lookupEventPayload` calls `Persist.NodeEvents().LatestByName(...)` — latest only.
- `code:lib/graph/attribute/substitution.go::resolveNodesValue` for the `event` kind uses `EventLookup` with the same latest-only semantics.

The `{{trigger.message.payload.X}}` directive in `code:lib/graph/attribute/substitution.go::resolveTriggerValue` is the right shape — it walks into the dispatch's triggering payload directly — BUT `ResolveContext.TriggerMessagePayload` is populated ONLY in test files (per `grep TriggerMessagePayload` across the codebase). The production dispatch path (`buildResolveContextForDispatch`) never sets it. So the directive exists as half-implemented infrastructure: the consumer side is wired, the producer side isn't.

**Concrete failure mode without this:** the bot's `fetch_and_prefilter` emits 5 `issue_to_triage` events. Rimsky cascades correctly — 5 triage dispatches fire. Every dispatch resolves `{{nodes.fetch_and_prefilter.event.issue_to_triage.issue_number}}` to whichever emission's payload was most recently persisted. The bot processes one issue 5 times and silently misses the other 4.

This breaks any consumer that uses named-events to fan out variable-cardinality work — which is exactly the documented pattern for non-claim-producer-driven fan-out (`concept:fan-out` requires a claim-producer with `supports_split_scope`; the bundled stores don't have it, so consumers reach for named-events instead).

## Design principle

**Faithful protocol coverage, no flow escape.** Same principle as the prior sketch: every input the protocol semantically delivers to a dispatch should be readable from the template's substitution grammar; the addition adds no capability beyond the protocol; the agent (and the template author) gain no new privileged read into rimsky-state outside the dispatch's natural inputs.

This sketch is firmly inside that principle: the per-dispatch trigger is information rimsky ALREADY knows (it's what caused the dispatch); the sketch just propagates it through the substitution context, where consumers naturally expect to find it.

## Current state (audit)

**What works (today):**

| Trigger kind | Per-dispatch payload accessor | Status |
|---|---|---|
| Message (from a publisher — e.g., `sensor-cron` tick) | `{{trigger.message.payload.X}}` | Directive resolver wired; **`ResolveContext.TriggerMessagePayload` NOT populated** in production dispatch path |
| Named-event from upstream node (e.g., per-emission downstream dispatch) | (none — `{{nodes.X.event.Y.<field>}}` is latest-only) | **Missing** |
| Terminal-signal settle (e.g., wait-set drain firing receiver) | (none — `{{nodes.X.attribute.Y}}` reads upstream's persisted attrs, doesn't carry trigger-specific info) | Adjacent / partially covered by attribute reads, not the same thing |

**What rimsky already tracks:**

- The cascade walker DOES know which event/message caused each dispatch — that's how it inserts the wait-set row in the first place. So the data is available; it just doesn't flow into the substitution context.
- `concept:named-event`'s persistence ledger is keyed by `(emitter, name, sequence)` — every emission has a stable identity. Linking a dispatch to the specific triggering emission would be straightforward.

## Proposed addition

### 1. Populate `ResolveContext.TriggerMessagePayload` for message-triggered dispatches

The simplest gap to close. When `buildResolveContextForDispatch` builds the context for a node-run whose trigger was a message (publisher emission), look up the message's payload and bind it into the context. The substitution directive `{{trigger.message.payload.X}}` then works as advertised — the half-implemented infrastructure becomes whole.

### 2. Add `{{trigger.event.payload.X}}` for event-triggered dispatches

For node-runs whose trigger was a specific named-event emission (per the wait-set row that caused the dispatch), bind the triggering emission's payload into the resolve context under a new field — `ResolveContext.TriggerEventPayload` — and add a substitution case `trigger.event.payload.<field>` that walks it.

Mechanism (rough):
- Wait-set rows already record `(sender_run_id, topic_kind, subscription_scope)`. For `topic_kind = "event/<name>"` rows, add a column for the specific event-emission sequence (or row id) that triggered the row.
- At dispatch, the cascade-walker queries the wait-set's "what triggered me" link and resolves to the specific emission's payload bytes.
- Substitution case for `trigger.event.payload.X` walks into those bytes via `walkPath`.

Inertness rule applies (per `@blessed-invariant 21` / `concept:inertness`): rimsky doesn't parse or log the payload; the walk happens at the sanctioned substitution leaf.

### 3. Uniform `{{trigger.payload.X}}` (optional sugar)

For templates that don't care which kind of trigger drove the dispatch, an unkind-discriminated directive `{{trigger.payload.X}}` could resolve from whichever payload is populated (message or event). Useful for templates whose node could be triggered by either. The bot's design doesn't need this — its nodes are triggered by exactly one kind each — but other consumers may.

This is the smallest piece. Could be folded into the spec or deferred.

### 4. Update `concept:named-event` and `concept:node-subscription` docs

The current concept docs (`.ok-planner/design/concepts/named-event.md`, `node-subscription.md`) note that substitution is latest-only and that subscribers fire per-emission. They don't currently say "if you need the specific triggering emission's payload, use `{{trigger.event.payload.X}}`" — because that doesn't exist yet. Once #2 lands, update both concepts to reference the new directive as the right pattern for per-dispatch event-payload access.

## Explicit non-goals

- **No new tools on claude-agent's MCP surface.** This is rimsky-internal substitution wiring; no consumer-side protocol change. Consumers (including claude-agent-based agents) just consume the resolved attribute bag as they already do.
- **No change to the executor protocol.** The substitution mechanism runs entirely in rimsky-core; what reaches the executor is already-resolved attribute values.
- **No new state for the agent to introspect.** The agent doesn't get a new way to query rimsky's internal cascade state. The trigger info reaches the agent only via the template author's substitution directives — same path as every other attribute.
- **No bypass of inertness.** Payload bytes are walked only at the sanctioned substitution leaf; never logged, formatted, or interpreted by rimsky.

## Why this is a general-purpose addition

Three reasons it earns its keep beyond just the bot:

1. **It closes a documented gap.** `TriggerMessagePayload` exists in `ResolveContext` with full doc-comments and consumer-side resolver; it's a documented-but-unimplemented half-feature. The fix is to complete the design, not invent something new.

2. **It's the only currently-supported fan-out pattern for non-store consumers.** `concept:fan-out` is gated on claim-producer `supports_split_scope`, which neither bundled store advertises. Any consumer that wants per-item fan-out without bringing their own claim-producer reaches for named-events + node-subscription — and hits exactly this gap. Closing it removes a real footgun from the supported-fan-out path.

3. **It strengthens `concept:named-event`'s usefulness.** Today the concept doc has to qualify "subscribers fire per emission, but substitution sees latest only" — an awkward asymmetry that surfaces as bugs in template authors' first use. With per-dispatch substitution, the per-emission semantic flows cleanly from sender to receiver without that caveat.

## Spec scope

- Minimum: #1 (populate `TriggerMessagePayload` for message-triggered dispatches) + #2 (add event-triggered analog).
- Worth folding in: #4 (concept-doc updates) — small but high-value for the next consumer.
- Defer: #3 (uniform `{{trigger.payload.X}}` sugar) — not load-bearing; add when a second consumer needs it.

## Touch points

- `lib/runtime/runner_dispatch.go::buildResolveContextForDispatch` — populate `TriggerMessagePayload` and (new) `TriggerEventPayload`.
- `lib/graph/attribute/substitution.go::ResolveContext` — add `TriggerEventPayload` field.
- `lib/graph/attribute/substitution.go::resolveTriggerValue` — extend to handle `event.payload.X` case.
- `lib/foundation/persistence/` (probably wait-set table) — add column linking wait-set row to the triggering event emission (or query-join path if the data is already reachable).
- `lib/foundation/cascade/` — pass the triggering-emission identity through to the dispatch.
- `.ok-planner/design/concepts/named-event.md` — note the new directive.
- `.ok-planner/design/concepts/node-subscription.md` — note the new directive.
- (No proto changes.)

## Relation to the prior sketch

The earlier `2026-05-28-claude-agent-protocol-coverage.md` sketch (which became Feature 5 of `spec:2026-05-28-quality-of-life-features`) added the EMITTER side: claude-agent gained `emit_named_event` so an agent can fan out N items. This sketch adds the RECEIVER side: each fanned-out subscriber dispatch can now access the specific item that triggered it. Together they make per-item fan-out via named-events + node-subscription a fully-supported pattern, which the existing concept docs already advertise as the recommended shape for non-claim-producer fan-out.

## Discovery path

Found while writing the bot's implementation plan in `rimsky-github-bot/.ok-planner/plans/2026-05-28-triage-and-fix.md`. Plan-review round 4 caught that the bot's `triage` and `fix_attempt` nodes would resolve `{{nodes.X.event.Y.<field>}}` to a single value across all per-emission dispatches, breaking the per-item-isolation guarantee that's the bot's whole security story against Anthropic content-classifier DoS. The bot's plan is paused pending this rimsky-core feature.
