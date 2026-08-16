---
audit: claude-agent-env-passthrough-allowlist
artifact: decision:claude-agent-env-passthrough-allowlist
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Operator env allowlist intersected with a per-node list, with rimsky never holding the values

Supported. The handler reads its operator allowlist of variable names from a single namespaced variable in its own process environment, the same name in both the containerized and the in-process deployment, and each dispatch reads a per-node list from node config. The intersection governs: a node-declared name outside the allowlist ends that dispatch with an error naming the variable, the instance, and the node, and no child is spawned; the surviving names are looked up in the handler's own environment and merged into the child's environment on both of the two spawn paths, start and resume, so the resume leg is covered as the decision claims. The child's environment is always constructed explicitly from three layers — the exposed values, the Claude auth material, and the fixed rimsky callback plumbing — and never inherited, which is what makes the unset-allowlist-plus-unset-list default safe; a test spawns a real subprocess and checks it sees only the requested variables. The security invariant holds structurally: only variable names travel through rimsky, in node config and in the dispatch request, and the values are read from the executor process at spawn time, so no plaintext value passes through rimsky's substitution surface or its persisted attribute bag.
