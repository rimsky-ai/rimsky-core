---
audit: fanout-list-array-store-agnostic
artifact: decision:fanout-list-array-store-agnostic
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:15Z
---

# The list fan-out grammar is one shared implementation across both bundled claim producers

Supported. `lib/services/claim_producers/shared/listarray` implements the `{"list":[...]}` parse-and-validate logic once, with no store-specific branches. Both bundled producers' `SplitScope` handlers call it directly — `lib/services/claim_producers/filesystem/server/server.go` and `lib/services/claim_producers/postgres/server/server.go` — and both call sites carry the `@decision: fanout-list-array-store-agnostic` annotation at the point the shared package's output is turned into sub-scope descriptors. The one store-specific partition idiom, folder expansion, lives only in the filesystem server behind its own `expand_folder` discriminator, keeping the store-agnostic/store-specific split honest as the decision claims. `TestFSFanOutListArrayE2E` (`lib/services/test/scenarios/claim_producers/fs_fanout_list_e2e_test.go`) proves the shared grammar end to end against the filesystem producer.
