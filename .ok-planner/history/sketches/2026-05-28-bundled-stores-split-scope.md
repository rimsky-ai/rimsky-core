# SplitScope support in the bundled claim-producers

**Date:** 2026-05-28
**Touches:** `lib/services/stores/filesystem/`, `lib/services/stores/postgres/`, possibly `lib/services/stores/shared/`
**Type:** Pre-spec sketch.

## Why

`concept:fan-out` is rimsky's first-class primitive for "do N parallel things from one parent unit of work." It's the right answer for any consumer doing dynamic, per-item, parallel work — and it's what we want for `rimsky-github-bot`'s per-issue dispatch (one triage run per issue, one fix-attempt run per selected issue).

The concept's invariant: *"The claim's producer MUST advertise split-scope support in its capabilities. Otherwise template registration rejects."*

Neither bundled claim-producer advertises it:

- `code:lib/services/stores/filesystem/server/server.go::Capabilities` returns only `WriteSemanticsAllowed` — no `supports_split_scope`.
- `code:lib/services/stores/postgres/server/server.go::Capabilities` — same.

So today: any consumer wanting first-class fan-out has to ship their own claim-producer. That's a Go binary plus protocol implementation, which makes the bundled stores look incomplete for an advertised pattern. The bot in particular is supposed to ship as a single Dockerfile + template — building a bot-specific claim-producer breaks that property.

The wire-level surface IS in place: `SplitScopeRequest` / `SplitScopeResponse` are defined in `proto:claim_producer.proto`; rimsky's `concept:fan-out` integration calls them; the only missing piece is producer-side implementation in the bundled stores.

## Design principle

**Complete a documented primitive, don't invent a new one.** `concept:fan-out` is already the recommended fan-out shape. SplitScope is already a documented protocol verb. The bundled stores SHOULD implement it; today they don't. This sketch finishes the picture.

**Bundled-store semantics stay substrate-native.** Filesystem-store SplitScope returns sub-scopes that address paths; postgres-store SplitScope returns sub-scopes that address rows. Each store interprets the opaque `partition_request` bytes against its own substrate model — the proto rule that the bytes are opaque to rimsky stays intact.

**Plus a uniform "list-array" shape across all bundled stores.** For consumers (like the bot) whose fan-out isn't substrate-native — the work items are an abstract list discovered at runtime — every bundled store accepts a uniform list-array `partition_request` that splits the parent claim into one sub-claim per element. This is the load-bearing shape for the bot's use case.

## Current state (audit)

**What's wired:**

- `proto:claim_producer.proto::SplitScopeRequest` / `SplitScopeResponse` exist.
- `proto:claim_producer.proto::PartitionDescriptor` defines `partition_key` (human-readable, persisted in rimsky) + producer-canonicalized scope bytes.
- `concept:fan-out` integration in `lib/runtime/` calls SplitScope at parent-acquisition time.
- Template DSL: `fan_out: { claim, partition_request, parallelism?, error_policy }` on a node.
- Substitution: `{{child.partition_key}}` + `{{claim.<name>.{address,payload,scope}}}` give per-child runs first-class per-dispatch access.

**What's missing:**

- Producer-side `SplitScope` RPC implementation in the bundled stores.
- `Capabilities.supports_split_scope = true` (or whatever the precise field/flag name is — verify against `proto:claim_producer.proto::CapabilitiesResponse`).
- Documented `partition_request` shapes for each bundled store.
- Tests + conformance.

## Proposed addition

### 1. Uniform list-array partition_request (load-bearing for the bot)

Both bundled stores accept this shape:

```json
{
  "list": [
    {"key": "42",  "sub_payload": <opaque bytes>},
    {"key": "47",  "sub_payload": <opaque bytes>},
    {"key": "123", "sub_payload": <opaque bytes>}
  ]
}
```

Semantics:
- One sub-scope per array element.
- Each sub-scope's `partition_key` = element's `"key"` (the human-readable key persisted in `concept:claim-handle`).
- Each sub-scope's address = parent scope's address + element's key suffix (substrate-specific framing; both stores carry the parent address forward so per-substrate accessors still work).
- Each sub-scope's `payload` = element's `"sub_payload"` (passed through verbatim, inert per `@blessed-invariant 21`).

For the bot: the prefilter node produces `{"list": [{"key":"42","sub_payload":{"issue_number":42}}, ...]}` as an attribute, the triage fan-out node's `partition_request` is that attribute, and each triage run reads its issue number from `{{claim.<name>.payload.issue_number}}`.

### 2. Substrate-native partition_request (filesystem)

The filesystem store ALSO accepts a directory-walk shape:

```json
{
  "walk": {
    "filter": "*.json",       // optional glob filter against children
    "depth": 1                // optional walk depth (default 1 = children only)
  }
}
```

Semantics:
- Parent scope MUST address a directory.
- Returns one sub-scope per matching child path.
- partition_key = child basename (or relative path if `depth > 1`).
- Sub-scope address = the child's full path.

### 3. Substrate-native partition_request (postgres)

The postgres store ALSO accepts a query-partition shape:

```json
{
  "by_column": {
    "column": "id",           // partition column name
    "batches": [
      {"key": "1-100",  "where": "id BETWEEN 1 AND 100"},
      {"key": "101-200","where": "id BETWEEN 101 AND 200"}
    ]
  }
}
```

Semantics:
- Parent scope MUST address a table or query.
- Returns one sub-scope per batch element.
- partition_key = batch's `"key"`.
- Sub-scope address = parent table + WHERE clause from the batch.

