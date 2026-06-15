# Make substitution-derived cascade behavior explicit

## Why this sketch

GitHub issue #18 surfaced a real surprise in the cascade model. A substitution ref like `{{nodes.X.attribute.Y}}` in a node's attribute schema auto-creates an ungated `attribute/Y/changed` subscription from X to the receiver — the documented "no orphan reads" rule (`concept:node-subscription`). A separate per-field flag `hard_dep: true` on the same attribute schema entry produces a second edge map (receiver-keyed, in `lib/graph/node/hard_dep_edges.go`) that proactively invalidates X when the receiver is invalidated.

Two distinct cascade behaviors live in two distinct grammar surfaces, with different defaults and different levels of visibility:

- "Wake on sender change" is **implicit**, derived from the substitution ref. The author can't see it without reading the rule. It carries no `when:` predicate, which surprises authors who write a precisely-gated explicit subscription only to discover that ungated implicit edges from other reads also fire them.
- "Pull the sender into this frame" is **explicit**, declared via `hard_dep: true` on the attribute schema field.

The issue's concrete scenario: a `resolve` node explicitly subscribes to `check-adapter-config`'s `attribute/status/changed` with `when: payload.value == 'needs_work'`, and reads prompt context from `check-gis-endpoints.attribute.endpoints` and `check-ingestion-strategy.attribute.strategy`. In steady-state cascades the two context reads fire the resolve regardless of the `status` gate, because their implicit edges are ungated. The author's workaround — fetch context inside the agent at runtime instead of via substitution — abandons the declarative read.

The deeper issue: the three primitives `concept:node-subscription` promises to decouple — read access (substitution grammar), cascade coupling (subscriptions), eligibility gating (`when:` predicate + wait-set) — are re-coupled in practice by the implicit-edge mechanism. Reads silently widen the cascade, and the `when:` predicate the author wrote does not actually govern when the receiver fires.

## Proposal

Consolidate cascade behavior into the `subscribes:` block, with two orthogonal booleans per subscription entry:

- **`wake_on_change`** — when the sender emits a matching signal, dispatch the receiver. Today's implicit-edge behavior, spelled explicitly.
- **`force_upstream_refresh`** — when the receiver is invalidated, also invalidate the sender so it re-runs and its value lands in the receiver's substitution context this frame. Today's `hard_dep: true` behavior, moved from the attribute schema field to the subscription entry.

Drop the implicit auto-subscribe. At registration, every substitution ref of the form `{{nodes.X.attribute.Y}}` (and `{{nodes.X.event.Y}}`, see open questions) must be covered by at least one `subscribes:` entry whose `node:` and `type:` match. Reject the template if not, with a registration error that names the uncovered ref and what subscription entry would cover it.

Remove `hard_dep: true` from the attribute schema entirely — the same intent is now expressed by `force_upstream_refresh: true` on the matching subscription entry.

## The four cells

