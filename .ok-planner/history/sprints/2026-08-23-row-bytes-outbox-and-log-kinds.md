---
closed: cecb7a6b89984bfc91c9400c46b3be06f4973cf0
---
# Sprint: Attribute bytes in the row, lifecycle delivery through one outbox, log kinds in the standard's form

## Intent

Five unrelated changes ride this sprint. Attribute bags and scratch become byte-column values that commit with their row, and the blob backend, its spill threshold, its orphan ledger, and its sweep go. Every lifecycle-subscriber event is staged in the transaction that performs its transition and drained by the reconciler, the idempotency ledger goes, the outbox row gains a due time, and peer delivery gains a stall signal, a diagnostics route, and a stated retention bound. The structured process log's kinds take the events standard's `SUBSYSTEM.NOUN.VERB` form at every emit site, with a lint that keeps the old form out. The sprint remediates fifty-five tests that break the testing standard, and the wall-clock lint learns the two constructs those tests hid behind. The vocabulary drops two terms: "peer" collapses onto "service" everywhere outside the TLS library's own usage, and the host agent becomes the host daemon in the concept, the binary, the proxy, the CLI verb, and the environment variables.

Issues promoted into this sprint:

- `instance-delete-drops-undelivered-lifecycle-events`
- `lifecycle-outbox-retention-narrows-at-least-once`
- `event-log-domain-for-peer-delivery-health`
- `structured-log-kind-case-convention`
- `tests-off-standard`
- `peer-and-agent-vocabulary`

## Corpus deltas

### Retire concept: blob-backend

### Amend concept: persistence-database

```markdown
---
concept: persistence-database
aliases:
  - persistence-driver
---

# Persistence database

## What it is

The persistence database is the umbrella over rimsky's whole persistence layer: the runtime handle a process opens once, holds for its lifetime, and closes at shutdown. In the split deployment each of the three runtime roles runs as its own process and holds its own handle; in the unified deployment the three roles run in one process and share one handle. The handle hands back a bundle of per-ledger accessors, one per persisted ledger. Most callers need only a few of them, and the bundle keeps their startup wiring compact. Rimsky carries two implementations behind the one interface: a client-server backend, the default everywhere except the all-in-one deployment, and an embedded-file backend, which the all-in-one deployment uses. One migration runner serves both, so the schema cannot fork between them. The driver name — the value that selects which implementation to load — is a separate thing from the handle: driver names the shape of the backend, not the runtime object.

An attribute bag and a node-run's scratch are byte-column values in their own rows, whatever their size. The engine never reads inside them, and the engine's own per-value cap bounds them (see `decision:attribute-bytes-in-the-row`).

## Purpose

The persistence database gives graph and control code one abstraction to reach durable state through, so no caller reaches a backend driver directly and an import boundary the build enforces keeps that driver behind the interface. It lets a fast embedded backend stand in for the client-server backend under test, and it admits a further backend without changing a caller.

## Boundaries

The persistence database owns the handle interface, the accessor bundle, the per-ledger accessors, the two implementations, and the migration runner. It owns the schema; executor state that must outlive a dispatch rides the generic surfaces the protocols expose (see `decision:scratch-column`).

It does not own what the schema says, which the migrations carry, nor connection-pool sizing, which an operator configures. It does not own the named locks it hands out, which belong to `concept:advisory-lock`.

see also: `advisory-lock`, `node-run`, `attribute`

## Aliases

- persistence-driver
```

### New decision: attribute-bytes-in-the-row

```markdown
---
decision: attribute-bytes-in-the-row
---

# Attribute bytes commit with the row

## Choice

Rimsky stores an attribute bag and a node-run's scratch whole in a byte column of their own row, whatever their size, and they commit in the row's transaction. The engine's own large-value handling carries the bytes, and the engine's per-value cap is the only ceiling: a write over it fails at the write with an error naming the node run, the attribute or scratch, and the byte count. Rimsky sets no threshold of its own, spills nothing to a store outside the row, and keeps no ledger of bytes to clean up.

## Rationale

A store outside the row's transaction cannot be cleaned up without a durable record of intent: after a crash, an expired stage cannot tell a commit from a rollback, so every such store needs an orphan ledger, a retention window, and a sweep, and none of them closes the gap. Bytes in the row have nothing to coordinate. Both engines rimsky ships cap a value at one gigabyte, and an attribute near that size is a design problem in the template, not a storage problem in rimsky. A rimsky-side limit below the engine's would be a second cap with a second error to explain; one above it is unreachable. A deployment that held values behind a spill handle re-creates its instances; no importer carries them over.

## Alternatives

- A transactional large-object store inside the engine — rejected: no caller streams a value, and it adds a transaction-scoped handle to every reader.
- An external blob store with an orphan queue and a sweep — rejected: the queue is the cleanup this choice removes, and it cannot be made exact.
- A rimsky-side size limit below the engine's cap — rejected: a second cap with its own error, and the policy it would carry belongs to a pluggable attribute store, which is a later design.
- A one-shot importer for values already spilled — rejected: pre-v1, re-creating instances is cheaper than a migration path nothing ships against.
```

### Retire decision: blob-backend

### Retire decision: blob-backends-pluggable

### Retire decision: blob-spill-threshold-config

### Retire decision: blob-backend-mismatch-read-refused

### Retire decision: memory-gate-premise-corrected

### Retire decision: process-role-unified-message-covers-rimsky-run

### Amend decision: artifact-layout

