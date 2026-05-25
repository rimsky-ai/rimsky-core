# Filter evaluation in the cascade walk

**Date:** 2026-05-19
**Status:** Sketch — pre-spec design exploration
**Audience:** Future planner / implementer; rimsky maintainers
**Related:** `concept:node-subscription` (filter fields), `concept:wait-set` (pessimistic-invalidate), `sketch:2026-05-19-control-mcp-subscribe-push` (filter-driven push notifications)

## Context

`code:foundation/spec/subscription.go::SubscriptionEntry` defines a rich filter vocabulary on `subscribes:` declarations:

- **State topics:** `When` (node state), `Outcome` (last_outcome), `ErrorClass`, `Reason` (ParkReason)
- **Attribute topics:** `Name` (attribute key)
- **Event topics:** `Name` (event name) — required
- **Message topics:** `Kind`, `Sender`, `SenderKind`, `Target`

These fields exist on the wire, they validate at template registration (`code:graph/node/template_validator.go::validateSubscribes`), they get persisted through `code:graph/node/subscription_edges.go::edgeFromSubscription` onto `SubscriptionEdge.Filter`. They are **documentation only at runtime.** The cascade walk at `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx#366-368` says it out loud:

> "The pessimistic-invalidate rule inserts a wait-set row for every subscription edge regardless of filter compatibility; idempotent re-fire handles filter mismatch."

So a node declaring `subscribes: { node: X, on: state, when: failed, error_class: my_class }` fires on **every** terminal of X — successes, other-class errors, parks. The receiver dispatches anyway, sees the actual transition doesn't match its filter, commits a no-op, and exits. Cheap by design, but wasted work, wasted token spend (the receiver is often a Sonnet dispatch), wasted operator attention in event logs.

Two concrete pressures have surfaced from the recent docs-pipeline work:

- **Self-cycle pattern doesn't actually need its own filter.** I added `outcome: fresh_changed` to the self-subscription in the 2026-05-19 cascade walker fix, partly out of design taste — "express the intent explicitly." Turns out it's a no-op: `cascadeSubscribersStaleInTx` is only called when `lastOutcome == cascade.LastOutcomeFreshChanged`, so `fresh_unchanged` commits don't even reach the walker. The filter is true-by-construction in that codepath. Other filters (the consolidate node's `when: failed`, say) genuinely matter and currently genuinely don't gate anything.
- **"Downstream waits for loop completion" is unbuildable today.** The user's design intuition for self-loops ("with rich enough signals, downstream nodes can wait until the loop completes") requires `outcome: fresh_unchanged` to actually filter out the intermediate `fresh_changed` cascade fires. Today it doesn't. Until it does, the choice between `frame: in` self-loop and `frame: next` self-loop is masked — both patterns fire downstream on every iteration regardless of filter.

The lift to make filters load-bearing is small, the win is principled, and the breaking-change cost is bounded (rimsky is pre-v1; the affected surface is templates that use filters, which is a minority today).

## Goals

1. **Filters gate cascade-walk firings.** A subscription with a non-empty filter only contributes to wait-set inserts (or new-frame opens) when the transition actually matches.
2. **Filter semantics are per-topic-kind.** State filters consult `(state, last_outcome, error_class, reason)`. Attribute filters consult the `(name)` of the changed attribute. Event filters consult the `(name)` of the emitted event. Message filters consult the message envelope's `(kind, sender, sender_kind, target)`.
3. **Idempotent re-fire stops happening.** Receivers no longer get false-positive dispatches whose only job is to no-op back to fresh.
4. **Backward compatibility: pre-v1 break, documented.** No flag, no opt-in. Templates with imprecise filters change behavior. CHANGELOG entry + migration note in the concept doc.

## Non-goals

- **Wait-set drain semantics stay unchanged.** `drainWaitSetOnSettled` still bulk-deletes all rows where the sender is settled. The change is only at insert time: if the filter doesn't match, no row inserts in the first place.
- **No new filter operators.** The vocabulary stays the same — equality checks on the existing fields. Future expansions (regex, set-membership, expression DSL) are out of scope.
- **No "any-of" filter combinators within one subscription entry.** If a receiver wants to fire on `outcome IN {fresh_changed, passed}`, it declares two subscription entries. The cascade walker considers each separately.
- **No change to the no-filter case.** A bare `subscribes: { node: X, on: state }` (no `when`, no `outcome`, etc.) still fires on every state transition of X. Filter-evaluation only narrows; absent filter = match-all.

## Design

### Per-topic filter-match functions

Each topic kind gets a `Match` function over the runtime transition data:

