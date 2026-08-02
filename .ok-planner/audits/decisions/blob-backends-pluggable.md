---
audit: blob-backends-pluggable
artifact: decision:blob-backends-pluggable
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:53Z
---

# A pluggable blob backend interface, with inline, Postgres-large-object, filesystem, and memory implementations

Supported. `persistence.BlobBackend` (`lib/foundation/persistence/blob.go`) is a five-method interface, and checked all four named backends as the population: `InlineBackend` (`blob_inline.go`), `MemoryBackend` (`blob_memory.go`), `FilesystemBackend` (`blob_filesystem.go`), and `postgres.PgLargeObjectBackend` (`lib/foundation/persistence/postgres/blob_largeobject.go`) — each carries a `var _ BlobBackend = …` (or `persistence.BlobBackend`) compile-time assertion. A shared conformance suite (`lib/protocols/conformance/blobbackend`) runs against the memory and filesystem backends via `TestBlobRoundtripBackends`; the inline backend has its own direct test (`TestInlineBackend`) matching its no-spill contract, and the Postgres large-object backend has nine of its own tests (`postgres/blob_largeobject_test.go`) against a real database.