```markdown
---
decision: artifact-layout
---

# artifact-layout

## Choice

A per-run directory under a stable per-root parent, named by timestamp plus run name, holding the run's state database. A pointer entry at the parent level resolves to the most-recent run directory. The state database stays openable with widely available tooling for its format — no rimsky-specific reader is required to inspect an artifact.

## Rationale

A single folder per run is the natural archive-and-ship unit, and under the embedded-file backend the one database file is the whole record of the run. The pointer entry covers the common "open the last one" case without timestamp-parsing. Third-party readability is the artifact's operational value — the operator who inherits a run must be able to open it with standard tooling — so openness is a binding constraint on future storage changes, not a byproduct of today's driver choices.

## Alternatives

- One shared state database across all runs — rejected: loses the copy-one-folder archive-and-ship unit and couples every run's lifecycle to one file.
- Flat per-run files keyed by run id in a single directory — rejected: a run's state database and its configuration file no longer travel together.
- A compressed or encrypted rimsky-specific artifact encoding opened through a bundled reader — rejected: trades third-party post-mortem inspection away for storage convenience.
```

### Amend decision: scratch-column

```markdown
---
decision: scratch-column
---

# Executor scratch persists on the node-run row

## Choice

Executor scratch persists as a byte column on the node-run row, committed with the row (see `decision:attribute-bytes-in-the-row`). Default is empty.

## Rationale

Scratch lives and dies with the dispatch row, so the row is its natural home, and a column on that row is the same idiom the attribute bag uses.

## Alternatives

- A dedicated scratch table keyed by node-run — rejected: an extra join and a second payload idiom for state whose lifecycle is exactly the dispatch row's.
```

### Amend decision: single-process-mode

```markdown
---
decision: single-process-mode
---

# The all-in-one runs all three roles in one process

## Choice

The entrypoint's no-command path runs all three roles (scheduler, supervisor, control-api) in one process via the existing library entry points, each on its configured port, with one signal-handled shutdown; the bundled executor and claim-producer handlers register into the process's in-proc dispatch pool via the bundled registration entrypoint (see `decision:bundled-registry-entrypoint`), and a failure to construct any configured bundled handler aborts the boot before any role starts. The single-role path (explicit role command) keeps its per-role process behavior. A process-role env marker names the unified single-process mode; it is set exactly by the paths that genuinely run rimsky in one shared process, and the roles read it to place their metrics listeners and to decide whether the embedded-file backend needs the shared-file warning (see `story:single-process-all-in-one`, `decision:rimsky-run-self-hosts-templates`).

## Rationale

The unified env marker promises a shared-process deployment; the role mains are thin wrappers over library calls, so the promised deployment is the cheap honest fix.

## Alternatives

- Keep three spawned processes under the unified marker — rejected: leaves the unified marker meaningless.
```

### Amend decision: launch-integration

```markdown
---
decision: launch-integration
---

# The compose verb and the entrypoint share one role launcher

## Choice

One exported launcher runs the three role runners — scheduler, supervisor, control-api: it starts each in order, tracks each runner's stop function, owns the combined role-failure channel, and drains in reverse order. Both the all-in-one entrypoint and the compose verb call it. Each site writes its own signal-versus-failure select, because each has its own signal source. Both sites set the process-role marker, so the roles read the unified topology (see `decision:single-process-mode`).

## Rationale

The start / track / fail / drain loop is identical at both sites and load-bearing at both — a drain that runs in the wrong order or a failure channel nobody owns is a shutdown bug, not a style difference — so it lives in one place and the two sites cannot drift. What genuinely differs is the signal source: the entrypoint watches process signals, the compose verb watches its own lifecycle, so the select stays per site.

## Alternatives

- Mirror the loop at both sites rather than share it — rejected: two copies of a shutdown ordering that must agree, with nothing to keep them agreeing.
- Spawn the all-in-one entrypoint as a child process from the compose verb — rejected: forfeits in-process control of the runners (config injection, lifecycle, teardown) that the verb needs.
```

### Amend decision: launch-config-injection

```markdown
---
decision: launch-config-injection
---

# Synthetic config file injected through standard discovery

## Choice

The compose verb writes one synthetic YAML file to the run directory — a unified config file matching the `concept:rimsky-yml` shape, the supervisor's tuning under its per-role section — and points the role runners at it through the standard config-discovery surface before the runners start. The synthetic file persists alongside the SQL state as part of the run artifact (see `decision:artifact-layout`).

## Rationale

The role runners load YAML from disk; there is no programmatic config seam. The standard config-load surface loads the synthetic file; it costs a write per run at startup and turns the config into an audit artifact for free — operators reading a post-mortem run see exactly what config the run used.

## Alternatives

- A programmatic config-injection seam on the role-runner surface — rejected as a larger refactor than this verb's scope warrants.
- A second synthetic file carrying the supervisor's tuning — rejected: it breaks the single-file commitment of `concept:rimsky-yml`, and a file the image and the test harness consume is no longer this verb's to own.
```

### Amend decision: intx-suffix-convention

```markdown
---
decision: intx-suffix-convention
---

# The InTx suffix means "requires an open transaction"

## Choice

A persistence-layer function carrying the `...InTx` suffix requires an
already-open transaction passed in by its caller — that is the
suffix's whole meaning. Paired variants (a public `X` that opens its
own transaction backed by a private `XInTx`) are forbidden: one method
taking an optional transaction parameter is the house shape.

## Rationale

"Requires a transaction" and "optionally opens one" are different
jobs, and one-idiom-per-job permits a distinct spelling for each.
Writing the convention down stops the bare suffix being flagged as
residue of the forbidden pairing, and keeping no live pairs removes
the copy-source hazard — a live pair would read as the house pattern
and get copied by the next contributor.

## Alternatives

- Rename the bare-suffix helpers away from `InTx` — rejected: the
  suffix reads correctly for "requires a transaction"; renaming ~20
  call surfaces buys nothing.
- Tolerate public/private pairs alongside the optional-parameter
  shape — rejected: same job, second dialect, and a live copy source
  for the forbidden idiom.
```

