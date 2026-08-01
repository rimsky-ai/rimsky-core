# Sprint: Guidance-realignment drain

## Intent

A pure intake-drain sprint: it realizes the rulings of all 18 verified issues in the intake — re-ruled under the v14.1.0 authoring guidance — with no unifying theme beyond bringing the design corpus into agreement with that guidance and with verified code reality. The largest single item reconciles the `design/intent/` dossier directory into the live corpus and archives it.

One delta rides in from the out-of-band baseline review rather than an issue: `concept:host-agent` catches up with the fourth stamped child-environment variable the strengthened isolation test introduced.

Issues promoted into this sprint: `coverage-gap-decisions-bulk-160-uncited`, `decisions-corpus-wide-missing-alternatives`, `decisions-enumerate-routes-and-envs-in-body`, `host-agent-anonymous-mode-bundles-two-outcomes`, `host-agent-proxy-invariants-may-be-decisions`, `stories-name-rimsky-yml-and-config-keys`, `story-service-enrollment-mechanism-in-body`, `cli-distribution-channel`, `store-vocabulary-test-fake-boundary`, `build-file-enforced-decisions-uncitable`, `conflict-network-binding-loopback-vs-remote-reachability`, `conflict-node-reset-retry-budget-inert`, `conflict-template-unconditional-validation-vs-ref-validation-mode`, `decisions-historical-language-residue`, `decisions-spec-altitude-mechanism-detail`, `intent-directory-corpus-status`, `stories-delivery-surface-named-in-body`, `stories-mechanism-prescription-tail`.

## Corpus deltas

### Amend decision: network-binding

```markdown
---
decision: network-binding
status: adopted
---

# network-binding

## Choice

The control-api HTTP server binds to the loopback interface by default (see `concept:control-api`), and the bind address is itself configuration-overridable: a split or containerized deployment sets a wide bind so other containers and enrolled services can reach the control plane — the sanctioned split/production posture, which the shipped all-in-one image uses. Loopback remains the local-dev default. In normal single-role or split deployments the port is a fixed, configuration-overridable default; the one-shot self-host launcher (compose-run and equivalent single-invocation flows) instead binds a kernel-picked ephemeral port and retries on bind conflicts, since it may run concurrently with other local invocations.

The supervisor's async-callback HTTP listener binds to all interfaces, since executors dispatched from another container or host must be able to reach it. Because a wildcard bind carries no externally-reachable hostname on its own, the supervisor requires a configured callback advertise host to be stamped into the callback URL handed to executors, and fails fast at startup rather than silently advertising an unreachable address.

## Rationale

Loopback binding by default gives OS-level isolation against other users on the same host and needs no firewall rules to negotiate; the one-shot launcher's ephemeral port plus bind-conflict retry removes the port-conflict story between concurrent local invocations without imposing that cost on every deployment shape. The bind-address override is part of the Choice because the platform's own components depend on it: the host-agent proxy fails closed without a control-API endpoint it can dial from another container, and standing services enroll against the control plane over the network — the wide bind is the posture every split deployment already runs, so a decision that named only loopback misdescribed its own scope. The supervisor's callback listener binds wider than loopback out of necessity — it accepts inbound requests from executors that may not share a network namespace with it — so its correctness turns on an explicit, correct advertise host rather than on the bind address itself; failing fast on a missing advertise host beats silently handing executors a URL nothing can reach.

## Alternatives

- Loopback-only binding with an ingress or gateway layer providing remote reachability — rejected: no gateway exists anywhere in the codebase, and the proxy registration and enrollment surfaces need a directly dialable control plane.
- Hardcoded per-deployment-shape bind addresses — rejected: the same image serves single-role, split, and all-in-one shapes; only configuration can know which is running.
```

### Retire decision: node-reset-as-pure-retry-budget-clear

Delete `.ok-planner/design/decisions/node-reset-as-pure-retry-budget-clear.md`. Its corrected successor is the new decision below; the five code citation sites are repointed by a work item.

