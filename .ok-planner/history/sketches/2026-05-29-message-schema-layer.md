# Message schema: an instance receives messages; a schema binds them to attribute substitution + invalidation

**Date:** 2026-05-29
**Type:** Pre-spec sketch (structural / concept change). **This is a behavior change**, not a rename.
**Companion to:** `sketch:2026-05-29-reactive-nomenclature-rework` (the rename-only sketch). That sketch
coins the phrasing "an instance receives a message, which substitutes attributes and invalidates nodes
within it" but disclaims any behavior change. This sketch is the behavior/structure that makes that
phrasing literally true.

## Why

Today the message→node relationship is wired at the **node**: a node declares a `message`-kind entry in
its `subscribes:` block (`concept:node-subscription`, the fourth topic kind), filtered on the envelope
fields `kind` / `sender` / `sender_kind` / `target`; `target: self` is the common form. The message
arrives at an instance, but *nodes* are the things that declare reactivity to it, and the
boundary-delivery walk matches the envelope against per-node subscriptions to decide who gets
stale-marked.

That placement is wrong in the same way the pub-sub vocabulary is wrong (the motivation behind the
companion rename sketch). It models messages as **delivered to nodes**, when the engine is
**invalidate-then-pull at the instance**: a boundary-crossing message lands on the *instance*, the
instance marks some nodes stale, those nodes re-run and pull the body via substitution. Nothing is
delivered to a node. Three concrete symptoms of the misplacement:

1. **The "which body" ambiguity.** Because a `coalesce` frame can carry several delivered messages,
   `{{trigger.message.payload.X}}` has no single well-defined value. The approved-pending-review
   `spec:2026-05-29-console-upstream-auth-audit-and-fixes` (item 7) has to *refuse* the directive under
   coalesce-with-multiple and only define it for `serial_queue` (one message per frame). That carve-out
   exists only because messages aren't pinned to one-per-frame.
2. **Message reactivity is tangled into node↔node reactivity.** `concept:node-subscription` is supposed
   to be a node's wait-set on a *sibling's* signal — purely internal, frame-synchronous. The
   `message` topic kind smuggles a boundary-crossing, frame-*creating* concern into the same block, and
   `concept:signal` has to carry a `message/<kind>/<sender_kind>/<target>` type-path just to make
   messages subscribable. Two different kinds of reactivity wearing one surface.
3. **The body is opaque at the boundary.** A message body is inert bytes (`@blessed-invariant 24` /
   `concept:inertness`); there is no declared shape for what an instance will accept. Unknown/garbage
   messages are silently dead-lettered at delivery (`concept:message`) rather than rejected against a
   declared contract.

## The layer being added

Insert a **message schema** between the message and the nodes — a template-level declaration (call the
block `messages:`) that does for inbound messages what the `attributes:` block does for node I/O. The
schema is the single place where "this instance accepts message type T, T's body looks like S, and a T
invalidates nodes [...]" is declared. Nodes stop carrying `message`-kind subscriptions entirely.

```
            today                                  proposed
  ┌─────────┐                          ┌─────────┐
  │ message │                          │ message │
  └────┬────┘                          └────┬────┘
       │ envelope matched per-node            │ instance receives it
       ▼                                       ▼
  ┌──────────────────┐               ┌───────────────────────┐
  │ each node's       │               │ message schema (template) │
  │ subscribes:[msg]  │               │  • legal types + body shape│
  └────┬─────────────┘               │  • per-type invalidation set│
       │                              └────┬──────────────────────┘
       ▼                                    ▼  stale-mark targets; body becomes
  node stale-marked,                       node stale-marked,
  pulls {{trigger.message…}}               pulls {{trigger.message.body…}}
```

The runtime flow becomes, on message receipt at frame boundary:

1. Look up the message's **type** against the instance's template message schema. No matching entry →
   reject (loud), instead of silent dead-letter at delivery.
2. Open a frame carrying exactly that one message (see "One message per frame" below).
3. Stale-mark the schema entry's declared invalidation targets in the new frame.
4. The body is the frame's unambiguous trigger source; the invalidated nodes pull it via substitution
   (`{{trigger.message.body.X}}`) at their dispatch — the existing sanctioned inert leaf.

