---
issue: subgraph-builtin-kind-node-never-dispatches
kind: audit
category: conflicting
artifacts:
  - story:attribute-carry-forward
  - concept:sub-graph
  - concept:delegation
  - concept:node
status: promoted
opened: 2026-08-16T08:55:56Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# A builtin-kind node inside a delegated sub-graph never dispatches

A template may declare a node by builtin kind (a shorthand the registration path resolves to a bundled executor name) and may delegate work to a sub-graph. Combine the two — a builtin-kind node inside a delegated sub-graph — and the internal node never dispatches: registration validates it against the builtin's schema and accepts the template, but sub-graph child dispatch builds each child's spec from the raw per-graph declaration, whose executor field is empty for a kind-declared node, so the child sits dispatchable forever, the exit node fires without its upstream, and the frame never settles. The same node under an executor name works; the same kind in the main graph works. Nothing in the node, sub-graph or delegation concepts scopes kind-sugar to the main graph. The ruling fixes dispatch to read the resolved executor.

## Options

- Have sub-graph child dispatch read the canonicalized (kind-resolved) form the flattened main-graph path already uses; cost: none beyond the change and a scenario test.
- Reject kind-sugar inside delegated graphs at registration; cost: introduces an inconsistency the corpus never states and registration already contradicts.

The ruling makes kind-sugar work wherever a node is declared.

## Ruling

> Generated ruling (/verify-issues): Resolve the child's executor from the same canonicalized declaration the main-graph dispatch path uses, so a kind-declared internal node dispatches to its bundled executor like any other node. Forced by the node concept's uniform kind-sugar rule and the sub-graph concept's absence of any carve-out; the hang is a dispatch bug, not a design boundary. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