### Amend story: single-process-all-in-one

```markdown
---
story: single-process-all-in-one
---

# Operator runs the all-in-one deployment as one process

## Story

As an operator running the all-in-one deployment, I get one process serving all three roles (scheduler, supervisor, control-api), so that the deployment is genuinely unified and needs no external service to run.
```

### Amend concept: conformance

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/conformance.md`)

### Amend concept: inertness

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/inertness.md`)

### Amend concept: node-run

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/node-run.md`)

### Amend concept: rimsky-yml

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/rimsky-yml.md`)

### Amend concept: lifecycle-subscriber

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/lifecycle-subscriber.md`)

### Amend decision: lifecycle-subscriber-at-least-once-delivery

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/lifecycle-subscriber-at-least-once-delivery.md`)

### Amend decision: lifecycle-fanout-after-commit

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/lifecycle-fanout-after-commit.md`)

### New decision: lifecycle-drain-per-role

```markdown
---
decision: lifecycle-drain-per-role
---

# Every runtime role drains the lifecycle outbox

## Choice

Each of the three runtime roles runs its own drain over the one lifecycle outbox, and a role kicks its own drain the moment it stages a row, so a delivery follows its transition without waiting for an interval in any deployment. The per-scope advisory lock serialises delivery across drains; the interval is the retry path alone.

## Rationale

A kick is an in-process wake, so it reaches only a drain in the staging process. With one drain in the control-api role, the scheduler's and the supervisor's run-scope terminals wait for the interval in the split deployment — a latency the direct call never had — and the kick helps only the all-in-one deployment. A drain per role keeps the promise that delivery does not wait on the interval in every topology, and the lock that already guards concurrent drains makes the extra drains free of double delivery.

## Alternatives

- One drain in the control-api role — rejected: in the split deployment every staged run-scope terminal lags by up to the interval, and the kick reaches no drain in two of the three roles.
- A cross-process wake through the database — rejected: a notification channel is a second delivery mechanism beside the one the outbox already is, with its own failure modes.
```

### New decision: service-delivery-stall-signal

```markdown
---
decision: service-delivery-stall-signal
---

# A stalled service delivery is an event-log edge pair and a diagnostics route

## Choice

Both retry loops that deliver to services — the lifecycle outbox and the producer-verb outbox — persist their failure state on the outbox row: the attempt count, the time of the next attempt, and the last error. A service is stalled when the oldest pending row in any of its streams has waited longer than one deployment-wide duration, `service_delivery.stall_after`, which also caps the retry backoff, so the drain retries a stalled service no less often than the threshold that declares it stalled. Rimsky writes one event-log entry when a service's delivery first stalls and one when it next succeeds; the two kinds join the closed operational set (see `decision:event-log-kind-enum`). An operator reads what is owed right now from a diagnostics route per outbox, which lists each service's pending rows, their age, their attempts, and their last error.

## Rationale

`concept:event-log` promises the operator a record they ask instead of reconstructing history from process output, and "when did this service stall" is exactly such a question. An entry per failed attempt would write tens of thousands of rows a day against one dead subscriber and carry nothing new, so the signal is the edge, not the attempt. The route alone answers the present and not the past; the edge pair alone answers the past and not the present; together they cover both. One threshold serves both loops because the ruling asks the signal to cover service delivery generally, and age rather than attempt count defines it because backoff makes an attempt count mean a different elapsed time in each loop.

## Alternatives

- An event-log entry per failed attempt — rejected: volume without information.
- A diagnostics route alone — rejected: leaves the event log's promise unmet for this class of failure.
- Declare service-delivery health out of the event log's scope — rejected: it narrows a live concept's purpose to avoid two enum values.
- A fixed threshold constant — rejected: a deployment's tolerance for a slow service is its own.
```

### Amend concept: host-agent-proxy

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/host-agent-proxy.md`)

### Amend concept: control-api

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/control-api.md`)

### New decision: structured-log-kind-format

```markdown
---
decision: structured-log-kind-format
---

# Structured process-log kinds follow the events standard's format

## Choice

Every structured process-log emit site names its kind as a raw string literal in the form `SUBSYSTEM.NOUN.VERB`, upper-case dotted segments, declared at the site and nowhere else. Prose lives in a field, never in the kind. A lint over the tree fails on a literal at a process-log emit site that does not match the form, with an empty baseline, so the lower-case and prose dialects cannot return. The event log is a different system — a durable product surface whose kinds are a closed enum under `decision:event-log-kind-enum` — and this decision does not reach it.

## Rationale

The events standard governs every log that represents code flow for debugging, and the structured process log is that channel; its inventory sees only kinds in the standard's form. A partial adoption is the two-dialect split the one-idiom-per-job rule forbids, and the certification gate's architect reverted an earlier round's partial rename for exactly that reason, so the sweep is whole or not at all. The event log serves a different purpose and already records its own departure.

## Alternatives

- Ratify the lower-case dotted form as this project's process-log convention — rejected: the inventory stays blind to the channel, and the project would carry a second departure from the standard for no gain.
- Rename the dotted kinds and leave prose messages as they are — rejected: two dialects on one channel.
- Hold both channels outside the standard's naming rule — rejected: the process log is precisely the channel the standard is for.
```

### Amend decision: test-wallclock-lint-ratchet

```markdown
---
decision: test-wallclock-lint-ratchet
---

# Wall-clock verdict idioms are lint-forbidden in test code

## Choice

A lint forbids wall-clock verdict idioms in test code: fail-on-timeout
selects, deadline-bounded poll loops that fail on expiry,
deadline-polling helpers — including third-party ones such as
`require.Eventually`, whose deadline is a verdict input — a context
deadline whose expiry feeds a verdict, and a test that writes a
package-level variable. The gate
fails on any violation. Its recorded baseline is empty. Every wait the
lint admits carries a class marker per `decision:polling-audit`. A
per-site suppression marker exists for sleeps that are genuinely not
verdict inputs (fixture pacing); each carries its justification at
the site.

