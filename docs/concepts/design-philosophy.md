# Design philosophy

Rimsky is a domain-agnostic orchestration platform. The codebase ships
no domain vocabulary, no project terminology, no consumer-specific
defaults. Every domain-shaped piece of a deployment — the templates,
the userdata, the MCP catalogs, the audit logs — lives in the
consumer's repository, not in rimsky's.

This page sets the lens through which the rest of the documentation is
written. It is the answer to "why do you draw the line where you do?"

## The core split: graphs are control flow, executors are domain logic

Rimsky owns:

- **Cascade.** When a node's outputs change, the platform decides
  which dependents get re-run. Whether `changed` is meaningful, what
  the node produced, and what the dependents do with the value are
  all out of rimsky's scope.
- **Claims.** The atomic acquisition transaction, the conflict
  predicate, the producer-side write semantics envelope, the
  deterministic-sorted-order multi-lock acquisition, the held-claim
  auto-terminal — every piece of contention safety. Whether a claim
  is "this row in items table" or "this concrete path on disk" is
  out of scope; the platform compares scope bytes, not their meaning.
- **Frames.** The unit of cascade resolution. Operators see frames
  start, hold, and complete; the platform never inspects what the
  frame is about.
- **Error policy.** `retry / invalidate / give_up`, the consecutive-
  retries-without-progress cap, the four lifecycle handler slots,
  `on_event` handlers. Any domain-shaped retry semantics live in the
  policy declarations; rimsky executes them.
- **Persistence.** Every state mutation goes through the persistence
  driver; Postgres and SQLite are the shipped reference impls. No
  in-memory state crosses a process boundary except through that
  driver. Producers may also persist, but their state is opaque to
  rimsky.

The executor owns:

- **Vocabulary.** The userdata schema declares the shape; the
  attributes schema declares the output. Anything more specific
  than "JSON object" is out-of-scope for the platform.
- **Domain logic.** The agent prompt, the API call, the
  transformation, the validation. Rimsky moves bytes in and bytes
  out.
- **External integrations.** MCP servers, webhooks, third-party
  APIs. The executor — and the project-built tools wired into its
  catalog — handle every integration concern.

The producer (the "store" colloquially) owns:

- **Domain-specific scope canonicalization.** Whether "row 42" in the
  items table conflicts with "row 42 of last hour's snapshot" is the
  producer's policy. Rimsky compares scope bytes and asks no
  questions.
- **Domain-specific commit/abandon semantics.** A filesystem store
  unlinks; a postgres store deletes; a future S3 store finalizes a
  multipart upload. Rimsky calls verbs and trusts the producer.

## Why "blob bytes are inert"

Several of the platform's behaviors hinge on rimsky never reading
domain data in any way other than the substitution path. Attribute
values, named-event payloads, parked-state payloads, claim
addresses, claim payloads — all of them are byte slices from
rimsky's perspective. Logging them, normalizing them, indexing them,
validating them beyond the schema gates, attaching them to traces,
including them in error messages — every one of those acts would
make rimsky a partial participant in the domain. It would force the
platform to take on bug surface and security surface that belongs to
the consumer.

Two `@blessed-invariant`s lock this in source:

- Invariant 11: userdata is opaque.
- Invariant 20: claim content is inert.
- Invariant 21: blob content is inert.

Removing or weakening these would unwind the design.

## Why no domain helpers ship

A platform that ships domain helpers becomes a platform whose users
file requests for more domain helpers. The shipped reference
impls (`http-node`, `claude-agent`, `stub`, `filesystem`,
`postgres`) are deliberately illustrative: they cover the protocol's
shape and the deployment story. They are not a curated catalog of
production-ready domain pieces.

If a project needs a Salesforce executor, an audit-trail
LifecycleSubscriber, a tickets-table ClaimProducer, those live in the
project's own repository. They depend on the public protocol surface
(`protocols/proto/v1/*.proto`) and the documented control-API; they
do not need to fork or vendor rimsky.

## Why pre-v1 still has invariants

The "pre-v1" release stance gives rimsky permission to break wire
shapes and schema between versions. It does not give rimsky permission
to weaken the safety properties — the deterministic-sorted-order
multi-lock acquisition, the verify-before-run guard, the
claimant-guarded release, the unified terminal-decision engine, the
auto-terminal aggregate-outcome rule. Those are load-bearing for any
project that adopts rimsky; without them, every consumer would have to
invent their own.

Pre-v1 is about iteration speed on the surfaces; the safety
properties are stable.

## How to read the documentation

The concept pages (`docs/concepts/*.md`) are the canonical reference.
Each is one noun in the platform's vocabulary, with a tight
description, the wire shape if relevant, and the mechanics. The
protocol pages (`docs/protocols/*.md`) walk a builder through
implementing a peer. The executor and producer per-component pages
(`docs/executors/*`, `docs/stores/*`, `docs/blob-backends/*`) cover
the shipped reference impls in operator-facing detail. The narrative
pages (`docs/humans/*`) and the design-philosophy page (this one) set
the framing.

When in doubt: cite from the concept pages. They are the load-bearing
material; everything else points at them.
