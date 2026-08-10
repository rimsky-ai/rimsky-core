---
audit: claude-agent-expose-env-per-node
artifact: story:claude-agent-expose-env-per-node
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Per-node expose-env declarations, the operator allowlist, and secret containment

Supported. One template declaring three agent nodes, each naming a different
single environment variable, was run twice against an all-in-one deployment
carrying the bundled claude-agent executor and three distinct secrets in its own
environment: once with an operator allowlist naming two of the three variables,
once with the allowlist unset. Each node's agent read exactly the variable that
node declared, at the operator-set value, and never the variable another node
declared. The node naming the variable outside the allowlist failed its
dispatch, and the refusal named the variable, the instance, the node and the
allowlist variable; with the allowlist unset that same node read the variable,
so the refusal is the operator's act. None of the three plaintext secrets
appears in the instance's event log, node-run rows, instance record, audit log
or per-node attributes — the agent reported only digests, and rimsky recorded
nothing more.
