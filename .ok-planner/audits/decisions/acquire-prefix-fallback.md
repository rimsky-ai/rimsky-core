---
audit: acquire-prefix-fallback
artifact: decision:acquire-prefix-fallback
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:34Z
---

# Acquisition-failure policy lookup falling back one family level before the unknown-class default

Supported. The policy lookup takes a primary class and one fallback class, tries the node's declared error-types map for the exact producer-declared class, then for the fallback, and returns nothing when neither is declared — at which point the shared policy evaluator resolves a nil policy to give-up with an unknown-error-class reason. That is exactly the three-step order the Choice describes, and it is one family level, not a walk through every prefix segment, which is the alternative the decision rejects. The two acquire-phase handlers that can receive a producer-named class pass the producer's class as primary and the synthetic family class as fallback, so an operator who declared only the generic policy keeps coverage when a producer starts naming its own classes — the rationale's case, exercised end to end by a scenario test that runs a producer declaring its own unavailable class against a template declaring only the generic acquire policy and asserts the retry fires. A sibling test covers the exact-match path. The lookup site carries the decision annotation, which is what "documented at the lookup site" amounts to under this codebase's no-prose-comment rule.
