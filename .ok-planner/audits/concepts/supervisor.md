---
audit: supervisor
artifact: concept:supervisor
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:08:13Z
---

# The supervisor role, its callback listener, and its ten invariants

Supported. The supervisor registers a row carrying only concurrency and callback host and port — no service routing — and the three callback routes the concept enumerates all exist: a bodyless keepalive, an attribute writeback, and the async terminal ack. Both per-run routes authenticate on a bearer credential compared in constant time against the acting supervisor's identity joined to the run id, and both return the stated contract of no-content, unauthorized, not-found, and bad-request, with the writeback adding a conflict response for a run that is neither running nor held. The progress timestamp is bumped from all three sources the concept names — scratch writeback, mid-dispatch attribute writeback, and keepalive — each renewing the claim expiry in the same transaction. All ten invariants were checked. Every claim-handle mutator that carries ownership semantics takes the acting supervisor's id as a guard predicate; verify-before-run emits its own lost-race event class, covered by three scenario tests; the producer open verb fires inside the rimsky-side acquisition transaction, which inserts the handle rows and records producer addresses or none. Candidate selection filters on neither executor nor store names and does skip paused instances, while breakpoint evaluation runs at two checkpoints strictly after acquisition. An unset advertise host over a wildcard bind is a startup error naming the two settings that fix it. The superseding-dispatch stamp admits exactly the three dispositions the concept names, constrained at the schema level to that set, and each is written by the path the concept assigns it. An async terminal callback is honoured only from running or held, decided inside the same locked read that drives the state mutation, and a late or duplicate delivery returns an ack status naming the rejection rather than an error.

## Compliance

The Purpose section names a specific storage product as the coordination substrate; a concept body must not name library or product instances, and the platform in fact supports two adapters. Compliant text: "Multiple supervisors run concurrently and coordinate only through the shared persistence layer."
