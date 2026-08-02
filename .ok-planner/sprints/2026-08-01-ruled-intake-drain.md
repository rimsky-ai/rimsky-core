# Sprint: Ruled-intake drain — story reductions, stray-commitment homes, and the carry-verbatim erasure

## Intent

Drain the ruled issue intake. This sprint carries the 18 ruled issues left open after the 2026-08-01 verify-issues run (four keep-bundled rulings were retired at framing with no corpus change). There is no single theme beyond the intake itself; the recurring shapes are: reduce every story still carrying prose past its canonical sentence, home each stray commitment that prose held (in a concept invariant, a completed decision, or a new decision), collapse the one same-outcome story pair, add the one missing bundled-service story, carry one ratified invariant into its concept, and erase one stale decision together with its non-compliant code footprint.

Promoted issues: `story-claude-agent-restructure`, `stories-fanout-partition-collapse`, `stories-bundled-sensor-collapse`, `stories-claim-producer-backend-collapse`, `story-bundled-park-resume-recipe-mechanism-home`, `story-host-agent-control-plane-verb-contract-home`, `story-host-agent-per-binding-overrides-defaults-home`, `story-publisher-protocol-restart-reissue-home`, `story-rimsky-deployment-bootstrap-unknown-command-home`, `story-rimsky-health-check-dependency-semantics-home`, `stories-doc-accuracy-gates-decision`, `story-single-process-migrate-ordering-home`, `story-verifier-severity-allowlist-home`, `story-subscriber-lineage-receiver-poller-decision`, `intent-verifier-shape-checks-executor-uncited`, `intent-cascade-two-boundary-opacity-uncarried`, `intent-carry-verbatim-decision-doc-stale`, `story-audit-artifact-no-special-reader-home`.

## Corpus deltas

### New story: fanout-list-array

Replaces the retired pair `fs-fanout-list-array` / `pg-fanout-list-array` (same outcome, different backend; the backend choice is recorded in `decision:fanout-list-array-store-agnostic`).

```markdown
---
story: fanout-list-array
---

# Template author fans out over an upstream list

## Story

As a template author, I can declare a fan-out node whose partition request is a list of items I produced upstream, so that I run one parallel work unit per item with no custom claim-producer to write.
```

### Retire story: fs-fanout-list-array

Delete the file. Its outcome is carried by `story:fanout-list-array`; the citing test annotation is repointed by the work items.

### Retire story: pg-fanout-list-array

Delete the file. Its outcome is carried by `story:fanout-list-array`.

### New story: verifier-shape-checks

The one bundled public-surface service with no corpus presence. Sibling `story:verifier-severity-partition` keeps the severity partition; this story owns the check capability itself.

```markdown
---
story: verifier-shape-checks
---

# Template author validates data shape with built-in checks

## Story

As a template author, I can validate a claim's tabular data against checks I declare in node config using the bundled shape-checks verifier, so that I enforce data shape without writing a custom verifier.
```

### New decision: claude-agent-error-classes-closed

```markdown
---
decision: claude-agent-error-classes-closed
---

# Claude-agent error classes are a closed set

## Choice

The bundled claude-agent executor's declared error classes are a closed set: the executor advertises the full set through its observability surface and rejects emission of any class outside it at its own boundary, so an undeclared class never leaves the executor. Free-form error strings are not accepted. The member spellings are protocol surface, owned by the executor's declaration and its tests, not enumerated in the corpus.

## Rationale

Error-policy routing (`concept:error-policy`) routes outcomes on declared classes, which is only dependable if the class vocabulary is stable and enumerable. Closedness moves the failure to the executor's emission gate — a loud rejection at the boundary — instead of a silently unroutable outcome downstream. Recording the closedness but not the member list keeps the corpus out of the churn-prone spelling business the declaration already owns.

## Alternatives

- Free-form error strings, routed by pattern — rejected: policies rot as spellings drift, and there is no enumerable set for a template to subscribe against.
- Recording the member list in the corpus alongside the closedness — rejected: a second copy of a churn-prone list that the executor's declaration and tests already own.
```

### New decision: fanout-list-array-store-agnostic

```markdown
---
decision: fanout-list-array-store-agnostic
---

# List fan-out is one grammar across both bundled stores

## Choice

The list partition grammar for fan-out — a partition request carrying a list of items produced upstream — is store-agnostic: both bundled claim producers (filesystem and Postgres) serve it through the same split-scope surface, and which bundled store holds the parent claim is a deployment choice, not a separate capability.

## Rationale

The list grammar has no store-dependent semantics — the items come from upstream, not from the store — so per-store variants would duplicate one capability behind two doors and force the story catalog to tell one outcome twice. The one genuinely store-specific partition idiom that exists, folder expansion, is its own grammar with its own story (`story:fs-fanout-expand-folder`), which keeps the line honest: grammars split by semantics, never by backend.

## Alternatives

- Per-store list grammars, each its own capability — rejected: identical semantics duplicated per backend, with the authoring surface diverging for no user-visible reason.
- Serving the list grammar on only one bundled store — rejected: forces a store migration on anyone who needs list fan-out, though the grammar never touches store internals.
```

### New decision: bundled-recipes-production-paths

```markdown
---
decision: bundled-recipes-production-paths
---

# Bundled recipes demonstrate through production paths

## Choice

Bundled recipes induce the behavior they demonstrate through production paths: the park-then-resume recipe drives a real park through the production parking machinery, never a synthetic conformance probe or test hook.

## Rationale

A recipe's whole value is evidence — an operator runs it to see the production behavior before wiring a real upstream, and a demo that parks via a test hook demonstrates the hook, not the behavior. Fidelity was chosen over the cheaper, more deterministic probe.

## Alternatives

- Inducing the demonstrated state through a synthetic probe or test hook — cheaper and fully deterministic; rejected: proves nothing about the production path the operator is evaluating.
```

### New decision: doc-accuracy-gates

```markdown
---
decision: doc-accuracy-gates
---

# Enumerating documentation is gated against the code facts it enumerates

## Choice

Documentation that enumerates code facts is kept honest by build-time gates that mechanically diff the prose against those facts. Two current instances: the after-code-changes rules gate (recognized filesystem-path citations must resolve against the repo tree) and the substitution-doc gate (the documented source-kind list must match the runtime resolver's dispatch set). New documentation surfaces that enumerate code facts follow the pattern.

## Rationale

Enumerating prose rots silently — a rename lands, the doc still reads plausibly, and the reader hits a missing surface. A mechanical diff at build time catches the drift when it is introduced, not when a reader trips on it. This is the repository's own methodology (mechanical checks are authoritative over prose) applied to documentation.

## Alternatives

- Review discipline — rejected: drift lands silently between reviews, and the reviewer has no enumeration to check against.
- Generating the enumerating prose from the code — avoids drift entirely; rejected: couples doc builds to code internals and surrenders hand-authored framing where the gate keeps prose free and still checked.
```

