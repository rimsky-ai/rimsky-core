---
audit: observability
artifact: concept:observability
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:36:14Z
---

# Whether the two optional observability protocols, the startup handshake, and the executor schema surface hold as described

Supported. Enumerating the protocol definitions from the ten shipped wire files, exactly two are observability protocols, one per service kind, and their method sets match the description with nothing left over: the executor one exposes a capabilities query, a single-trace fetch, and a trace stream; the claim-producer one exposes a capabilities query, a claim-detail fetch, a claim-state stream, a claim inventory whose request and response carry a cursor and a next cursor, and a producer-declared admin view fetch. The capabilities query carries the same method name in both, which settles the uniform-naming invariant by reading the two declarations. The handshake fans out over the declared executors and claim producers concurrently and joins, writing one cache entry per peer; an unreachable peer is recorded with an unreachable reachability and its error, logged at information level, and the handshake returns no error at all — startup cannot abort on it — with a refresh loop that later heals such entries. Eight handshake tests cover this, three of them on the unreachable path. The executor-declared attribute schema is read from the capabilities response and validates in both places the concept names: at template registration through the validator's executor-schema hook, and at dispatch after the executor, template-default, and node schemas are merged and after claim-reference substitution has filled the bag, failing with its own named reason.
