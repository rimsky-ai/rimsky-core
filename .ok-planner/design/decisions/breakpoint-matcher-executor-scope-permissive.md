---
decision: breakpoint-matcher-executor-scope-permissive
---

# Breakpoint matchers accept any declared executor

## Choice

A breakpoint matcher validates an executor key against every executor the deployment declares, not only against the executors the target template dispatches to. Every other cross-check still applies: declared node types, existing graphs, and the closed matcher grammar (see `concept:breakpoint`).

## Rationale

An operator debugging across many templates carries one matcher set from session to session. A matcher pinned to an executor a given template never dispatches to does not fire, which costs that operator nothing. The attribute by-match overlay takes the stricter reading for a different reason: a dead overlay silently changes a dispatch's attributes from what its author expected, while a dead breakpoint is the operator's own working state (see `concept:attribute`).

## Alternatives

- Apply the by-match rule and reject an executor the template does not use — rejected: an operator would edit their matcher set per template, and a breakpoint that never fires harms nothing.