### New decision: lineage-subscriber-poller

```markdown
---
decision: lineage-subscriber-poller
---

# The lineage subscriber polls the durable projection

## Choice

The bundled lineage subscriber emits by periodically polling the durable lineage projection — reading records newer than its cursor on an interval and forwarding them — rather than registering as a push-style lifecycle subscriber.

## Rationale

The projection is durable, so polling is restart-safe and at-least-once by construction: records written while the subscriber is down are picked up on the next tick, with no registration lifecycle to manage and no live-delivery dependency. A push subscriber would still need a reconciling read to cover missed events, so polling is the load-bearing mechanism either way — the same posture the object-store sensor's deposit detection records (see `decision:deposit-detection-watermark`).

## Alternatives

- A push-style lifecycle subscriber registered with rimsky — rejected: adds a registration lifecycle and a live-delivery dependency, and a missed event during downtime still requires a reconciling read against the projection.
```

### Amend decision: subscription-reconciler

```markdown
---
decision: subscription-reconciler
---

# A reconciliation worker drives publisher Subscribe

## Choice

A reconciliation worker performs Subscribe RPCs for mounting subscription rows at a fixed reconcile interval with no attempt cap; the failed state is reserved for non-retryable errors (e.g. an unknown publisher name); the startup resync pass remains the durable safety net — it lists each publisher's live subscriptions and issues only the rows missing from that set, never re-issuing an already-active subscription.

## Rationale

Retry-forever matches desired-state semantics; bounded retry budgets convert contention spikes into silent failures.

## Alternatives

- A bounded retry budget that lands exhausted rows in failed — rejected: converts contention spikes into silent failures; failed then stops meaning "non-retryable".
- No worker, relying on the startup resync pass alone — rejected: a mounting row would wait for a process restart to mount.
```

### Amend decision: image-entrypoint-role-selection

```markdown
---
decision: image-entrypoint-role-selection
---

# Single-binary multi-role entrypoint

## Choice

The shared entrypoint binary with no command → all roles; a single role command → that role; any other command exits non-zero with an error naming the value. Migrate runs once per deployment, owner role determined by command, and an invocation that owns migration runs it synchronously to completion before starting any role — in all-in-one and split-role topologies alike (see `concept:rimsky`).

## Rationale

One image, many topologies. Failing loud on an unknown command keeps a typo'd role name from silently running the wrong topology, and migrate-before-roles means no role ever sees a half-migrated schema.

## Alternatives

- One image per role binary — rejected: multiplies the build and publish surface for binaries that ship together anyway.
- A mandatory dedicated migration job in every topology — rejected: forces an extra deployment step on the smallest setups; deriving migrate ownership from role selection covers single-container and split topologies without racing or missing runs.
```

### Amend decision: artifact-layout

```markdown
---
decision: artifact-layout
---

# artifact-layout

## Choice

A per-run directory under a stable per-root parent, named by timestamp plus run name, holding the run's state database and its blob store side by side. A pointer entry at the parent level resolves to the most-recent run directory. Both stores stay openable with widely available tooling for their formats — no rimsky-specific reader is required to inspect an artifact.

## Rationale

A single folder per run is the natural archive-and-ship unit. The pointer entry covers the common "open the last one" case without timestamp-parsing. Third-party readability is the artifact's operational value — the operator who inherits a run must be able to open it with standard tooling — so openness is a binding constraint on future storage changes, not a byproduct of today's driver choices.

## Alternatives

- One shared state database across all runs — rejected: loses the copy-one-folder archive-and-ship unit and couples every run's lifecycle to one file.
- Flat per-run files keyed by run id in a single directory — rejected: a run's state database and its blob store no longer travel together.
- A compressed or encrypted rimsky-specific artifact encoding opened through a bundled reader — rejected: trades third-party post-mortem inspection away for storage convenience.
```

### Retire decision: carry-verbatim-requires-one

Delete the file. The decision describes a retired aggregation policy; the live four-value vocabulary is carried by the child-execution and fan-out concepts, and per `decision:pre-v1-pure-removal-for-retired-surfaces` the retired policy's remaining named recognition in code is erased by the work items (generic unknown-value rejection only).

### Amend concept: sensor

Full final body — the only change is the last sentence of the webhook invariant (ack-after-persist, previously held only in story prose).

```markdown
---
concept: sensor
---

# Sensor

## What it is

A sensor is a class of `concept:publisher` implementation that observes external state. Sensors poll, listen, or otherwise watch some out-of-rimsky substrate (clock, HTTP endpoint, object-store prefix, webhook port) and publish messages into rimsky when the watched substrate changes.

Sensors implement the `concept:publisher` protocol — a capabilities handshake, subscribe, unsubscribe, and list-subscriptions — and POST message envelopes to the universal operator message-send endpoint, identifying themselves as publishers and presenting the per-subscription capability token.

The bundled reference implementations are sensors-by-construction; they share no protocol-level surface with rimsky beyond the publisher protocol itself.

## Purpose

To bridge external substrate changes into rimsky's instance frames without requiring rimsky-core knowledge of the substrate. A sensor observes the substrate, builds an opaque payload, and hands it to rimsky as a generic `concept:message`; rimsky routes the message through the existing cascade machinery.

## Boundaries

Owns: the watching loop, the per-substrate dialect, the in-binary per-subscription state (next fire time, body hash, watermark cursor, last idempotency key), and the message-envelope construction at fire time.

Does NOT own: the wire protocol (that's `concept:publisher`), the message envelope shape (that's `concept:message`), the per-instance binding state (that's `concept:publisher-subscription`, stored in the rimsky-side publisher-subscription ledger), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher` (sensors implement it), `concept:publisher-subscription` (sensors hold its publisher-side state in their own per-binary state DB), `concept:message` (sensors send them), `concept:replica` (sensor binaries are single-replica by that concept's posture), `concept:peer-auth` (the webhook sensor's inbound-auth requirement realizes the public-web ingress boundary).

## Invariants

