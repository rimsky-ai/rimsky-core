---
decision: object-store-watching-model
status: as-is
---

# Deposits are watched through one object-store abstraction

## Choice

The deposited-content story is delivered by a single sensor built on an object-store abstraction — named buckets and key prefixes served by pluggable backend listers — with the local filesystem as one backend (first-level directories as buckets, files as objects) alongside real object stores.

## Rationale

Everything that makes watching trustworthy — subscriptions, polling, watermarks, durable seen-state, idempotent publishing, the settle window — is identical regardless of where content physically lives; only listing differs. Folding the filesystem in as a backend keeps one idiom for the job, and the filesystem maps onto the object model losslessly. A dedicated filesystem sensor would duplicate roughly ninety percent of the machinery to change only the listing call.

## Alternatives

- A dedicated filesystem sensor with native path and glob semantics — considered and rejected: a second idiom for the same job, duplicating the watch machinery.
- Event-driven detection (filesystem notification, bucket notification) — per-backend mechanisms that fracture the uniform model, buying latency the use case does not need.

## Proof

The sensor's tests drive multiple backends — in-memory and local filesystem — through the identical watch-and-publish path, and a backend has no other path to publish through, so per-backend machinery divergence has nowhere to exist.