### New decision: node-reset-clears-failure-marker

```markdown
---
decision: node-reset-clears-failure-marker
status: as-is
---

# Node reset clears the failed run's settling-signal marker

## Choice

The node-reset endpoint is an observability-surface verb. It preserves the state gate (refusing non-failed-terminal nodes with a conflict rejection); on a valid reset it clears the failed run's persisted settling-signal marker so the node-inspect surface no longer reports a settled failure; it does not enqueue an envelope, open a frame, or affect dispatch eligibility in any way. Retry budgets are per-dispatch (`concept:node-run`): every new run starts with a fresh budget, so no cross-run budget exists for reset to act on. The operator's workflow for retrying an errored node remains two explicit steps: reset (clearing the stale marker), then a message that invalidates the node so a fresh dispatch is attempted.

## Rationale

The marker-clear is genuinely observable on the node-inspect surface and keeps a non-debug operator recourse for tidying a failed node's reported state. A framing that claims an effect on acquisition eligibility would be false — the per-dispatch budget always starts fresh, so no reset can change whether the next run dispatches.

## Alternatives

- Retire the endpoint — rejected: loses the only non-debug operator verb for clearing a stale failure marker; the observability value is modest but real.
- Keep the retry-budget framing — rejected: documents an effect the per-dispatch budget design makes impossible.
- Fold reset into the debug-channel surface — rejected: requires the operator to pause first, a heavier workflow for common-case tidy-up.
```

### Amend story: node-admin

```markdown
---
story: node-admin
status: as-is
---

# Operator inspects and resets nodes

## Story

As an operator, I can inspect a node's full state on a running instance and clear a failed-terminal node's stale failure marker, so that I read the node's true current state — without a settled failure masking it — while deciding how to intervene.
```

### Amend decision: release-distribution

```markdown
---
decision: release-distribution
status: as-is
---

# Distribution channels

## Choice

Four channels: container-registry images with SBOM and provenance attestations, the protocols npm package, Go modules consumed from a full checkout, and GitHub Releases carrying prebuilt CLI archives built by goreleaser for Linux and macOS on Intel and ARM. The CLI ships as prebuilt archives only: Windows is unsupported (the CLI transitively embeds Unix-only system calls), and network `go install` is unsupported (the workspace's modules are wired with local-path redirects that resolve only inside a full checkout).

## Rationale

Multiple consumption patterns need multiple channels — images for deployment, npm for protocol consumers, Go modules for embedders, prebuilt archives for CLI users without a Go toolchain. Naming the two non-channels in the Choice keeps each from being rediscovered as a bug: the Unix-only matrix and the go-install gap are consequences of deliberate choices (system-call usage, workspace module layout), not oversights.

## Alternatives

- A Homebrew tap — deferred, not rejected: broader macOS/Linux reach at the cost of a standing extra repo to maintain, with no requester yet.
- Publishing the workspace's sub-modules as independently versioned Go modules so `go install` works — rejected: a packaging overhaul far larger than CLI distribution itself, motivated by nothing current.
```

### Retire story: ref-validation-mode

Delete `.ok-planner/design/stories/ref-validation-mode.md`. The registration-validation strictness mode it describes was deliberately deleted from the code; the register-before-provision use case is served by the template-level late-bind list (`concept:template`).

### Retire story: validation-names-the-mode

Delete `.ok-planner/design/stories/validation-names-the-mode.md`. Same deleted mode.

### Retire decision: validation-error-names-mode

Delete `.ok-planner/design/decisions/validation-error-names-mode.md`. Same deleted mode.

### Amend concept: instance

The full body below is the current file with one change: the mandatory-gate invariant no longer references a relaxed registration mode (the deleted knob).