- Sensors are deployed as standalone services advertised in the publisher service registry of `concept:rimsky-yml`. Same deployment model as `concept:claim-producer` or `concept:executor`.
- Templates declare sensors as publisher entries (sensors ARE publishers); at instance creation, rimsky resolves each publisher entry's config via parameter substitution and calls the publisher protocol's subscribe verb.
- At instance termination, rimsky calls the publisher protocol's unsubscribe verb for each registered publisher-subscription.
- Each send constructs a message envelope (see `concept:message`) and posts it to the universal message-send endpoint under an idempotent send. Inert payload per invariant: 24.
- Sensors observe; they do not interpret. Payload bytes flow through rimsky unread until a consumer's substitution leaf walks into them.
- Single-replica per `concept:replica` — operators run one pod per sensor binary; rimsky does not coordinate multi-replica fan-in.
- Emission-failure semantics: a message post rejected by the control API with a permanent 4xx is **dropped, not retried** — the sensor logs loudly (`<sensor>.message_rejected_dropped`, naming the subscription and status) and advances its consumed-state exactly as a successful post would (body-hash cursor, fire-window cursor, watermark, idempotency watermark). Transient failures — transport errors, 5xx, and the retryable 4xx carve-out 408/429 — do not advance state, so the observation is re-attempted on the next cycle. Rationale: retry-forever was never durable (a newer observation supersedes via the hash/state dedup) and it wedges misconfigured watches into permanent retry. The permanent/transient split is machine-detectable via the publisherkit typed rejection error; 408 and 429 retry within the send's attempt budget and surface as transient on exhaustion.
- The webhook sensor requires per-subscription authentication, configured as exactly one of `hmac` (HMAC-SHA256 over the raw body, with an optional timestamp header and replay window), `secret_header` (constant-time compare of a configured header), or `none` (explicit opt-out). Polarity is fail-loud: a subscription with no `auth` block is refused at bind time — the insecure `none` mode must be typed explicitly, mirroring the closed-by-default polarity of the bundled-image egress guard. This closes unauthenticated message injection and forged-idempotency-key pre-seeding on the public-web ingress boundary (see `concept:peer-auth`). The webhook sensor acknowledges an inbound request with success only once its outcome is definitive — the translated message durably persisted in rimsky, or dropped under the permanent-rejection emission semantics above; a transient failure returns non-success so the sender retries. External senders can build their retry logic on the acknowledgment.
```

### Amend concept: claim-producer

Full final body — the only change is the pick-disposition sentence appended to the Split-scope bullet (previously held only in the filesystem story's prose).

```markdown
---
concept: claim-producer
aliases:
  - claim-store
---

# Claim producer

## What it is

A claim producer is an implementation of the claim-producer protocol — four verbs plus capability advertisement — running either as an out-of-process service (capabilities via the startup handshake) or as an in-process bundled handler registered inside the rimsky all-in-one process (capabilities declared at registration). Both shapes are dispatched through the same protocol surface.

The protocol carries two optional methods on the claim-producer service itself, each gated by its own capability flag:

- **Split-scope** — partitions a claim's claim scope into sub-scopes for fan-out. Advertised via a split-scope capability flag. Rimsky opens one sub-claim per sub-scope at parent-acquisition time. Each sub-scope descriptor carries the same substrate-meaningful claim content a regular open returns (scope, address, payload) plus per-partition discriminators identifying the sub-scope within its parent. A sub-claim is a claim; substitution paths over a sub-claim resolve identically to those over a regular claim. Where a producer partitions by picking items from a shared pool, each pick policy declares the item's disposition at the claim's terminal — one action applied at commit, another at abandon — so rimsky's terminal verbs, not producer-side timing, decide when a picked item is consumed or returned.
- **Scopes-conflict** — a producer-aware overlap predicate over two claim scopes. Advertised via a scopes-conflict capability flag. Producers that don't advertise default to byte-equal comparison.

Two further optional mix-ins are separate sibling protocols advertised through the capabilities list rather than a dedicated flag:

- **Validation** — the same validate request/response any service can advertise via the validation protocol. Validates claim bindings at template-registration time against the producer's domain (selector, intent, lifetime, scope). See `concept:validation`.
- **Data-processing** — the control-plane surface for typed-data version lifecycle: begin / commit / abandon a candidate, plus list-versions / list-partitions / get-version-schema. Data motion stays substrate-direct via the acquired result's address; the protocol carries control-plane only. See `concept:data-processing`.

## Purpose

Out-of-process producers let rimsky stay project-agnostic: the producer knows what "the same data" means in its own domain (path canonicalization, MVCC, queue keys) and emits canonical claim scope bytes; rimsky's default conflict predicate is byte-equal, and a producer that needs richer overlap semantics supplies its own predicate via the scopes-conflict capability. A producer can be written in any language; protocol wire compatibility is the only requirement.

## Boundaries

Owns: the producer-side resource state, the canonical claim-scope-bytes emission, the realized write-semantics per claim. Does NOT own: lock state ledger (lives in `claim-handle`), the conflict predicate (lives in rimsky). Adjacent: `claim`, `claim-handle`, `claim-scope`, `write-semantics`, `auto-terminal`, `lifecycle-subscriber` (sibling opt-in protocol on the same service).

For the in-process shape, the handler exposes the full protocol — the four core verbs, any implemented mix-ins (split-scope, scopes-conflict, validation, data-processing), and capability advertisement — through a handler-side interface consumed by the in-process dispatch path. For the out-of-process shape, the same surface is served over gRPC. See `decision:parallel-inproc-claim-producer-registry`.

A producer may register the executor protocol alongside the claim-producer protocol on the same endpoint to support verification of its own staged content; see `concept:executor`.

## Invariants

- The claim-producer protocol — its verbs and capabilities handshake — is the only contract. Rimsky depends on the protocol; no concrete-producer dependency is permitted.
- Producers do not persist lock state (invariant 9a) and do not internally serialize on lock-shaped predicates (invariant 9b).
- Producers MUST satisfy byte-equal-claim-scope uniformity: two open calls returning byte-equal claim scope MUST also return the same realized write semantics.
- Terminal verbs (commit/abandon/release) must be idempotent in the claim identifier: rimsky delivers them at-least-once from a durable, ordered, per-producer outbox (redelivery after a crash between delivery and acknowledgment bookkeeping is legal), and a recovering producer receives each scope's undelivered terminals in order before any new open against that scope.
- The two shapes exhibit protocol equivalence: an in-process handler and its gRPC-wrapped counterpart share the same underlying implementation, so any capability advertised by one is advertised by the other, and the capability envelope (realized write semantics within the advertised set, split-scope and scopes-conflict gated on their flags) is enforced identically on both dispatch paths.
```

### Amend concept: host-agent

Full final body — the only change is in the spawn-inheritance invariant: the argv / working-directory / ready-timeout defaults join the env default (previously held only in story prose).

```markdown
---
concept: host-agent
---

