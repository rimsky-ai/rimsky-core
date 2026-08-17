# Sprint: Accepted intake drain

## Intent

Work the 42 accepted issues in `.ok-planner/issues/accepted/` to completion. Every one carried a generated ruling from `/verify-issues`; the owner accepted all 42 as a batch and left the remaining intake alone. The sprint has no theme. Twenty corpus amendments bring stale text up to what the code does. The work items fix the code where the corpus was right and the code was wrong.

Issues promoted into this sprint:

`abandoned-rationale-names-two-nonexistent-error-classes`,
`acquire-phase-carveout-count-is-five-not-two`,
`blob-intx-helpers-take-optional-transaction`,
`callback-listener-mounts-a-bare-liveness-path`,
`child-execution-close-paths-incomplete`,
`claim-handle-expiry-renewal-unguarded`,
`claude-agent-http-bridge-path-mismatch`,
`claude-agent-operator-envs-not-service-namespaced`,
`cli-register-drops-validation-warnings-on-success`,
`cli-reports-a-write-that-dry-run-mode-refused`,
`dispatcher-claims-by-enqueue-time-not-sequence`,
`egress-guard-absent-from-verifier-http`,
`empty-tls-does-not-default-to-off`,
`event-payload-fields-are-not-free-form-maps`,
`event-payload-messages-shared-across-kinds`,
`go-get-root-module-cannot-resolve`,
`handler-context-has-no-scratch-accessor`,
`hmac-timestamp-header-is-mandatory-not-optional`,
`host-agent-spawn-path-anchor`,
`keepalive-also-renews-claim-expiry`,
`late-bind-claim-producer-name-unresolvable`,
`lifecycle-conformance-suite-lacks-its-own-package`,
`malformed-cursor-answers-500-with-store-error-text`,
`messages-tail-prints-only-the-newest-row`,
`observability-frames-omit-message-join`,
`parallel-cap-decision-has-no-fitness-test`,
`parity-suite-misses-nine-runtime-depended-methods`,
`parked-force-fail-omits-sibling-cancel`,
`parked-run-discarded-on-upstream-cascade`,
`permanent-rejection-advances-deposit-watermark`,
`remote-run-diverges-from-exit-code-classes`,
`retry-cap-does-not-precede-policy-lookup`,
`role-orchestration-is-shared-not-mirrored`,
`semver-detector-misses-cli-flags-and-root-module`,
`sensor-webhook-outside-port-precedence`,
`settling-payloads-omit-tags-and-attributes-delta`,
`store-specific-partition-idiom-count-stale`,
`strict-aggregate-cancel-action-inert`,
`subgraph-builtin-kind-node-never-dispatches`,
`template-delete-refusal-escapes-as-server-error`,
`three-service-paths-are-http-not-grpc`,
`wait-set-insertion-path-not-single`.

## Corpus deltas

### Amend concept: host-agent

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/host-agent.md`)

### Amend concept: claim-handle

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/claim-handle.md`)

### Amend concept: child-execution

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/child-execution.md`)

### Amend concept: terminal-resolution

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/terminal-resolution.md`)

### Amend concept: event-log

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/event-log.md`)

### Amend concept: parked-state

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/parked-state.md`)

### Amend concept: wait-set

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/wait-set.md`)

### Amend concept: sensor

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/sensor.md`)

### Amend concept: peer-auth

body: in the sidecar (`2026-08-17-accepted-intake-drain-deltas/concepts/peer-auth.md`)

### Amend decision: deposit-detection-watermark