## What a message schema is (new concept: `message-schema`)

A template-level block enumerating the message types an instance of this template will accept. One
entry per type:

| Field | Purpose |
|---|---|
| type (discriminator) | The envelope value that selects this entry (reuse `kind`, or a new `message_type` field — open detail). One schema declares **many** types. |
| body schema | JSON Schema for the body shape (optional). Gives the body a declared contract instead of pure opacity. **Inertness caveat below.** |
| invalidates | The node(s) this message type marks stale — the routing that today lives in each node's `subscribes:[message]`. |
| body→substitution binding | How the body feeds the invalidated nodes' attributes (the central open question — see below). |

The schema is **declared on the template** (static, content-addressed into the template hash like
`attributes:` and the `publishers:` block) and **applied per instance** at runtime. "An instance can
receive message type T" is the runtime reading of a template-level declaration; the schema itself is
not per-instance state.

This is deliberately parallel to `concept:attribute`: the `attributes:` block is the typed contract for
a node's I/O; the `messages:` block is the typed contract for an instance's inbound boundary. Same shape
(a JSON-Schema-defined surface plus a mapping), different direction.

## What changes for nodes

- **The `message` topic kind retires from `concept:node-subscription`.** `subscribes:` goes back to
  being purely node↔node (terminal signals, attribute-changed, named events). The envelope filter
  fields (`kind` / `sender` / `sender_kind` / `target`) move into the schema's routing, or drop.
- **`concept:signal` loses the `message/<kind>/<sender_kind>/<target>` subscribable type-path** as a
  *cascade* surface. Message arrival can still emit an audit signal (the audit log is unconditional),
  but it is no longer a subscribable edge — nothing subscribes to it; the schema routes it.
- **`target` on the message envelope** (optional node alias) becomes redundant — the schema decides
  targets — and can drop, or survive only as an operator-side override.

