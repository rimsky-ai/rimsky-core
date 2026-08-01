---
issue: concept-signal-historical-surfaces-phrasing-in-delta
kind: audit
category: other
artifacts:
  - concept:signal
status: repaired
opened: 2026-07-25T03:18:31Z
---

# Does `concept:signal` still carry the changelog-style "unifies the historical parallel surfaces" sentence, and does it need rewording now that the corpus-matching timing constraint has passed?

Yes, and it has been repaired. `concept:signal`'s opening section carried "The signal vocabulary unifies the historical parallel surfaces (run outcome, transition reason, subscription's structured-filter fields) into one type-path-plus-payload contract." — historical framing forbidden by `{{CURRENT-STATE-ONLY-RULE}}`, and factually wrong besides: `concept:transition-reason` is a live, still-current concept, not something signal absorbed. This is a mechanical repair per `{{MECHANICAL-VS-JUDGMENT-RULE}}` — the current-state-only rule fully determines that the historical sentence must go, and no commitment changes by dropping it. Dropped the sentence and added a present-tense boundary note distinguishing signal (owns audit identity — every signal's type-path is the audit-event kind) from transition-reason (a narrower, distinct vocabulary consulted only by the node-state machine's next-state function, never written as an audit-event kind), plus added `concept:transition-reason` to signal's Adjacent list. `concept:transition-reason` itself already stated its side of this boundary correctly and needed no change. Prose-only fix, no build/test surface.