## Rationale

The testing rules already retired the dialect — any finite timeout is
a guess about machine load, so a deadline that fails a test is a
load-dependent verdict, not a verdict. Prose alone let roughly two
hundred sites accumulate. A ratchet stopped new instances while the
backlog stood. One sweep drained the backlog once the class marker
made every site classifiable. An empty baseline keeps the gate
absolute, so the banned dialect cannot re-enter with a new test. A
reading audit later found the same verdict hiding behind a context
deadline and behind shared package state, two constructs the lint
did not read; the lint reads them now, for the same reason.

## Alternatives

- Keep the ratchet with a standing baseline — rejected: a standing
  backlog is a standing excuse, and the class marker made the sweep
  mechanical rather than judgment-heavy.
- Keep auditing periodically without a gate — rejected: leaves the
  rule permanently unenforced; the banned dialect re-enters with
  every new test.
- Loosen the rule to sanction generous documented timeouts —
  rejected: "why 30 and not 29?" has no answer; any finite bound is
  an unprovable load guess.
- Leave the context-deadline and shared-state shapes to reading
  audits — rejected: a construct the lint does not read is a
  construct the next test re-introduces.
```

## Corpus deltas: the vocabulary sweep

These deltas carry the rename of "peer" onto "service" and of "host agent" onto "host daemon" through every artifact that uses either term. The sprint retires a renamed artifact under its old slug and creates it under its new one with the same body in the new vocabulary; it amends every other artifact. Each body is in the sidecar.

### Amend concept: anonymous-mode

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/anonymous-mode.md`)

### Amend concept: api-key

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/api-key.md`)

### Amend concept: cascade-graph

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/cascade-graph.md`)

### Amend concept: discovery-cache

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/discovery-cache.md`)

### Amend concept: error-policy

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/error-policy.md`)

### Amend concept: executor

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/executor.md`)

### Amend concept: frame

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/frame.md`)

### Retire concept: host-agent-proxy

### New concept: host-daemon-proxy

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/host-daemon-proxy.md`)

### Retire concept: host-agent

### New concept: host-daemon

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/host-daemon.md`)

### Amend concept: instance

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/instance.md`)

### Amend concept: module-layout

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/module-layout.md`)

### Amend concept: node-subscription

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/node-subscription.md`)

### Amend concept: observability

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/observability.md`)

### Amend concept: permission

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/permission.md`)

### Amend concept: publisher

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/publisher.md`)

### Amend concept: rimsky

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/rimsky.md`)

### Amend concept: sensor

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/sensor.md`)

### Amend concept: service-address-book

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/service-address-book.md`)

### Retire concept: peer-auth

### New concept: service-auth

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/service-auth.md`)

### Amend concept: service

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/service.md`)

### Amend concept: supervisor

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/supervisor.md`)

### Amend concept: template

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/template.md`)

### Amend concept: terminal-resolution

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/terminal-resolution.md`)

### Amend concept: validation

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/concepts/validation.md`)

### Retire storie: anonymous-agents-isolated

### New storie: anonymous-daemons-isolated

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/anonymous-daemons-isolated.md`)

### Retire storie: host-agent-anonymous-mode

### New storie: host-daemon-anonymous-mode

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/host-daemon-anonymous-mode.md`)

### Retire storie: host-agent-control-plane

### New storie: host-daemon-control-plane

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/host-daemon-control-plane.md`)

### Retire storie: host-agent-late-bind-all-protocols

### New storie: host-daemon-late-bind-all-protocols

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/host-daemon-late-bind-all-protocols.md`)

### Retire storie: host-agent-per-binding-overrides

### New storie: host-daemon-per-binding-overrides

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/host-daemon-per-binding-overrides.md`)

### Retire storie: host-agent-per-run-scope-isolation

### New storie: host-daemon-per-run-scope-isolation

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/host-daemon-per-run-scope-isolation.md`)

### Retire storie: permissive-peer-build

### New storie: permissive-service-build

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/permissive-service-build.md`)

### Retire storie: peer-auth-mtls-mutual

### New storie: service-auth-mtls-mutual

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/service-auth-mtls-mutual.md`)

### Amend storie: service-enrollment

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/service-enrollment.md`)

### Retire storie: peer-tls-enforced

### New storie: service-tls-enforced

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/service-tls-enforced.md`)

### Amend storie: validation-mixin-uniform

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/stories/validation-mixin-uniform.md`)

### Amend decision: bundled-executor-inproc-capability-advertisement

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/bundled-executor-inproc-capability-advertisement.md`)

### Amend decision: default-port-allocation

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/default-port-allocation.md`)

### Amend decision: destination-allowlists-default-closed

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/destination-allowlists-default-closed.md`)

### Amend decision: enroll-token-is-api-key

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/enroll-token-is-api-key.md`)

### Amend decision: env-var-registry

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/env-var-registry.md`)

### Amend decision: graceful-shutdown

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/graceful-shutdown.md`)

### Amend decision: grpc-internal-protocols

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/grpc-internal-protocols.md`)

### Retire decision: host-agent-late-bind-schema-check-deferred

### New decision: host-daemon-late-bind-schema-check-deferred

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-late-bind-schema-check-deferred.md`)

### Retire decision: host-agent-path-resolution-anchored-to-agent-cwd

### New decision: host-daemon-path-resolution-anchored-to-daemon-cwd

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-path-resolution-anchored-to-daemon-cwd.md`)

### Retire decision: host-agent-port-assignment-no-handshake

### New decision: host-daemon-port-assignment-no-handshake

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-port-assignment-no-handshake.md`)

