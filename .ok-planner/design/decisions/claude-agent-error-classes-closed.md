---
decision: claude-agent-error-classes-closed
---

# Claude-agent error classes are a closed set

## Choice

The bundled claude-agent executor's declared error classes are a closed set: the executor advertises the full set through its observability surface and rejects emission of any class outside it at its own boundary, so an undeclared class never leaves the executor. Free-form error strings are not accepted. The member spellings are protocol surface, owned by the executor's declaration and its tests, not enumerated in the corpus.

## Rationale

Error-policy routing (`concept:error-policy`) routes outcomes on declared classes, which is only dependable if the class vocabulary is stable and enumerable. Closedness moves the failure to the executor's emission gate — a loud rejection at the boundary — instead of a silently unroutable outcome downstream. Recording the closedness but not the member list keeps the corpus out of the churn-prone spelling business the declaration already owns.

## Alternatives

- Free-form error strings, routed by pattern — rejected: policies rot as spellings drift, and there is no enumerable set for a template to subscribe against.
- Recording the member list in the corpus alongside the closedness — rejected: a second copy of a churn-prone list that the executor's declaration and tests already own.