```markdown
---
decision: deposit-detection-watermark
---

# Deposit detection is polling with a durable watermark, at-least-once

## Choice

Deposits are detected by periodically listing the watched location and comparing against a durable per-subscription watermark and seen-set, persisted in the sensor's state store. Each discovery publishes with an idempotency key derived from subscription, object name, and content etag. A transient publish failure — a transport error, a 5xx, or the retryable 408/429 carve-out — leaves the watermark where it is, so delivery is at-least-once with downstream dedup by key. The sensor logs a permanent rejection, drops it, and advances its consumed state exactly as a success would.

## Rationale

Listing is the lowest common denominator every storage technology offers, so polling is the only detection that works uniformly across backends — which is what keeps the single-abstraction choice honest (see also: object-store-watching-model). The durable watermark is what turns polling into a promise: restarts do not re-trigger consumed deposits, and content deposited during downtime is caught on the next poll rather than lost. At-least-once with idempotent keys puts the dedup burden where it is cheap (a key comparison downstream) instead of where it is expensive (transactional exactly-once in the sensor). Consuming a permanent rejection rather than retrying it keeps a misconfigured watch moving; a newer observation supersedes the dropped one through the hash and state dedup anyway (see `concept:sensor`).

## Alternatives

- Stateless polling — trivially simple, but every restart re-triggers the world.
- Exactly-once delivery via a transactional outbox in the sensor — heavier machinery for a guarantee downstream idempotency already provides.
- Holding the watermark on a permanent rejection too — rejected: it retries a request the server will refuse identically forever, and the watch never advances past the bad object.
- Operating-system filesystem-change notification for the filesystem backend, with bucket-notification analogs for object stores — the mechanisms are per-backend, are undelivered or lossy across exactly the deployment boundaries this sensor crosses (network filesystems, containerized watchers on bind-mounted host directories), and still require a reconciling scan to be correct against dropped events — so polling is the load-bearing mechanism either way, and hooks would only buy latency the story does not need.
```

### Amend decision: fanout-list-array-store-agnostic

```markdown
---
decision: fanout-list-array-store-agnostic
---

# List fan-out is one grammar across both bundled stores

## Choice

The list partition grammar for fan-out — a partition request carrying a list of items produced upstream — is store-agnostic: both bundled claim producers (filesystem and Postgres) serve it through the same split-scope surface, and which bundled store holds the parent claim is a deployment choice, not a separate capability.

## Rationale

The list grammar has no store-dependent semantics — the items come from upstream, not from the store — so per-store variants would duplicate one capability behind two doors and force the story catalog to tell one outcome twice. The genuinely store-specific partition idioms are separate grammars of their own: folder expansion and batch pick on the filesystem producer, partition policy on the Postgres producer. Keeping those apart from the list grammar keeps the line honest: grammars split by semantics, never by backend.

## Alternatives

- Per-store list grammars, each its own capability — rejected: identical semantics duplicated per backend, with the authoring surface diverging for no user-visible reason.
- Serving the list grammar on only one bundled store — rejected: forces a store migration on anyone who needs list fan-out, though the grammar never touches store internals.
```

### Amend decision: tls-mode-validation

```markdown
---
decision: tls-mode-validation
---

# The tls value is a validated enum

## Choice

The peer TLS config field is parse-time validated, accepting exactly off-or-required; opportunistic and any other value are config errors. An empty field inherits the deployment's peer-auth posture: off while peer auth is unset or none, required once peer auth is mutual TLS (see `decision:peer-auth-mtls`).

## Rationale

Opportunistic TLS is not a real gRPC client mode; a documented third value with no honest semantics is surface noise. Pre-v1, deletion over deprecation. Deriving the empty field from the deployment posture lets an operator enable mutual TLS once and harden every peer that never named a TLS mode, instead of editing each peer block.

## Alternatives

- Keep `opportunistic` as an accepted third value — rejected: no honest client-mode semantics stand behind it; it would document a behavior the transport cannot deliver.
- Lenient parsing that maps unknown values to a default — rejected: a typo would silently downgrade the intended security posture instead of failing at parse time.
- A fixed empty-means-off default independent of the deployment posture — rejected: enabling mutual TLS would then leave every unedited peer block plaintext, which is the opposite of what enabling it asks for.
```

### Amend decision: event-log-payload-shapes

