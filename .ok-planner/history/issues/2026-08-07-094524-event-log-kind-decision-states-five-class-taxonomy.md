---
issue: event-log-kind-decision-states-five-class-taxonomy
kind: human
category: decision-drift
artifacts:
  - decision:event-log-kind-enum
  - concept:signal
status: repaired
opened: 2026-08-07T09:45:24Z
github: https://github.com/rimsky-ai/rimsky-core/issues/84
---

# The event-log-kind decision record states a five-class signal taxonomy; there are three

Question: does the signal taxonomy have five top-level classes (as `decisions/event-log-kind-enum.md` said) or three?

Re-verified at HEAD: `lib/foundation/signal/types.go` still defines exactly three top-level `TopLevelKind` constants — `terminal`, `transient`, `attribute` — and `concept:signal` itself is unambiguous: "Three top-level kinds. Type-paths are canonical and validator-enforced." Code and the counterpart concept doc already agree on three; `decisions/event-log-kind-enum.md`'s "five-class taxonomy" was the stale outlier.

Rule that determined the fix: this is a stale sentence in a decision doc contradicted by both the code and its own counterpart concept doc, which already agree — a corpus-side intent-preserving repair, not a commitment change (the decision's actual Choice — typed oneof for a settled operational subset, free-form JSON for signal-class events — did not depend on the class count).

Fix: changed "five-class" to "three-class" in `.ok-planner/design/decisions/event-log-kind-enum.md`.