# Host agent

## What it is

A long-running daemon on a user's dev machine. Dials a `concept:host-agent-proxy` outbound over TLS — verifying the proxy's server certificate against a pinned deployment-CA root — and authenticates inside that encrypted channel as the USER, carrying the user's `concept:api-key`, or, when none is configured, an **anonymous routing identity** — either an agent-supplied routing label, or a persistent silly-name assigned by the proxy on first connect and re-presented thereafter (see `concept:host-agent-proxy`). Serves spawn / dispatch / reap requests against locally-running binaries, and relays local-HTTP callback requests those binaries raise on to the proxy.

## Purpose

Lets users run arbitrary local binaries as rimsky services on a per-invocation basis without static deployment configuration. Eliminates the manual "start the local process, wire up reachability, trigger an instance, tear down on completion" setup that would otherwise be required for dev workflows.

## Boundaries

Owns: dev-machine process spawn/exec, its two local listeners (a bootstrap-enroll surface and a mutually-authenticated dispatch/callback surface), the agent-side end of the agent ↔ proxy bidi stream, child-process reaping on Reap or connection close, the self-contained LOCAL certificate authority that secures the agent↔spawned-child loopback — a trust domain separate from the deployment's `concept:peer-auth` CA, minting no `concept:api-key` ledger rows and requiring no `concept:permission` — and a local state file persisting the anonymous routing identity assigned by the proxy so that reconnects preserve routing continuity. Does NOT own: service discovery, capability advertisement (the spawned binary advertises its own Capabilities via a handshake the agent itself drives against the child, on protocols the proxy names), any across-restart behavior state beyond the anonymous identity, the supervisor-facing service protocols (those live on the proxy), a client certificate for the agent→proxy hop (on that hop the agent is user session tooling, not a service enrolled in the deployment CA — it authenticates to the proxy by api-key over TLS rather than by mTLS enrollment — see `concept:peer-auth`). Adjacent: `concept:host-agent-proxy`, `concept:service`, `concept:api-key`, `concept:peer-auth`, `concept:anonymous-mode`.

## Invariants

- No capability config; the agent does not know in advance what binaries exist, though spawn paths may optionally be bounded by an operator-supplied path-glob allowlist, refusing any spawn outside it.
- Path resolution happens at exec time, without a shell: absolute paths are used as-is, relative paths resolve against the spawn's working directory, and bare names resolve via the system's default executable-search mechanism.
- The agent↔spawned-child loopback runs mandatory, always-on mutual mTLS, and the agent is a self-contained LOCAL enrollment authority for it: on startup it generates a local CA (reusing the same certificate-authority machinery `concept:peer-auth` uses) and issues itself a leaf, and it serves a plaintext bootstrap enroll endpoint on a local listener. The CA is a trust domain separate from the deployment's `concept:peer-auth` CA — it mints no `concept:api-key` ledger rows and needs no `concept:permission` — so the loopback is secured independently of the deployment's peer-auth posture and does not require the deployment to run in mTLS mode.
- Spawned children inherit the agent's full environment, overridable per binding on any name collision by that binding's own declared env vars; the child's argv comes from the binding's declaration, and a binding that leaves working directory or ready-timeout unset falls back to the spawn's global values. Spawning adds a per-spawn provisioning that makes the child enroll: the agent mints a fresh bootstrap token and stamps the child's environment with the peer-auth mode, its api-key credential, its control-API endpoint pointing at the agent's local enroll listener, and the agent's own routing identity — so work a child executes is attributable to the serving agent. The child self-enrolls exactly as any service enrolls under `concept:peer-auth` (the bundled peer-auth path, no executor-side code change), and the agent validates its own token and issues the child a short-lived leaf from the local CA.
- Both loopback legs — the agent→child dispatch and the child→agent callback — verify the peer's local-CA leaf. A port-squatter or a plaintext-only binary holds no such leaf and fails the handshake, so forged dispatches, forged callbacks, and dispatch interception all fail closed in one mechanism. A late-bound binary that speaks only plaintext is not a valid executor.
- On bidi-stream close (clean or unclean), all live children are sent a terminate signal and force-killed after a configurable grace period.
- The agent picks the child's listening port itself (an OS-assigned free port, not a binding declared anywhere) and tells the spawned child which port to bind; the agent then polls that port until the child's server answers, bounded by the spawn's ready-timeout. There is no port handshake back to the agent — a spawned binary that does not bind the port it was told fails the readiness poll and the spawn is reported failed.
- **Anonymous identity persistence.** When the agent authenticates as anonymous (no user api-key configured), the routing identity the proxy adopts — an agent-supplied routing label or a proxy-generated silly-name — is persisted in a local state file under the agent's config directory. On subsequent starts the agent re-presents that identity so previously-created instances targeting it continue to reach it after a reconnect. This is the agent's ONLY across-restart behavior state; other on-disk state is informational only and is never read back to affect behavior. Authentication is either the user's `concept:api-key` or — in anonymous mode — the persisted anonymous routing identity, verified by the proxy before any routing.
```

### Amend concept: control-api

Full final body — the only change is the health-probe sentence in the auth-gating invariant (success semantics, previously held only in story prose, narrowed to what is implemented: persistence availability).

```markdown
---
concept: control-api
---

# Control API

## What it is

The operator interface exposed by the control-api binary. Serves multiple protocol skins on the same TCP port and the same operation set, covering template registration, instance lifecycle, per-instance breakpoint management, the auth surface, observability reads, and admin diagnostics. One skin is a direct request/response surface intended for scripts and operator tooling; another is an agentic-tool surface whose catalog is computed from the canonical action registry and filtered by the requesting key's permission grant.

Both skins pass through the same auth + permission middleware. Fires lifecycle-subscriber events at state transitions (synchronously; see `concept:lifecycle-subscriber`).

## Purpose

The operator, the rimsky thin-client CLI, and agentic clients all speak to this surface. The simpler request/response skin is easier to script, expose through ingress, and inspect during incidents. The agentic-tool skin is the operator-facing surface for LLM-based agents that can self-discover the catalog and dispatch tool calls.