```go
// State-topic match. Called from applyTerminalComplete /
// applyTerminalError / applyTerminalPark paths, each carrying the
// state-shaped transition data for that terminal.
func MatchesStateFilter(f SubscriptionFilter, newState, lastOutcome, errorClass, parkReason string) bool {
    if f.When != "" && f.When != newState { return false }
    if f.Outcome != "" && f.Outcome != lastOutcome { return false }
    if f.ErrorClass != "" && f.ErrorClass != errorClass { return false }
    if f.Reason != "" && f.Reason != parkReason { return false }
    return true
}

// Attribute-topic match. Called from applyTerminalComplete after the
// per-field delta is known. One walk per changed attribute field.
func MatchesAttributeFilter(f SubscriptionFilter, attributeName string) bool {
    if f.Name != "" && f.Name != attributeName { return false }
    return true
}

// Event-topic match. Called from runner_named_events when the agent
// emits a NamedEvent. Per the existing surface, Name is always set
// on event subscriptions (validator enforces).
func MatchesEventFilter(f SubscriptionFilter, eventName string) bool {
    return f.Name == eventName
}

// Message-topic match. Called from message-delivery cascade.
// `target == "self"` resolution already happens at canonicalization
// (`code:graph/node/subscription_edges.go`); by walk-time `Target`
// holds the resolved receiver alias.
func MatchesMessageFilter(f SubscriptionFilter, msg MessageEnvelope) bool {
    if f.Kind != "" && f.Kind != msg.Kind { return false }
    if f.Sender != "" && f.Sender != msg.Sender { return false }
    if f.SenderKind != "" && f.SenderKind != msg.SenderKind { return false }
    if f.Target != "" && f.Target != msg.Target { return false }
    return true
}
```

These live next to the `SubscriptionEdge` type in `code:graph/node/subscription_edges.go` so the cascade walks can import them without back-edges into runtime.

### Wiring into the cascade walks

`code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` gains new parameters for the state-transition context (new state, last outcome, error class if any, park reason if any). Inside the per-edge loop, before any wait-set insert or `EnqueueOrCoalesce`:

```go
for _, edge := range candidateEdges {
    if edge.TopicKind == "state" {
        if !MatchesStateFilter(edge.Filter, newState, lastOutcome, errorClass, parkReason) {
            continue  // filter rejects this edge; receiver does not fire
        }
    }
    // ... rest of the per-edge logic (FrameNext / FrameIn dispatch)
}
```

Attribute-topic edges similarly: the walk iterates per changed-attribute-name, and the per-edge gate calls `MatchesAttributeFilter`. Event-topic edges run in their own walk (`code:runtime/runner_named_events.go`); same shape, calling `MatchesEventFilter`. Message-topic edges run in the message delivery cascade (existing surface) and check `MatchesMessageFilter`.

The visited map keeps its role: cycle-guarding within one BFS walk. Filter-rejection happens BEFORE the receiver is pushed onto the queue, so a rejected edge contributes nothing to the BFS or to the wait-set.

### Walks against multiple terminal kinds

Today only `applyTerminalComplete` calls `cascadeSubscribersStaleInTx` (gated on `last_outcome == fresh_changed`). With filter evaluation, the obvious follow-on is firing the walk for the OTHER terminal paths too — because a receiver subscribing with `when: failed` or `when: parked` actually wants to know about those transitions:

- `applyTerminalError` should cascade-walk so `when: failed` subscriptions can fire. The state-filter match correctly gates non-matching subscribers.
- `applyTerminalPark` should cascade-walk so `when: parked` subscriptions can fire.

The wait-set's settled-state drain already handles `failed` and `parked` settlements (`drainWaitSetOnSettled` is unconditional on settled-state); the missing piece is the symmetric walk on the way in.

This is half the value of filter-evaluation: receivers can now subscribe to specific failure modes (e.g., `when: failed, error_class: silence_timeout` for an external observer that monitors timeouts) and the cascade walks for those terminal paths actually exist.

### Self-loop semantics under the new rules

With state-filter evaluation, `frame: in` self-edges become genuinely useful and safe. Walking through the post-change behavior of a self-loop with `outcome: fresh_changed, frame: in` on node X:

1. X's iteration N commits `fresh_changed`. Walk fires; self-edge filter matches (`outcome == fresh_changed`). `MarkStaleForCascade(X)` inserts new pending+stale run row. Other subscribers with non-matching filters are skipped. Other subscribers with matching filters fire (as today).
2. Supervisor tick picks up new pending run; iteration N+1 starts.
3. ... loop continues.
4. Eventually X's iteration M commits `fresh_unchanged`. Walk is NOT called (gated on fresh_changed in applyTerminalComplete). Loop terminates.

