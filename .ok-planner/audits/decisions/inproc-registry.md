---
audit: inproc-registry
artifact: decision:inproc-registry
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:20:40Z
---

# The in-process handler registry is built by explicit wiring at supervisor startup

Supported. The registry is a guarded map from in-process executor identity to handler, constructed empty by a plain constructor and populated by an explicit call in the supervisor's startup path, which fails startup if registration fails. Population is by ordinary function call over a literal table of builtin entries: the builtin package imports each of the three handler packages by name, constructs each handler instance, and registers it under its canonical in-process identity, so every utility handler the binary serves is a visible import and a visible table row. The registry is handed to the executor client pool through the pool constructor that takes it, which is the wiring the decision describes. The no-hidden-registration claim holds by enumeration: there is no package-level initialiser anywhere in the executor package tree, so nothing self-registers and ordering cannot matter. The testability rationale is borne out in practice — the client tests construct fresh registries with arbitrary fake handlers, and the registry rejects a duplicate identity with a named error.
