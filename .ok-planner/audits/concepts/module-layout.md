---
audit: module-layout
artifact: concept:module-layout
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 11
unaccounted: 0
---

# Whether the four-module, four-layer layout and all eleven of its invariants hold

Supported: all 11 invariants check out against the tree. Seven are lint rules, and each exists in the dependency-lint configuration with the denials the concept describes — the driver allow-list including the two Postgres test-support packages, the foundation-internal denial, the protocols budget with its test-infrastructure denial, the bundled-services isolation whose lint really is the sole guard since that module's manifest requires the core modules for its own test tree, and the three layer-purity rules, the foundation and graph ones carrying no negated globs, which is what "unconditional" means. Reality matches every one: 23 foundation, 6 graph and 14 runtime packages import nothing above them, no shipped service package imports a rimsky-internal layer, and the protocols module's direct requirements are the four libraries named. The single-library pins hold for logging, routing, both database drivers and cron parsing. The three role binaries import only a shared boot helper, never each other. No internal ops package exists, guarded by a walk of the whole tree. The verification gate is workspace-wide — build, lint and test targets each run across all four modules, and the CI matrix shards by module. The licensing boundary is also as described: one mapping file names the permissive island and eight copyleft prefixes covering everything else shipped, and the build-step check resolves a path by longest-prefix match and enforces the one-way import direction.