Now consider a downstream receiver D that wants to fire ONLY when X has FINISHED looping (not on every intermediate iteration). D declares `subscribes: { node: X, on: state, when: fresh, outcome: fresh_unchanged }`. Today this fires on every iteration (filter ignored). After filter evaluation: it doesn't fire on intermediate iterations (`fresh_changed` doesn't match `fresh_unchanged`), and since `cascadeSubscribersStaleInTx` isn't called for `fresh_unchanged` commits at all, D never fires from this subscription chain — which is also wrong.

So `fresh_unchanged` needs a special path. Two options:

- **Add a cascade walk for `fresh_unchanged` commits**, gated by filter evaluation. Receivers with `outcome: fresh_unchanged` (or no outcome filter) fire; the cycle-self-edge with `outcome: fresh_changed` doesn't fire (filter rejects). Loop terminates as before, downstream fires once at loop-completion.
- **Use `outcome: passed`** for downstream's filter, which the user is more likely to mean for "post-loop." `passed` is the `on_acquire_unavailable: pass` outcome — the empty-queue exit signal. Adding a cascade walk for `passed` outcomes too.

Probably **both**, since `fresh_unchanged` and `passed` are semantically different exit signals (one means "node tried and found nothing to do," the other means "no claim was available"). Each should be a cascade-walk-firing terminal under the new rules.

