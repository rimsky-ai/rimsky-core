---
audit: discovery-cache
artifact: concept:discovery-cache
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:06:31Z
---

# The in-memory capabilities cache: two fill paths, two kind-partitioned indexes, and its three invariants

Supported. All three invariants hold and the described shape matches the code. The cache is a single guarded structure holding exactly two maps, one for executors and one for claim producers, each keyed by service name; an entry carries declared tags, declared error classes, the expected-attributes schema, the observability-protocol availability flags, a reachability status of reachable or unreachable, and a static marker. Two paths fill it. The startup handshake probes every configured out-of-process peer concurrently and records each result; a probe failure records the unreachable status with the error text and returns normally, so no unreachable peer can abort startup — the handshake function has no failure return at all. The bundled registration path writes entries directly without probing, advertising an in-process executor handler's schema, tags and error classes, and an in-process claim producer's declared error classes and nothing else, marking both static; the refresh loop skips every static entry, so an in-process handler is never probed and never flips to unreachable. Reads are plain map lookups under a read lock with no freshness check, and the refresh loop re-probes on its own ticker, defaulting to a minute — eventual consistency by construction. The structure holds no persistence handle and is rebuilt by a fresh handshake at each start. The registration-time consult path the concept claims is wired: template registration reaches the cache through the capability accessors for the tag cross-check, the error-class vocabulary check and the schema gate, and dispatch resolves the expected-attributes schema through a resolver built over the same cache.
