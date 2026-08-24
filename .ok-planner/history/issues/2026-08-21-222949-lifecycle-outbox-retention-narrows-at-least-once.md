---
issue: lifecycle-outbox-retention-narrows-at-least-once
kind: audit
category: conflicting
artifacts:
  - decision:lifecycle-subscriber-at-least-once-delivery
  - concept:lifecycle-subscriber
status: promoted
sprint: 2026-08-23-row-bytes-outbox-and-log-kinds.md
opened: 2026-08-21T22:29:49Z
---

# The lifecycle-outbox retention window narrows at-least-once

An operator who sets `cfg:retention.lifecycle_outbox_trailing` makes rimsky's at-least-once lifecycle promise conditional: the sweep deletes every staged row older than the window, delivered or not, and for the template events and `instance_created` a dropped row is permanent silent loss — nothing re-derives them. The ruled decision (`decision:lifecycle-subscriber-at-least-once-delivery`) rejects dropping a failed delivery by name and carves out no operator exception.

The mechanism: each control-plane transition stages one outbox row per subscriber; the drain deletes a row only on successful delivery or an explicit drop. A permanently unreachable subscriber's rows therefore survive and retry forever — the intended reading of at-least-once — so the table grows with operator actions until the subscriber recovers or an operator truncates it. The sweep ships off by default (the knob is zero unless set), matching the other retention knobs.

The corpus holds a precedent for exactly this trade: `decision:message-queue-mode-per-instance` ships a no-loss default (`backlog`) with an operator-named discard mode (`coalesce`), and its rejected alternative is discard **by default** — not discard on opt-in.

## Options

- **Keep the opt-in window and amend the decision to state the bound.** Cheapest; follows the message-queue precedent. Cost: the at-least-once promise becomes explicitly conditional in the corpus.
- **Remove the knob and surface the dead subscriber instead.** The table stays unbounded; the operator gets a staleness signal (whatever surface the event-log-domain issue settles) and fixes the subscriber rather than truncating history. Cost: the signal must be built, and an abandoned subscriber grows the table forever.
- **Re-derive lifecycle events from source so a sweep drops nothing durable.** The only zero-loss option. Cost: a re-derivation path for the template events and `instance_created` that does not exist (and that an earlier fix round deliberately removed as a full-scan).

The ruling decides whether the at-least-once promise is unconditional or operator-bounded.

## Ruling

> Recommended ruling (/verify-issues): keep the opt-in window and
> amend `decision:lifecycle-subscriber-at-least-once-delivery` to
> state the bound — at-least-once holds unconditionally under the
> shipped default, and a positive retention window is an explicit
> operator decision to discard undeliverable history — and pair the
> knob with the dead-subscriber signal the event-log-domain issue
> lands, so truncation is a choice made with the failure visible.
>
> Rationale: the message-queue-mode precedent already establishes
> opt-in discard with a no-loss default as this corpus's shape for
> bounded-resource trades, and the re-derivation option rebuilds the
> full-scan the reconciler work deliberately retired. Flip case: if
> the owner reads at-least-once as a promise no operator knob may
> narrow, remove the knob and accept unbounded growth against a
> visible staleness signal.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