### Retire decision: host-agent-proxy-enrollment

### New decision: host-daemon-proxy-enrollment

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-proxy-enrollment.md`)

### Retire decision: host-agent-proxy-error-vocabulary-reuse

### New decision: host-daemon-proxy-error-vocabulary-reuse

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-proxy-error-vocabulary-reuse.md`)

### Retire decision: host-agent-proxy-tls

### New decision: host-daemon-proxy-tls

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-proxy-tls.md`)

### Retire decision: host-agent-proxy-uniform-routing-identity

### New decision: host-daemon-proxy-uniform-routing-identity

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/host-daemon-proxy-uniform-routing-identity.md`)

### Amend decision: image-set-four-core

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/image-set-four-core.md`)

### Amend decision: late-bound-services-direct-spawn

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/late-bound-services-direct-spawn.md`)

### Amend decision: licensing-dual-apache-agpl

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/licensing-dual-apache-agpl.md`)

### Amend decision: logging-slog-only

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/logging-slog-only.md`)

### Amend decision: network-binding

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/network-binding.md`)

### Amend decision: openlineage-outbound-bearer

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/openlineage-outbound-bearer.md`)

### Amend decision: parallel-inproc-claim-producer-registry

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/parallel-inproc-claim-producer-registry.md`)

### Amend decision: plumb-validation-roles

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/plumb-validation-roles.md`)

### Amend decision: proxy-single-spawn-multiplexing

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/proxy-single-spawn-multiplexing.md`)

### Amend decision: run-token-swept

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/run-token-swept.md`)

### Amend decision: secret-at-rest-posture

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/secret-at-rest-posture.md`)

### Retire decision: peer-auth-mtls

### New decision: service-auth-mtls

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/service-auth-mtls.md`)

### Amend decision: service-spawn-flag

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/service-spawn-flag.md`)

### Retire decision: peer-tls-enforcement

### New decision: service-tls-enforcement

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/service-tls-enforcement.md`)

### Amend decision: tls-mode-validation

body: in the sidecar (`2026-08-23-row-bytes-outbox-and-log-kinds-deltas/decisions/tls-mode-validation.md`)

## Work items

Each item names the decisions and stories it makes true. Items are unordered; dependencies are stated where they exist.

### Attribute bytes in the row

- **Byte columns for the bag and scratch.** Makes true `decision:attribute-bytes-in-the-row`, `decision:scratch-column`. One new numbered migration per backend changes `rimsky_node_attributes.data` and `dispatch_input_bag` to `BYTEA` (Postgres) / `BLOB` (SQLite), drops `value_handle` and `value_handle_backend`, renames `rimsky_node_runs.scratch_inline` to `scratch`, drops `scratch_handle` and `scratch_handle_backend`, and drops `rimsky_blob_orphans`. The attribute-bag writer and the scratch writer store the whole value; a write the engine refuses for size surfaces as an error naming the node run, the attribute or scratch, and the byte count, and reaches the executor or the route the way any persistence error does. Every reader loads the value from the column. Pre-v1: no importer for values a deployment had spilled.
- **Remove the blob backend.** Makes true `decision:attribute-bytes-in-the-row`. Delete the blob backend interface and its four implementations (inline, filesystem, memory, Postgres large object), the spill and orphan code, the orphan sweep and its scheduler interval, the blob handle on the callback server and on run arguments, the blob wiring in the config and launch layers, the `persistence.blob` configuration section whole (backend, spill threshold, filesystem root, retention), the memory-backend topology gate and its error text, the `blob-backend` conformance battery, its command, and its entry in the conformance verb list, and the storage-conformance cases that exercised spill or orphans. The process-role marker stays: the roles still read it to place metrics listeners and to decide the embedded-file backend's shared-file warning. A deployment that sets a removed configuration key fails at startup naming the key, as for any unknown key. Depends on the byte-columns item.
- **Documentation and gotchas.** Remove the `CLAUDE.md` gotcha that names the memory blob backend and the unified marker, and remove from `docs/` the protocol-table rows and any section that documents the blob backend, the spill threshold, or the `blob-backend` conformance command, so the tree ships no document for a surface that is gone. The next `/document` run revises the rest.

### Lifecycle delivery through one outbox