(Exact shape TBD during spec work — could also be a SQL-fragment shape, or auto-bucketing by row count. The list-array shape from #1 covers explicit-row-list cases without needing this.)

### 4. Capabilities advertisement

Both stores set `supports_split_scope: true` (or the equivalent field per `proto:claim_producer.proto`) in their `Capabilities` response. Template registration's existence check passes; `fan_out:` nodes referencing claims from these stores validate cleanly.

### 5. Conformance

Per `concept:conformance`, the claim-producer conformance binary (under `lib/protocols/`) gains SplitScope test cases:
- Reject SplitScope on a producer that doesn't advertise it (today's behavior — keep).
- Round-trip list-array partition_request and check per-sub-scope shape.
- Substrate-native shape per store (where applicable).

## Explicit non-goals

- **No change to `proto:claim_producer.proto`.** The wire surface already supports SplitScope; this is producer-side implementation only.
- **No change to rimsky's runtime fan-out integration.** Already in place; just becomes usable now that producers advertise the capability.
- **No new substitution directives.** `{{child.partition_key}}` + `{{claim.X.payload}}` already cover per-dispatch access.
- **No change to `concept:fan-out` semantics.** The concept doc already describes the right behavior; the bundled stores just hadn't caught up.
- **No "list partitioner" service as a separate binary.** Folding the list-array shape into both bundled stores is cheaper than running a third service alongside them, and gives operators a choice of substrate to back the bot's state.

## Why it's general-purpose

Three reasons this lands beyond just the bot:

1. **It completes a documented invariant.** `concept:fan-out` says producers MUST advertise split-scope to be usable as fan-out parents. The bundled stores don't — meaning rimsky's documented first-class fan-out primitive has zero bundled implementations. That gap pushes every consumer to build custom claim-producers for what's supposed to be off-the-shelf.

2. **Uniform list-array fan-out unlocks every dynamic-cardinality consumer.** Anyone doing "I discovered N items at runtime, fan out one work unit per item" hits this. Bot is the proximate trigger; the broader pattern is everywhere — batch processors, per-record reconcilers, multi-tenant runners.

3. **Substrate-native fan-out matches what the existing stores are good at.** Filesystem stores are natural for "fan out over directory contents"; postgres stores are natural for "fan out over query partitions." Letting consumers use those substrates directly for fan-out (without a separate partitioner) keeps the deployment surface small.

## Spec scope

- Minimum to unblock the bot: #1 (uniform list-array) on both stores + #4 (capabilities advertisement) + #5 (conformance for the list-array case).
- Worth folding in: #2 (filesystem walk) — small once #1 is in.
- Defer: #3 (postgres by-column) — semantics warrant their own design pass; not load-bearing for the bot.

## Touch points

- `lib/services/stores/filesystem/server/server.go::Capabilities` — advertise `supports_split_scope`.
- `lib/services/stores/filesystem/server/server.go::SplitScope` — implement (new RPC handler).
- `lib/services/stores/filesystem/store/` — sub-scope address construction.
- `lib/services/stores/postgres/server/server.go::Capabilities` — advertise.
- `lib/services/stores/postgres/server/server.go::SplitScope` — implement.
- `lib/services/stores/postgres/store/` — sub-scope address construction.
- `lib/services/stores/shared/` — if the list-array unmarshal logic is shared across both stores, factor here.
- `lib/protocols/` — conformance test additions for the SplitScope verb.
- `.ok-planner/design/concepts/claim-producer.md` — update Notes to record that the bundled stores now advertise split-scope.
- `.ok-planner/design/concepts/fan-out.md` — update Notes to record that fan-out is now usable with bundled stores out of the box.

## Discovery path + relation to other sketches

Found while planning the implementation of `rimsky-github-bot`. Plan-review round 4 caught that the bot's earlier design (using named-events + node-subscription for per-item dispatch) was the wrong mechanism — `concept:fan-out` exists for exactly this case, and `{{nodes.X.event.Y.<field>}}` is correctly latest-only because per-emission downstream access was never the design intent for named events.

The earlier sketch (`2026-05-28-event-trigger-payload-binding.md`) was written under the wrong-mechanism premise and proposed adding per-dispatch named-event payload binding. That sketch is superseded by this one: the right answer is claim-fan-out via SplitScope, and per-dispatch sub-claim access already works via `{{child.partition_key}}` + `{{claim.X.payload}}`.

Companion to the prior `2026-05-28-claude-agent-protocol-coverage.md` sketch which added `emit_named_event` for the emitter side: that one is still load-bearing for the *signal* role of named events ("something happened mid-run"), even though the bot ends up using fan-out instead of named-events for fan-out.

## Status of the consuming project

`rimsky-github-bot` plan is paused at round 4 of plan review pending this rimsky-core feature. Once SplitScope lands on the bundled stores, the bot's template restructures:

- `fetch_and_prefilter` outputs the issue list as an attribute (uniform list-array shape).
- `triage` is a `fan_out:` node holding a claim from one of the bundled stores; `partition_request` substitutes from `fetch_and_prefilter`'s attribute. Each triage run reads its issue via `{{claim.<name>.payload.issue_number}}` — natively per-dispatch.
- Same shape for `prioritize` → `fix_attempt`.

The bot's spec doesn't need editing; the plan does (rewrite the template to use fan-out instead of subscribes-to-named-events).