## Boundaries

Owns: the operation surface and its handlers, the lifecycle-subscriber fan-out for template and instance events and for the administrative-termination run-scope-terminal (every remaining scope in each frame's tree, children before parents, with re-offers to unacknowledged peers), the observability read handlers, the auth middleware and endpoint surface, the agentic-tool envelope handler and catalog, and — under `peer_auth: mtls` — the enrollment endpoint where a `service:enroll`-bearing key exchanges for a short-lived certificate plus the CA root. The control plane hosts the per-deployment CA and is the identity authority the other trust boundaries defer to. Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), the sub-graph and fan-out-partition run-scope-terminal fan-out (fired by the supervisor when it closes those scopes at rendezvous), the settlement-time root run-scope-terminal fan-out (fired by the scheduler's frame engine when a frame settles), the out-of-process service protocols, the certificate lifecycle on the service side (memory-only, auto-renewed — see `concept:peer-auth`). Adjacent: `rimsky` (CLI), `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`, `peer-auth`.

## Invariants

- The operation surface serves a single wire version at a time, with no version negotiation and no multi-version compatibility. Rolling upgrades are operator-managed. The URL path convention (whether and which version prefix appears) is a wire detail the design does not fix.
- Lifecycle events fire synchronously from the process that owns the state transition: control-api for template and instance events and the administrative-termination run-scope-terminal, the scheduler's frame engine for the settlement-time root run-scope-terminal, the supervisor for sub-graph and fan-out-partition run-scope-terminal at rendezvous. A slow subscriber holds up the firing process's path — not necessarily the response to the request that triggered the transition, since a gracefully terminating instance's terminated event fires only once the transition actually completes.
- The reserved `compose:` prefix on tags and instance keys is server-enforced: requests originating outside the CLI's compose surface (`concept:rimsky`) are rejected when they target it.
- **Every operation is auth-gated** except the health probe, which is an unauthenticated infrastructure path. The probe returns success while persistence is available and non-success when it is not — persistence availability is the one dependency it checks. The action registry is the canonical surface-to-action mapping; an unmapped operation is a wiring bug.
- **Auth gating cannot be constructed away.** The control plane refuses to come up without an auth state wired in — there is no configuration or startup path that serves the operation surface ungated. The zero-configuration entry point for a fresh deployment is `concept:anonymous-mode`, which is itself gated (and audited) rather than a bypass of the gate.
- **No operation is reachable through two aliased routes.** An action may legitimately expose several routes when each addresses a genuinely distinct resource (a collection versus an item, or a lookup keyed a different way); it may not expose two routes that address the same resource under different paths. The action registry is the single source of the surface-to-route mapping, and pins each action's route set so a second, redundant path cannot silently accrete.
- **The agentic-tool skin shares the auth gate.** Tool invocations re-enter the routing pipeline via the catalog's invoke path, so the same action-gating middleware runs; the audit row records the protocol skin used. The skin also exposes an MCP resource surface for breakpoint-hit discovery, gated by the endpoint-level auth plus a per-read permission check rather than by router re-entry.
- **Enrollment is gated by `service:enroll`.** Under `peer_auth: mtls` the enroll endpoint passes the same auth middleware and requires the `service:enroll` grant; it issues a short-lived leaf certificate whose SAN binds the calling key's id. The control plane fails closed at startup when `mtls` is on but the CA encryption key is missing or malformed (see `concept:peer-auth`).

## Skin-as-implementation

The agentic-tool skin is hosted in-process by the control-api. Tool invocations dispatch back into the router via an in-process handler — there is no self-loopback round trip.
```

### Amend concept: cascade

Full final body — the only change is the new run-scope-boundary invariant (the ratified 2026-07-14 two-boundary/opacity ruling, previously recorded only at transcript tier; `concept:run-scope` already defers its cascade-edge semantics here).

```markdown
---
concept: cascade
aliases:
  - reactive-cascade
---

# Cascade

## What it is

Cascade is the engine that turns one node-state transition into the set of downstream node-state transitions. Three precise words name its parts:

| Word | Meaning |
|---|---|
| **walk** | The event-driven traversal of the graph, fired inline inside the transaction that settles the triggering `terminal/success`, `terminal/error/<class>`, or `attribute/<key>/changed` signal — not a separate topology-ordered pass. The mechanism. |
| **propagation** | Cascade-of-stale on a subscription-edge match (sender node-type × emitted settling signal type) whose `when:` predicate evaluates true. Mark dependents stale, insert wait-set rows, and recurse; the gate evaluator advances a receiver from pending to stale once its wait-set drains. |
| **fallthrough** | No-dispatch fresh-roll for a node with no executor. Roll fresh state forward without running the node; detected per-node (executor unset) and executed by the scheduler's tick-driven pure-cascade sweep, which records the transition-reason `pure_cascade`. |

One walk; two node-level behaviors (propagation, fallthrough). The walk itself is event-driven, firing inside the settling terminal's own transaction; only the fallthrough behavior's actual node advancement runs off a scheduler tick, via the pure-cascade sweep.

## Purpose

A reactive graph orchestrator only earns its keep if a single executor's "I changed" signal causes the right downstream nodes to recompute and no others. Cascade is the mechanism that turns one terminal outcome into the set of downstream node-state transitions.

## Boundaries

Owns: the firing-gate predicate, the downstream walk, the two node-level behaviors (propagation vs fallthrough). Does NOT own: invalidate emission (see `concept:message`), frame creation (see `concept:frame`), terminal-handler resolution (see `concept:terminal-resolution`), the queue-shaping rules applied at gate-clear (see `concept:cascade-mode`). Adjacent: `concept:message`, `concept:signal`, `concept:transition-reason`, `concept:frame`, `concept:terminal-resolution`, `concept:cascade-mode`.

The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); upstream-refresh edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams a receiver names with an upstream-refresh subscription. Under the subscription-edge map's empty sender-key, runtime-injected structural-root edges live — consulted when the implicit empty-message receiver settles, waking every structural root of the template.

## Invariants