- **Staging moves to foundation.** Makes true `decision:lifecycle-fanout-after-commit`. The staging primitive, the typed template and instance wrappers, the event enum, the staged payload, and the subscriber-list resolver move from the control layer to `lib/foundation/lifecycle`. A new wrapper stages one row per service for a closed run scope with the payload `{run_scope_id, instance_id, terminal_reason}`, taking the instance id and resolving instance → template → subscribing services inside the caller's transaction. The six control-layer call sites import from the new home.
- **Run-scope terminal staged at scope close.** Makes true `decision:lifecycle-fanout-after-commit`, `decision:lifecycle-subscriber-at-least-once-delivery`. The frame engine's frame-end transition stages run-scope rows inside its transaction, one per (service, scope), children before parents; the supervisor's child-scope close at rendezvous (sub-graph exit, fan-out partition settle) stages the same rows inside its own transaction. The direct fan-out in both places, its callback through the frame engine, and the scheduler's and supervisor's subscriber registries and subscriber-list configuration go; the supervisor no longer dials lifecycle subscribers at all. Each role runs its own drain (next item), and the scheduler and the supervisor kick theirs after the staging transaction commits. Depends on the staging-moves item.
- **Instance terminated staged at the transition.** Makes true `decision:lifecycle-fanout-after-commit`, `decision:lifecycle-subscriber-at-least-once-delivery`. The terminate route and the delete route stage `instance_terminated` in the transaction that stamps `terminated_at`, after staging run-scope terminal for each scope the termination closes, and deliver inline after commit as the other routes do. The reconciler's terminated-instance pass and the persistence reads that served it go; the reconciler is the staged drain alone. Delete purges nothing from the outbox; rows for a deleted instance drain as any other. Depends on the staging-moves item. Resolves `instance-delete-drops-undelivered-lifecycle-events`.
- **The reconciler runs in every role and gains a kick and a due time.** Makes true `decision:lifecycle-subscriber-at-least-once-delivery`, `decision:service-delivery-stall-signal`, `decision:lifecycle-drain-per-role`. The reconciler moves from the control layer to `lib/runtime` beside the staging package's consumers, and each of the three roles — scheduler, supervisor, control-api — runs one over the same outbox; in the all-in-one deployment the one process runs one. The per-scope advisory lock serialises delivery across drains. The outbox row gains `attempt_count`, `next_attempt_at`, and `last_error` by migration in both backends. The drain delivers the oldest pending row per stream whose due time has passed; a failure records the attempt, the next due time on an exponential backoff from the reconciler interval up to `service_delivery.stall_after`, and the error, and blocks that stream until the due time. Each drain exposes a kick — a capacity-one channel send; a kick that finds the channel full drops — that wakes it at once, and a role kicks its own drain after it stages; the interval becomes retry-only.
- **The idempotency ledger goes.** Makes true `decision:lifecycle-subscriber-at-least-once-delivery`. A new numbered migration in both backends drops `rimsky_lifecycle_idempotencies`; the ledger table accessor, its state enum, the purge inside instance hard-delete, and the conformance cases that exercised it go. The per-scope advisory lock stays and its critical section is the service call and the row delete. Depends on the run-scope and instance-terminated items.
- **Peer delivery health.** Makes true `decision:service-delivery-stall-signal`. The `service_delivery.stall_after` configuration key (duration, default five minutes) loads in its own section of the unified configuration file. Both outboxes persist their failure state on the row — the producer-verb outbox gains the same three columns where it lacks any. Two new operational event-log kinds, declared in the proto enum and emitted when a service's oldest pending row first exceeds the threshold and when that service next delivers, with the service name and the outbox as fields; the emit-site fitness check covers them. A new route `GET /v1/admin/diagnostics/lifecycle-outbox`, gated by `diagnostics:read`, lists each service's pending rows with age, attempts, and last error, beside the existing producer-outbox route. The service-status route (today `GET /v1/observability/peers`, renamed to `GET /v1/observability/services` by the vocabulary sweep) reports each service's pending outbox rows in place of ledger rows, and the CLI command that prints it changes with it. Resolves `event-log-domain-for-peer-delivery-health` and `lifecycle-outbox-retention-narrows-at-least-once`.
- **The proxy's reap through the outbox.** Makes true the amended `concept:host-agent-proxy`. The proxy's lifecycle handler is unchanged; the scenario tests that waited on the direct run-scope call now wait on the proxy's own event after the kick delivers, and a test of the retry path fires the reconciler tick itself.

### Log kinds in the standard's form

- **Rename every process-log kind.** Makes true `decision:structured-log-kind-format`. Every structured process-log emit site under `lib/`, `cmd/`, and `tools/` — product code and tests alike — names its kind in `SUBSYSTEM.NOUN.VERB` form; a literal that carried prose keeps that prose in a field, and a literal that named a Go symbol becomes a kind with the symbol in a field. A test that waits on a kind waits on the renamed literal. Resolves `structured-log-kind-case-convention`.
- **A lint keeps the old form out.** Makes true `decision:structured-log-kind-format`. A lint under `tools/`, shaped like the wall-clock lint, scans every process-log emit site and fails on a literal outside the form; a fitness test under `test/plumbline/` runs it with an empty baseline and is part of `make lint`. Depends on the rename item.

### Tests on the standard

- **Remediate the fifty-five tests.** Makes true `decision:test-wallclock-lint-ratchet`. For every test the ledger at `.ok-planner/workbench/2026-08-22-test-audit-ledger.tsv` names: rewrite a wall-clock wait onto the event-driven form its sibling uses; give a vacuous test an assertion on the behavior its name claims, or delete it; delete a redundant test, folding any assertion it alone carries into the test that keeps the proof. Where the wait is in the product, inject the `Clock` so the test drives time: the claude-agent silence and tool-use timeout loop, the agent-stop escalation, the control-API readiness wait, and the compose drain's child grace window. The four blob tests the ledger names go with the blob backend. The ledger names fifty-five; the sprint is done with this item when none of them remains in the form the ledger recorded.
- **The wall-clock lint reads two more constructs.** Makes true `decision:test-wallclock-lint-ratchet`. The lint reads a `context.WithTimeout` or `context.WithDeadline` whose expiry feeds a verdict, and a test that writes a package-level variable, and fails each under the same class rules as the five constructs it reads today. The ratchet test under `test/plumbline/` stays at an empty baseline. Depends on the remediation item.

### The vocabulary sweep

