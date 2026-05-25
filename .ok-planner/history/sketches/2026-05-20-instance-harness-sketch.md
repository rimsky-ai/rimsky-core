# Instance Harness — Design Sketch

**Date:** 2026-05-20
**Status:** Sketch (not a spec; not authorization to build)

## Idea

A first-class **instance harness** on the `concept:control-api` that lets external consumers' tests (and operators' debug sessions) influence dispatches and observe state on a live `concept:instance` without consumer-specific scaffolding. Pre-v1 platform feature; smallest useful slice is a matcher-extended overlay on `concept:userdata`, packaged alongside two sibling primitives (event tap, breakpoint) that share the same operator-facing umbrella.

The immediate forcing function: crimefinder's integration test (`file:apps/crimefinder/test/integration/full-pass.test.ts`) can register a template and create an instance against a real rimsky stack, but cannot drive its DAG to terminal because there's no way to script per-(zone, iter_num) stub outcomes — `concept:userdata` is inert (`@blessed-invariant 11`) and the existing `userdata_overrides` mechanism routes only by `by_executor` / `by_node`, which is too coarse when one node dispatches many children with different identities. The structural answer is to extend the routing dimension with a matcher predicate over dispatch-time identity (executor, node, resolved attrs); generalising the surface as an "instance harness" buys us breakpoints and event taps in the same package, which any consumer's test suite or live debugging session benefits from.

## Shape

### Naming

Three sibling primitives under one umbrella:

- **Matcher overlay** — extension to `concept:userdata`'s existing per-instance override merge, gaining a third routing dimension keyed by a dispatch-time matcher predicate.
- **Event tap** — streaming control-API endpoint emitting state-transition events and `concept:named-event` payloads for a live instance, replacing the poll-the-JSONL pattern consumers fall back to.
- **Instance breakpoint** — supervisor-cooperative pause-points on a live instance, surfaced via control-API; `concept:parked-state` is the existing kin pattern but breakpoints are distinct (operator-injected vs executor-emitted Snooze).

Collectively: the **instance harness**. The three are independent primitives; consumers pick whichever subset they need. Alternatives considered: "debug harness" (implies dev-only and gates the production-utility framing), "test scaffolding" (honest about primary user but unhelpful to the operator-debug use case), "dispatch overlay" (only names the first primitive).

### Primitive 1: matcher overlay

**Today.** `col:rimsky_instances.userdata_overrides` carries `{by_executor, by_node}`; `code:runtime/userdata_overrides.go::applyUserdataOverrides` deep-merges four layers at dispatch (`code:foundation/shared/jsonmerge.go::DeepMergeJSON`). Validation at instance-create inspects only routing keys (`@blessed-invariant 11`).

**Extension.** Add a third routing dimension, `by_match`, holding an ordered list of `{matcher, overlay}` entries. At dispatch, after the existing four layers merge, every entry whose `matcher` evaluates true against the dispatch context is folded on top, in declaration order. More specific (later) wins.

Wire shape:

```jsonc
{
  "by_executor": {"<executor-name>": { ...fragment... }},
  "by_node":     {"<node-name>":     { ...fragment... }},
  "by_match":    [
    {
      "matcher": {
        "node_type": "fix-fan-out",                    // string equality
        "executor":  "crimefinder",                    // optional
        "frame":     "sub-graph:fix-iteration",        // optional; main or sub-graph name
        "attrs":     {"iter_num": 1, "zone_id": "z_a"} // equality on resolved attribute values
      },
      "overlay": { ...fragment... }
    },
    ...
  ]
}
```

**Matcher grammar.** Equality-only on a fixed set of keys. No expressions, no regex, no wildcards (yet). Keys:

- `node_type` — string, equals the template's `node.type`. Required.
- `executor` — string, equals the resolved executor name. Optional.
- `frame` — string; `main` or `sub-graph:<name>`. Optional. Distinguishes a node-type that appears in both the main graph and a sub-graph (the `concept:delegation` absorption rule means one type can fire in multiple frame contexts).
- `attrs.<key>` — primitive equality (string / number / bool) against the resolved `concept:attribute` value visible to substitution at that dispatch. Optional; nested keys via JSON Pointer slash syntax for nested attribute schemas.

Missing keys in `matcher` are wildcards. Empty `matcher` (`{}`) matches every dispatch — useful for "apply this overlay to every dispatch in the instance" smoke patterns.

