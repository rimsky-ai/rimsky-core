---
audit: force-upstream-refresh-via-receiver-keyed-map
artifact: decision:force-upstream-refresh-via-receiver-keyed-map
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:46Z
---

# A registration-time, receiver-keyed hard-dep edge map drives proactive upstream invalidation at cascade time

Supported. `BuildHardDepEdges` (`lib/graph/node/hard_dep_edges.go`) builds a `map[receiver-type][]sender-type` from every `subscribes:` entry carrying `force_upstream_refresh: true` (`hardDepSendersOf`, which also de-duplicates repeated same-sender entries via a `seen` set and skips self-references), rejects any edge whose sender is a fan-out node type, and runs cycle detection over the whole map once via `detectHardDepCycle`/`findAllCycles`. The map is cached per template hash (`lib/runtime/subscription_loaders.go::hardDepEdgesForTemplate`, backed by a `sync.Map`) and consumed by the cascade walker at receiver invalidation (`lib/runtime/runner_terminal.go::pullForceRefreshUpstreams`), which looks up the receiver's named upstream types and proactively creates/invalidates them before the receiver dispatches. Unit tests (`lib/graph/node/hard_dep_edges_test.go`, 7 cases: no-refresh, simple edge, self-reference-ignored, single cycle, multi-cycle, cycle-path-exclusion, fan-out-rejection) exercise the registration-time construction and rejection rules directly; `lib/runtime/hard_dep_cascade_test.go` and the broader `hard_dep_cascade`/related e2e suite exercise the walker's consumption path.