- **"Peer" becomes "service".** Makes true the vocabulary-sweep deltas above. Across code, protos, configuration, tests, fixtures, and the tree's documents: the `peer_auth` configuration key becomes `service_auth` and `RIMSKY_PEER_AUTH` becomes `RIMSKY_SERVICE_AUTH`; the `lib/protocols/peerauth` package becomes `serviceauth`; the proxy's service-facing listener and its port variable `RIMSKY_PROXY_SERVICE_GRPC_PORT` follow; the route `GET /v1/observability/peers` becomes `GET /v1/observability/services` and the CLI command that prints it follows; every identifier, log kind, event field, error message, and prose sentence that says "peer" for a deployed service says "service". The word survives only where it is the TLS library's own vocabulary for the far end of a connection. Every `@concept:` / `@story:` / `@decision:` citation of a renamed slug cites the new slug, so the citation lint stays green; the same change updates `CLAUDE.md` and the surface intent.
- **"Host agent" becomes "host daemon".** Makes true the vocabulary-sweep deltas above. The `rimsky-host-agent` binary becomes `rimsky-host-daemon`; the `rimsky-host-agent-proxy` binary, image, and Dockerfile become `rimsky-host-daemon-proxy`; the CLI verb `rimsky agent …` becomes `rimsky daemon …`; `RIMSKY_AGENT_PORT`, `RIMSKY_AGENT_TLS_CA`, `RIMSKY_HOST_AGENT_INSECURE`, and every other `RIMSKY_AGENT_*` / `RIMSKY_HOST_AGENT_*` variable take `DAEMON`; the `lib/runtime/hostagent` package and every `hostagent` identifier become `hostdaemon`; the daemon-facing listener, the proxy's protocol, its log kinds, and its error vocabulary follow; the env-var registry, the image set, the default-port audit, and the Makefile targets follow. "Agent" stays only in the claude-agent executor and the agentic-tool surface, where it means an LLM agent. The pin test that keeps "supervisor" out of the proxy's source stays and still passes. Every citation of a renamed slug cites the new slug; the same change updates `CLAUDE.md`, the surface intent, and the tree's documents.

## How to execute this sprint

This sprint is self-sufficient. Every executor — an inline session,
an agent handed this file via `/goal`, an orchestrator with its own
planning — runs the same shape: a team of two workers the session
relays, then one cold certification.

1. Read the sprint whole first: intent, deltas, work items,
   completion contract. Do not look for context behind it, in the
   intake (`.ok-planner/issues/`) or in `history/`. Raise a gap with
   the owner; never fill it by inference.

2. Stage the work. Group the items by theme, file surface, or
   dependency, and order the groups so nothing is built on something
   not yet there. Before building, write the staged list as the
   opening section of the completion report (step 9): `## Stages`,
   one line per stage naming the work items it groups, each marked
   pending. Seed the closing stages
   now — finish the completion report, run `/certify-work` with this
   sprint's path as its argument, walk the presentation, offer
   archive-and-commit. The builder marks each build stage done as it
   lands. The session marks the closing stages after the team
   retires. The report is the record of the stages, never a plan
   document. The session keeps one task per stage in the harness task
   tools, where available, mirroring the report's staged list, and
   marks each task done as its stage lands. The task list is display;
   the report remains the record.
   An orchestrator uses its own graph and still records the stages in
   the report.

3. Run the team. The session orchestrates and never joins as a
   worker: it relays messages between the two workers, reads their
   task notifications, and holds the reviewer's ledger. It opens the
   completion report with the staged list before the build and marks
   the closing stages after the team retires; during the build it
   edits no file a worker owns. Every dispatch names its model.
   - **The builder** (`opus`), dispatched once with this sprint's
     path and the report's path, fed one stage per message. It
     writes the code, applies the stage's corpus deltas, tests what
     it built, marks the stage in the report with what it did, and
     stands by. It fixes the reviewer's findings in its own context
     when they arrive.
   - **The standing reviewer** (`opus`), dispatched once under the
     standing-reviewer brief in the certification core
     (`_shared/certification-core.md` under `.claude/skills/`), fed
     each landed stage's paths and the work items it lands. It reads
     the increment under the certification gate's code-review brief
     — findings reach anywhere in the tree the increment breaks —
     and the gate's alignment questions scoped to the stage's own
     items and deltas, plus the read-only per-stage producers each
     present family's ceremony contribution names under **Standing
     producers**, keeps a ledger of open findings, and replies with
     the ledger. It reports each claimed
     fork outside the ledger, in every reply until the completion
     report carries it. It edits nothing and runs no suite.
   - **The relay.** The session runs the relay protocol stated with
     that brief in the certification core: the message it sends the
     reviewer as each stage lands, the lines and claimed forks it
     relays back to the builder, the fix-only rounds it runs after the
     final stage, and the bound on those rounds. On every relay the
     session writes the reviewer's open ledger and the open claimed
     forks to `<sprint-name>-ledger.md` beside the completion report,
     so the state it holds survives it. A replacement session and a
     replacement reviewer read that file from disk.
   - **Retirement.** Retire a worker only at a stage boundary,
     inside the band the worker-pool rule sets: roughly 300k to 500k
     tokens of measured context (`subagent_tokens`) on a 1M-token
     window, scaled on a smaller window. At each boundary the session
     projects what the next stage costs and hands it over only when
     the worker will still retire inside the band. A replacement
     builder reads this sprint and the report and continues at the
     next stage; a replacement reviewer reads the open ledger and the
     open claimed forks from the ledger file.
   - **Without messaging.** Where the harness offers no cross-agent
     messaging, one session runs the same shape in bounded batches.
     The session orchestrates here too. Per batch it dispatches a
     fresh builder (`opus`) with this sprint's path, the report's
     path, one stage, and the open findings, then a fresh reviewer
     (`opus`) under the same brief over that stage's paths. The
     ledger and the open claimed forks travel in the prompt. After
     the last stage's batch, the session runs fix-only batches — a
     builder with the open ledger, then a reviewer over the fixed
     paths — until the reviewer reports an empty ledger, under the
     same bound the protocol sets.

4. Apply each corpus delta as part of the work that realizes it:
   copy the final-form body into `.ok-planner/design/` verbatim (from
   the sidecar where the heading points there), or delete the file
   for a retirement. Apply a delta no work item implements on its
   own.