**Where evaluation happens.** Just before `applyUserdataOverrides` (or as a fifth layer inside it). The dispatch context at that point already has executor name, node name, frame identity, and resolved attribute values; matcher evaluation is local.

**What `overlay` can override.** The userdata fragment, exactly like `by_executor`/`by_node`. Nothing else — selectors, attributes, claim metadata stay untouched. (See Open questions for the "should overlays also influence attribute values" thread.)

**Validation discipline.** Continues to inspect only routing keys (`by_match` is a list, each entry's `matcher` keys validated, each entry's `overlay` fragment opaque). `@blessed-invariant 11` preserved end-to-end.

**Why this shape.** It's the smallest extension to a mechanism that already exists. Consumers needing the "per-child fan-out scripting" pattern (crimefinder's case) get exactly that. Consumers without per-child needs continue to use `by_executor` / `by_node`. The matcher grammar is intentionally tiny — adding a real expression language is a separate spec.

### Primitive 2: event tap

**Today.** `concept:event-log` (`table:rimsky_events`) and `concept:named-event` (`table:rimsky_node_events`) are append-only JSONB tables; `concept:cascade-graph` exposes them over HTTP (`route:GET /observability/events`, `route:GET /observability/frames`, etc.) as JSON snapshots. Consumers poll these endpoints to assert on what happened.

**Extension.** A streaming endpoint on `concept:control-api`:

```
GET /instances/{id}/tap
  ?since=<event_id>
  ?kinds=node_state,named_event,frame_created,...
```

Server-Sent Events (SSE) — chosen over WebSocket for simpler ops and because consumers are pure readers. Each event is a JSON envelope:

```jsonc
{
  "kind": "node_state | named_event | frame_created | claim_handle_transition | instance_terminal",
  "instance_id": "<uuid>",
  "frame_id": "<uuid?>",
  "node_run_id": "<uuid?>",
  "ts": "<ISO8601>",
  "seq": <monotonic-int>,        // per-instance, for resume-from-seq
  "payload": { ...kind-specific... }
}
```

**Kinds emitted.** At minimum: `node_state` (every `concept:transition-reason`-bearing transition), `named_event` (every `concept:named-event` emission), `frame_created` / `frame_settled`, `claim_handle_transition` (the three-state `concept:claim-handle` transitions), `instance_terminal`. Tests assert on ordering and contents.

**Resumability.** `?since=<seq>` replays from the persistent log; the in-memory tap shouldn't be the source of truth. Persistence backed by the existing `rimsky_events` + `rimsky_node_events` tables, with a thin in-memory pub-sub for the live tail. Two-phase: subscriber asks for since-seq, server fans out the historical pages then attaches to the live pub-sub.

**Why SSE.** Simpler than WebSocket for unidirectional read. Works through proxies. No new transport dependency.

**Why now.** Consumers' tests fall back to polling JSONL (crimefinder), polling control-API JSON snapshots, or sleeping. SSE replaces all of these with one assertion-friendly stream. Operators get a real-time dashboard hook for free.

### Primitive 3: instance breakpoint

**Today.** No mechanism to pause an instance mid-run. `concept:parked-state` is the closest kin — entered when an executor emits `Snooze` — but it's executor-driven, not operator-injected.

**Extension.** Operator-injected pause-points on the supervisor loop. Wire shape (control-API):

```
POST /instances/{id}/breakpoints
  body: {matcher: {...}, when: "before_dispatch" | "after_terminal"}
  → {breakpoint_id}

GET  /instances/{id}/breakpoints/{bp_id}/hits
  ?wait=<seconds>
  → 200 {hit_id, node_run_id, frame_id, snapshot: {...}} on hit
  → 204 (no hit yet) on timeout

POST /instances/{id}/breakpoints/{bp_id}/resume
  body: {hit_id}
  → 200 {resumed: true}

DELETE /instances/{id}/breakpoints/{bp_id}
```

Matcher reuses the matcher grammar from Primitive 1 (same shape, no new vocabulary).

**Supervisor cooperation.** At the supervisor's `before_dispatch` and `after_terminal` checkpoints, query the breakpoints table for matching predicates; if any match, write a `breakpoint_hit` row, suspend the runner waiting on a `pg_notify` (or in-process channel for SQLite), and only continue after the resume RPC fires. Hits accumulate in a per-instance queue; consumers drain via the wait-endpoint.