|  | `force_upstream_refresh: false` | `force_upstream_refresh: true` |
|---|---|---|
| **`wake_on_change: true`** | TODAY (implicit edge): fire on sender's change, read whatever value is current at dispatch | NEW: fire on sender's change AND drag the sender into the frame whenever I'm dispatched by any other path |
| **`wake_on_change: false`** | NEW (the issue #18 fix): context-gathering read — drain into my substitution context if the sender is already in this frame, else the read is absent (substitution-side fallback applies) | TODAY (`hard_dep: true`): pull the sender so it's fresh when I dispatch; the wake-up comes from a different subscription |

All four cells are coherent:

- `(true, false)` and `(false, true)` cover today's two existing behaviors.
- `(false, false)` is the issue #18 fix — context-gathering reads that don't widen the cascade.
- `(true, true)` is a generalization of `hard_dep` for receivers that want the sender current under either dispatch path (whatever wakes me, the sender is brought current first; and if only the sender changes, that's also enough to wake me).

The two axes are honestly orthogonal — `(true, true)` is a real combination — which is why a flat two-bool form fits the design better than a single `mode:` enum with three or four values.

## Why this works

**The "no orphan reads" invariant is preserved**, not by side-effect edge generation, but by registration-time validation: a substitution ref with no covering subscription is rejected with a clear message. The guarantee is the same — you cannot read a value that no edge keeps fresh — but enforced statically instead of through silent edge insertion.

**Cascade edges become exactly what the author wrote.** The `subscribes:` block is the single declarative surface for "what wakes this node and what gets pulled into its frame." Reading a template tells you the full cascade behavior of every node.

**The three primitives are actually decoupled.** Read access lives in the substitution grammar; cascade coupling lives in `wake_on_change`; eligibility gating lives in the `when:` predicate (which now genuinely controls when the receiver fires, regardless of what it reads).

**`hard_dep`'s registration-time discipline carries over for free.** The existing hard-dep cycle detection and the "no fan-out target" rule (both in `lib/graph/node/hard_dep_edges.go`) move to apply against `force_upstream_refresh: true` subscription entries without semantic change.

## Migration

Pre-v1, this is a loud-failure registration break:

- Existing templates that rely on implicit auto-subscribe fail registration after upgrade. The author adds explicit `subscribes:` entries covering each read.
- Existing templates that use `hard_dep: true` on attribute schema fields fail registration after upgrade — the flag is unknown at the field site. The author moves the intent to a `force_upstream_refresh: true` subscription entry.

The bundled examples + cookbook entries get the same treatment as part of the change. No backwards-compat shim — the rule under `.claude/rules/rules.md` ("break freely") authorizes the clean cut.

The registration error for an uncovered substitution ref should be specific enough that an author can fix it without consulting docs: it names the ref, the implied `(node, type)` pair, and a copy-pasteable subscription entry to add.

## Open questions

- **Event refs.** Today's implicit rule also covers `{{nodes.X.event.Y}}` (creates `type: event/Y`). The same model applies — events get covered by `subscribes:` entries with the same two flags. `force_upstream_refresh: true` on an event subscription is meaningful (pull the sender so it emits its event this frame), but rarer in practice; needs a thought-experiment pass.

- **Whole-pull `{{nodes.X.attribute}}` (no field).** Today's implicit produces `type: attribute/*`. Under the new model, the covering subscription entry uses `type: attribute/*` — no new mechanic, just a coverage-check rule that knows whole-pull reads require a wildcard subscription (an `attribute/Y/changed` subscription does not satisfy a whole-pull read).

- **Defaults.** Two reasonable starting points for the two flags when omitted:
  - Require both to be explicit, no defaults. Maximally explicit; verbose for the common case.
  - Default `wake_on_change: true, force_upstream_refresh: false`, matching today's implicit behavior. Friendlier; the explicit case for non-default behaviors is the typical author intent.

  Probably the second. Defaulting both to `false` would be a footgun (a subscription that fires nothing and refreshes nothing).

- **Interaction with cross-cutting subscriptions (`instance: true`).** Cross-cutting is sender-agnostic; `force_upstream_refresh: true` presumes a specific upstream to refresh. A cross-cutting subscription with `force_upstream_refresh: true` has no defined target and should be rejected at registration.

- **Interaction with `when:`.** A `when: false` (or a falsy predicate) on a `wake_on_change: true` subscription has the same observable behavior as `wake_on_change: false`. Both forms remain legal — they're not redundant, because the predicate form evaluates dynamically per signal while the flag form installs no wake-up edge at all. Pick the one that fits the author's intent: a permanent "don't wake me" reads as the flag; a conditional gate reads as the predicate.

- **Migration ergonomics for the `hard_dep: true` removal.** The field-level flag and the subscription-level flag live in different parts of the template, so the registration error needs to teach the move ("`hard_dep: true` on this attribute field is removed; add `force_upstream_refresh: true` to the subscription entry from `<node>` matching `attribute/<field>/changed`"). Worth budgeting registration-error UX as part of the work.

- **Sketch-internal naming check.** `wake_on_change` and `force_upstream_refresh` are the working names. They survived the discussion in the issue thread; if a clearer pair surfaces during plan-writing, swap them then. The shape of the design doesn't depend on the names.

## Out of scope for this sketch

- Snapshot reads against last-committed values across frames. The substitution-context builder today is strictly per-frame, drained-only (`lib/runtime/substitution_context.go`). The `(false, false)` cell preserves that contract — if the sender isn't in this frame, the read is absent. Loosening to cross-frame snapshot reads is a separate design question with its own freshness contract and isn't required for the issue #18 fix.

- Any change to publisher-subscription (`concept:publisher-subscription`). This sketch is purely about node-subscription / receiver-side cascade behavior.

- Changes to the wait-set ledger shape. The two new flags don't require new wait-set columns; they only change which edges populate it.
