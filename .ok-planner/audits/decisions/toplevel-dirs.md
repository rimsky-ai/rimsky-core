---
audit: toplevel-dirs
artifact: decision:toplevel-dirs
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:17Z
---

# Four idiomatic top-level code directories: cmd, lib, test, tools

Supported. The repo root's tracked, code-bearing top-level entries are exactly `cmd/` (8 binaries), `lib/` (the module-layout library group), `test/` (out-of-tree scenario/plumbline/smoke/support tests), and `tools/` (7 dev-tooling packages/scripts), plus the separately-acknowledged fifth module `examples/`; no flat root-level Go packages or a `pkg/`-style wrapper exist outside this grouping. `.golangci.yml`'s `depguard` block hangs directory-rooted rules (e.g. `protocols-purity`, `foundation-purity`, `graph-purity`, `runtime-purity`) directly off these four roots, and `concept:module-layout` documents the same four-way split as the grouping's owner, consistent with the rationale that the split gives the import-boundary lint stable directory roots.