```markdown
---
concept: instance
status: as-is
aliases: []
---

# Instance

## What it is

An instance is one live deployment of a template, identified by a rimsky-generated UUID. Created via the instance-create control endpoint, carrying a template binding plus initial params and optional attribute overrides. Bound to a specific template hash. Carries a free-form params blob (substitutable into node configurations) and optional per-instance per-node attribute fragments.

## Purpose

Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against. Instance creation creates the per-instance row and the per-instance node rows and triggers the instance-created lifecycle callback; no frame is enqueued and no work begins until a sender posts a message. The empty-message trigger (`story:empty-message-wakes-roots`) is the universal convenience for waking every structural root without crafting a typed envelope.

## Boundaries

Owns: the per-deployment runtime state, params, attribute overrides (including match-based overlays), the per-instance late-bound service-binding catalog (set at creation), the creator's api-key linkage (see `concept:api-key`), paused state, the binding to a template hash, the message queue that accumulates pending wake messages while the running frame (if any) is in flight and its per-instance coalesce mode (`message_queue_mode`: `backlog` (default) or `coalesce`). Does NOT own: the template spec (see `template`), live node rows (those carry their own instance reference), claim conflict (that scopes to the supervisor), the frame currently running against the queue (see `concept:frame`). Adjacent: `template`, `tag`, `frame`, `node`, `message`, `api-key`, `host-agent-proxy`, `breakpoint`.

## Invariants

- The template binding is a foreign key to the template hash, fixed at creation.
- `instance_key` is nullable; canonical identity is the UUID.
- Attribute-overrides validation inspects only routing keys (the per-executor / per-node selectors and, for match-based overlays, the matcher key names plus cross-checked discriminators); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via event-derived per-entry match counts, aggregated at read time from `concept:event-log` rather than persisted on the instance row.
- Candidate selection by the supervisor skips paused instances (the candidate query filters out paused rows).
- The late-bound service-binding catalog is opaque, set at instance creation and consumed by the `concept:host-agent-proxy` at dispatch time to resolve a late-bound service name to a dev-machine binary.
- The creator's api-key linkage records the api-key whose authenticated request created the instance (absent for instances created under `concept:anonymous-mode`); it is retained for ownership and audit only. Routing to the serving `concept:host-agent` uses a separate routing identity stamped onto the instance at creation time, resolved uniformly across owner-instantiated and anonymous instances per `concept:host-agent-proxy` — not the creator's api-key linkage.
- An instance is terminal exactly when its terminal timestamp is set. The force-terminate control action is the production mechanism that sets it, force-failing every in-flight node-run (all five in-flight states) through the runtime's terminal-resolution path — the instance-kill transition reason and settling signal are recorded on each run, active claims are abandoned through their producers (falling back to a record-only abandon when a producer is unreachable, so the kill always lands), and the run-tree aggregate hears the verdict — then ending every open frame (which derives `terminated` per `concept:frame`) and closing the frame's `concept:run-scope` tree; any pending messages on the instance's queue are marked cancelled and never open a frame. Terminal is not removal: the instance key is freed for reuse only by the subsequent row delete, which is permitted only once the instance is terminal.
- `message_queue_mode` is per-instance, one of `backlog` (default) or `coalesce`, declared on the template (`message_queue_mode`) and materialized onto the instance row at creation; the operator may override it per instance at creation time, and the override wins over the template default. Under `backlog`, every pending message survives until its frame opens. Under `coalesce`, inserting a new message into the instance's queue cancels any prior pending messages for the instance in the same transaction, bounding the pending set at ≤ 1 per the `story:message-queue-coalesces-pending` outcome. The mode applies uniformly to every message type on the queue; it is distinct from the per-node intra-frame `cascade_mode` (`concept:cascade-mode`).
- An instance is durable by default and never self-terminates. It becomes terminal only through the operator-initiated force-terminate control action; there is no per-frame or per-run auto-termination path.
- Termination is independent of `concept:sensor` / `concept:publisher-subscription` and of node presence — the termination decision reads nothing about subscriptions or nodes.
- Instantiation is the mandatory static-config validation gate: the instance-create endpoint validates each node's statically-knowable attribute config (value constraints included) against every referenced service's schema and rejects create on any static misconfiguration. All referenced services exist at instantiation (the bound-on-demand host-agent proxy is itself a present service). Substitution-sourced values, knowable only once a node acquires its inputs, stay validated at dispatch (invariant 12, validate-twice — that pass serves as defense-in-depth for the static part).
- **Frame processing mutates only the message queue on the instance row.** Two channels are legitimate: (a) append a new envelope (operator-sent, publisher-sent, or cascade-sent via `concept:message-sender-node`), and (b) cancel prior pending messages under `coalesce` mode on new-envelope insert. No other instance-row field — the template binding, params, attribute-overrides map, service-binding catalog, api-key linkage, paused state, terminal timestamp, `message_queue_mode` — is written by any code path running inside a frame. Operator lifecycle actions (pause, force-terminate, params-update if any) mutate the instance row through the control API, not through frame processing.
```

