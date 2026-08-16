---
audit: iterative-workflows-converge
artifact: story:iterative-workflows-converge
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:08Z
---

# Declared graph cycles iterate, stay one unit of work, and stop on a declared condition

Supported: a run through the control API of an all-in-one deployment drove both
cyclic shapes the story names. A node re-running against its own output iterated
three rounds and stopped, and a two-node cycle walking back to its start did the
same with the back-edge node running once per round below the condition. In both
legs the round ceiling available to the author was set far above the rounds
actually run, so what stopped each cycle was the stop condition declared on the
subscription and not a count. The converged output left each cycle for a
downstream node, which ran exactly once, so iteration composes with the rest of
the graph; each whole iteration reads back through observability as a single
completed frame; and the instance came to rest with no live runs. Nine checks,
none failing.
