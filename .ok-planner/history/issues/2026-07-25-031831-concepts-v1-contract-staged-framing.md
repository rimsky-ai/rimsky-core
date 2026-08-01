---
issue: concepts-v1-contract-staged-framing
kind: audit
category: unclear
artifacts:
  - concept:replica
  - concept:publisher
  - concept:sensor
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: do the three "v1 contract" mentions of the single-replica stance (in `concept:replica`, `concept:publisher`, `concept:sensor`) improperly stage a permanent posture by version, and should "v1" be dropped or reworded?

Rule that determined the fix: `CURRENT-STATE-ONLY-RULE` bans version-staged framing ("changes at v1" implications) for facts that don't change. No corpus document anywhere defines what "v1 contract" commits to or plans a post-v1 multi-replica capability, and `concept:replica`'s own body is otherwise written as a plain permanent stance ("provides no generic replica-aware coordination") — so nothing supports the narrower "each binary's own release contract" reading the issue flagged as a wrinkle; the rule forces dropping the qualifier, stated present-tense.

What changed: three files, one "v1" occurrence each. `.ok-planner/design/concepts/replica.md` Invariants: "Each binary's v1 contract documents its own replica posture" → "Each binary's own contract documents its replica posture." `.ok-planner/design/concepts/publisher.md` Invariants: "Single-replica is the v1 contract per `concept:replica`" → "Single-replica is the durable posture per `concept:replica`." `.ok-planner/design/concepts/sensor.md` Boundaries' Adjacent list: "(sensor binaries are single-replica per v1 contract)" → "(sensor binaries are single-replica by that concept's posture)."

How verified: `grep -n "v1 contract" .ok-planner/design/concepts/replica.md .ok-planner/design/concepts/publisher.md .ok-planner/design/concepts/sensor.md` returns no matches. Markdown-only change, no build/test impact.
