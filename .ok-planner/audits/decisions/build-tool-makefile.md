---
audit: build-tool-makefile
artifact: decision:build-tool-makefile
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether a root Makefile is the single source of truth for build orchestration

Supported. A Makefile sits at the repo root and declares targets for each of the five areas the decision names: compilation across the four workspace modules, per-module and aggregate test targets, lint (which chains the licensing check), the three image-set targets, and the release chain plus its scan, push, and dev-release steps — 40 declared targets in total, read from the file. No competing orchestrator exists in the tree: there is no Taskfile, justfile, or magefile, and the only package manifest outside the modules belongs to the vendored lint tooling. The CI workflow drives the same targets rather than raw toolchain commands, so contributors and CI run identical recipes. Shell helper scripts exist under the tools directory but are invoked from Makefile recipes, not as an independent task surface. A fitness test pins ten of the targets by name and pins the release chain's dependency order.
