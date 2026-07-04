---
decision: handler-package-in-service-directory
status: as-is
aliases: []
---

# Bundled service handler code lives in importable packages inside each service directory

## Choice

Each bundled service directory exposes its handler code as importable Go packages alongside a thin standalone `main`. The standalone binary constructs a handler and serves it over gRPC (unchanged operational shape); the all-in-one entrypoint constructs the same handler and registers it into the in-process dispatch pool. Physical layout varies per service — executors keep their handler code flat in one package with a small `cmd` main; claim producers keep their existing `cmd` main plus server/store/lifecycle subpackage split. The invariant is that a handler package (or coherent set of packages) is importable and reusable across both surfaces; both modes run the same handler code.

## Rationale

The services module already imports only the protocols module, so existing handler code inherits that isolation for free. Forcing a uniform layout across services with different internal shapes adds no value; the dual-mode property needs importability, not uniformity.

## Alternatives

- Mandate a uniform handler subpackage per service — rejected: forces restructuring of claim-producer directories that already have coherent subpackage splits.
- Extract handler code into a separate handlers module — rejected: pays new-module tax for marginally stricter isolation.
- Keep executor handler code in `package main` — rejected: a main package is not importable, which forecloses the in-process consumption path entirely.
