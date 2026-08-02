---
audit: layer-ordering
artifact: decision:layer-ordering
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:50Z
---

# Four ordered layers under the root module, enforced by depguard

Supported. The four layer boundaries the decision names are each backed by a dedicated `.golangci.yml` depguard rule (`foundation-purity`, `graph-purity`, `runtime-purity`, plus the protocols-module rule for the module beneath them), and a direct, repo-wide check of every `.go` file under `lib/foundation`, `lib/graph`, and `lib/runtime` found zero imports of any layer above it (foundation imports nothing from graph/runtime/control; graph imports nothing from runtime/control; runtime imports nothing from control), so the directed DAG holds in the actual import graph, not just in the lint config text.