### New decision: claim-producer-vocabulary-boundary

```markdown
---
decision: claim-producer-vocabulary-boundary
status: as-is
---

# The claim-producer rename stops at the shipped surface

## Choice

The store→claim-producer vocabulary sweep covers every shipped, user-facing surface: binaries, entrypoints, config grammar, example templates, and every name a template author or operator observes. Internal test machinery is exempt and keeps its existing names — the test-fake helper package, test-fixture names, harness database names, container-internal mount paths. The per-producer internal storage layer keeps its separate "store" sense (each claim producer's own storage packages): storage-layer naming, a different word doing a different job, not producer vocabulary.

## Rationale

No template author or operator ever observes internal test names, so renaming them buys no user-facing consistency while churning many test files; and the tree already tolerates storage-layer "store" beside claim-producer vocabulary by design, so the exemption is consistent with an existing boundary rather than a new inconsistency. Writing the boundary down is the point: without it, the leftover names refile as an audit finding every cycle.

## Alternatives

- Extend the sweep into the test machinery — rejected: invasive churn across many files with zero user-observable benefit.
- Leave the boundary undocumented — rejected: the question has no home and reopens indefinitely.
```

### New decision: config-enforced-fitness-tests

```markdown
---
decision: config-enforced-fitness-tests
status: as-is
---

# Config-enforced decisions are proven by grouped fitness tests

## Choice

A decision enforced solely by a configuration surface — the dependency-lint config, the module manifests, the Makefile and image definitions — is linked to code through a grouped fitness test: one Go test file per enforcement surface, asserting the presence and shape of every config-enforced rule it covers and carrying the `@decision:` annotations for all the decisions it proves. Citation tags are never stamped into lint configs, manifests, Makefiles, or Dockerfiles.

## Rationale

The per-edit comment lint polices annotations only in code file types, so a tag stamped into YAML or a manifest would rot unpoliced — a renamed decision would orphan it silently. A grouped fitness test is self-policing (its annotations live in Go, where the lint sees them), fails loudly when the config drops a rule, and gives the periodic implementation audit something exhibitable to point at. The repo already proves config-enforced choices this way — the env-var registry test and the canonicalization-pin check — so this generalizes an existing idiom rather than inventing one.

## Alternatives

- Citation comments inside the config files — rejected: cheapest, but permanently unpoliced by the per-edit lint.
- Exempting config-enforced decisions from annotation — rejected: the audit loses navigation for exactly the decisions hardest to find by reading.
```

### Amend story: host-agent-anonymous-mode

```markdown
---
story: host-agent-anonymous-mode
status: as-is
---

# Late-bind works under anonymous mode

## Story

As an operator running a fresh anonymous-mode rimsky deployment (no api-keys minted yet) with a host-agent connected, I can register and dispatch to late-bound services from an anonymous-mode instance, targeting a specific connected agent, so that the dev-loop works without minting credentials first.
```

### New story: anonymous-agents-isolated

```markdown
---
story: anonymous-agents-isolated
status: as-is
---

# Concurrent anonymous agents stay isolated

## Story

As a developer sharing an anonymous-mode deployment with other developers, I can run my own host-agent and my own instances targeting it while others run theirs, so that each of us receives only our own instances' dispatches — no displacement, no cross-talk.
```

