---
decision: depguard-graph-purity
status: as-is
---

# Graph layer import surface

## Choice

The graph layer's import surface is unconditionally pure: a dependency
lint rule denies the graph layer from importing the runtime, control,
or cmd layers, with no per-site exemptions. The scheduler's periodic
tick loop and the orchestration it drives (sweep triggering and the
pure-cascade settlement pass) live in the runtime layer; the graph
layer's scheduler package exports step functions (frame pass,
pure-cascade readiness, and the like) that the runtime loop calls
downward.

## Rationale

The loop was in the wrong layer, not the work: the usual shape is a
loop in the higher layer calling step functions exported from the
lower one. Relocating the tick dissolved the one remaining per-site
exemption without any interface machinery, so the graph layer is a
clean dependency target under the same unconditional enforcement as
the foundation layer.

## Alternatives

- Keep a file-level exemption whitelisting the scheduler's runtime
  imports — rejected: file-level grain lets any unrelated runtime
  import ride in silently, and the exemption's original sweep-only
  rationale had already been outgrown by per-cascade call sites.
- An interface defined in the graph layer and implemented by the
  runtime layer (dependency inversion) — rejected: machinery whose
  only purpose is preserving a loop in the wrong layer.
