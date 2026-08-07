---
issue: delegation-invariant-stale
kind: human
category: doc-drift
artifacts:
  - concept:delegation
status: repaired
opened: 2026-08-07T08:49:19Z
github: https://github.com/rimsky-ai/rimsky-core/issues/71
---

# concepts/delegation.md invariant is stale: a dangling delegate target is now rejected at registration

Question: does a delegate target naming no declared sub-graph fail at template registration, or only at runtime?

Re-verified at HEAD: `lib/graph/node/template_validator_graphs.go::validateDelegateTargets` is still called unconditionally from `ValidateTemplate` (`lib/graph/node/template_validator.go`) and still rejects a dangling delegate target with `subgraph_unknown_delegate_target: delegate %q does not name a declared sub-graph (with both entry: and exit:) in this template`. A runtime backstop (`lib/runtime/subgraph_dispatch.go`) still errors if a dangling reference somehow reached dispatch, but registration catches it first. The filed Problem still holds exactly as described — `concepts/delegation.md`'s invariant was still describing the pre-validation behavior.

Rule that determined the fix: design docs are current-state only (`.ok-planner/CLAUDE.md`) and code is source of truth for current behavior; this is an intent-preserving correction of a stale factual description to match settled, deliberate current behavior (the commitment — a dangling delegate target is illegal — is unchanged; only its enforcement point moved earlier).

Fix: updated the invariant in `.ok-planner/design/concepts/delegation.md` to state that template validation rejects a dangling delegate target at registration, with the runtime backstop noted as now-redundant defense-in-depth.
