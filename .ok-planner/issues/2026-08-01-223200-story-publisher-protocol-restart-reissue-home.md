---
issue: story-publisher-protocol-restart-reissue-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:publisher-protocol
  - concept:publisher
  - concept:publisher-subscription
status: verified
opened: 2026-08-01T22:32:00Z
---

# The restart no-re-issue contract belongs in the startup-resync decision that already names the mechanism

Publishers (external services rimsky subscribes to for event delivery) survive rimsky restarts, and the publisher-protocol story promises in its prose that a restart does not re-issue subscriptions the publisher already holds live. The format rules force the story down to its sentence; the restart contract needs a home first.

Re-verification confirms the behavior (`code:lib/runtime/publishers.go::ResyncPublisherSubscriptions`): at startup rimsky lists each publisher's live subscriptions and only issues the rows missing from that set — an already-active subscription is left untouched. The corpus already has an artifact anchored on exactly this mechanism: the subscription-reconciler decision names "the startup resync pass" as the durable safety net and records the tradeoff space around it (`decision:subscription-reconciler`) — it just never says what the pass does with rows that are already live. The publisher-subscription concept discusses resync only loosely. One artifact owns the mechanism and is silent on this one property of it; the authoring rules' one-home principle points there, and completing a decision's Choice text is a sprint-level act.

## Options

- Complete the subscription-reconciler decision's Choice with the no-reissue-when-already-live behavior — finishes the artifact that already owns the mechanism.
- State it as a publisher-subscription concept invariant — restates a mechanism a decision already anchors, splitting one behavior across two homes.
- Rule it below corpus altitude — the corpus already records comparably fine-grained reconciliation behavior, so dropping this one is inconsistent.

The ruling confirms the rule-forced homing.

## Ruling

> Generated ruling (/verify-issues): complete the startup-resync decision with the missing property — the pass issues only subscriptions absent from the publisher's live set and never re-issues an already-active one — then reduce the story to its sentence. The decision already owns this mechanism by name; the one-home principle forces the contract into it rather than into a second, looser restatement.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
