---
audit: persistence-database
artifact: concept:persistence-database
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:08:13Z
---

# The database umbrella interface, its two adapters, and its five invariants

Supported. The top-level database interface exposes exactly the seven members the concept names — queue, per-row-type accessor umbrella, advisory locker, migration runner, ping, blob-backend setter, and close — and the umbrella hands back 25 singular per-row accessor sub-interfaces through plural bag methods, matching the stated naming split; the row structs are singular against pluralised ledgers. Two adapters implement it, selected by a string-valued driver field validated at config load, and both feed one shared migration runner that applies files in filename order and skips those already recorded, which is the mechanism the append-only filename discipline exists for. One database is opened per process: the three per-role binaries each open their own, and the single-process launcher constructs one and hands the same instance to all three roles. Of the five invariants: the embedded adapter opens with immediate-mode transactions and no startup gate, and a topology-conditional warning naming the shared-local-file precondition is raised at config load and does not block boot; the memory blob backend is rejected outside the unified topology, with unit coverage over split, unified, and zero-value topologies; the raw-driver isolation rule is enforced by a lint deny-list whose exemptions are the adapter and its pool, the binary entrypoints, the test and scenario harnesses, and the bundled services; and all 25 persisted ledgers enumerated from the two migration sets are executor-agnostic — none is named for or shaped by an executor implementation.
