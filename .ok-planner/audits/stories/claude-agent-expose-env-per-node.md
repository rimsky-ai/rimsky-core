---
audit: claude-agent-expose-env-per-node
artifact: story:claude-agent-expose-env-per-node
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:58:36Z
---

# Per-node environment declarations meet an operator allowlist, and secrets stay out of persisted state

Supported. Driven through the public surface against a released-image stack
running the bundled agent executor with a stand-in agent binary that reports only
digests of the variables it can read, on one template whose three agent nodes each
declare a different single variable, run twice — once with an operator allowlist
naming two of the three, once with no allowlist. Ten checks, none failing. With
the allowlist set, each permitted node read exactly its own declared variable at
the operator-set value and neither read the other's, while the node declaring the
variable outside the allowlist failed its dispatch with an attribute-invalid
error naming the variable, the instance, the node and the allowlist itself — the
intersection, enforced from both sides. With the allowlist unset the third node
read its variable and the other two still read only their own declarations, so the
per-node declaration is the author's boundary and the allowlist is the operator's.
The secrecy claim was taken as a count: none of the three plaintext values appears
in any of the five persisted surfaces read back — the instance's event log, its
node-run rows, the instance record, the audit log, or per-node attributes.
