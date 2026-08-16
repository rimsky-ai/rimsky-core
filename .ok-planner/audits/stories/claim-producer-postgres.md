---
audit: claim-producer-postgres
artifact: story:claim-producer-postgres
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:55:00Z
---

# Pick policies, atomic staging, verifier checks and subscribable error classes on the bundled postgres producer

Supported. A database, two instances of the bundled postgres producer over it —
one with a pick policy, one staged with its verifier enabled — and a stack
pointed at both. The pick policy handed a distinct seeded row to each of two
claimants, each row's payload reaching its node's dispatch, and the claim
handles record synchronous write semantics. A staged claim on the canonical
schema resolved to a staging address distinct from it, its handle recording
staged semantics rather than a downgrade, and after commit the canonical schema
held the ten staged rows in place of the one it had while the staging schema no
longer existed — swapped in, not copied. A row-count-ratio check run by a
co-holding node over the staged content passed against a baseline of ten, and a
second staged claim writing only two rows settled its checking node on the
producer's per-check class, with a node subscribed to the producer's class
namespace running on that signal. On error classes the run counts rather than
assures: the producer advertises exactly three — claim-unavailable, swap-failed
and not-atomically-replaceable — and two of the three were driven to fire and
drive a subscriber, the drained pick policy giving claim-unavailable and a
canonical schema carrying an external dependent giving
not-atomically-replaceable with the schema and its dependent left untouched. The
per-check verifier class the story names fires and is subscribable but is not
among the three advertised, and swap-failed is advertised but no public-surface
route provoked it in this run.

## Compliance

The body enumerates mechanism: "row-locking claims", "(staging schema swap at commit)" and "a row-count-ratio check over aggregate-only queries" describe how the producer works, which belongs to a decision; compliant text names what the operator declares and observes without the internals.
The benefit clause judges the implementation rather than naming a need — "so that I have a postgres-backed claim-producer that delivers staged-async semantically rather than as a no-op"; compliant text says what the operator gets, e.g. "so that a batch is visible to readers only once it has been written whole and checked".
