---
audit: claude-agent-cli-expose-env-field
artifact: decision:claude-agent-cli-expose-env-field
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Per-node expose-env as a node-config field intersected with the operator allowlist

Supported. The claude-agent node-config schema declares an expose-env property as an array of non-empty strings, the request parser lifts it onto the parsed config, and the dispatch path checks every declared name against the operator allowlist before anything is spawned: the first name outside the allowlist ends the dispatch as an error whose message names the variable, the instance, and the node, and whose payload carries the variable, instance, and node as separate fields. A unit test asserts all four of those and that no spawn happened. The allowed names are then handed to the spawn request, and the runner looks each one up in its own process environment at spawn time and merges the values into the child's explicit environment; the child never inherits the handler's environment wholesale, since the command's environment is always set rather than left to default. Exposure is per-node only: the lookup is driven by the node-declared name list alone, so an operator allowlist with no node declaring anything exposes nothing, and there is no whole-environment or container-wide exposure switch anywhere in the executor. Two further tests cover the name reaching the spawn request and the spawned child seeing only the requested variables.