- **Cascade fires only on settling signals.** The firing-gate predicate admits exactly the settling signal kinds — `terminal/success`, `terminal/error/<class>`, and `attribute/<key>/changed` — and rejects every other signal kind outright, before any subscription-edge lookup runs. A single predicate is the sole authority for both what a template may subscribe to and what actually reaches the walk at runtime, so the subscribable set is derived from that predicate rather than tracked as a second, independently-maintained list. Dispatch-internal signals never cascade and are not subscribable; template registration rejects any subscription that targets one.
- A cascade walk inserts a wait-set row for the receiver whenever a subscription edge matches the emitted signal's type AND the subscriber's `when:` predicate evaluates true; the receiver is unconditionally stale-marked on that match — every declared subscription is a wake declaration, there is no separate wake opt-in. An upstream-refresh edge additionally inserts a wait-set row for a receiver's own named upstream independent of any emitted signal or filter, proactively invalidating that upstream within the same walk.
- Cascade always happens in a frame.
- The cascade walker operates entirely within a single frame. It never creates a new frame; cross-frame coupling is expressed by message-sender nodes whose dispatch lands a message in the ledger, with the next frame opening on the standard delivery path.
- **Cascade crosses a run-scope boundary at exactly two places, and nowhere else**: a sub-graph invocation's entry, where the calling node's success seeds the sub-graph's internals, and a fan-out parent's settlement, where the partitions' rendezvous bridges back to the parent. Partition-internal cascades never propagate outward, and an outside cascade fires the calling node without descending into sub-graph internals — sub-graphs are externally opaque to cascade (`concept:run-scope` defers its cascade-edge semantics here).
- Settled-color — the fresh/failed label a node-run's terminal outcome carries — is informational. The functional equivalent of suppressing downstream auto-fire on a failed sender is expressed receiver-side via subscribers' `when:` predicates or via not subscribing to `terminal/error/*` at all.
- **In-flight node-runs are sealed against cascade-driven mutation, with one narrow parked-wake carve-out.** A node-run in any in-flight state (`pending`, `stale`, `running`, `held`, `parked` per `concept:node-run`) is never re-invalidated and never has its persisted attribute bag rewritten by anything other than its own executor's writeback. When a cascade walk targets a receiver that already has an in-flight run, the walker creates a NEW cascade-driven pending row (or accumulates the wait-set entry into the latest pending per the per-sender-node accumulation rule below); the existing in-flight run's bag and identity are left untouched. The one state mutation the walker performs on an existing run: a PARKED receiver run is woken in the walk's transaction — parked → stale through the single parked-wake path, resume-at cleared, wake event appended — so the interrupted work resumes promptly instead of sleeping until its resume-at while fresh upstream information waits behind it (see `concept:parked-state`). The woken run re-dispatches with its own preserved bag and scratch; the cascade round itself still lands on the new pending row's wait-set, and the downstream sees the upstream's freshened value only at the dispatch of that new node-run, which the dispatcher claims after the woken run settles (the serialization gate refuses to claim while the same (node, run-scope) has a run in {running, held, parked}).
- **Per-sender-node accumulation rule** (the walker's accumulate-or-queue gate): on each cascade walk, find the receiver's latest cascade-driven pending in the current frame. If none exists, create a new pending. If the triggering sender node-run already has a wait-set row on that pending, accumulate — a second matching signal from the same node-run (for example a terminal signal followed by an attribute-changed signal from the same settle) never opens a new pending. Otherwise, if the sender's node is NOT in that pending's wait-set sender-nodes, accumulate (insert the wait-set row into the existing pending). If the sender's node IS already in that pending's wait-set sender-nodes from a different node-run, create a NEW pending (the previous pending is sealed; subsequent cascades from other sender-nodes accumulate into the new one). Multiple cascade-driven pendings can coexist per (receiver, run-scope, frame); the latest is always the accumulation target. See `decision:walker-rule-per-sender-node` for the rationale.
- **Cascade-defer for held**: when a node-run's terminal includes a held=true claim, the cascade walker fires immediately at the held terminal but filtered to subgraph co-members only — held-subgraph members keep cascading among themselves during the hold. Non-member receivers are deferred until the auto-terminal handler resolves the holder's full claim portfolio: Commit walks with `terminal/success`; Abandon walks with `terminal/error/abandoned`. See `decision:held-as-state-not-phase` and `decision:terminal-error-abandoned-as-error-class`.

## Common pitfalls

- **Rimsky's cascade is not CSS cascade.** CSS's cascade resolves competing style rules by specificity and order; Rimsky's cascade propagates staleness through the per-template subscription-edge inverse map. The two share a name and nothing else.
- Treating "recalculate" as a second message. Staleness propagation is a graph-traversal step, not a service message that travels alongside; recalculation is what the scheduler does next.
- Expecting cascade to skip nodes whose new value would be byte-identical to the old. Cascade is subscription-driven, not value-diff-driven; the executor commits a "no change" indicator on its emitted payload if it wants downstream subscribers that filter on that indicator to suppress.
- Confusing cascade reach with executor invocation. Cascade creates new pending rows and inserts wait-set rows; the gate evaluator transitions pending→stale when the wait-set drains and no subscribed upstream has a genuinely blocking in-flight run — a held upstream that is a subgraph co-member of the receiver does not gate it. At that same gate-clear point the node's `concept:cascade-mode` rule can drop the pending outright instead of advancing it. The dispatcher claims a stale row when the serialization gate (no same-(node, run-scope) run in running/held/parked) clears. Cascade does NOT directly invoke executors; it queues work.
- Treating error-class subscribers as automatically downstream-firing. Under the subscriber-driven cascade model, an error-class subscriber fires only if it has declared the subscription; the sender's color does not fire downstream nodes by itself. A node that wants to halt propagation on errors simply omits the subscription; a node that wants to act on every error subscribes broadly.
```

### Amend story: claude-agent

```markdown
---
story: claude-agent
---

# Operator wires agentic node with full controls

## Story

As an operator wiring an agentic node, I can use the bundled claude-agent executor to dispatch async agent work, let template authors declare each node's MCP servers and expose-env needs inline in node config while I hold operator allowlists bounding both, gate the run with a cryptographic sign-off over the real bound output, and observe the declared error classes routed via policy or subscribed via wildcard, so that I run controllable, secure, observable agentic dispatches.
```

### Amend story: sensor-cron

```markdown
---
story: sensor-cron
---

# Operator wires durable cron-driven message

## Story

As an operator wiring a cron-driven message into a workflow, I can use the bundled cron sensor to fire at declared cron expressions with firing position surviving process restarts, so that time-driven work enters the workflow on schedule with no external scheduler to run.
```

### Amend story: sensor-webhook

```markdown
---
story: sensor-webhook
---

# Operator wires inbound-webhook message

## Story

As an operator wiring an inbound-webhook-driven message into a workflow, I can use the bundled webhook sensor to expose authenticated HTTP routes that translate inbound POSTs into messages for the subscription's target instance, so that external systems trigger rimsky nodes via webhooks without polling overhead.
```

### Amend story: claim-producer-filesystem

```markdown
---
story: claim-producer-filesystem
---

# Operator uses filesystem-backed claim-producer

## Story

As an operator wiring a workflow whose claims persist on a POSIX filesystem, I can use the bundled filesystem claim-producer to acquire directory-per-scope claims with synchronous in-place write semantics and partition fan-out work from the store's own contents, so that I get production-grade claim semantics on plain files without standing up a database.
```

### Amend story: bundled-park-resume-recipe

```markdown
---
story: bundled-park-resume-recipe
---

# Operator demonstrates park-then-resume on the bundled stack

## Story

As an operator evaluating rimsky's parking behavior, I can run a self-contained, copy-runnable recipe on the bundled stack that drives a node through a real park and its resumed completion, so that I can see park-then-resume work end to end before wiring a real rate-limited upstream.
```

### Amend story: host-agent-control-plane

```markdown
---
story: host-agent-control-plane
---

# Operator manages agent lifecycle via CLI

## Story

As an operator running rimsky-dispatched workflows on a dev machine, I can start the host-agent locally, check its connection status, and stop it cleanly (children reaped) through the host-agent control-plane CLI surface, so that I manage the agent's lifecycle from the same CLI that drives the rimsky stack.
```

### Amend story: host-agent-per-binding-overrides

```markdown
---
story: host-agent-per-binding-overrides
---

# Per-binding env/args/cwd/timeout honored

## Story

As a template author declaring late-bind bindings for varied local binaries, I can specify per-binding env vars, command-line args, working directory, and spawn timeout, so that I run different binaries with different configuration through the same agent without global config soup.
```

### Amend story: publisher-protocol

```markdown
---
story: publisher-protocol
---

# Service author writes custom publisher

## Story

As a service author, I can write a custom publisher (or sensor) that plugs into a rimsky stack, so that my service feeds messages into workflows and its subscriptions survive rimsky restarts without being re-issued.
```

### Amend story: rimsky-deployment-bootstrap

```markdown
---
story: rimsky-deployment-bootstrap
---

# Entrypoint role selection + migrate discipline

## Story

As an operator deploying rimsky to a stack, I can run the bundled multi-role entrypoint with no command to launch all three roles together for dev (or as a single role for multi-process production), and trust that database migrations run exactly once per deployment regardless of role split — never racing across roles, never silently skipped — with an explicit environment-variable override for one-shot init containers, so that the deployment topology is whatever I choose and the schema arrives at the right state deterministically.
```

### Amend story: rimsky-health-check

```markdown
---
story: rimsky-health-check
---

# Health probe surface for LBs and k8s

## Story

As an operator running rimsky behind a load balancer or container-orchestrator probe, I can query the unauthenticated deployment-health probe (or the health CLI verb) and get success while persistence is available and non-success when it is not, so that I gate traffic on a real health signal rather than a silently degraded happy path.
```

### Amend story: rules-doc-accuracy

```markdown
---
story: rules-doc-accuracy
---

# Contributor trusts rules citations

## Story

As a contributor following the project's after-code-changes verification rules, I can trust that a path the rules cite in a recognized filesystem-path shape resolves to a real repo artifact, and that a curated set of known-dead references never creeps back in, so that acting on the documented verification steps is unlikely to hit an obviously missing surface.
```

### Amend story: substitution-doc-accuracy

```markdown
---
story: substitution-doc-accuracy
---

# Substitution doc matches resolver

## Story

As a template author reading the substitution documentation, I can trust that the listed source kinds match exactly what the resolver actually recognizes, so that I don't silently miss a supported source.
```

### Amend story: single-process-all-in-one

```markdown
---
story: single-process-all-in-one
---

# Operator runs the all-in-one deployment as one process

## Story

As an operator running the all-in-one deployment, I get one process serving all three roles (scheduler, supervisor, control-api), so that the deployment is genuinely unified — including the memory blob backend working there, because the roles actually share a process.
```

### Amend story: verifier-severity-partition

```markdown
---
story: verifier-severity-partition
---

# Template author distinguishes warning vs error

## Story

As a template author declaring data-quality checks, I can label a check with the warning or error severity and have the verifier honor the partition — failing-warning is non-blocking, failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.
```

### Amend story: subscriber-lineage-receiver

```markdown
---
story: subscriber-lineage-receiver
---

# Operator emits lineage events to an external receiver

## Story

As an operator running rimsky in a data-platform environment, I can use the bundled lineage subscriber to deliver run-lineage records to an external lineage receiver, so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.
```

### Amend story: audit-artifact

```markdown
---
story: audit-artifact
---

# Operator inspects the durable record of a completed one-shot run

## Story

As an operator, I can inspect the durable record of a completed one-shot run, so that I can debug failures and verify successful runs without re-running.
```

## Work items

Flat and unordered; the only real dependency is stated on the item that has one.

- **Apply the story reductions and the list-fanout collapse** (`story:fanout-list-array`, and the sixteen amended stories above; retires `fs-fanout-list-array` and `pg-fanout-list-array`). Copy the final-form bodies into place, delete the two retired story files, and refresh the stories TOC. Repoint the one code annotation citing a retired slug (`@story: fs-fanout-list-array`, in the sub-claim runner test) to `fanout-list-array`, and confirm the annotated test exercises the merged story end-to-end against at least one bundled store; sweep for any other annotation citing either retired slug.
- **Add the shape-checks verifier story** (`story:verifier-shape-checks`). Copy the final form into place; annotate the end-to-end test that drives the shape-checks verifier through a real dispatch with `@story: verifier-shape-checks` (add such a test to the ordinary suites if none exists). Reconcile with `story:verifier-severity-partition`: the partition story keeps severity; the new story owns the check capability.
- **Create the five new decisions** (`decision:claude-agent-error-classes-closed`, `decision:fanout-list-array-store-agnostic`, `decision:bundled-recipes-production-paths`, `decision:doc-accuracy-gates`, `decision:lineage-subscriber-poller`). Copy final forms into place, refresh the decisions TOC, and leave `@decision:` annotations at the enforcement sites: the claude-agent emission gate that rejects undeclared classes, the lineage subscriber's poll loop, the two doc-gate tests, the recipe's park-induction site, and the split-scope list handling in each bundled producer.
- **Complete the three amended decisions** (`decision:subscription-reconciler`, `decision:image-entrypoint-role-selection`, `decision:artifact-layout`). Copy final forms into place.
- **Erase carry-verbatim** (realizes the retirement of `decision:carry-verbatim-requires-one`; depends on nothing, but the file deletion and the code change land together). Delete the decision file; in the template validator, fold the named `carry_verbatim` case into the generic unknown-value rejection so no named recognition of the retired policy survives — the generic message listing the four valid kinds is the only failure mode — and remove the `@decision: carry-verbatim-requires-one` annotation; update the validator tests to assert the generic rejection.
- **Apply the five concept amendments** (`concept:sensor`, `concept:claim-producer`, `concept:host-agent`, `concept:control-api`, `concept:cascade`). Copy final forms into place; refresh the concepts TOC only if a summary line changes (none is expected to).

## How to execute this sprint

This sprint is self-sufficient. Whoever executes it — an inline
working session, an agent this file is handed to via the native
`goal` mechanism, or an orchestrator that does its own planning —
proceeds the same way.

1. Read the sprint whole first — intent, deltas, work items,
   completion contract — before touching anything. Do not go looking
   for context behind it (not in the issue intake under
   `.ok-planner/issues/`, not in `history/`). The sprint is
   self-sufficient by construction; a genuine gap is raised with the
   owner, never filled by inference.

2. Stage the work into a task list. The items above are a flat,
   unordered list; group them by theme, file surface, or dependency,
   order the groups so nothing is built on something not yet there,
   and build the list in your own working state — the harness's task
   tracking where available, one entry per stage; an orchestrator
   uses its own graph. Seed the closing entries up front — finish
   the completion report, run `/certify-work` with this sprint's
   path as its argument, walk the presentation, offer
   archive-and-commit — so the ceremony is a
   standing unchecked item from the first minute, not a memory to
   retain past a long run. Staging is never rewritten into a plan
   document: this sprint is the whole brief.

3. Apply each corpus delta as part of the work that realizes it —
   copy the final-form body into `.ok-planner/design/` verbatim, or
   delete the file for a retirement. A delta no work item implements
   (a clarification, a retirement) is applied on its own.

4. Build stage by stage. Every new or amended story whose substance
   is implemented in code is exercised end-to-end by a test in the
   project's ordinary suites, carrying the `@story:` annotation for
   navigation — that annotation is also how the periodic audit finds
   the test later. No test ever checks the existence of static text,
   code, or prose: a commitment realized in prose carries no test.
   Write the tests with the work, not at the end.

5. Completeness is the floor. Never stub, defer, narrow, no-op, or
   leave a `TODO` in place of a promised outcome. A capability the
   deltas or work items promise is delivered in full, or the blocker
   that prevents it is surfaced — never silently dropped.

6. Never destroy uncommitted work. Stage progress as each stage
   finishes (`git add -A`) so a stray revert cannot reach it. Do not
   run `git checkout`/`restore`/`reset`/`stash`/`clean` on your own
   initiative; fix a bad edit forward by editing again.

7. Work unsupervised to a defensible done — no pausing for approval,
   confirmation, or progress checks. Stop only on a genuine blocker:
   a credential or access that cannot be obtained, a step literally
   impossible in the current state, a destructive/irreversible
   action not clearly authorized — or the closing `/certify-work`
   step being unrunnable for you (e.g. its subagent dispatches are
   unavailable): surface that and stop; never skip the ceremony and
   call the work done. Ambiguity is not a blocker — pick
   the most plausible reading and continue, surfacing the choice at
   the end. (An orchestrator that supervises its own executors folds
   this into its own control.)

8. Keep the completion report current. Beside this sprint file lives
   its report — same filename with `-completion` before the
   extension — and you write it as you go: as each stage lands,
   record what was done, every divergence, and every call you made
   where the sprint was silent. It is the durable record the closing
   ceremony finishes and walks with the owner, the artifact a goal
   checker requires, and it is archived together with this sprint.
   It is a record of this execution, never a plan document.

9. Close by running `/certify-work` with this sprint's path as its
   argument — the argument is what puts the sprint in the gate's
   scope; the gate never adopts one on its own. It brings the work into
   alignment with this sprint and discharges the completion contract
   below at the change's own scope: the project's own test suites
   over the touched work, change-scoped corpus checks over the
   touched artifacts and annotations, code review over the diff —
   all producers feeding a no-discretion review-fix loop (a fixer
   fixes everything a reasonable owner would wave through; an
   architect adversarially checks its kickbacks, fixing the refuted
   and promoting only genuine intent forks to the issue intake),
   and the outcomes and divergences are presented to the owner.
   (Whether the corpus's claims still hold is the periodic
   `/verify-corpus` run, on the owner's cadence, never this close.) The goal is to finish the work: this
   file stays in `sprints/` through the presentation (so a stop
   condition keyed to its path can verify completion against it),
   and `/certify-work` ends the run as the ceremony: it writes its
   composed presentation into the completion report (finishing the
   record kept in step 8), walks it with the owner, and offers the
   close-out — archiving this sprint together with its completion
   report and the issue files it resolved to `history/`, and
   committing the work — performed only on the owner's word. The
   close-out then stamps the archived sprint's frontmatter with
   the closing commit (`closed: <sha>`, one small follow-on
   commit): the baseline the next planning ceremony uses to
   detect work done out of band.

## Completion contract

The work is not done until all of the following hold, each
verifiable from the repository as it stands:

1. The design corpus matches every delta above (applied verbatim).
2. The project's own test suites pass, and every new or touched
   story implemented in code is exercised end-to-end by a test the
   suites run.
3. The completion report beside this sprint (same filename with
   `-completion`) is finished: it records the work done and the
   divergences, and carries `/certify-work`'s presentation — the
   review-fix loop run last and come back clean, every finding
   fixed or promoted-and-verified.

**The goal rule, for any checker verifying this contract.** The goal
is met in exactly two ways: this sprint file has moved to
`.ok-planner/history/sprints/` bearing a `closed:` stamp — the owner
accepted and closed the work; terminal, stop checking — or this file
is still at its `sprints/` path and items 1–3 all verify against the
repository. A missing completion report means NOT done, however
green the rest looks; an archived, stamped sprint means DONE,
whatever else seems unfinished. A run parked at the review-fix
loop's cycle cap awaiting the owner's direction is a legal in-flight
state — not done, not failed, and never grounds for the run to take
either cap step itself. Nothing else counts either way.
