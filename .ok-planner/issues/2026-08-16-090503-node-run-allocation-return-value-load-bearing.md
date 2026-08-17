---
issue: node-run-allocation-return-value-load-bearing
kind: audit
category: conflicting
artifacts:
  - concept:run-scope
status: verified
opened: 2026-08-16T09:05:03Z
---

# The run-scope concept describes one node-run allocation primitive whose result callers must ignore; the tree has six paths, and callers depend on two of the results

The run-scope concept says one lazy-allocation primitive allocates node-runs, and callers must not depend on its return beyond error or no error. The rule exists to keep a later lazy-to-eager storage rewrite possible. The tree has six insertion statements per backend (queue enqueue, cascade-pending, non-cascade-stale, run-tree root, run-tree child, source-node stale mark). Two of them return the new run id, and every production caller consumes it: the wait-set insert is keyed on the receiver run, and the attribute and dispatch-bag writes target it. The run-tree pair take a caller-supplied id. The invariant describes a shape the code never had. The ruling decides whether the lazy-to-eager property is still wanted.

## Options

- Rewrite the invariant to name the allocation paths as legitimate id-producing writes, and say what still protects the storage-rewrite property, if anything; cost: the project may drop the property without noticing.
- Collapse the paths behind one primitive that rediscovers the run by (node, scope) instead of returning an id; cost: refactors the cascade walker, message delivery and debug override for a speculative future rewrite.
- Give the node-run concept ownership of allocation and narrow run-scope to "refuses allocation into a closed scope"; cost: moves a boundary, which brings its own coherence work.

The ruling decides whether the lazy-to-eager rewrite is a property the corpus keeps.

## Ruling

> Recommended ruling (/verify-issues): Drop the property. Rewrite the invariant to describe the allocation paths as they are, several and id-returning where a caller needs the id, and keep run-scope's real guarantee, that no allocation lands in a closed scope. Move the list of allocation paths to node-run, which owns the row.
>
> Rationale: nothing in the tree or the corpus pursues the lazy-to-eager rewrite the rule guards, and correctness depends on the two id-returning paths. A rule that protects a hypothetical against live wiring has it the wrong way round. Flip case: if the storage rewrite is a real roadmap item, take the second option now, while the callers are few.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
