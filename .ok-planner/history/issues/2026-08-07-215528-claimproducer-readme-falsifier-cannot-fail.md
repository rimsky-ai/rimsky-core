---
issue: claimproducer-readme-falsifier-cannot-fail
kind: human
category: doc-drift
artifacts:
  - concept:claim-producer
  - story:claim-producer-protocol
  - decision:doc-accuracy-gates
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:28Z
github: https://github.com/rimsky-ai/rimsky-core/issues/105
---

# The claim-producer example presents a falsifier that cannot fail

The claim-producer example's README makes a guarantee readers care about — that
rimsky only emits a completion event after the producer has acknowledged the
commit — and names a specific check as the falsifier that would catch a
violation.

That check cannot fail. The event is written in the same transaction that
enqueues the commit for delivery, before any call reaches the producer; the
actual RPC goes out later, from the outbox dispatcher. So the named falsifier is
green whether or not the commit ever arrives. A reader trusting the guarantee
because a test proves it is trusting a test that proves nothing about it.

Seven more claims in the same README were re-verified and are false. Together
they make the file unreliable rather than merely imprecise:

- It says the test builds its image on demand from a Dockerfile. Images are
  pre-built and resolved by content-addressed tag — the same README's own
  prerequisites section says so, contradicting itself. The Dockerfile's own
  header comment carries the same stale claim.
- It doesn't mention that a plain package-wide test run boots a database
  container and a full rimsky stack; there's no build tag or skip guard.
- It names the termination function and its subject wrong: the released claims
  are the committed ones, not the held ones.
- It cites a test-only unexported helper under a public-looking name.
- It says the supervisor calls the producer's release directly at termination. It
  enqueues an outbox row; the dispatcher makes the call.
- It says commit bumps an in-memory counter. The handler is a bare no-op — the
  counting happens in the test's wrapper.
- It says a malformed address gets a specific database error on both backends.
  Only on Postgres, whose column is JSON-typed; SQLite's is text and accepts it
  silently.

## Ruling

> Retired: the examples module is being removed in full, so the README this issue
> reports on ceases to exist and the drift it documents is dissolved rather than
> corrected. The documentation project maintains the cookbook that replaces it.
>
> Findings underneath these issues that concern rimsky rather than the examples
> were pulled out as their own issues before retirement — an unenforced publisher
> kind, an unproven ordering guarantee on terminal events, and a lifecycle
> callback that cannot refuse — so nothing about the platform is lost with the
> module.
