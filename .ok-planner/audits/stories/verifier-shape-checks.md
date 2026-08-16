---
audit: verifier-shape-checks
artifact: story:verifier-shape-checks
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:04:03Z
---

# Declared shape checks govern a claim's tabular data, with no verifier written

Supported. Driven through the public surface against a released-image stack whose
bundled shape-checks verifier is reachable by name with no service wiring of any
kind. Four legs, seven checks, none failing. Rows satisfying all three declared
checks settled the node fresh, with the verifier reporting how many checks it ran
and how many rows it read; rows violating two of them blocked instead, with the
terminal naming the failing check kind. The declaration is what governs, not the
data alone: the same clean rows re-submitted under a stricter declaration were
rejected under the added check. A check kind the verifier does not implement
failed the node with an attribute error rather than passing silently.
