# Message schema layer: one frame-creation mechanism, declared types, cascade-driven emits, retire `frame: next`

**Date:** 2026-06-13
**Type:** Pre-spec sketch (structural / concept change). **This is a behavior change**, not a rename.
**Supersedes:** the earlier `2026-05-29-message-schema-layer` sketch (which scoped only external boundary-crossing messages); the May 29 content is preserved and extended here.
**Companion to:** `sketch:2026-05-29-reactive-nomenclature-rework` (the rename-only sketch). That sketch coins the phrasing *"an instance receives a message, which substitutes attributes and invalidates nodes within it"* but disclaims any behavior change. This sketch is the behavior/structure that makes that phrasing literally true.
**Companion to:** `sketch:2026-06-13-explicit-substitution-cascade-behavior`. That sketch makes in-frame cascade coupling explicit on `subscribes:` (drops the implicit auto-subscribe from substitution reads, makes `hard_dep` a flag on the subscription). This sketch makes cross-frame coupling go through messages. Together they collapse `subscribes:` to a single clean job: declare in-frame node↔node edges. No `frame:` field; no implicit edges; no message topic kind.
**Closes by superseding:** GitHub issues #18 (companion sketch) and #19 (this sketch; see "How this closes #19" below).

## Why

The `subscribes:` block on a node carries **two structurally different kinds of reactivity** under one surface, and both of the non-fitting ones should retire from it:

### Tangle 1: External-message reactivity in `subscribes:`

Today the message→node relationship is wired at the node: a node declares a `message`-kind entry in its `subscribes:` block (`concept:node-subscription`, the fourth topic kind), filtered on the envelope fields `kind` / `sender` / `sender_kind` / `target`; `target: self` is the common form. The message arrives at an instance, but *nodes* are the things that declare reactivity to it, and the boundary-delivery walk matches the envelope against per-node subscriptions to decide who gets stale-marked.

That placement is wrong in the same way the pub-sub vocabulary is wrong (the motivation behind the companion rename sketch). It models messages as **delivered to nodes**, when the engine is **invalidate-then-pull at the instance**: a boundary-crossing message lands on the *instance*, the instance marks some nodes stale, those nodes re-run and pull the body via substitution. Nothing is delivered to a node.

Three concrete symptoms:

1. **The "which body" ambiguity.** A `coalesce` frame can carry several delivered messages, so `{{trigger.message.payload.X}}` has no single well-defined value. The approved-pending-review `spec:2026-05-29-console-upstream-auth-audit-and-fixes` (item 7) has to *refuse* the directive under coalesce-with-multiple and only define it for `serial_queue` (one message per frame). That carve-out exists only because messages aren't pinned to one-per-frame.
2. **Message reactivity is tangled into node↔node reactivity.** `concept:node-subscription` is supposed to be a node's wait-set on a *sibling's* signal — purely internal, frame-synchronous. The `message` topic kind smuggles a boundary-crossing, frame-*creating* concern into the same block, and `concept:signal` has to carry a `message/<kind>/<sender_kind>/<target>` type-path just to make messages subscribable.
3. **The body is opaque at the boundary.** A message body is inert bytes (`@blessed-invariant 24` / `concept:inertness`); there is no declared shape for what an instance will accept. Unknown/garbage messages are silently dead-lettered at delivery (`concept:message`) rather than rejected against a declared contract.

### Tangle 2: Internal cross-frame triggering in `subscribes:` (the `frame: next` modifier)

`concept:frame` states the rule: *"A frame begins only when a node is invalidated — a direct operator/user invalidation, or message delivery."* But that rule is silently violated *inside* the cascade walker by the `frame: next` subscription modifier, which opens new frames from within an in-frame cascade event:

```go
// lib/runtime/runner_terminal.go:732
case node.FrameNext:
    if _, fErr := frame.EnqueueOrCoalesce(ctx, args.Persist, tx, instanceID, r.ID); fErr != nil { ... }
```

So today there are *two* frame-creation paths: messages (external, declared, auditable) and `frame: next` (internal, implicit, buried in cascade-walker behavior). Two structural problems follow:

1. **The cross-frame substitution wall.** A receiver woken in a new frame via `frame: next` cannot read its sender's attributes via substitution — the substitution-context builder is strictly per-frame (`code:lib/runtime/substitution_context.go`: *"There is NO scope-walk and NO cross-frame caching"*). If the receiver tries, it gets `template_resolution_failed`. So `frame: next` only works for receivers whose executors re-read external state, not consume the sender's data. That constraint is undocumented; nothing catches the mismatch at registration.
2. **Silent failure on multi-node back-edges (GitHub issue #19).** A per-node subscription defaults to `frame: in`. The in-frame cascade walker has an explicit settled-this-frame guard at `code:lib/runtime/runner_terminal.go:869` that suppresses the dispatch when the receiver already ran in the current frame. Self-edges bypass the guard at line 868 (a special case for the self-subscription "drain my own queue" idiom); multi-node back-edges (an N-cycle for N ≥ 2) don't. So a node A subscribing to a downstream B's `terminal/success` with the default `frame: in` is silently dropped on the back-edge: `message_received { type: recalculate }` appears in the audit log, but no second run starts. The author hitting this had to read runtime source to discover that `frame: next` would have unblocked them — and even then, only because their re-fired node didn't need to read the downstream's data (the cross-frame substitution wall would have killed it if it had).

Both tangles share the same shape: **cross-frame coupling is wearing the in-frame surface**, with different syntactic dressings (the `message` topic kind for external, the `frame: next` modifier for internal). Both should retire from `subscribes:`.

## The layer

Insert a **message schema** between messages and nodes — a template-level declaration (call the block `messages:`) that does for inbound messages what the `attributes:` block does for node I/O. Add a **cascade-emit declaration** on senders (call the block `emits:`) that mirrors the `publishers:` block for outbound cascade-driven emissions. Together they replace both tangle surfaces:

```
                today                                           proposed
  ┌─────────┐    ┌──────────────┐                ┌─────────┐    ┌──────────────────┐
  │ message │    │ terminal/    │                │ message │    │ sender's emits: │
  │ (extern)│    │ success      │                │ (extern)│    │ block (intern)  │
  └────┬────┘    │ + frame:next │                └────┬────┘    └────┬─────────────┘
       │         └──────┬───────┘                     │              │
       │ envelope        │ cascade walker             │              │ schema lookup
       │ matched         │ implicitly opens frame       │              │ by type
       │ per-node        │                              ▼              ▼
       ▼                 ▼                       ┌───────────────────────────┐
  ┌──────────────┐ ┌────────────────┐            │ instance message ledger    │
  │ each node's  │ │ EnqueueOrCoalesce│            │ (one message per frame)   │
  │ subscribes:  │ │ in cascade walker│            └────────────┬──────────────┘
  │ [message]    │ └──────┬─────────┘                            ▼
  └────┬─────────┘        │                            ┌───────────────────────┐
       │                  ▼                            │ messages: schema      │
       ▼            new frame opens                    │  • body shape         │
  node stale-marked, recv re-fires                     │  • invalidation set   │
  pulls {{trigger.message.payload…}}                   └────────────┬──────────┘
                                                                    ▼
                                                       targets stale-marked
                                                       in new frame; targets
                                                       pull {{trigger.message
                                                             .body…}}
```

The runtime flow on a message landing (external **or** cascade-emitted) at frame boundary:

1. Look up the message's **type** against the instance's template `messages:` schema. No matching entry → reject (loud), instead of silent dead-letter at delivery.
2. Open a frame carrying exactly that one message (see "One message per frame" below).
3. Stale-mark the schema entry's declared invalidation targets in the new frame.
4. The body is the frame's unambiguous trigger source; the invalidated nodes pull it via substitution (`{{trigger.message.body.X}}`) at their dispatch — the existing sanctioned inert leaf.

## What a `messages:` schema entry is (new concept: `message-schema`)

A template-level block enumerating the message types an instance of this template will accept. One entry per type:

| Field | Purpose |
|---|---|
| `type` (discriminator) | The envelope value that selects this entry (reuse `kind`, or a new `message_type` field — open detail). One schema declares **many** types. |
| `body_schema` | JSON Schema for the body shape (optional). Gives the body a declared contract instead of pure opacity. **Inertness caveat below.** |
| `invalidates` | The node(s) this message type marks stale — the routing that today lives in each node's `subscribes:[message]`. |
| `body → substitution binding` | How the body feeds the invalidated nodes' attributes (the central open question — see below). |

The schema is **declared on the template** (static, content-addressed into the template hash like `attributes:` and the `publishers:` block) and **applied per instance** at runtime. "An instance can receive message type T" is the runtime reading of a template-level declaration; the schema itself is not per-instance state.

This is deliberately parallel to `concept:attribute`: the `attributes:` block is the typed contract for a node's I/O; the `messages:` block is the typed contract for an instance's inbound boundary. Same shape (a JSON-Schema-defined surface plus a mapping), different direction.

## What an `emits:` block is (extension on the sender)

A template-level declaration on a node type enumerating the cascade-driven messages that node emits. One entry per emission rule:

| Field | Purpose |
|---|---|
| `on:` | The triggering signal — `terminal/success`, `terminal/error/<class>`, `attribute/<key>/changed`, `event/<name>`. Reuses the existing signal taxonomy. |
| `when:` | Optional CEL predicate over the triggering signal payload. Gates emission. Required for self-emit loops to converge (see Termination below). |
| `type:` | The message type discriminator. Must match a `messages:` entry in the (same-template) receiver schema; registration validates. |
| `body:` | The message body. Built from substitution directives over the sender's own context — typically the sender's run state, output attributes, and the triggering signal payload. Uses the existing `{{...}}` grammar. |
| `target:` | Optional in-instance routing override. Default: the message goes to the same instance, where the `messages:` schema's invalidation set decides which nodes to stale-mark. (See open questions for fate.) |

Example: self-subscription drain-my-own-queue.

```yaml
- type: process-inbox
  emits:
    - on: terminal/success
      when: payload.changed   # converges when queue empties
      type: process-inbox/iterate
      body:
        next_offset: "{{nodes.process-inbox.attribute.next_offset}}"
  messages:
    - type: process-inbox/iterate
      body_schema: { type: object, properties: { next_offset: { type: integer } } }
      invalidates: [process-inbox]
```

Example: cycle back-edge (the #19 case).

```yaml
- type: pong
  emits:
    - on: terminal/success
      type: ping/recheck
      body:
        pong_status: "{{nodes.pong.attribute.status}}"

- type: ping
  messages:
    - type: ping/recheck
      body_schema: { type: object, properties: { pong_status: { type: string } } }
      invalidates: [ping]
  # ping reads `{{trigger.message.body.pong_status}}` in its attribute schema —
  # solving what was the cross-frame substitution wall.
```

The `emits:` block is parallel to `publishers:` in shape (template-level, content-addressed into the template hash) but for the **in-instance internal-emission** case rather than the cross-instance external-publisher case. The principle of locality: the sender owns what it emits.

## What changes for nodes (combined)

- **The `message` topic kind retires from `concept:node-subscription`.** `subscribes:` goes back to being purely node↔node (terminal signals, attribute-changed, named events). The envelope filter fields (`kind` / `sender` / `sender_kind` / `target`) move into the schema's routing, or drop.
- **The `frame:` modifier retires from `subscribes:`.** Subscriptions become unambiguously in-frame. Every cross-frame intent is now expressed via `emits:` + `messages:`.
- **`concept:signal` loses the `message/<kind>/<sender_kind>/<target>` subscribable type-path** as a *cascade* surface. Message arrival can still emit an audit signal (the audit log is unconditional), but it is no longer a subscribable edge — nothing subscribes to it; the schema routes it.
- **`target` on the message envelope** (optional node alias) becomes redundant — the schema decides targets — and can drop, or survive only as an operator-side override.

The combined win: every kind of reactivity finally lives on the surface that matches its scope.

- **In-frame node↔node coupling** (frame-synchronous, pull-on-recompute): `subscribes:`. With the companion `sketch:2026-06-13-explicit-substitution-cascade-behavior` reforming this block's flags (drop implicit auto-subscribe, add `wake_on_change` + `force_upstream_refresh`), this block has one clean job.
- **External instance←message coupling** (boundary-crossing, frame-creating): `messages:` (schema) + `publishers:` (binding).
- **Internal cascade-emit coupling** (frame-creating from within): `emits:` (sender) + `messages:` (receiver).

That layering is exactly the one `concept:named-event` already draws in prose (*"events are internal-to-rimsky and frame-synchronous; distinct from messages — external, frame-bounded"*) but the code blurs by putting both in `subscribes:` and by hiding cross-frame triggers under `frame: next`.

## One message per frame (the load-bearing invariant)

**A frame carries at most one received message.** This is what makes "the message body" an unambiguous substitution source — the schema maps *a* body onto substitutions, and two bodies in one frame would have no defined winner.

This generalizes what `spec:2026-05-29-console-upstream-auth-audit-and-fixes` had to special-case. That spec defines `{{trigger.message.payload.X}}` only when a frame has a single delivered message and refuses (`ErrMissingSource`) under `coalesce`-with-multiple. Make one-per-frame the **invariant** and the refusal carve-out evaporates — the directive is always well-defined.

Mechanically this **decouples message delivery from the frame-resolution mode**. Today the per-instance `col:rimsky_instances.frame_delivery_mode` (`coalesce` default / `serial_queue` opt-in, `code:lib/runtime/message_delivery.go`) governs *both* how invalidates/cascades merge *and* how pending messages bundle into frames. Under this sketch:

- **Message delivery is always serial** — N pending messages → N sequential frames, one message each. This is today's `serial_queue` message behavior, made universal. Both external messages and cascade-emitted messages share this rule.
- **The frame-resolution mode (`coalesce` / `serial_queue`) still governs invalidate/cascade merging** *within* a frame — that axis is untouched (`concept:frame`, `concept:invalidate`). Coalesce stays a meaningful mode for operator-invalidate and cascade coalescing; it just no longer bundles *messages*.

Open: whether `frame_delivery_mode` survives as a column at all once message bundling is fixed at one-per-frame (it may collapse into the frame-resolution mode, or vanish). See open questions.

## Inertness: route on the type, validate/pull the body at the leaf

The body is inert (`@blessed-invariant 24` / `21`): read only at the sanctioned substitution leaf and the persistence fetch. The schema must respect this. The inertness-preserving design:

- **Route on the type discriminator only.** Selecting the schema entry reads a *top-level envelope field* (the type), never the body bytes. Stale-marking the invalidation set needs nothing from the body.
- **The body→attribute "mapping" is a declaration, not a receipt-time read.** It says *which* substitution slots the body will feed; the actual walk into body bytes still happens at the receiver node's dispatch (the existing sanctioned leaf in `code:lib/graph/attribute/substitution.go`), not at receipt.

The one genuine tension: **does the schema's `body_schema` *validate* the body at receipt?** If yes, that's a new sanctioned introspection site — a parallel to the attribute schema's dispatch+commit validation gates, but it punctures the current "message body is read only at the substitution leaf" wording of inertness. Two coherent answers:

- **(a) No receipt-time body read.** The body schema is documentation + drives substitution-slot typing, but is *checked* at the pulling node's dispatch (where attribute validation already runs). Preserves inertness verbatim. The body shape is advisory until something pulls it.
- **(b) Sanctioned receipt-time validation gate.** The schema validates the body when the message lands, mirroring attribute validation. Cleaner "reject bad messages loudly," but requires amending the inertness invariant to add the receipt gate as a sanctioned site.

Recommend **(a)** — it keeps `@blessed-invariant 24`/`21` intact and matches the pull model. Flag for the brainstorm.

## The central open question: who owns the body→attribute mapping?

"The schema maps the message body onto the attribute substitutions" admits two readings. This is the fork the spec has to resolve:

- **Design A — schema routes invalidation; nodes still pull.** The schema owns the legal-types set, the body shape, and the **invalidation targets** (the part that replaces `subscribes:[message]`). The body→attribute binding stays where it is today: each invalidated node's `attributes:` declares `source: "{{trigger.message.body.X}}"`. Smaller change; one place still names attributes (the node); the schema's "mapping" is really "which nodes, fed by which body — by reference to the directives the nodes already carry."
- **Design B — schema owns the full binding.** The schema entry declares, per type, both the invalidation targets *and* explicit `body.foo → node.attr` substitution wiring. Nodes carry no message-substitution directives at all; the schema is the one and only message→attribute map. Matches the literal phrasing best, but introduces a *second* place that names node attributes (the schema and the node's `attributes:` block), and overlaps the substitution grammar.

Recommend **Design A** as the baseline: it makes the *required* change (pull message reactivity out of `subscribes:` into the schema) without duplicating attribute-naming authority, and "the schema maps the body onto substitutions" is satisfied by the schema declaring which nodes a type invalidates while those nodes keep their existing `{{trigger.message.body…}}` pulls. Design B is the fuller realization if the goal is to make nodes entirely message-agnostic — worth weighing in the brainstorm, but it doubles the attribute-naming surface.

## Use case mapping — every `frame: next` use becomes `emits:` + `messages:`

| Today's `frame: next` use | Becomes |
|---|---|
| Self-subscription drain-my-own-queue | Sender's `emits:` on `terminal/success` with `when: payload.changed`; self-receiver's `messages:` invalidates self |
| Cross-cutting (`instance: true`) — default `frame: next` | Per-sender `emits:` (or a template-level wildcard emit; see open questions); receiver `messages:` with broad invalidation set |
| Back-edges in cycles (#19 case) | Downstream sender's `emits:` on `terminal/success`; upstream receiver's `messages:` invalidates self; body carries downstream data |
| External-event handlers (publisher messages) | Unchanged in `emits:` direction (no internal emit needed); use the `messages:` schema layer for the receiver |
| Reset/reconfig | Sender's `emits:` on the config-changed signal; reset target's `messages:` invalidates the reset surface |

The cross-cutting case is the one that needs design care, because today's `instance: true` matches any sender of any node-type. Under the new model that would require either a per-sender-node-type `emits:` declaration (verbose but explicit) or a template-level wildcard emit ("for any node's terminal/success matching X, emit message Y"). The verbose form is cleaner but breaks the existing `instance: true` ergonomics; the wildcard form preserves the ergonomics but adds a new construct. See open questions.

## Addressing model

Cascade-emitted messages are **in-instance**. The sender is a node-run in instance I; the receiver is instance I. There is no cross-instance routing problem — unlike external publisher-subscription messages, which carry topic + capability discovery, internal emits know their receiver instance by construction.

The type discriminator alone is the routing key inside the instance: the cascade-emitted message lands in the ledger, the `messages:` schema lookup matches on type, the schema's invalidation set is stale-marked in the new frame.

External messages keep their existing addressing — operator-API invalidates target the instance directly; publisher-subscription messages route through capability + binding (`concept:publisher-subscription`). Both paths converge at the same ledger and the same schema lookup.

## Termination

Self-emit and back-edge cycles loop infinitely without a convergence gate, exactly the way today's `frame: next` self-subscription does without `when:`. The `emits:` block's `when:` predicate is the gate. Two concrete patterns:

- **Self-emit drain:** `when: payload.changed` on the emit. When the sender settles `terminal/success` with `changed: false` (queue empty / nothing to do), the emit is suppressed and the cycle terminates.
- **Back-edge re-check:** the cycle terminates when the receiver's re-evaluation produces a value that the cycle's *other* subscription gate (e.g. the original forward-edge `attribute/status/changed when: payload.value == 'needs_work'`) no longer matches.

These are the same termination shapes that work today. The difference is that the gate is on the *emit* side, not on the *receive* side, which matches the principle "the sender owns what it emits."

Per `attribute/<key>/changed`'s current behavior — the cascade walker emits the signal for every merged attribute on every settle, not gated by an actual delta — the `when:` predicate is load-bearing here. The one-message-per-frame invariant doesn't help convergence: it deduplicates within-frame, not across the loop. Convergence is the template author's responsibility, expressed through `when:` on the emit.

## How this closes #19

GitHub issue #19 reports that in `serial_queue` mode, a 2-cycle node A → B → A drops the back-edge: B's `terminal/success` does not re-dispatch A even though A's subscription matches. The author traced the cause to runtime source: the cascade walker's settled-this-frame guard at `code:lib/runtime/runner_terminal.go:869` suppresses the dispatch because A already ran in the current frame, and the guard bypasses self-edges but not multi-node back-edges. The documented affordance — `frame: next` on A's subscription — is buried in `concept:frame`'s self-subscription discussion and isn't named as the load-bearing fix for the general N-cycle case.

This sketch resolves the issue not by patching the guard but by retiring `frame: next` entirely and replacing it with the cascade-emit primitive:

- The author writes pong's terminal/success as an `emits:` declaration: `{ on: terminal/success, type: ping/recheck, body: { ... } }`.
- ping's `messages:` block accepts the type and declares itself as the invalidation target.
- The message lands in the ledger, opens a new frame, ping is stale-marked as the new frame's source, ping dispatches and reads the back-edge body via `trigger.message.body.<field>`.
- No silent failure mode: a misaddressed `emits:` (no matching `messages:` entry) is a registration error. An unset `when:` on a self-emit loop converges via the same change-detection patterns that work today, just declared on the sender side.
- The cross-frame substitution wall disappears for back-edges: the receiver doesn't need to read the sender's attributes across frames because the message carries the data.

## What this removes (combined)

- **`subscribes:[message]` entries** retire — every `message`-kind subscription becomes a `messages:` schema entry.
- **The `frame:` modifier on `subscribes:`** retires — every cross-frame intent becomes an `emits:` + `messages:` pair.
- **The `case node.FrameNext` branch in the cascade walker** (`code:lib/runtime/runner_terminal.go:732-766`) goes away. The `EnqueueOrCoalesce` call from within the cascade walker retires. Frames are opened only by message delivery (external or cascade-emitted).
- **The `message/<kind>/<sender_kind>/<target>` subscribable type-path** in `concept:signal` retires.
- **The self-subscription special case** at `code:lib/runtime/runner_terminal.go:868` retires. Self-edges no longer need to bypass the settled-this-frame guard, because self-re-fire goes through `emits:` not `subscribes:`.
- **The in-frame settled guard's silent-failure footgun** retires. With cross-frame triggering routed through `emits:` + messages, the settled-this-frame guard at `code:lib/runtime/runner_terminal.go:869` no longer has to discriminate "self-edge / back-edge / late-upstream." Every cross-frame intent is explicit on the sender. The guard becomes a pure correctness measure for its named scenario (late-settling upstreams).
- **The two-spelling drain-my-own-queue ambiguity in `concept:frame`** retires. Today the doc has to explain why `frame: in` and `frame: next` are both valid for self-subscription. Under the new model there's no ambiguity: the in-frame self-edge is its own iteration inside one long frame; the per-iteration-frame form is a self-emit.
- **The coalesce-multiple refusal** (the console-upstream spec's item-7 carve-out) is superseded — `{{trigger.message.body.X}}` is always well-defined under the one-message-per-frame invariant.
- **Dead-lettering tightens to rejection.** Today an unmatched message is silently dead-lettered at delivery (`concept:message`). With a declared closed set of accepted types, an unknown type is a loud reject at receipt — the same "silent miss becomes loud miss" discipline the matcher-overlay counters follow (`concept:attribute`).
- **`concept:backfill` collapses into a schema entry.** Backfill is "a message with a `partition_request_override` body, targeting a fan-out node, pulled via substitution." Under the schema model that *is* a declared message type: body shape = `{partition_request_override, …}`, invalidation target = the fan-out node. The "warn/reject if the target isn't a fan-out node wired for the override" validation (being tightened from warn→reject in the console-upstream spec, item 6) becomes ordinary schema validation. One less special case.
- **`concept:signal` and `concept:node-subscription` both shrink** — one topic kind / one type-path prefix each, plus the `frame:` modifier, all gone.

## Open questions / subtleties — the real work

1. **Mapping ownership (Design A vs B)** — the fork above. Load-bearing; decides whether the substitution grammar gains schema-side wiring or stays node-side.
2. **Inertness gate (a vs b)** — does the schema validate the body at receipt, or only type-route? Recommend no receipt-time read (preserves `@blessed-invariant 24`).
3. **The type discriminator** — reuse the envelope's `kind` (today `invalidate`-only) as the schema selector, or add a dedicated `message_type` field? `kind`'s current meaning ("the one graph effect") becomes redundant once the *effect* comes from the schema's invalidation set.
4. **Fate of `frame_delivery_mode`** — once message bundling is fixed at one-per-frame, does the per-instance column survive, fold into the template frame-resolution mode, or vanish? Relates to `tension:serial-queue-per-instance`.
5. **Where the schema is declared** — template-level only (recommended; content-addressed into the hash), or does it admit per-instance overrides the way `attribute_overrides` does?
6. **Publisher routing** — `concept:publisher-subscription` carries inline `target_node` + `message_kind` routing fields. If routing moves to the schema, do those fields drop, or stay as the publisher-side mirror? (`message_kind` defaults to `invalidate` today.)
7. **`target` envelope field** — drop entirely, or keep as an operator-side targeting override that narrows the schema's invalidation set?
8. **Naming** — `message-schema` concept slug + `messages:` template block + `emits:` template block. Tracks the rename sketch's `payload → body`; pick `body` to stay consistent with that direction even though it lands separately.
9. **Cross-cutting (`instance: true`) ergonomics.** Two options for cascade emits: (a) per-node-type `emits:` declarations everywhere — explicit but verbose; touches every node when adding a new cross-cutting receiver. (b) Template-level wildcard emit construct ("for any node's terminal/success matching <filter>, emit message Y") — preserves the existing `instance: true` ergonomics but adds a new construct. The verbose form is structurally cleaner. Worth a brainstorm.
10. **`emits:` granularity and placement.** Mirroring `publishers:` suggests a top-level template block (one block listing all emissions across node-types). Mirroring `attributes:` (per-node) suggests a per-node-type block. The publishers parallel is closer in shape (declaration of outbound) so a per-node-type `emits:` block (one per node-type that emits) is the natural fit.
11. **Body-build substitution context.** The emit's `body:` is built at settle-time on the sender. What context does the substitution have? Plausibly: the sender's own attributes, the triggering signal's payload (as `trigger.signal.<field>`?), and per-instance `params`. NOT downstream attributes (that would re-open a cross-frame leak). Probably needs its own narrow substitution-context shape, distinct from the dispatch-time context.
12. **`target:` field on `emits:` — drop, keep, or extend.** With in-instance addressing + schema-driven invalidation, the message has a well-defined receiver set without a `target:` field. Keep as an operator-style narrowing override? Drop entirely?
13. **Termination guarantees in the runtime.** Today's `frame: next` self-subscription has the `TestSubscriptionCascade_FrameNextLoopConverges` scenario test confirming convergence. Equivalent guarantees should hold for self-emit loops — registration probably can't statically prove convergence (CEL `when:` is data-dependent), but a per-instance emit-loop budget (max consecutive self-emit frames before parking the instance with a diagnostic) is worth considering.
14. **`emits:` validation at registration.** Each emit's `type:` must match a `messages:` entry in the receiver schema. The body's substitution refs must resolve against the sender's available context. The receiver's invalidation set in `messages:` must be a valid node-type list. Per the "loud failure" stance, mismatches are registration errors.
15. **Interaction with `force_upstream_refresh` (companion sketch).** The companion sketch's `force_upstream_refresh` is purely an in-frame mechanism — it invalidates the sender so the sender's value lands in the receiver's substitution context this frame. It has no interaction with `emits:` (which is cross-frame). Worth confirming in the brainstorm that the two concerns stay orthogonal.
16. **Audit and observability.** Today's `frame: next` creates frames with no obvious "what caused this" audit row. Under the new model, every frame has a message-ledger row naming its trigger. Operators looking at "why did this frame open" get an immediate answer. Worth scoping the audit-row shape during plan-writing.

## Blast radius (rough)

- **Template DSL:** new `messages:` block (template-level); new `emits:` block (per-node-type); `subscribes:` loses the `message` topic kind and the `frame:` modifier.
- **`concept:node-subscription`:** drop the `message` topic kind and the `frame:` modifier; subscriptions become unambiguously in-frame node↔node.
- **`concept:frame`:** the *"frame begins only on a boundary-crossing invalidate or message delivery"* rule becomes literally true (no asterisk for `frame: next`). The "self-subscription is first-class in both shapes" subsection retires. The implicit "back-edges and cycles" gap that bit issue #19 stops existing.
- **`concept:cascade`:** the in-frame settled-this-frame guard becomes pure correctness (no longer the silent-failure mechanism it is today). The "cascade fires iff edge matches + when: true" invariant becomes literally true for the in-frame case (no implicit suppression to footnote).
- **`concept:signal`:** drop the `message/*` subscribable type-path (keep an audit-only emit if wanted).
- **Persistence:** message-ledger delivery path (`code:lib/runtime/message_delivery.go`) goes one-per-frame; `col:rimsky_instances.frame_delivery_mode` reconsidered; the boundary subscription-walk (`concept:message` "subscription-walk at frame boundary") replaced by a schema lookup. The cascade walker's `EnqueueOrCoalesce` call retires.
- **Substitution:** `{{trigger.message.payload.X}}` → `{{trigger.message.body.X}}` (rename-sketch direction); `buildResolveContextForDispatch` populates the single trigger message unconditionally (no coalesce carve-out). New substitution-source kind for emit-body construction at sender's settle (open: see body-build context above).
- **Concepts in scope:** `message`, `node-subscription`, `signal`, `frame`, `invalidate`, `backfill`, `publisher-subscription`, `cascade`, plus a **new** `message-schema` concept and a **new** `cascade-emit` concept (or an extension of `message`). `attribute` adjacent (the substitution surface). `named-event` unaffected (internal, already correctly distinct).
- **Tensions touched:** `event-vocabulary-implies-delivery` (the message half), `serial-queue-per-instance`, `substitution-introspection-site-count` (if a receipt gate is added).

## Explicit non-goals

- **Not the rename.** `event→response`, `subscribe→watch`, `payload→body` is `sketch:2026-05-29-reactive-nomenclature-rework`'s job. This sketch uses `body` for consistency but the structural change stands on its own; it can land before or after the rename.
- **No change to in-frame node↔node reactivity beyond removing the `frame:` modifier.** The `subscribes:` flags work belongs to `sketch:2026-06-13-explicit-substitution-cascade-behavior`; this sketch's only impact on `subscribes:` is to remove what doesn't belong there (the `message` topic kind, the `frame:` modifier). The two sketches compose cleanly and can land in either order, though landing this sketch's `messages:` block first gives the companion sketch a tidier in-frame-only `subscribes:` to reason about.
- **No new agent-facing read.** Same as the event-trigger sketch's principle: the body reaches the executor only through the template author's substitution directives, at the sanctioned inert leaf — no new privileged introspection into rimsky state.
- **No external-message addressing changes.** External boundary-crossing messages (operator-API invalidates, publisher-origin) keep their existing routing surfaces (`publishers:` bindings, operator-API endpoints). This sketch adds the `messages:` schema as the receiver-side contract and adds the `emits:` block as the internal-emission source; the existing external paths converge into the schema lookup.

## Relation to other work

- `sketch:2026-05-29-reactive-nomenclature-rework` — the companion rename. This sketch is its behavioral counterpart; together they make *"an instance receives a message, substitutes attributes, invalidates nodes"* true in both name and mechanism.
- `sketch:2026-06-13-explicit-substitution-cascade-behavior` — companion. Together they collapse `subscribes:` to a single clean job: declare in-frame edges with `wake_on_change` + `force_upstream_refresh`. No `frame:` field; no implicit edges; no message topic kind.
- `spec:2026-05-29-console-upstream-auth-audit-and-fixes` — its item 7 (complete `{{trigger.message.payload}}` for `serial_queue`) and item 6 (backfill target reject) are both *generalized* here; the one-message-per-frame invariant supersedes item 7's coalesce carve-out, and the schema absorbs item 6's validation. Land that spec first; this sketch builds on it.
- `sketch:2026-05-28-event-trigger-payload-binding` — already dispositioned (its per-emission half dropped as premise-false). This sketch is unrelated to that dropped half; it concerns the *message* trigger path, which that sketch's item 1 correctly identified as the real fan-out surface.
- GitHub issue #18 — surfaced the implicit-auto-subscribe footgun; addressed by the companion sketch.
- GitHub issue #19 — surfaced the `frame: next` silent-failure footgun on multi-node back-edges; closed by this sketch's retirement of `frame: next` and replacement with the `emits:` + `messages:` pair.
- Pre-v1 (`.claude/rules/rules.md`): take the clean path — retire the `message` topic kind, drop the `frame:` modifier, drop columns, retire the cascade walker's frame-creation path, no compat shim. Existing templates relying on either retired surface fail loud at registration on upgrade and get rewritten to the new shape.