5. Build stage by stage. Every new or amended story implemented in
   code is exercised end-to-end by a test in the project's ordinary
   suites, carrying the `@story:` annotation. No test checks the
   existence of static text, code, or prose; a commitment realized in
   prose carries no test. Write the tests with the work; the
   builder runs the tests that cover what it built, never the full
   suites — the gate runs the regression. Leave
   `.ok-planner/audits/` and `.ok-planner/experiments/` untouched:
   only a running `/audit` reads or writes them, and they record
   behavior at the time of the audit. An experiment the work breaks
   stays broken until the next run repairs or retires it.

6. Completeness is the floor. Never stub, defer, narrow, no-op, or
   leave a `TODO` in place of a promised outcome. Deliver every
   capability the deltas or work items promise in full, or surface
   the blocker that prevents it.

7. Never destroy uncommitted work. Stage the paths you touched as
   each stage finishes (`git add <paths>`). Never run `git checkout`/
   `restore`/`reset`/`stash`/`clean` on your own initiative. Fix a bad
   edit forward by editing again.

8. Work unsupervised to a defensible done. Do not pause for
   approval, confirmation, or progress checks. Stop only on a
   genuine blocker: a credential or access you cannot obtain, a step
   impossible in the current state, a destructive or irreversible
   action not clearly authorized, or the closing `/certify-work`
   step being unrunnable for you (its subagent dispatches
   unavailable). Surface that and stop; never skip the ceremony and
   call the work done. Ambiguity is not a blocker. The builder never
   files an issue: where the sprint is silent, it makes the most
   plausible call, continues, and records the call in the report as
   a divergence; where the sprint and corpus do not determine the
   fix and reasonable owners diverge, it records the fork with its
   options, builds the reading it judges most plausible, and
   continues. The gate reads both.
   An orchestrator that supervises its own executors folds this into
   its own control.

9. The completion report stays current. It lives beside this sprint
   file, same filename with `-completion` before the extension. The
   session opens it in step 2 with the staged list and marks the
   closing stages after the team retires. The builder marks each build
   stage done as it lands and records what it did. It writes every
   divergence and every claimed fork — its own and the reviewer's —
   into one `## Divergences` section, one entry each. Each entry opens
   with a stable identifier on its first line: `D<n>` for a
   divergence, `F<n>` for a claimed fork, numbered in the order the
   builder wrote them. The identifier lets the gate's architect
   rewrite an entry in place. A fork entry carries the fork's options
   and, where the builder built one, the reading it built. The report is the record the closing ceremony
   finishes and walks with the owner, the artifact a goal checker
   requires, the brief a replacement builder reads, and it is archived
   with this sprint. It is a record of this execution, never a plan.

10. Code complete means the built work works and the reviewer's ledger
    is empty. Close by running `/certify-work` with this sprint's path
    as its argument, immediately after. The argument puts the sprint
    in the gate's scope; the gate never adopts one on its own. The
    gate is cold and is the regression: it runs the project's test
    suites over the touched work, change-scoped corpus checks over the
    touched artifacts and annotations, and one code review over the
    whole diff by a reviewer holding no history and blind to the
    report; its sprint-alignment judge reads the report's divergences
    under the veto test and routes each claimed fork to the architect.
    All producers feed a no-discretion review-fix loop: standing
    agents work in rounds against a finding ledger. The loop ends at
    the first round in which neither the fixer nor the architect
    edited any file (code, corpus, or the report's `## Divergences`).
    A fixer fixes everything a reasonable owner would wave
    through. An architect adversarially checks its kickbacks, its
    refutations, the claimed forks, and any reversal. It makes the fix
    wherever it overturns the claim, and promotes only genuine intent
    forks to the intake.
    Whether the corpus's claims still hold is the periodic `/audit`
    run's question, never this close's. `/certify-work` ends the run:
    it writes its presentation into the completion report, walks the
    presentation with the owner, offers the close-out, and stops.

**After the run stops.** The owner archives this sprint and commits
the work. The run offers both at the end of the presentation and
does neither on its own. Until the owner answers, this file stays at
its `sprints/` path. On yes, the run moves this file, its completion
report, its ledger file, its delta sidecar, and the issue files it
resolved to `history/`, commits the work, then stamps the archived
sprint with the closing commit — `closed: <sha>` in the frontmatter,
one small follow-on commit. The next planning ceremony reads that
stamp to detect work done out of band. "Finish the sprint" and
"follow the boilerplate" are not a yes; both ask for the
presentation.

## Completion contract

The work is done when all of the following hold, each verifiable
from the repository as it stands:

1. The design corpus matches every delta above, applied verbatim
   (from the sidecar where a heading points there).
2. The project's own test suites pass, and every new or touched
   story implemented in code is exercised end-to-end by a test the
   suites run.
3. The completion report beside this sprint (same filename with
   `-completion`) is finished: it records the work done and the
   divergences, and carries `/certify-work`'s presentation — the
   review-fix loop run last and come back clean, every finding
   settled: `fixed <pass>`, `refuted`, `dissolved`, `reversal-ruled`,
   or promoted-and-verified.

**The goal rule, for any checker verifying this contract.** The goal
is met when items 1–3 verify against the repository as it stands.
Decide from the repository, never from the session transcript: an
earlier session may have done the work, and a term the transcript
does not show may hold on disk. That state is the goal met. Walking
the presentation, archiving, committing, and the `closed:` stamp all
follow completion; a pending archive-and-commit offer is evidence
the goal is met. Where this sprint file sits is no term of the rule:
`sprints/` and `.ok-planner/history/sprints/` satisfy it alike, and
a sprint already archived with a `closed:` stamp is terminal — stop
checking. A missing completion report means not done. The ledger file
is no term of the contract: it is the relay's working state, and
whether it exists decides nothing. A run parked
at the review-fix loop's cycle cap awaiting the owner's direction
has not met the goal: a legal in-flight state, not done, not failed,
and never grounds for the run to take either cap step itself.
Nothing else counts either way.