### Amend concept: host-agent-proxy

The full body below is the current file with one change: the concurrent-dispatch multiplexing invariant moves to the new decision `proxy-single-spawn-multiplexing` and its bullet is removed here.

```markdown
---
concept: host-agent-proxy
status: as-is
aliases: []
---

# Host agent proxy

## What it is

A rimsky-stack `concept:service` implementing the multi-protocol composition pattern (per `concept:service` invariants: distinct handler types per protocol, separately registered on one server). Presents the rimsky service protocols on the supervisor-facing side. Maintains agent connections on the dev-facing side via a long-lived agent-connection protocol. Routes dispatches to whichever agent is connected under the instance's stamped routing identity. Declared in the rimsky config (`concept:rimsky-yml`) once per protocol it serves, all entries pointing at the same binary.

## Purpose

Lets rimsky dispatch work to dev-machine binaries declared per-instance, while the supervisor and graph-processing layers see only the standard service protocols. The proxy implements the dispatcher and the URL-rewriting boundary; the supervisor, dispatch resolution, error vocabulary, and callback handling traffic in the platform's standard vocabulary.

## Boundaries

Owns: the agent ↔ proxy protocol, the spawn-lifecycle state machine, the per-instance service-bindings cache (populated via `concept:lifecycle-subscriber`, with a cache-miss fallback that fetches the instance directly from the control API and caches the result, and evicted on the instance's termination lifecycle callback so entries do not accumulate over a long-running proxy's lifetime and a terminated instance's binding cannot shadow a later instance that reuses the same binding name), the per-protocol dispatch handlers that proxy through to spawned processes, the callback-URL rewriting that lets spawned processes post to the agent's local listener rather than dialing the supervisor. Does NOT own: the rimsky-side service protocols themselves (those are `concept:executor`, `concept:claim-producer`, etc.), the supervisor's dispatch logic, the per-instance state (that's `concept:instance`), the lifecycle-subscriber wire protocol (that's `concept:lifecycle-subscriber`). Adjacent: `concept:host-agent`, `concept:service`, `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:instance`, `concept:rimsky-yml`, `concept:peer-auth`, `concept:anonymous-mode`.

## Invariants

- Implemented via the existing multi-protocol composition pattern on `concept:service` — distinct handler types, no shared capabilities provider.
- One spawn per (run-scope, binding-name), lazy birth on first dispatch, run-scope-lifetime, reaped on run-scope termination.
- Routing is uniform: every dispatch resolves the serving agent by the instance's stamped **routing identity** — the api-key id for ordinary owner-instantiated instances, or the target anonymous agent's silly-name for instances created in `concept:anonymous-mode`. There is no special-case anonymous routing rule; the identity slot is filled at instance-creation time in both cases. An instance whose stamped identity has no currently-connected agent surfaces the ordinary agent-not-connected terminal.
- Anonymous agents each get a per-agent **silly-name routing identity** (adjective-noun form, e.g. `sparkling-wombat`), assigned by the proxy at registration from the anonymous sentinel: the agent may supply an explicit label (which the proxy accepts iff not colliding with another currently-connected anonymous agent, rejecting the registration otherwise); with no label the proxy generates a fresh silly-name and returns it in the registration response. The agent persists whatever name the proxy accepted and re-presents it on subsequent connects so previously-created instances targeting that name continue to reach it. Displacement between two anonymous agents is impossible by construction — the routing table is keyed by the assigned silly-name, not by a shared sentinel.
- The agent-facing side is served over TLS: the proxy presents a server certificate the agent verifies against a pinned deployment-CA root, so the agent's api-key transits an encrypted channel rather than plaintext over the dev-machine→deployment hop (see `concept:peer-auth`). The agent is user session tooling and presents no client certificate; it authenticates by api-key inside that channel.
- Registration is authenticated. The registration credentials the agent presents (its api-key credential, or an anonymous sentinel when it has none) are verified against the control API's identity check, and the proxy adopts the routing identity the control API reports — the key's id for a real api-key, or the assigned silly-name for anonymous agents (see the silly-name invariant above). The presented value is never used verbatim as a routing identity: unknown, revoked, expired, or unverifiable credentials are rejected before any routing-table mutation, so an unauthenticated client can neither displace a registered agent nor receive an owner's dispatches. A proxy with no control-API endpoint configured fails closed and accepts no registrations.
- All dispatch failures surface as executor-error / claim-producer-unavailable terminals on the supervisor-facing protocol — no new synthetic supervisor-side acquire error classes.
- The proxy is declared in the rimsky config per protocol it serves, using the same binary across all entries (one endpoint, N namespace registrations).
- The proxy is the URL-rewriting boundary for rimsky URLs handed to spawned processes: the callback URL is the only URL it rewrites.
- The proxy's sanctioned late-bind surface is `concept:executor` and `concept:claim-producer`: both are transparent forwarders through one uniform spawn/forward mechanism, each presenting exactly the fronted service's protocol, and a service that conforms to its own protocol works behind the proxy by construction — so the proxy needs no protocol of its own to conform to; its transparency is proven instead by running the standard executor and claim-producer conformance suites against it, behind a spawned process stood up by an in-process agent test double. Late-binding `concept:publisher`, `concept:validation`, or `concept:data-processing` through the proxy is rejected loudly (an unimplemented-protocol error, not silent non-routing); the proxy is not the routing mechanism for those protocols.
- The schema a specific spawned binary actually accepts is unknowable until that binary's own capabilities handshake completes at spawn time, so attribute-schema conformance for late-bound executors is deferred from the usual registration-time check to per-dispatch: once a binary is spawned, the resolved attribute values are checked against the schema that binary itself advertised, and a mismatch settles the dispatch as a contract error rather than forwarding a payload the binary never promised to accept.
```