The win: the two kinds of reactivity finally live on the surface that matches their scope. Node↔node
coupling (internal, frame-synchronous, pull-on-recompute) stays in `subscribes:`. Instance←message
coupling (boundary-crossing, frame-creating) lives in the message schema. That boundary is exactly the
one `concept:named-event` already draws in prose ("events are internal-to-rimsky and frame-synchronous;
distinct from messages — external, frame-bounded") but the code blurs by putting both in `subscribes:`.

## One message per frame (the load-bearing invariant)

**A frame carries at most one received message.** This is what makes "the message body" an
unambiguous substitution source — the schema maps *a* body onto substitutions, and two bodies in one
frame would have no defined winner.

This generalizes what `spec:2026-05-29-console-upstream-auth-audit-and-fixes` had to special-case. That
spec defines `{{trigger.message.payload.X}}` only when a frame has a single delivered message and
refuses (`ErrMissingSource`) under `coalesce`-with-multiple. Make one-per-frame the **invariant** and
the refusal carve-out evaporates — the directive is always well-defined.

Mechanically this **decouples message delivery from the frame-resolution mode**. Today the per-instance
`col:rimsky_instances.frame_delivery_mode` (`coalesce` default / `serial_queue` opt-in,
`code:lib/runtime/message_delivery.go`) governs *both* how invalidates/cascades merge *and* how pending
messages bundle into frames. Under this sketch:

- **Message delivery is always serial** — N pending messages → N sequential frames, one message each.
  This is today's `serial_queue` message behavior, made universal.
- **The frame-resolution mode (`coalesce` / `serial_queue`) still governs invalidate/cascade merging**
  *within* a frame — that axis is untouched (`concept:frame`, `concept:invalidate`). Coalesce stays a
  meaningful mode for operator-invalidate and cascade coalescing; it just no longer bundles *messages*.

Open: whether `frame_delivery_mode` survives as a column at all once message bundling is fixed at
one-per-frame (it may collapse into the frame-resolution mode, or vanish). See open questions.

## Inertness: route on the type, validate/pull the body at the leaf

The body is inert (`@blessed-invariant 24` / `21`): read only at the sanctioned substitution leaf and
the persistence fetch. The schema must respect this. The inertness-preserving design:

- **Route on the type discriminator only.** Selecting the schema entry reads a *top-level envelope
  field* (the type), never the body bytes. Stale-marking the invalidation set needs nothing from the
  body.
- **The body→attribute "mapping" is a declaration, not a receipt-time read.** It says *which*
  substitution slots the body will feed; the actual walk into body bytes still happens at the node's
  dispatch (the existing sanctioned leaf in `code:lib/graph/attribute/substitution.go`), not at receipt.

The one genuine tension: **does the schema's body JSON Schema *validate* the body at receipt?** If yes,
that's a new sanctioned introspection site — a parallel to the attribute schema's dispatch+commit
validation gates, but it punctures the current "message body is read only at the substitution leaf"
wording of inertness. Two coherent answers:

- **(a) No receipt-time body read.** The body schema is documentation + drives substitution-slot
  typing, but is *checked* at the pulling node's dispatch (where attribute validation already runs).
  Preserves inertness verbatim. The body shape is advisory until something pulls it.
- **(b) Sanctioned receipt-time validation gate.** The schema validates the body when the message
  lands, mirroring attribute validation. Cleaner "reject bad messages loudly," but requires amending
  the inertness invariant to add the receipt gate as a sanctioned site.

Recommend **(a)** — it keeps `@blessed-invariant 24`/`21` intact and matches the pull model. Flag for
the brainstorm.

## The central open question: who owns the body→attribute mapping?

"The schema maps the message body onto the attribute substitutions" admits two readings. This is the
fork the spec has to resolve:

- **Design A — schema routes invalidation; nodes still pull.** The schema owns the legal-types set, the
  body shape, and the **invalidation targets** (the part that replaces `subscribes:[message]`). The
  body→attribute binding stays where it is today: each invalidated node's `attributes:` declares
  `source: "{{trigger.message.body.X}}"`. Smaller change; one place still names attributes (the node);
  the schema's "mapping" is really "which nodes, fed by which body — by reference to the directives the
  nodes already carry."
- **Design B — schema owns the full binding.** The schema entry declares, per type, both the
  invalidation targets *and* explicit `body.foo → node.attr` substitution wiring. Nodes carry no
  message-substitution directives at all; the schema is the one and only message→attribute map. Matches
  the literal phrasing best, but introduces a *second* place that names node attributes (the schema and
  the node's `attributes:` block), and overlaps the substitution grammar.

Recommend **Design A** as the baseline: it makes the *required* change (pull message reactivity out of
`subscribes:` into the schema) without duplicating attribute-naming authority, and "the schema maps the
body onto substitutions" is satisfied by the schema declaring which nodes a type invalidates while those
nodes keep their existing `{{trigger.message.body…}}` pulls. Design B is the fuller realization if the
goal is to make nodes entirely message-agnostic — worth weighing in the brainstorm, but it doubles the
attribute-naming surface.

## Knock-on simplifications (things this *removes*)

- **Backfill collapses into a schema entry.** `concept:backfill` is "a message with a
  `partition_request_override` body, targeting a fan-out node, pulled via substitution." Under the
  schema model that *is* a declared message type: body shape = `{partition_request_override, …}`,
  invalidation target = the fan-out node. The "warn/reject if the target isn't a fan-out node wired for
  the override" validation (being tightened from warn→reject in the console-upstream spec, item 6)
  becomes ordinary schema validation. One less special case.
- **The coalesce-multiple refusal disappears** (see One-message-per-frame). The console-upstream spec's
  item-7 carve-out is superseded.
- **Dead-lettering tightens to rejection.** Today an unmatched message is silently dead-lettered at
  delivery (`concept:message`). With a declared closed set of accepted types, an unknown type is a loud
  reject at receipt — the same "silent miss becomes loud miss" discipline the matcher-overlay counters
  follow (`concept:attribute`).
- **`concept:signal` and `concept:node-subscription` both shrink** — one topic kind / one type-path
  prefix each, gone.

## Open questions / subtleties — the real work

1. **Mapping ownership (Design A vs B)** — the fork above. Load-bearing; decides whether the
   substitution grammar gains schema-side wiring or stays node-side.
2. **Inertness gate (a vs b)** — does the schema validate the body at receipt, or only type-route?
   Recommend no receipt-time read (preserves `@blessed-invariant 24`).
3. **The type discriminator** — reuse the envelope's `kind` (today `invalidate`-only) as the schema
   selector, or add a dedicated `message_type` field? `kind`'s current meaning ("the one graph effect")
   becomes redundant once the *effect* comes from the schema's invalidation set.
4. **Fate of `frame_delivery_mode`** — once message bundling is fixed at one-per-frame, does the
   per-instance column survive, fold into the template frame-resolution mode, or vanish? Relates to
   `tension:serial-queue-per-instance`.
5. **Where the schema is declared** — template-level only (recommended; content-addressed into the
   hash), or does it admit per-instance overrides the way `attribute_overrides` does?
6. **Publisher routing** — `concept:publisher-subscription` carries inline `target_node` + `message_kind`
   routing fields. If routing moves to the schema, do those fields drop, or stay as the publisher-side
   mirror? (`message_kind` defaults to `invalidate` today.)
7. **`target` envelope field** — drop entirely, or keep as an operator-side targeting override that
   narrows the schema's invalidation set?
8. **Naming** — `message-schema` concept slug + `messages:` template block. Tracks the rename sketch's
   `payload → body`; pick `body` to stay consistent with that direction even though it lands separately.

## Blast radius (rough)

- **Template DSL:** new `messages:` block; `subscribes:` loses the `message` topic kind.
- **`concept:signal`:** drop the `message/*` subscribable type-path (keep an audit-only emit if wanted).
- **Persistence:** message-ledger delivery path (`code:lib/runtime/message_delivery.go`) goes
  one-per-frame; `col:rimsky_instances.frame_delivery_mode` reconsidered; the boundary subscription-walk
  (`concept:message` "subscription-walk at frame boundary") replaced by a schema lookup.
- **Substitution:** `{{trigger.message.payload.X}}` → `{{trigger.message.body.X}}` (rename-sketch
  direction); `buildResolveContextForDispatch` populates the single trigger message unconditionally
  (no coalesce carve-out).
- **Concepts in scope:** `message`, `node-subscription`, `signal`, `frame`, `invalidate`, `backfill`,
  `publisher-subscription`, plus a **new** `message-schema` concept. `attribute` adjacent (the
  substitution surface). `named-event` unaffected (internal, already correctly distinct).
- **Tensions touched:** `event-vocabulary-implies-delivery` (the message half), `serial-queue-per-instance`,
  `substitution-introspection-site-count` (if a receipt gate is added).

## Explicit non-goals

- **Not the rename.** `event→response`, `subscribe→watch`, `payload→body` is
  `sketch:2026-05-29-reactive-nomenclature-rework`'s job. This sketch uses `body` for consistency but
  the structural change stands on its own; it can land before or after the rename.
- **No change to node↔node reactivity.** `subscribes:` over terminal signals / attribute-changed /
  named events is untouched except for losing the `message` topic kind. Cascade, `invalidate`, `stale`
  are unchanged.
- **No new agent-facing read.** Same as the event-trigger sketch's principle: the body reaches the
  executor only through the template author's substitution directives, at the sanctioned inert leaf —
  no new privileged introspection into rimsky state.

## Relation to other work

- `sketch:2026-05-29-reactive-nomenclature-rework` — the companion rename. This sketch is its behavioral
  counterpart; together they make "an instance receives a message, substitutes attributes, invalidates
  nodes" true in both name and mechanism.
- `spec:2026-05-29-console-upstream-auth-audit-and-fixes` — its item 7 (complete
  `{{trigger.message.payload}}` for `serial_queue`) and item 6 (backfill target reject) are both
  *generalized* here; the one-message-per-frame invariant supersedes item 7's coalesce carve-out, and
  the schema absorbs item 6's validation. Land that spec first; this sketch builds on it.
- `sketch:2026-05-28-event-trigger-payload-binding` — already dispositioned (its per-emission half
  dropped as premise-false). This sketch is unrelated to that dropped half; it concerns the *message*
  trigger path, which that sketch's item 1 correctly identified as the real fan-out surface.
- Pre-v1 (`rules.md`): take the clean path — retire the `message` topic kind, drop columns, no compat
  shim.
