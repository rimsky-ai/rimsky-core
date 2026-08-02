---
decision: object-store-watching-model
---

# Deposits are watched through one object-store abstraction

## Choice

The deposited-content story is delivered by a single sensor built on an
object-store abstraction — named buckets and key prefixes served by pluggable
backend listers. The abstraction admits real object stores as backends by
design; the current build ships the local filesystem as its only registered
backend (first-level directories as buckets, files as objects), alongside an
in-memory backend that is a test fixture, not a shipped store.

## Rationale

Everything that makes watching trustworthy — subscriptions, polling,
watermarks, durable seen-state, idempotent publishing, the settle window — is
identical regardless of where content physically lives; only listing differs.
Folding the filesystem in as a backend keeps one idiom for the job, and the
filesystem maps onto the object model losslessly. A dedicated filesystem
sensor would duplicate roughly ninety percent of the machinery to change only
the listing call. Keeping the generalization while shipping only the
filesystem backend is deliberate: the backend seam is a single listing
operation, so a real object-store backend is a drop-in lister, and retiring
the abstraction would buy nothing while closing that door.

## Alternatives

- A dedicated filesystem sensor with native path and glob semantics —
  considered and rejected: a second idiom for the same job, duplicating the
  watch machinery.
- Event-driven detection (filesystem notification, bucket notification) —
  per-backend mechanisms that fracture the uniform model, buying latency the
  use case does not need.
- Renaming the sensor to a dedicated filesystem watcher and retiring the
  object-store abstraction until a cloud backend ships — considered and
  rejected: the abstraction's cost is one small interface, and the rename
  would churn the shipped surface twice.
