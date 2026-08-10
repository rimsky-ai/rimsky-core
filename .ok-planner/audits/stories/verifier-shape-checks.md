---
audit: verifier-shape-checks
artifact: story:verifier-shape-checks
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Tabular data validated by checks declared in node config

Supported. Against a zero-config all-in-one deployment, whose bundled
shape-checks verifier needs no service wiring, four templates settled it: three
rows satisfying all three declared checks left the node fresh, with the
verifier's own output recording 3 checks run over 3 rows; three rows violating
two of them left the node failed with the terminal error naming the failing
check kind; the same clean rows under one extra declared check were rejected,
so the declaration and not the data alone is what governs; and a check kind the
verifier does not implement failed the node with an attribute error rather than
passing. No custom verifier was written for any leg. The rows and the checks
both reach the verifier as node attributes; feeding the rows from an upstream
node through an attribute-source substitution was attempted and did not
dispatch the verifier, which the story promises nothing about.
