---
sketch: subscribe-contention-hazard
date: 2026-06-10
---

# Publisher Subscribe handshake degrades silently under contention

Surfacing as a sketch so the underlying production hazard doesn't get
lost behind the `-parallel 4` test cap that papered over it.

## The behavior today

When an operator creates an instance whose template declares any
`publishers:` block, the instance-create HTTP handler runs the
`StartPublisherSubscriptionsForInstance` step synchronously: for
every publisher it INSERTs a `rimsky_publisher_subscriptions` row,
then calls the publisher's `Subscribe` RPC with a bounded
retry-with-backoff loop (3 attempts, 200ms → 560ms → ~1.6s, jittered
±25%, ~2.36s total retry budget). On RPC failure the row's `state`
is flipped to `failed` and a `publisher.subscribe.rpc_failed` log
line is emitted, but **the error is discarded by the HTTP handler**:
instance-create still returns 201 Created.

For an operator polling the publisher's side (e.g. waiting for a
sensor's state DB row to appear) this looks like a healthy create
followed by a never-arriving subscription. The "resync sweeper" is
documented as the operator-recoverable path, but its cadence isn't
named in the symptom; if the operator is waiting on a window of a
minute or two, the sweeper doesn't help in time.

## Where it surfaces

Reproducible under heavy parallel-test load on a single host.
`lib/services/test/scenarios/` runs many `t.Parallel()` subtests,
each spinning up its own rimsky-all-in-one container plus a sensor
container plus a state postgres plus a ryuk reaper. At
GOMAXPROCS-wide parallelism (typically 10–12 on a modern machine)
that's 40+ containers fighting for CPU, disk, and network. Under
that contention the Subscribe RPC's 2.36s budget gets eaten, the
row flips to `failed`, and the corresponding sensor test fails
with `sensor never persisted subscription within 90s`. A different
sensor test bites every run — classic contention-driven
non-determinism, not a deterministic regression.

## Why it's a production hazard, not just a test issue

The contention shape isn't unique to testcontainers. An operator
running a single rimsky stack with many instances each declaring
several publishers — bursty deploys, parameterized fan-out, an
instance create wave during a backfill — can hit the same budget.
The failure is silent: an operator who relies on the 201 from
`POST /instances` as a "subscription mounted" signal is wrong, and
nothing in the create response surface tells them so. The
`rimsky_publisher_subscriptions.state = failed` row is the only
observable, and it's not on the obvious diagnostic path.

The workaround we shipped — `-parallel 4` in the Makefile — keeps
the test gate green but doesn't address the production shape. It
also has decay risk: a future contributor seeing the cap may
remove it ("why is this limited?") and the flake will reappear.

## Concerns to chew on next session

- Should Subscribe retries be asynchronous from the instance-create
  HTTP handler — so create returns immediately on the row INSERT and
  a worker handles the RPC + retry independently?
- Should the retry budget itself be larger, or backoff-only-bounded
  rather than attempt-bounded, so contention spikes don't cause
  silent failure?
- Should the create-response surface include a "subscription
  mounted" signal, so an operator can wait on it instead of trusting
  the 201?
- Should the resync sweeper's cadence be operator-tunable, with a
  documented default that bounds time-to-recovery?
- Is the `state = failed` row sufficiently observable, or does it
  warrant a metric / loud emit that an operator can alert on?

Nothing here is decided; this sketch exists so the next investigator
isn't starting from "why is `-parallel 4` in the Makefile."