Mechanically: extend `applyTerminalComplete` to call `cascadeSubscribersStaleInTx` for ALL settled outcomes (`fresh_changed`, `fresh_unchanged`, plus other terminals' equivalents) — the filter gate at the per-edge level handles the gating that the outer `if` used to provide. The `cascadeSubscribersStaleInTx` function should still be called for all settled outcomes; the filter-match function gates per-receiver.

### Validator changes

The validator already enum-validates `When` and the message-filter fields; that prevents typos like `when: faild`. We should add:

- **Validate `Outcome` values** against the LastOutcome enum (`fresh_changed | fresh_unchanged | passed | pure_cascade | failed`). Today this isn't checked at the validator (the field is free-form string).
- **Validate `ErrorClass` values** against the executor's declared error classes if the upstream executor exposes them via observability. Soft-validate only — silent-skip when unreachable, mirroring the existing `on: event` `name:` validation pattern.

Both are net-new defenses against authoring errors that filter-evaluation would otherwise turn into "this receiver never fires."

### Backward compatibility

Pre-v1, no migration shims. CHANGELOG should call out the breaking change explicitly:

> Subscription filters (`when`, `outcome`, `error_class`, `reason`, `name`, `kind`, `sender`, `sender_kind`, `target`) are now load-bearing at cascade-walk time. Templates that declared filters but relied on idempotent re-fire to no-op the non-matching cases will see those receivers NO LONGER FIRE on non-matching transitions. Audit existing templates for filter precision; in particular, `outcome: fresh_changed` filters now correctly skip `fresh_unchanged` and `passed` commits, where previously the receiver would fire and immediately no-op.

A migration note in `concept:node-subscription` mirrors this.

## Open questions

1. **Wait-set rows for non-firing receivers.** Today the wait-set rows insert pessimistically and drain on sender settlement. Under filter evaluation, non-matching edges skip the insert entirely. Question: does any existing flow rely on the wait-set row being present even when the filter doesn't match (e.g., for some bookkeeping or count)? Best-guess no — the wait-set's documented role is dispatch gating. But worth a code-search-for-WaitSet-reads before committing.
2. **Per-topic-kind walk fan-out.** Today one walk per terminal; with filter evaluation, attribute-name-specific walks become a possibility ("fire one walk per changed attribute"). Worth doing? Pro: precise — receivers subscribing to one specific attribute only fire when that attribute changes. Con: more walks per commit. Default position: collapse attribute-name handling into one walk per commit, with the filter check internal; only fan out per-name if profiling says we should.
3. **Filter evaluation in the wait-set-INSERTING side vs. in the dispatch-ELIGIBLE side.** The cleaner shape is "filter at insert" (don't insert non-matching rows). The other shape is "filter at dispatch" (insert all rows, but skip dispatching the receiver if filter doesn't match). The former is more efficient (fewer rows in the table); the latter is more defensive (rows record what would have fired). For pre-v1 simplicity, filter-at-insert. Future tracing-rich versions could revisit.
4. **Should `cascadeSubscribersStaleInTx` be renamed?** Today's name implies "mark subscribers stale." Under filter evaluation it's "mark matching subscribers stale." Probably fine to leave — the implementation comment explains the new behavior.
5. **Substitution-ref auto-subscriptions get filters by accident.** Substitution refs auto-add subscription edges with `Name=<attribute-key>` (per `code:graph/node/subscription_edges.go::edgeFromSubstitutionRef`). Under filter evaluation, those auto-edges correctly fire only on changes to the named attribute. This is a behavior change — today substitution-ref auto-subscriptions fire on every commit. Probably good (closer to author intent — "I depend on this attribute, fire me when it changes") but worth confirming with the spec author.
6. **Operator-driven invalidate.** The admin invalidate API (`route:POST /admin/instances/{id}/nodes/{n}/invalidate`) bypasses subscriptions entirely. No change needed; filter evaluation only affects cascade-fired stales, not operator-fired ones.

## Test plan

- **Unit tests** in `graph/node/subscription_edges_test.go` (new file or existing) for each `MatchesXxxFilter` function. Truth tables for the 2^N field combinations are short enough to enumerate.
- **Scenario tests** in `test/scenarios/subscription_cascade_test.go`:
  - `TestSubscriptionCascade_OutcomeFilterRejectsFreshUnchanged` — sender commits fresh_unchanged; receiver with `outcome: fresh_changed` does NOT fire; receiver with `outcome: fresh_unchanged` (or no filter) DOES fire.
  - `TestSubscriptionCascade_ErrorClassFilter` — sender fails with class A; receiver with `when: failed, error_class: A` fires; receiver with `when: failed, error_class: B` does not.
  - `TestSubscriptionCascade_ParkReasonFilter` — sender parks with reason `time_wait`; receiver with `when: parked, reason: time_wait` fires; receiver with `reason: signal_wait` does not.
  - `TestSubscriptionCascade_SelfLoopWithFilters_Frame_In` — node with `frame: in` self-edge + outcome:fresh_changed; loop iterates until changed=false; downstream subscriber with `outcome: passed` fires once at loop completion (assuming `on_acquire_unavailable: pass` is wired).
  - `TestSubscriptionCascade_AttributeNameFilter` — substitution ref to one attribute; receiver fires only when that attribute changes.
- **Negative tests:** validator rejects `outcome: typo` at registration after the validator's enum check lands.

## What this enables

- **The "drain my own queue + fire downstream once at completion" pattern.** Self-loop with `outcome: fresh_changed`; downstream with `outcome: passed` or `outcome: fresh_unchanged`. Today unbuildable; after this change, three lines of YAML.
- **First-class error-routing.** Receivers can subscribe to specific failure modes: "fire me when ANY upstream fails with `silence_timeout`" (`subscribes: { instance: true, on: state, when: failed, error_class: silence_timeout }`). A monitoring node or a dead-letter-handler node becomes one subscription block.
- **Per-attribute reactivity.** A node subscribing to `nodes.X.attribute.area_path` fires when that specific field changes, not on every X commit. With our docs-pipeline shape, this would let consolidate fire only when area-pass's `change_summary` actually populated — and stay quiet on no-op commits.
- **Sketch:2026-05-19-control-mcp-subscribe-push gets sharper.** External agents subscribing via the MCP `resources/subscribe` surface get filter-precise notifications instead of "wake up on every cascade fire and figure out if it's interesting." Token-efficient observers.
- **Self-loop ergonomics finally match the spec.** `frame: in` self-subscriptions become safely permissible at the validator level once the filter gate prevents the runaway-cascade footgun. The validator's current rejection of `frame: in` self-edges (which I added in the 2026-05-19 cascade walker fix) can be relaxed in the same release.

## Sketch boundaries

This sketch deliberately stops short of:

- Specifying the exact data plumbing for `error_class` and `park_reason` into `cascadeSubscribersStaleInTx`'s signature. Probably one new struct, `CascadeContext`, carrying the transition data, replacing the long parameter list.
- Choosing between "one walk per commit, internal per-attribute filter" vs. "one walk per changed attribute" for the attribute-topic case.
- Designing the admin-side "show me which receivers a given transition would have fired" diagnostic tool. Could be useful for template authoring; out of scope for the first cut.
- Specifying the per-topic-kind data each walk needs to gather. For state walks, `(state, last_outcome, error_class, park_reason)` is enough; for attribute walks, the per-field delta list from the executor's `attributes_delta` is needed and already available; for event walks, the named-event name; for message walks, the envelope. Each walk's signature follows from there.

A proper spec settles each. This sketch's job is to argue that filter-evaluation is the missing primitive that makes the subscription model do what its API surface promised, and that the implementation surface is small enough to land in a focused plan rather than a multi-piece cycle.