**Snapshot payload.** What the breakpoint surfaces on hit. At minimum: the dispatch context (executor, node, resolved attrs, userdata after all overlays), the current `concept:node-run` row, the current frame's open wait-set, the held claim handles. Read-only snapshot; the breakpoint doesn't let the consumer mutate state mid-pause (that's a deliberate boundary — overlays handle "inject something" before the dispatch starts).

**Interaction with `concept:auto-terminal`.** A breakpoint at `after_terminal` runs after the supervisor processes the terminal but before the held-claim resolution fires. This lets a test observe "what did the executor return?" without racing the cascade.

**Bigger than the other two.** This is the runtime-cooperation-needed primitive; both supervisor binaries (`code:cmd/rimsky-supervisor`) and the persistence layer need a `breakpoint_hits` table. Honest scope: this could be a follow-up sketch if the user wants the matcher overlay shipped sooner.

### Surface summary

| Primitive | Storage | Control-API surface | Runtime touch |
|---|---|---|---|
| Matcher overlay | extend `col:rimsky_instances.userdata_overrides.by_match` | extend `route:POST /instances` body validator | extend `code:runtime/userdata_overrides.go::applyUserdataOverrides` |
| Event tap | reuses `table:rimsky_events` + `table:rimsky_node_events` + in-memory pub-sub | new `route:GET /instances/{id}/tap` (SSE) | supervisor publishes to the in-memory bus on transitions; cascade-fire path already writes the persistent rows |
| Breakpoint | new `table:rimsky_instance_breakpoints` + `table:rimsky_breakpoint_hits` | four new routes under `/instances/{id}/breakpoints` | supervisor `before_dispatch` and `after_terminal` checkpoints query + await |

### Auth and production guardrails

`concept:control-api` already has the `concept:permission` model (`code:control/auth/`). New action verbs:

- `instance:overlay` — set `by_match` on an instance at create-time. Existing `instance:create` should imply this; explicit verb gives operators fine-grained control if they want to forbid debug-overlay creation in prod.
- `instance:tap` — read the SSE stream. Read-only, low-risk.
- `instance:breakpoint` — create / resume / delete breakpoints. High-risk in prod (pauses live work); recommend gating behind a separate role-template (`debug-operator`) that production deployments need to grant explicitly.

Single env-var gate (`env:RIMSKY_INSTANCE_HARNESS_ENABLED=1`) is the simpler alternative — disable all three primitives unless set. Bias toward the permission-based gate (consistent with the rest of the auth model) but the env-var path is fine if simpler is preferred at v1.

### Interaction with idempotency

`POST /instances` already supports `Idempotency-Key` semantics (`table:rimsky_message_idempotencies`) on its message-emit cousin; the `Idempotency-Key` for instance creation makes the full request payload (params + `userdata_overrides` including `by_match`) part of the dedup hash. Same overlay map + same key → same instance returned. No new idempotency surface needed.

### Consumer-side shape (illustrative, not in scope)

Consumer test libraries would offer a thin wrapper:

```ts
const instance = await harness.createInstance(templateHash, {
  params: { repo_root, mission: "integration test", trigger: "manual" },
  overlay: matchers([
    when({ node_type: "review-fan-out", attrs: { zone_id: "z_a" } }).overlay({ stub_outcome: ... }),
    when({ node_type: "fix-fan-out", attrs: { iter_num: 1, zone_id: "z_a" } }).overlay({ stub_outcome: ... }),
    ...
  ]),
});

for await (const event of harness.tap(instance.id, { kinds: ["instance_terminal", "named_event"] })) {
  if (event.kind === "instance_terminal") break;
  // assert on intermediate events
}
```

This is a `concept:rimsky-cli` extension and per-language sugar, not part of the rimsky platform sketch. Listed only to show the ergonomics target.

## Open questions

- **Should overlays also be able to influence `concept:attribute` values?** Overrides on attributes would let tests inject state that downstream substitution sees (e.g. "pretend iter-guard returned `affected_zones: [z_a, z_b]` regardless of producer state"). Strong feature, but expands the "what is inert" boundary — would need to reason about cascade fire semantics if an overlay changes an attribute mid-frame. Likely a follow-up.
- **Matcher grammar — equality only, or add `in` / `prefix`?** Equality is fine for crimefinder's use cases (specific zone_ids, specific iter_nums). `attrs.zone_id in ["z_a", "z_b"]` would be useful but starts down the "we have an expression language" path. Probably worth resisting.
- **Frame matcher granularity.** `frame: "sub-graph:fix-iteration"` is the natural minimum. Do we need per-instance-of-a-sub-graph distinguishing (e.g. matching only the second time `fix-iteration` runs)? Probably not — `attrs.iter_num` covers it.
- **Event tap kinds — what's the exact taxonomy?** Sketch lists five; the right list comes from grepping what currently writes to `table:rimsky_events`. The set should be small (~5-10) and stable.
- **Event tap resume window.** Forever (rely on `concept:event-log` retention)? Or capped at the instance's lifetime + some grace? `concept:event-log` has its own retention; tap inherits whatever cascade-graph already exposes.
- **Breakpoint resume snapshot mutability.** Strict no-mutation is the proposed boundary. But the most-asked feature might be "let the test override the dispatch's userdata at hit-time, not just at instance-create." That collapses overlays and breakpoints. Worth a follow-up conversation if breakpoints get built.
- **Hit-queue overflow.** If a consumer creates a breakpoint but never drains the hits, the queue grows unbounded. Per-breakpoint cap with backpressure (block the supervisor on a full queue)? Or drop with a metric? Production safety question.
- **Naming.** "Instance harness" is the proposal; alternatives discussed in the Naming subsection. If the user dislikes "harness," "instance tooling" or "instance debug" are fallbacks.
- **Where overlays live in the merge order.** Sketch proposes: fifth layer, on top of the existing four. The existing four are `template_defaults → node.userdata → by_executor → by_node`. Matcher overlays as fifth makes "operator-specific" win over "operator-broad" which matches the existing more-specific-wins discipline. Worth confirming.

## Risks / unknowns

- **Matcher grammar slipping.** Once an expression language starts, it's hard to stop. Equality-only is a defensible boundary but the first feature request (`in`-set match) will be tempting. Plan to push back hard on grammar growth in the spec.
- **Event tap publish-on-transition adds supervisor hot-path work.** SSE pub-sub is cheap in absolute terms but it's another non-zero thing on every state transition. Probably fine; worth measuring under load.
- **Breakpoint correctness under concurrent frames.** A breakpoint that fires in frame A while frame B is also resolving against the same instance: does B continue? Probably yes (breakpoints suspend the matching `concept:node-run`, not the whole instance), but the snapshot needs to honestly say "you're paused in frame A; frame B is still moving." Document carefully.
- **The "useful test scaffold" framing tempts feature creep.** Mocking patterns like "respond differently to the third call" are tempting but pull the matcher grammar toward state. Hold the line: matchers are stateless predicates over dispatch identity. Consumers that want stateful scripting build it on top of the tap (subscribe → assert sequence → respond via overlay on the next instance).
- **Auth model — debug-operator role template.** If the role-template approach is chosen, it's another `concept:role-template` to maintain. If env-var gating is chosen, it's a binary on/off that mismatches the rest of the auth model. Pick one, commit.
- **Crimefinder's commit-fix path needs file edits.** Even with matcher overlays, the fix-cycle stub needs to mutate the working tree before calling `review_commit_fix` (the gate validates working-tree changes overlap the finding's file). That's a stub-executor concern, not a rimsky concern, but the consumer-side library will need a `file_edits` step in its outcome shape. Worth flagging that even with this sketch shipped, crimefinder still has a small extension to its own stub-mode to land.
- **SQLite vs Postgres parity for breakpoints.** `pg_notify` is the natural pause-resume signal; SQLite needs in-process channels. Both are fine; the persistence layer abstracts cleanly (`concept:persistence-database`).

## What this is not

- **Not a general-purpose runtime debugger.** No "set a watch on this attribute" or "evaluate this expression in the current scope." This is dispatch-time control + observation, not a REPL.
- **Not a workflow-state mutator.** Overlays influence what the executor sees; they don't directly mutate `concept:node-run` rows, `concept:claim-handle` rows, or `concept:attribute` values. The only state mutation is through normal dispatch.
- **Not a replacement for unit testing.** Producers and executors should still have their own unit tests; this exists to test the wire surface and DAG integration that unit tests can't reach.
- **Not a substitute for the gated real-Claude E2E.** Real-Claude smoke tests (`file:apps/crimefinder/test/e2e/smoke.test.ts`) still serve a different purpose — they catch real API / real CLI subprocess behavior. The instance harness exists for everything below that line.
- **Not authorization to build.** Sketch only. Path to implementation is `/brainstorm` → spec → `/write-plan` → `/execute-plan`. The breakpoint primitive specifically might warrant its own sketch + brainstorm separate from the overlay+tap pair, given its larger scope.
