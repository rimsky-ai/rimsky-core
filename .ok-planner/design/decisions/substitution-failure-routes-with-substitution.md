---
decision: substitution-failure-routes-with-substitution
status: as-is
aliases: []
---

# Attribute substitution failures route in the same transaction where substitution runs

## Choice

When attribute substitution fails (missing-source directive, schema-validation error, or other classifiable substitution failure), the failure is classified and applied to the receiver's `template_resolution_failed` error-class policy in the same transaction where substitution runs. For cascade-driven rows that transaction is the sender's terminal-apply transaction (where the gate evaluator runs); for non-cascade rows it is the row-creation transaction. The classification and policy-application happen inline; the failure is not propagated to a later phase.

## Rationale

Substitution and its failure handling form one logical operation: build the bag, and if it cannot be built, drive the receiver to its declared policy. Splitting them across transactions creates a class of bugs where the failure has nowhere to go.

If a substitution error at gate-eval propagates back to the caller, it bubbles into the sender's terminal-apply transaction and rolls it back. The sender — whose own terminal outcome is correct — fails to commit; the receiver's wait-set never drains; the receiver stays in pending forever; the configured `give_up` (or `retry`, or other policy) is never consulted. The user-visible symptom is a stuck node whose template expected it to surface as `failed`.

Co-locating the failure routing with substitution restores the policy contract: substitution failures drive the receiver through its declared error-class policy regardless of when substitution physically runs. The sender's commit is decoupled — a receiver's substitution failure does not invalidate the sender's terminal outcome, because the receiver's policy fires inside the same transaction as the substitution attempt.

## Alternatives

Re-substitute at dispatch as a "second chance" when the gate-eval substitution failed — rejected. Violates the build-once invariant on the persisted attribute bag (per `concept:node-run`), and reintroduces the dispatch-time substitution cost that gate-eval substitution exists to remove.

Persist the failure as a sentinel value in the bag and let the executor adjudicate — rejected. A substitution failure means the bag is structurally incomplete; handing it to an executor moves a policy-layer decision into an executor-author concern, and not every executor can be reasonably expected to handle every kind of bag corruption.

Defer the failure to the dispatcher's existing dispatch-time failure path — rejected. The dispatcher never sees cascade-driven rows whose substitution failed at gate-eval, because the row never transitions to stale (the gate is exactly what would have transitioned it). The dispatcher has no opportunity to apply the policy.