```markdown
---
decision: event-log-payload-shapes
---

# Event log payload shape

## Choice

Typed oneof payloads for a settled subset of operational event kinds (state-transition, work-started, lock-acquired, claim-acquired, and the rest of the oneof's members — see `concept:event-log`); free-form JSON for signal-class events (the `terminal/*`, `transient/*`, `attribute/*` taxonomy) and every operational kind outside the typed subset. A descriptor test rejects any oneof case that looks signal-class. Rimsky's internal write and read path is independent of that proto wrapper: it carries a payload as an opaque value whose only constructor takes a declared generated message, so a map literal does not compile at an emit site.

## Rationale

Type safety where a subset of the operational vocabulary is settled and worth a typed schema; lightweight JSON where the payload is audit data (the rest of the operational kinds) or where the signal taxonomy's shape is expressed through the signal type-path rather than a payload schema. Kind-discriminator typing is a separate decision (see `decision:event-log-kind-enum`). Constructing the internal payload from a generated message rather than a map closes the drift the split otherwise invites: a field nobody sets and a key nobody declared are both unrepresentable.

## Alternatives

- Typed oneof payloads for every kind, signal-class included — rejected: the signal taxonomy is typed through its type-path discipline, not a payload schema, so a oneof case per signal kind buys no consumer gain.
- Free-form JSON payloads everywhere — rejected: discards type safety on the settled subset of the operational vocabulary that benefits from a typed schema.
- Free-form maps on the internal write and read path — rejected: it is the shape that let declaration and emission drift apart, and nothing mechanical caught it.
```

### Amend decision: grpc-internal-protocols

```markdown
---
decision: grpc-internal-protocols
---

# Inter-service transport

## Choice

gRPC for the declared peer protocols. Three shipped service-to-service surfaces are HTTP-JSON by deliberate choice and are the named exceptions: the supervisor's HTTP executor transport and the bundled executor's HTTP bridge (see `decision:http-bridge-preserved`), and the executor-to-supervisor callback, keepalive and attribute-writeback routes (see `decision:async-callback-post-json`).

## Rationale

Type-safe binary, codegen, streaming. The named exceptions each buy something gRPC cannot: the bridge keeps a working surface for callers that already speak HTTP-JSON, and the callback routes let an executor report an outcome with an ordinary HTTP client rather than serve a gRPC server for the supervisor to dial back on.

## Alternatives

- HTTP-JSON for the declared peer protocols too — rejected: hand-maintained contracts, no generated stubs, no native streaming.
- A message-broker transport between services — rejected: adds standing infrastructure to every deployment for what are point-to-point calls.
- gRPC with no exceptions — rejected: it would retire the HTTP bridge and force every callback-posting executor to serve gRPC, both of which the decisions that own those surfaces chose against on their own merits.
```

### Amend decision: terminal-error-abandoned-as-error-class

```markdown
---
decision: terminal-error-abandoned-as-error-class
---

# `terminal/error/abandoned` is an error-class signal, not a new root signal

## Choice

The held-claim abandon outcome cascades downstream as the signal `terminal/error/abandoned`, shaped uniformly with other error signals as `terminal/error/<class>` where `class=abandoned`. Subscribers can match it via:

- The exact path `terminal/error/abandoned`.
- The wildcard `terminal/error/*` (which already matches every error-class signal).

No new top-level signal root is introduced. The signal taxonomy already supports per-class error signals; abandoned slots into that existing surface.

## Rationale

The taxonomy choice is between:

- **Class form**: `terminal/error/abandoned` (this decision).
- **Root form**: a distinct `terminal/abandoned` root signal.

The class form wins on uniformity. Every failure mode the runtime produces is expressed as a class under the `terminal/error/` root — the taxonomy carries no second shape for a failure, so a subscriber learns the pattern once. Subscribers already use it both ways: a specific class for targeted compensation, or `terminal/error/*` for blanket error handling. Adding abandoned as a new root would force subscribers to learn a second pattern and would split the "I want to react to any failure" use case into two subscriptions.

Abandoned is also semantically an error in the rollback sense: the held work was rolled back, the executor's output is not authoritative, downstream effects predicated on the run succeeding need compensation. It is not a benign termination like `terminal/success` or a deliberate dispatch-internal hold like `transient/park/*`. The error namespace is the right home.

## Alternatives

Distinct `terminal/abandoned` root signal — rejected because it splits the "any failure" use case across two roots and adds a top-level taxonomy entry for a single class. The wildcard surface (`terminal/*`) becomes the only way to match "any failure including abandon," which is broader than most subscribers want.

Reuse an existing error class (e.g., `terminal/error/rollback`) — rejected because abandoned is a specific outcome of the held-claim auto-terminal mechanism, not a general rollback. Distinct class makes the cause legible at the subscriber boundary; subscribers that only care about held-rollback (vs. other rollback flavors that might exist later) can subscribe narrowly.

Suppress the cascade entirely on abandon (no downstream signal) — rejected because downstream subscribers that depended on the held work's success need to know it was rolled back, in order to compensate. Silent rollback is worse than a signaled rollback.
```

### Amend decision: acquire-unavailable-carveout

```markdown
---
decision: acquire-unavailable-carveout
---

# The acquire-phase error handlers are named carve-outs outside the resolution engine

## Choice

Five acquire-phase error handlers remain outside the unified claim-handle resolution engine: unavailable, producer error, nil frame id, fan-out substitution failure, and lock-spec substitution failure. All five are explicitly named carve-outs sharing one downstream policy-application path: abandon partial opens with no row delete. The unavailable and producer-error handlers route via the producer-declared error class where the producer supplied one, falling back to a synthetic class (`acquire/unavailable` and `acquire/producer_error` respectively); the other three carry no producer-declared class and route by their own synthetic class alone.

## Rationale

All five handlers run after their acquisition transaction has already rolled back, so there is no claimant-guarded delete to fold; forcing any of them into the engine would widen the engine's contract with a verb-only mode, diluting the single audited verb-then-delete promise. The five share that reasoning and share their downstream policy-application code, differing only in which error class and payload fields each reports.

## Alternatives

- Fold the handlers into the resolution engine as a verb-only mode — rejected: widens the engine's contract and dilutes the single audited verb-then-delete promise for a case with nothing to delete.
```

### Amend decision: webhook-auth-required

```markdown
---
decision: webhook-auth-required
aliases:
  - webhook-auth-fail-loud
---

# The webhook sensor requires per-subscription auth, fail-loud

## Choice

The bundled webhook sensor requires per-subscription authentication, configured as exactly one of `hmac` (HMAC-SHA256 over the timestamp header's value joined to the raw body — the timestamp header is required and a subscription omitting it is refused at bind time; only the replay window is optional), `secret_header` (constant-time compare of a configured header), or `none` (explicit opt-out). Polarity is fail-loud: a subscription with no `auth` block is refused at bind time — the insecure `none` mode must be typed explicitly (see `concept:sensor`).

## Rationale

An unauthenticated webhook port on the public web accepts message injection and forged-idempotency-key pre-seeding from anyone who can reach it. Requiring the operator to name an auth mode — and to type `none` deliberately when they truly want none — makes the insecure choice visible rather than the silent default. Requiring the timestamp header inside HMAC mode is the same polarity one level down: the timestamp is signed material, so replay protection arrives with the mode rather than as a separate opt-in. This mirrors the closed-by-default polarity of the bundled-image egress guard, and is the opposite polarity from the claude-agent allowlists (unset = open) precisely because this is an inbound public-web boundary, not an internal policy knob.

## Alternatives

- **Default `none` (auth optional)** — rejected: an omitted auth block would silently expose the port, exactly the failure mode fail-loud exists to prevent.
- **An optional timestamp header in HMAC mode** — rejected: a signature over the body alone replays forever, and making replay protection opt-in leaves the weaker configuration one omission away.
- **A single fixed auth scheme** — rejected: HMAC-over-body and shared-header schemes are both common among upstream webhook producers, so the sensor offers both plus an explicit opt-out.
```

### Amend decision: inproc-handler-interface

```markdown
---
decision: inproc-handler-interface
---

# What in-process utility executors implement

## Choice

In-process utility executors implement a small Go interface in the runtime executor package: one Execute method taking a context, the executor's execute-request DTO, and a handler-context struct carrying the cascade-message sender — the handler's one side channel. The handler returns the terminal Outcome DTO directly, or an error. Scratch is not a side channel: it rides the request and the outcome messages like any other payload field (see `decision:scratch-protocol`). Translating a handler error into an error terminal belongs to the shared dispatch layer, which does it for every transport alike. Generated protobuf types are passed as DTOs at the function-call boundary — no wire encoding.

## Rationale

Shape-matched to `decision:executor-unary-rpc`'s unary Execute call but Go-idiomatic: the handler returns its outcome value directly instead of writing to a stream, and the handler-context struct gives it the one side-channel effect it legitimately needs without exposing runtime internals. Handlers stay simple and testable. Putting error translation on the shared dispatch layer rather than the in-process client keeps one behavior across the three transports instead of three copies.

## Alternatives

Have handlers implement the gRPC executor server interface directly. Heavier — drags gRPC-server streaming machinery into handlers that don't need it.

An event-sink interface passed into Execute (a Send method emitting each event individually) — rejected: the executor protocol is unary, so there is nothing to stream; returning the Outcome directly is simpler and matches the other two transports.

A scratch accessor on the handler-context struct — rejected: scratch already travels on the request and outcome messages, so an accessor would be a second channel for one thing.
```

### Amend decision: keepalive-endpoint

```markdown
---
decision: keepalive-endpoint
---

# Dedicated keepalive endpoint

## Choice

A dedicated keepalive route on the supervisor, keyed by run id and authenticated with the dispatch's existing cancel token. A call carries no body and answers with the same no-content convention as the attribute-writeback callback. It persists two effects in one transaction: it bumps the dispatch's last-progress timestamp, and it renews the expiry of every claim the run holds.

## Rationale

Async executors that do not have meaningful attribute updates need an explicit liveness primitive that does not pollute the attribute bag with dummy values. A dedicated endpoint keeps the liveness purpose distinct from the attribute-writeback purpose. Renewing the run's claim expiries in the same call completes the primitive: a dispatch long enough to need keepalives is long enough for its claim leases to fall behind the orphan reaper, and splitting the two would let a caller keep the dispatch alive while the reaper reclaims its claims underneath it.

## Alternatives

Reuse the attribute writeback callback only — rejected because it forces meaningless writes for liveness. Reverse polling via a `Ping` RPC on the executor — rejected because of supervisor-side polling load and executor-side state burden. A keepalive that touches only the progress timestamp, with claim renewal on its own route — rejected: two calls on the same cadence for one notion of "still working", and a caller that makes one and forgets the other loses its claims.
```

### Amend decision: launch-integration

```markdown
---
decision: launch-integration
---

# The compose verb and the entrypoint share one role launcher

## Choice

One exported launcher runs the three role runners — scheduler, supervisor, control-api: it starts each in order, tracks each runner's stop function, owns the combined role-failure channel, and drains in reverse order. Both the all-in-one entrypoint and the compose verb call it. Each site writes its own signal-versus-failure select, because each has its own signal source. The process-role marker is set so the memory-blob backend gate (per `concept:blob-backend`) permits memory if chosen.

## Rationale

The start / track / fail / drain loop is identical at both sites and load-bearing at both — a drain that runs in the wrong order or a failure channel nobody owns is a shutdown bug, not a style difference — so it lives in one place and the two sites cannot drift. What genuinely differs is the signal source: the entrypoint watches process signals, the compose verb watches its own lifecycle, so the select stays per site.

## Alternatives

- Mirror the loop at both sites rather than share it — rejected: two copies of a shutdown ordering that must agree, with nothing to keep them agreeing.
- Spawn the all-in-one entrypoint as a child process from the compose verb — rejected: forfeits in-process control of the runners (config injection, lifecycle, teardown) that the verb needs.
```

## Work items

- **Guard the claim-handle expiry renewal by its holding supervisor.** The renewal that runs on keepalive and on acquire-reuse names the holding supervisor in its predicate, in both persistence backends; its two callers pass the acting supervisor. The claimant-guard conformance suite covers the operation, so the suite fails when a future mutator skips the guard. Makes `concept:claim-handle`'s no-carve-out invariant true. (`decision:keepalive-endpoint`'s renewal clause depends on this being the guarded shape.)

- **Cover the keepalive claim renewal with a test.** A keepalive test builds the server against a real claim-handle table and asserts the renewal fires, so the second persisted effect the decision now names has coverage. Depends on the guard item above, since the renewal's signature changes.

- **Give the remote one-shot run the four exit-code classes.** The remote ephemeral run reports timeout as 2, interrupt as 130, and reads the instance's outcome to choose between 0 and 1, using the same classification the compose coordinator carries. The test that pins 0 on interrupt is replaced by one asserting 130. Makes `story:script-friendly-outcome` hold on every run-to-terminal verb and `decision:exit-codes` carve-out-free.

- **Add a fitness test for the parallelism caps.** A grouped fitness test reads the Makefile's test recipes and asserts that the docker-backed module suites carry the parallelism cap and the protocols suite carries none, annotated `@decision: parallel-cap-removal`. The bare prose reference to the decision comes out of the Makefile comment. Makes `decision:config-enforced-fitness-tests` true of `decision:parallel-cap-removal`.

- **Namespace the two claude-agent operator variables.** The dispatch spend cap and the observability bridge URL carry the claude-agent service segment, matching how the sibling http-node executor names its bridge URL. The pin test widens from host and port names to every operator variable outside `decision:operator-env-namespaced-per-service`'s exempt set. Both variables are `RIMSKY_*` and public by the surface intent's environment-variable rule, so the rename is a documented surface change.

- **Resolve late-bound claim-producer proxy names through the address book.** The claim-producer dispatch path resolves a proxy name through the in-process registry and then the address book, as the executor path already does, so a template naming a late-bind service reaches a spawned claim-producer binary. Makes `story:host-agent-late-bind-all-protocols` true for both implementable protocols and `concept:service-address-book`'s every-declared-name invariant hold.

- **Keep a woken parked run through a most-recent cascade round.** The most-recent mode's coalescing delete skips a row that reached stale by a park-wake. The woken row dispatches first with its dispatch-time inputs; the newly queued round dispatches after it settles. A scenario test drives an upstream cascade during a park and asserts the parked unit of work executes. Makes `story:resume-preserves-snapshot`, `story:cascade-defers-during-flight`, and `concept:parked-state` true together.

- **Dispatch builtin-kind nodes inside delegated sub-graphs.** Sub-graph child dispatch resolves each child's executor from the same canonicalized, kind-resolved declaration the flattened main-graph path uses, so a node declared by builtin kind inside a delegated sub-graph dispatches like any other. A scenario test runs such a template to a settled frame.

- **Route the webhook sensor's serving port through the shared precedence helper.** The webhook sensor resolves its port as the other bundled binaries do — agent-assigned port, then its own variable, then the built-in default — so the host agent can late-bind it. Makes `concept:service`'s port-precedence invariant unconditional across all eleven binaries.

- **Give the lifecycle-subscriber conformance suite its own package.** The suite moves out of the executor suite's package into a sub-package beside the other seven, and the existing `rimsky conformance` subcommand points at it, so certifying lifecycle compiles no executor fixtures. Makes `concept:conformance` and `decision:conformance-suite-per-protocol` true.

- **Make strict fan-out aggregation cancel its siblings.** The cancel-action executor handles the cancel-siblings action by force-failing every remaining in-flight clone through the same run-tree walk the first policy's cancel-non-winners already uses. A scenario test watches a sibling's run row reach failed, rather than asserting the returned action value. Makes `concept:fan-out` and `concept:cancel-siblings` true of strict.

- **Emit the two settling terminal signals through the typed builder.** The fan-out sibling-cancellation and instance-kill settlement sites build their terminal-error signals with the typed terminal-error builder and emit through the validating path, so both payloads carry tags and the attributes delta. The builder-only fitness test resolves a type path named by a constant, so it sees these two sites. Makes `concept:signal`'s every-terminal-payload invariant hold.

- **Join the observability frame routes to their triggering message.** The dashboard's frame list and frame get handlers use the joined store reads and return the triggering message's type, sender and sender kind, matching the instance-scoped routes. Makes `concept:cascade-graph` and `story:frame-origin-audit` true across all four frames-read routes.

- **Order stale-row claims by sequence.** Candidate selection orders by enqueue time, then sequence, then row id, in both persistence backends, and the keyset paging cursor carries sequence, so two rows written in one transaction claim in creation order. Makes `concept:wait-set`, `concept:cascade-mode` and `decision:non-cascade-direct-to-stale` true where they promise sequence order.

- **Move the callback listener's liveness probe under the version prefix.** The supervisor's callback listener serves its liveness probe under `/v1`, as the control API's own probe does, leaving no path on the control-plus-callback surface reachable outside the prefix. The route-registry test's population widens to include the callback listener's routes. Makes `decision:protocol-version-v1-namespaced` carve-out-free. The route stays public under the surface intent's rule that the supervisor's callback routes are public; any external probe configured against the bare path needs repointing, which the completion report should call out.

- **Serve the claude-agent HTTP bridge on the versioned execute path.** The claude-agent's bridge serves the same versioned execute path the http-node bridge serves and the supervisor's HTTP executor client dials, and the executor's README names that path. Makes `decision:http-bridge-preserved` true for both shipped bridges.

- **Drop the root-module fetch line from the release-notes template.** The `go get` instruction for the root module comes out of the release skill's notes template and out of `RELEASING.md`'s matching block; the protocols-module instruction stays, and both point at the limitation note `RELEASING.md` already carries about installing the CLI by version. Publishing the sibling modules at real versions stays an open question this sprint does not decide.

- **Rename the two blob in-transaction helpers.** The two package-level blob helpers that take an optional transaction take the plain spelling instead of the in-transaction suffix, and their call sites follow. The pair-detecting fitness test's population widens from receiver-bearing methods to every top-level persistence declaration, so the test catches a future top-level escapee. Makes `decision:intx-suffix-convention` true.

- **Answer a malformed pagination cursor with 400.** A cursor that fails to decode is a client error on the control API and the observability handlers alike, answered 400 with a caller-safe message that discloses no internal operation name — matching how the same routes already treat limit and active.

- **Route the verifier-http executor through the egress guard.** The verifier-http executor dials its node-attribute-supplied URL through the shared egress guard, default-closed like its two sibling dialers, with its own opt-in CIDR allowlist variable named in the established per-service pattern. The variable is `RIMSKY_*` and public by the surface intent's environment-variable rule. This adds a third instance of the established default-closed polarity and decides nothing about the open intake question on how `decision:allowlist-defaults-open` should be worded.

- **Print every row of a page in the CLI's message tail.** The tail loop filters each received page against the watermark taken before the poll, prints every row that passes, and advances the watermark only after the page is printed — in one-shot and follow modes alike.

- **Map SQLite's trigger-constraint code to the template-in-use error.** The SQLite store's foreign-key predicate recognises the code SQLite reports for a restrict-on-delete trigger, so deleting a template referenced only by a terminated instance reaches the existing template-in-use error and its 409 instead of escaping as a 500 with raw driver text.

- **Print validation advisories on a successful template registration.** The CLI's client-side template response type carries the validator's advisories, and the register verb prints them on the success path as the failure path already does, in plain and structured output. Makes `story:validation-warnings-surfaced` hold where the author actually succeeds.

- **Teach the CLI to recognise the dry-run preview envelope.** The client's shared response decode recognises the top-level dry-run marker before unmarshalling into a resource type, and every write verb reports a preview as a preview — "would have <verbed>", exit 0, structured output marked dry-run — rather than as a completed write. One chokepoint covers all thirteen client write methods. Makes `decision:auth-dry-run-mode-floor-on-key` and `decision:auth-dry-run-request-flag` true at the CLI.

- **Repoint the release skill's SemVer detectors.** The CLI-flag inspection reads the CLI library's flag-set declarations rather than the binary entrypoint files, and the exported-symbol inspection covers the root module alongside protocols and foundation. Makes `decision:release-semver-from-diff` true of the surfaces it commits to.

- **Close the cross-driver parity gaps.** The parity suite gains cases for the nine interface methods it does not exercise, frame settlement first, plus an enumeration check asserting the suite's exercised set equals the three interfaces' method set — so the decision's coverage claim polices itself. Makes `decision:parity-expansion` true.

## How to execute this sprint

This sprint is self-sufficient. Every executor — an inline session,
an agent handed this file via `/goal`, an orchestrator with its own
planning — proceeds the same way.

1. Read the sprint whole first: intent, deltas, work items,
   completion contract. Do not look for context behind it, in the
   intake (`.ok-planner/issues/`) or in `history/`. Raise a gap with
   the owner; never fill it by inference.

2. Stage the work. Group the items by theme, file surface, or
   dependency, and order the groups so nothing is built on something
   not yet there. Before building, write the staged list as the
   opening section of the completion report (step 8): `## Stages`,
   one line per stage, each marked pending. Seed the closing stages
   now — finish the completion report, run `/certify-work` with this
   sprint's path as its argument, walk the presentation, offer
   archive-and-commit. Mark each stage done as it lands. The list
   lives in the report only: not in a harness task tool, never in a
   plan document.
   An orchestrator uses its own graph and still records the stages in
   the report.

3. Apply each corpus delta as part of the work that realizes it:
   copy the final-form body into `.ok-planner/design/` verbatim (from
   the sidecar where the heading points there), or delete the file
   for a retirement. Apply a delta no work item implements on its
   own.

4. Build stage by stage. Every new or amended story implemented in
   code is exercised end-to-end by a test in the project's ordinary
   suites, carrying the `@story:` annotation. No test checks the
   existence of static text, code, or prose; a commitment realized in
   prose carries no test. Write the tests with the work.

5. Completeness is the floor. Never stub, defer, narrow, no-op, or
   leave a `TODO` in place of a promised outcome. Deliver every
   capability the deltas or work items promise in full, or surface
   the blocker that prevents it.

6. Never destroy uncommitted work. Stage the paths you touched as
   each stage finishes (`git add <paths>`). Never run `git checkout`/
   `restore`/`reset`/`stash`/`clean` on your own initiative. Fix a bad
   edit forward by editing again.

7. Work unsupervised to a defensible done. Do not pause for
   approval, confirmation, or progress checks. Stop only on a
   genuine blocker: a credential or access you cannot obtain, a step
   impossible in the current state, a destructive or irreversible
   action not clearly authorized, or the closing `/certify-work`
   step being unrunnable for you (its subagent dispatches
   unavailable). Surface that and stop; never skip the ceremony and
   call the work done. Ambiguity is not a blocker: pick the most
   plausible reading, continue, and surface the choice at the end.
   An orchestrator that supervises its own executors folds this into
   its own control.

8. Keep the completion report current. It lives beside this sprint
   file, same filename with `-completion` before the extension. Open
   it in step 2 with the staged list. As each stage lands, mark it
   done and record what was done, every divergence, and every call
   you made where the sprint was silent. It is the record the
   closing ceremony finishes and walks with the owner, the artifact
   a goal checker requires, and it is archived with this sprint. It
   is a record of this execution, never a plan.

9. Close by running `/certify-work` with this sprint's path as its
   argument. The argument puts the sprint in the gate's scope; the
   gate never adopts one on its own. The gate brings the work into
   alignment with this sprint and discharges the completion contract
   at the change's scope, across every estate the project has: the
   project's test suites over the touched work, change-scoped corpus
   checks over the touched artifacts and annotations, code review
   over the diff. All producers feed a no-discretion review-fix loop:
   a fixer fixes everything a reasonable owner would wave through; an
   architect adversarially checks its kickbacks, fixes the refuted,
   and promotes only genuine intent forks to the intake. Whether the
   corpus's claims still hold is the periodic `/audit` run's
   question, never this close's. `/certify-work` ends the run: it
   writes its presentation into the completion report, walks the
   presentation with the owner, offers the close-out, and stops.

**After the run stops.** The owner archives this sprint and commits
the work. The run offers both at the end of the presentation and
does neither on its own. Until the owner answers, this file stays at
its `sprints/` path. On yes, the run moves this file, its completion
report, its delta sidecar, and the issue files it resolved to
`history/`, commits the work, then stamps the archived sprint with
the closing commit — `closed: <sha>` in the frontmatter, one small
follow-on commit. The next planning ceremony reads that stamp to
detect work done out of band. "Finish the sprint" and "follow the
boilerplate" are not a yes; both ask for the presentation.

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
   fixed or promoted-and-verified.

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
checking. A missing completion report means not done. A run parked
at the review-fix loop's cycle cap awaiting the owner's direction
has not met the goal: a legal in-flight state, not done, not failed,
and never grounds for the run to take either cap step itself.
Nothing else counts either way.
