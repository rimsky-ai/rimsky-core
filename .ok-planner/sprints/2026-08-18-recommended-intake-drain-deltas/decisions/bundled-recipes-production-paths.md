---
decision: bundled-recipes-production-paths
---

# Recipes demonstrate through production paths

## Choice

A recipe the project ships induces the behavior it demonstrates through production paths, never a synthetic conformance probe or a test hook. Recipes ship in the release documentation. The release run generates them from the tree; no author keeps them by hand.

## Rationale

A recipe's whole value is evidence — a reader runs it to see the production behavior before wiring a real upstream, and a demo that reaches the demonstrated state through a test hook demonstrates the hook, not the behavior. Fidelity was chosen over the cheaper, more deterministic probe. Generating each recipe at its release keeps it matched to the tree it describes, so a recipe cannot promise a shape the release no longer has.

## Alternatives

- Inducing the demonstrated state through a synthetic probe or test hook — cheaper and fully deterministic; rejected: proves nothing about the production path the reader is evaluating.
- Keeping recipes by hand in the tree as a maintained examples module — rejected: a hand-kept copy drifts from the release it demonstrates, and duplicates what the release documentation already produces.
