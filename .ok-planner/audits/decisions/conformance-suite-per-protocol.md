---
audit: conformance-suite-per-protocol
artifact: decision:conformance-suite-per-protocol
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095827-lifecycle-subscriber-conformance-subcommand-missing
---

# Conformance discipline

Unsupported. Checked against the canonical population of six rimsky-implementable protocols named in the module-layout concept document: five get both a conformance package and a matching dedicated command-line subcommand. The sixth, lifecycle-subscriber, has a real, independently capable conformance suite, but it is reachable only as a flag on an unrelated protocol's subcommand rather than its own dedicated subcommand, contradicting the decision's explicit claim that each protocol's suite is exposed as its own per-protocol subcommand.
