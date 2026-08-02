---
audit: fanout-list-array
artifact: story:fanout-list-array
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:15Z
---

# Fan-out over an upstream-produced list requires no custom claim-producer

Supported. The list partition grammar (`lib/services/claim_producers/shared/listarray`) parses a `{"list":[{key,payload}...]}` partition request into disjoint per-key sub-scopes with no producer-specific code, and is wired into both bundled claim producers (filesystem and Postgres `SplitScope` handlers). `TestFSFanOutListArrayE2E` (`lib/services/test/scenarios/claim_producers/fs_fanout_list_e2e_test.go`) boots the bundled filesystem claim-producer image plus a full rimsky stack via testcontainers, declares a fan-out node whose claim producer is the bundled filesystem store with a three-item list `partition_request`, and asserts exactly one sub-claim/partition run-scope per declared item with the parent node reaching a terminal-succeeded state — a real end-to-end proof against a bundled store, not just the shared-package unit tests.
