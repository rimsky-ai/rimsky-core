---
decision: mechanical-cluster-sweep
status: as-is
---

# Mechanical-cluster comment sweep as a dedicated pass

## Choice

The mechanical comment-hygiene clusters — divider, commented-out-code, todo-marker, and license-fragment-mis-classified — are swept in a dedicated pass distinct from the per-site prose-judgment passes. Per-cluster defaults: divider → delete; commented-out-code → delete; todo-marker → delete; license-fragment → resolve per `decision:comment-hygiene-uniform-rule` (the cluster is shape-misclassified — its sites are prose comments rather than license-text fixtures).

## Rationale

Mechanical work and per-site prose judgment want different validator reviews. Sweeping the mechanical clusters in their own pass leaves every subsequent pass purely about judgment, and the sweep's own validator review collapses to a single shape check.

## Alternatives

Folding the mechanical sites into the per-module prose-judgment passes — rejected because mixing mechanical deletes and per-site judgment in one pass forces the validator to switch review modes mid-pass.
