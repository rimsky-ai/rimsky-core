---
decision: depguard-graph-purity
---

# Graph layer import surface

## Choice

The graph layer's import surface is unconditionally pure: a dependency
lint rule denies the graph layer from importing the runtime, control,
or cmd layers, with no per-site exemptions. The scheduler's periodic
tick loop and the orchestration it drives live in the runtime layer;
the graph layer's scheduler package exports step functions that the
runtime loop calls downward.

## Rationale

The usual shape is a loop in the higher layer calling step functions
exported from the lower one. With the tick loop in the runtime layer,
the graph layer needs no interface machinery and no per-site
exemption, so it is a clean dependency target under the same
unconditional enforcement as the foundation layer.

## Alternatives

- A file-level exemption whitelisting the scheduler's runtime
  imports — rejected: file-level grain lets any unrelated runtime
  import ride in silently.
- An interface defined in the graph layer and implemented by the
  runtime layer (dependency inversion) — rejected: machinery whose
  only purpose is keeping a loop below the layer it belongs in.
