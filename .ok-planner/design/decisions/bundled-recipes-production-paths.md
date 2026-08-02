---
decision: bundled-recipes-production-paths
---

# Bundled recipes demonstrate through production paths

## Choice

Bundled recipes induce the behavior they demonstrate through production paths: the park-then-resume recipe drives a real park through the production parking machinery, never a synthetic conformance probe or test hook.

## Rationale

A recipe's whole value is evidence — an operator runs it to see the production behavior before wiring a real upstream, and a demo that parks via a test hook demonstrates the hook, not the behavior. Fidelity was chosen over the cheaper, more deterministic probe.

## Alternatives

- Inducing the demonstrated state through a synthetic probe or test hook — cheaper and fully deterministic; rejected: proves nothing about the production path the operator is evaluating.