### Amend concept: host-agent

The full body below is the current file with one change: the spawned-child environment-stamping invariant now names the routing identity among the stamped values (the code stamps it so a child's work is attributable to its serving agent; the concept previously enumerated only three stamped values).

```markdown
---
concept: host-agent
status: as-is
aliases: []
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
- Spawned children inherit the agent's full environment, overridable per binding on any name collision by that binding's own declared env vars, plus a per-spawn provisioning that makes the child enroll: the agent mints a fresh bootstrap token and stamps the child's environment with the peer-auth mode, its api-key credential, its control-API endpoint pointing at the agent's local enroll listener, and the agent's own routing identity — so work a child executes is attributable to the serving agent. The child self-enrolls exactly as any service enrolls under `concept:peer-auth` (the bundled peer-auth path, no executor-side code change), and the agent validates its own token and issues the child a short-lived leaf from the local CA.
- Both loopback legs — the agent→child dispatch and the child→agent callback — verify the peer's local-CA leaf. A port-squatter or a plaintext-only binary holds no such leaf and fails the handshake, so forged dispatches, forged callbacks, and dispatch interception all fail closed in one mechanism. A late-bound binary that speaks only plaintext is not a valid executor.
- On bidi-stream close (clean or unclean), all live children are sent a terminate signal and force-killed after a configurable grace period.
- The agent picks the child's listening port itself (an OS-assigned free port, not a binding declared anywhere) and tells the spawned child which port to bind; the agent then polls that port until the child's server answers, bounded by the spawn's ready-timeout. There is no port handshake back to the agent — a spawned binary that does not bind the port it was told fails the readiness poll and the spawn is reported failed.
- **Anonymous identity persistence.** When the agent authenticates as anonymous (no user api-key configured), the routing identity the proxy adopts — an agent-supplied routing label or a proxy-generated silly-name — is persisted in a local state file under the agent's config directory. On subsequent starts the agent re-presents that identity so previously-created instances targeting it continue to reach it after a reconnect. This is the agent's ONLY across-restart behavior state; other on-disk state is informational only and is never read back to affect behavior. Authentication is either the user's `concept:api-key` or — in anonymous mode — the persisted anonymous routing identity, verified by the proxy before any routing.
```

### New decision: proxy-single-spawn-multiplexing

```markdown
---
decision: proxy-single-spawn-multiplexing
status: as-is
---

# Concurrent dispatches multiplex over one spawn

## Choice

Concurrent dispatches against one (run-scope, binding-name) multiplex over the single agent connection: each dispatch carries its own stream identifier, so a slow in-flight call never blocks a faster one sharing the same spawned process — and only one spawn is ever issued, even when several dispatches race to be the first against a given (run-scope, binding).

## Rationale

One process per binding keeps the spawn lifecycle state machine simple and preserves run-scope-lifetime semantics (`concept:host-agent-proxy`); racing spawns would duplicate whatever side effects the spawned binary's startup performs. Per-dispatch stream identifiers remove the head-of-line blocking a shared connection would otherwise impose.

## Alternatives

- Spawn-per-dispatch — rejected: process churn, duplicated startup side effects, and it breaks the one-spawn-per-(run-scope, binding) lifecycle the proxy commits to.
- One spawn with serialized dispatches (no multiplexing) — rejected: a slow call head-of-line blocks every faster call sharing the binding.
```

### Amend story: claude-agent-mcp-servers-per-node

```markdown
---
story: claude-agent-mcp-servers-per-node
status: as-is
---

# Template authors declare per-node MCP servers; operators bound them

## Story

As a template author using the bundled claude-agent executor, I declare per node which MCP servers that node's dispatch may reach, while the operator running the claude-agent service separately bounds which MCP servers any template may use, and the service enforces the intersection — so that template authors own per-node tool surfaces and operators own the boundary of what's permitted, without either reaching into the other's territory.
```

### Amend story: claude-agent-expose-env-per-node

```markdown
---
story: claude-agent-expose-env-per-node
status: as-is
---

# Template authors declare per-node expose-env; operators bound them

## Story

As a template author using the bundled claude-agent executor, I declare per node which environment variables that node's agent may read, while the operator running the claude-agent service separately bounds which variables any template may expose, and the service enforces the intersection — so that template authors own per-node secret needs and operators own the exposure boundary, with secret values never landing in rimsky's persisted state.
```

### Amend story: service-enrollment

```markdown
---
story: service-enrollment
status: as-is
---

# Standing service enrolls and obtains rotating credentials

## Story

As an operator deploying a standing service under mutual-TLS peer auth, I give it a single api-key carrying the enrollment grant; the service obtains its serving credentials at startup and renews them without operator action, and revoking that key stops future issuance — so that I manage exactly one credential per service, mintable, scopeable, and revocable in one place.
```

### Retire decision: typescript-claude-agent-retirement

Delete `.ok-planner/design/decisions/typescript-claude-agent-retirement.md`. It narrates a completed deletion event with no forward-looking choice; git history is the record. If the joint decisions pass (work item below) finds any of its content load-bearing and current (the copyleft licensing posture of the bundled service, the fake-CLI stub shape), it restates that content present-tense in the artifact that owns it rather than keeping the changelog.

## Work items

- **Repoint the node-reset citations.** The five code sites citing `@decision: node-reset-as-pure-retry-budget-clear` (`lib/control/controlapi/nodes.go` — the enforcement site, `lib/control/controlapi/app_test.go`, `test/scenarios/node_admin_e2e_test.go`, `test/scenarios/frame_resolution/reset_failed_node_drives_through_frame_engine_test.go` ×2) move to `@decision: node-reset-clears-failure-marker`. Realizes: `node-reset-clears-failure-marker`.
- **Annotate the isolation test for the new story.** `test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go` gains `@story: anonymous-agents-isolated` alongside its existing annotation (it is the end-to-end proof of the new story; `lib/runtime/hostagent/spawn.go`'s existing `@story: host-agent-anonymous-mode` annotation stays). Realizes: `anonymous-agents-isolated`, `host-agent-anonymous-mode`.
- **Joint decisions pass.** One read per file over the live decision catalog, applying three rules simultaneously: (a) missing `## Alternatives` (131 files) — author real Alternatives where a genuine rejected option existed; retire the file as a default where none did (a file failing both this and (b) is the strongest retirement candidate); (b) spec-altitude content (~20 files) — trim to Choice/Rationale/Alternatives, relocating schemas, literal routes, status codes, and algorithms into code and tests, with cuts governed by the tradeoff line: a Choice keeps the artifact whose identity carries the tradeoff (an algorithm, a lifetime, a binding property); route paths and env-var names strip — applied also to `decision:host-agent-proxy-tls`, `decision:enroll-token-is-api-key`, `decision:peer-auth-mtls`, `decision:secret-at-rest-posture`, and the same pattern in `concept:peer-auth`; (c) historical narration (16 files) — restate present-tense; `decision:signoff-crypto-ed25519`'s wire-compatibility guarantee survives as a present-tense contract statement. Retirements this pass makes are corpus deletions sanctioned by this work item. Realizes the rulings of `decisions-corpus-wide-missing-alternatives`, `decisions-spec-altitude-mechanism-detail`, `decisions-historical-language-residue`, `decisions-enumerate-routes-and-envs-in-body`.
- **Joint stories sweep.** One pass over the story catalog: strip delivery-surface boilerplate tails (`story:event-log-read`, `story:instance-lifecycle`, `story:lineage-admin`, `story:tag-management`, `story:template-lifecycle`, `story:runtime-diagnostics`); rewrite the three CLI-substance stories (`story:one-shot-to-terminal`, `story:script-friendly-outcome`, `story:spawned-local-services`) to outcome language with their surface commitments relocated into decisions (authoring a small decision where none exists); rewrite `story:mcp-transport` and `story:local-orchestrator-zero-config` as surviving pure statements with their parity/mechanism content moved to decisions; rewrite the mechanism-naming tail (~15 stories incl. `story:idempotent-mode-dedupes`, `story:iterative-workflows-converge`, `story:cascade-defers-during-flight`) to outcome language, converting legitimate references into concept citations; repair the two circular so-that clauses; give `story:iterative-workflows-converge` its missing mandatory so-that clause; strip remaining `rimsky.yml`/config-key mentions, absence-mentions ("no config file needed") staying as legitimate observables. Realizes the rulings of `stories-delivery-surface-named-in-body`, `stories-mechanism-prescription-tail`, `stories-name-rimsky-yml-and-config-keys` (the two claude-agent deltas above carry its determined core), `story-service-enrollment-mechanism-in-body` (delta above carries the rewrite; the sweep verifies nothing else regressed).
- **Grouped fitness tests for config-enforced decisions.** Build the three surface tests per `decision:config-enforced-fitness-tests` (dependency-lint config, module manifests, Makefile/image set), each carrying the `@decision:` annotations of the rules it asserts; then tag the remaining already-enforced decisions whose enforcement lives in code (the known-enforced bucket) with ordinary annotations at the enforcement sites. Realizes: `config-enforced-fitness-tests`, and the rulings of `build-file-enforced-decisions-uncitable`, `coverage-gap-decisions-bulk-160-uncited`.
- **Intent reconciliation and archive.** Walk each of the 77 dossiers under `design/intent/` against its live concept/story/decision counterparts: where the dossier holds intent the live corpus lacks or contradicts, fold it in (as amendments at the owning artifact); where a divergence is a genuine judgment call, file an intake issue per the issue-file format rather than deciding in the pass; where the corpus already carries the content, nothing moves. Fold the claim-scope naming symbols the fitness test relies on into `concept:claim-scope` and repoint `test/plumbline/claim_scope_naming_test.go`'s message to the concept. Then move `design/intent/` whole to `.ok-planner/history/intent/`. Realizes the ruling of `intent-directory-corpus-status`.

Real dependency: the intent reconciliation should run after the joint decisions pass and stories sweep, so dossiers are compared against the corpus's post-sweep state rather than text about to change.

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
