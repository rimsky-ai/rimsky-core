---
decision: implementation-language-go-plus-ts
status: as-is
---

# Implementation languages

## Choice

A single primary systems language for all core code; a secondary language used only inside the services module's bundled agentic executor reference, chosen to match the upstream SDK ecosystem for that integration.

## Rationale

A single core ecosystem keeps the build, lint, and module-graph discipline uniform across the codebase; the secondary language is confined to one bundled reference where the upstream SDK lives natively, so the cost of a second toolchain is paid in only one isolated place.
