---
decision: handler-package-in-service-directory
---

# Dual-mode bundled services keep handler code in importable packages inside the service directory

## Choice

A bundled service with both a standalone and an in-process surface exposes its handler code as importable Go packages alongside a thin standalone `main`. The executors and the claim producers are those services, and `decision:bundled-registry-entrypoint` fixes that membership. The standalone binary constructs a handler and serves it over gRPC; the all-in-one entrypoint constructs the same handler and registers it into the in-process dispatch pool. Physical layout varies per service; the invariant is that the handler package (or coherent set of packages) is importable and reusable across both surfaces, so both modes run the same handler code. A bundled service with only a standalone surface owes a standalone command and nothing more. The sensors and the lineage subscriber are those services, and each may keep all its code in its command package.

## Rationale

The services module already imports only the protocols module, so existing handler code inherits that isolation for free. Forcing a uniform layout across services with different internal shapes adds no value; the dual-mode property needs importability, not uniformity. A single-surface service has no second consumer to share a handler with, so importability buys it nothing.

## Alternatives

- Extend the importable-handler requirement to every bundled service — rejected: restructures single-surface services for a consumption path they do not have.
- Mandate a uniform handler subpackage per service — rejected: forces restructuring of claim-producer directories that already have coherent subpackage splits.
- Extract handler code into a separate handlers module — rejected: pays new-module tax for marginally stricter isolation.
- Keep executor handler code in `package main` — rejected: a main package is not importable, which forecloses the in-process consumption path entirely.
