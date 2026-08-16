---
audit: node-state-retired-from-operator-api
artifact: decision:node-state-retired-from-operator-api
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:34:05Z
---

# No synthesized node-level state on operator surfaces, with two unambiguous run surfaces instead

Supported. The operator-facing node response carries no state field and serializes no state key, and a reflective test walks the response type field by field and fails on either — so a future field cannot reintroduce it quietly. The node row genuinely owns no lifecycle state: the nodes table has no state column at first creation and no later migration adds one, only drops. The two replacement surfaces both exist and both do what the decision says. The categorical summary hangs off the node response as counts in four run-state buckets, computed per node and available in bulk for the list route. The per-run endpoint answers one run by identifier with its state and an explicit terminal flag, refuses a malformed identifier and reports an unknown one as not found, and gates on its own read action rather than riding on the node read action — covered by tests, including one asserting a node reader is refused and a run reader admitted. The CLI's node surfaces print no synthesized state either.
