# Sprint: Intent-ledger disposition and corpus normalization

## Intent

This sprint starts the ingestion of the **intent ledger** — 77 per-concept dossiers at `.ok-planner/history/intent/`, distilled 2026-07-13 from the project's recoverable design history (transcript tier outranks artifact tier; later intent supersedes earlier) — and normalizes the design corpus's expression to the canonical ok-planner artifact form.

The ingestion is deliberately two-stage, because corpus commitments change only through final-form sprint deltas drafted at planning time, never through judgment exercised during execution. **This sprint** dispositions every ledger claim — already stated in the corpus, settled by a delta below, superseded, or converted into an intake issue — and changes no commitment beyond its own six promoted resolutions. The issues it files are made ruling-ready by `/verify-issues` and carried by the **next** planning ceremony, which drafts the resulting rewrites as full final-form deltas. When this sprint closes, the ledger is fully dispositioned and stamped as ingested at its archive path; the commitment-level rewrites it surfaced ride the intake.

Corpus expression is normalized in the same pass: all 430 catalog files carry a retired `status:` frontmatter field, story bodies carry prose beyond the canonical single-sentence form, `_retired/` holdovers sit inside all three catalogs (11 concepts, 3 stories, 4 decisions), and the three catalog TOCs name a retired refresh mechanism. Expression repairs that change no commitment are executed directly; anything commitment-shaped — story splits and collapses, prescriptive story content that needs a new decision as its home — becomes an intake issue instead.

Issues promoted into this sprint: `cli-archive-sboms-after-first-release`, `breakpoint-overlay-visibility-across-matchers`, `proxy-same-identity-register-displacement`, `instance-key-idempotent-create-uncarried`, `subclaim-realized-write-semantics`, `src-tag-script-violates-plumbline-lint`.

Each corpus delta below is the complete final file body, already in canonical form. Execution copies each into place verbatim; the expression-normalization work item leaves these five files exactly as the deltas state.

## Corpus deltas

### Amend concept: breakpoint

```markdown
---
concept: breakpoint
---

# Breakpoint

## What it is

A breakpoint is a runtime-installed pause-point on a live `concept:instance`, identified by a stable identifier and bound to a matcher, a checkpoint position, an optional signal-type filter, a mode (pause vs notify-only), an overflow policy, and an optional self-deletion TTL. Persisted in a per-instance breakpoint ledger; hits in a separate hit ledger. The breakpoint's dimensions span: where in the supervisor's per-dispatch flow it fires (pre-dispatch vs post-terminal), whether it blocks the runner or only records, how to handle hits that arrive faster than they are drained, and how long the breakpoint itself lives.

## Purpose

Enable agent-driven debugging of live rimsky instances. The agent installs breakpoints at the dispatch points it cares about, optionally pauses execution, inspects the snapshot, and optionally mutates the dispatch via a one-shot overlay before resuming. An unresumed pause-mode hit also gates `concept:control-api`'s debug-override channel, which lets the agent apply persistent node-attribute writes and invalidations against the paused instance — a separate, persistent mutation path distinct from the one-shot resume overlay. This is the runtime-cooperative half of `concept:control-api`'s debugger surface; `concept:instance`'s paused/resume affordance is the other half (instance-level hold).

The debug-override channel is frame-scoped: both of its verbs resolve "the node's latest run" as the latest run within the instance's currently active frame only, never a run from any other frame. A node with no run at all in the active frame is a no-op; a node whose only runs belong to a different frame is refused outright, naming the frame mismatch, rather than silently paired with the active frame. Within the active frame, a node-run that has already reached a terminal state is never mutated in place — an attribute write targeting such a node instead lands on a freshly created invalidated run for that node, which carries the terminal run's attribute bag forward. A node-run merely paused mid-frame (held or parked, e.g. sitting at a breakpoint) is not terminal and remains a legal direct target for both verbs. When the in-frame run is dispatched and in flight (running, held, or parked), an attribute write both lands on that run's live bag and queues one operator-invalidate stale run behind it, whose creation-time snapshot carries the merged bag forward — so the value takes effect immediately in the paused run and again on the post-resume re-run (per `story:operator-invalidate-queues-during-flight`); a run still waiting in the queue (stale or pending) just receives the merge, with no extra row.

## Boundaries

Owns: the per-instance breakpoint ledger and the hit ledger (schema, CRUD, sweeps); breakpoint matcher evaluation and hit-recording at the pre-dispatch and post-terminal checkpoints; the resume-with-overlay merge — layer L6, applied after `concept:attribute`'s L5 override merge — that feeds the one-shot resume payload into the dispatch; the per-mode overflow policies and a queue-cap on unresumed hits.

Does NOT own: the matcher grammar itself (shared with `concept:attribute`'s by_match via the common foundation matcher package); template-baked pauses (none exist — `concept:parked-state` is executor-emitted, this concept is operator-injected at runtime); the audit-log emission for the API surface (covered by the existing auth audit event kinds per `concept:event-log`); hit *delivery* (`concept:control-api` owns it, exposing **both** the read-only MCP resource-listing and resource-read extension and a read-only REST route that surface hits — this concept owns the ledger, not the transport); the runner's checkpoint wiring — invoking evaluation at the pre-dispatch and post-terminal points in the dispatch flow — and the blocked-runner resume-polling loop (owned by `concept:supervisor`: this concept owns what happens when a checkpoint is evaluated, not where in the dispatch flow it is invoked from).

Adjacent: `concept:supervisor`, `concept:control-api`, `concept:attribute`, `concept:instance`, `concept:signal`, `concept:permission`, `concept:parked-state`.

## Invariants

- Only the supervisor creates hit rows; resume and TTL housekeeping sweeps by other roles may update or delete an existing hit row, but no non-supervisor role inserts one.
- Resume is idempotent on `hit_id`: replays return the original outcome unchanged.
- A signal-type filter is rejected on pre-dispatch breakpoints at registration (the filter only makes sense on post-terminal hits).
- Pause-mode hits combined with a silently-dropping overflow policy are rejected at registration (pause-mode hits cannot be silently dropped).
- Notify-only mode combined with a blocking overflow policy is rejected at registration (the policy contradicts the mode's non-blocking semantics).
- The L6 resume overlay applies only to the single dispatch that hit the breakpoint; it never persists into the instance's stored attribute-overrides.
- A resume overlay joins the dispatch's effective attribute bag the moment it is applied: when several breakpoints pause the same dispatch and resume in sequence, each later breakpoint's matcher evaluates against — and each later hit's snapshot records — the bag as amended by every earlier resume's overlay, so what the operator inspects at a pause is what the dispatch will actually run with.
- An L6 resume overlay on a post-terminal hit is rejected at the resume API as an invalid-overlay error — the dispatch the breakpoint observed has already committed, so the overlay can never feed back into the run; accepting it would silently no-op.
- Cascade-deletion of a breakpoint (the hit rows are deleted with their parent breakpoint) unblocks any paused runner waiting on a hit of that breakpoint, treating the missing-row case as auto-resume with no overlay.
- Pause-mode breakpoint evaluation fails closed: an infrastructure error while evaluating or persisting a pause-mode hit blocks the dispatch rather than silently skipping the pause — a pause is an operator-requested gate, and the dispatch does not proceed past it on error. After-terminal (observation-only) evaluation fails open: failures are logged, never blocking settlement.
- The pre-dispatch checkpoint fires exactly once per executor-invoking dispatch attempt — once, before the executor is invoked, and not again on any subsequent retry re-invocation of the executor — regardless of how the dispatch's attribute bag was sourced: a sealed bag built earlier per `concept:node-run`, then substitution and override application at dispatch. Every branch of the dispatcher's attribute-resolution path reaches the checkpoint before the executor invocation. Executor-less dispatch attempts (the pure-cascade and claim-acquired dispositions) skip the pre-dispatch checkpoint: with no executor invocation, the assembled bag cannot mutate after assembly, so there is nothing to gate or overlay pre-dispatch; those dispatches are observed at the post-terminal checkpoint only.
- The debug-override channel never pairs the instance's currently active frame with a `concept:run-scope` or node-run that belongs to a different frame — per `concept:frame`'s perfect frame isolation, a RunScope never spans frames, so a node's latest run from a prior or otherwise inactive frame is not a legal invalidation or attribute-write target; a request that would require such a cross-frame pairing is refused. Attribute writes additionally never mutate a terminal node-run row in place, even one that belongs to the active frame — the write is redirected onto a freshly created invalidated run instead, which carries the terminal run's attribute bag forward.

## Policy differences from `by_match`

The breakpoint matcher shares its grammar with `concept:attribute`'s `by_match` overrides, but the validator's used-executors cross-check is intentionally laxer on the breakpoint side:

- The attribute by-match overlay rejects an executor key that names an executor not referenced by any node in the template (the override is dead).
- Breakpoint matchers treat every declared executor as valid regardless of template usage, so an operator can install a breakpoint against any declared executor — including ones the current template doesn't dispatch to. This supports cross-template debugger habits (an operator who runs a debug session against many templates can carry one matcher pinned to a specific executor even on templates that happen not to use that executor; the breakpoint just doesn't fire).

The breakpoint matcher still enforces every other cross-check: declared node types, existing graphs, declared deployment-level executors, and the closed grammar.
```

### Amend concept: host-agent-proxy

```markdown
---
concept: host-agent-proxy
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
- Same-identity registration is latest-wins for api-key-identified routing identities: a new authenticated registration under an api-key identity that already has a connected agent supersedes the prior connection — the proxy closes the prior connection gracefully, routes subsequent dispatches to the newcomer, and the registration acknowledgment tells the new agent it displaced a prior connection. This is what makes an agent restart safe: the restarting agent takes over from its own stale connection rather than being locked out by it. Anonymous routing identities are outside this rule: a label colliding with a currently-connected anonymous agent is rejected at registration, per the silly-name invariant.
- All dispatch failures surface as executor-error / claim-producer-unavailable terminals on the supervisor-facing protocol — no new synthetic supervisor-side acquire error classes.
- The proxy is declared in the rimsky config per protocol it serves, using the same binary across all entries (one endpoint, N namespace registrations).
- The proxy is the URL-rewriting boundary for rimsky URLs handed to spawned processes: the callback URL is the only URL it rewrites.
- The proxy's sanctioned late-bind surface is `concept:executor` and `concept:claim-producer`: both are transparent forwarders through one uniform spawn/forward mechanism, each presenting exactly the fronted service's protocol, and a service that conforms to its own protocol works behind the proxy by construction — so the proxy needs no protocol of its own to conform to; its transparency is proven instead by running the standard executor and claim-producer conformance suites against it, behind a spawned process stood up by an in-process agent test double. Late-binding `concept:publisher`, `concept:validation`, or `concept:data-processing` through the proxy is rejected loudly (an unimplemented-protocol error, not silent non-routing); the proxy is not the routing mechanism for those protocols.
- The schema a specific spawned binary actually accepts is unknowable until that binary's own capabilities handshake completes at spawn time, so attribute-schema conformance for late-bound executors is deferred from the usual registration-time check to per-dispatch: once a binary is spawned, the resolved attribute values are checked against the schema that binary itself advertised, and a mismatch settles the dispatch as a contract error rather than forwarding a payload the binary never promised to accept.
```

### Amend concept: instance

```markdown
---
concept: instance
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
- `instance_key` uniqueness is scoped per template: the same key string is legal under two different templates, and under one template at most one instance carries it. Creating an instance whose key already exists under that template is idempotent — the create returns the existing instance and ignores the rest of the request — so a deployment path that derives deterministic keys can re-apply its manifest as a no-op rather than a duplicate-instance factory.
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

### Amend concept: claim-tree

```markdown
---
concept: claim-tree
---

# Claim tree

## What it is

The tree-shaped relationship across claim handle rows, formed by the nullable self-referential parent pointer. A root claim handle has a null parent pointer; a sub-claim points at its parent's id. The structure mirrors the run-tree (which lives at the run-scope layer per `concept:run-scope`, with the parent-child shape on the run-scope ledger) but exists at the claim layer rather than the dispatch layer. Created by fan-out: the parent's split-scope verb returns N sub-scope descriptors and rimsky inserts N child claim-handle rows in the same acquisition transaction.

## Purpose

Used by the parent-resolution walk, part of the unified terminal-resolution engine (see `concept:terminal-resolution`): when a child claim resolves, the walk reads the parent's children, and only once every claim-holder row on the parent and every child claim-handle row is no longer active does it compute the parent's aggregate verdict per its snapshotted aggregation policy (see `concept:fan-out` + `concept:node-run`) and fire the parent's own terminal — which itself may walk further up to a grandparent. While any holder or child remains active, the walk records the child's outcome and stops.

## Boundaries

Owns: the self-referential parent pointer on the claim-handle ledger, the child-listing accessor, the recursive parent-resolution walk, the recursive descendant-cancel walk (fires unconditionally whenever any claim resolves to Abandon, independent of aggregation policy). Does NOT own: claim acquisition (see `concept:claim`, `concept:claim-handle`), state aggregation policy (see `concept:fan-out`), the run-tree (see `concept:node-run`), the proactive in-flight-sibling cancel that strict aggregation layers on top of a resolving child (see `concept:cancel-siblings`). Adjacent: `concept:claim-handle`, `concept:fan-out`, `concept:cancel-siblings`, `concept:auto-terminal`, `concept:terminal-resolution`, `concept:node-run`.

## Invariants

- The parent pointer nulls on a parent's deletion (rather than cascading) so a parent's deletion does not cascade-delete its in-flight children. The recursive descendant-cancel walk fires before the parent's own terminal resolution — promotion to abandoned, or deletion under the ownership-bail source — so descendants are not left orphaned in-flight. The walk is scoped to descendants held by the acting supervisor; a descendant held by a different supervisor is skipped and can remain active after the parent's abandon in multi-supervisor deployments (see `concept:cancel-siblings`'s multi-supervisor scope).
- A sub-claim is a claim: every rule that governs a root claim-handle row (payload persistence, claimant-guarded mutation, the state lifecycle) applies to sub-claim rows uniformly, and a sub-claim inherits its parent claim's declared intent, lifetime, and realized write semantics rather than declaring its own — the split verb never re-declares them. A sub-claim row carries the parent's realized write semantics from insert, so a later acquisition whose scope overlaps an active sub-claim receives a normal coexistence evaluation, exactly as if it overlapped the parent.
- Each non-root claim-handle row is reachable from exactly one root via the parent chain. The single parent pointer per row guarantees at most one parent; acyclicity, and with it the tree shape, is operational rather than structural — a row is only ever inserted pointing at a pre-existing parent under a freshly generated id, never rewired after insertion.
- Both recursive walks terminate because they are bounded by claim-tree depth. The descendant-cancel walk additionally shrinks its own frontier on every step: a resolved descendant leaves the active state (promoted, or deleted under the ownership-bail source) and the walk only ever recurses into rows still active, so no row is visited twice.
- The parent's aggregation counters (expected, committed, and abandoned child counts) are claimant-guarded (invariant 4): the mutation targets whichever supervisor currently holds the parent row at settlement time, not necessarily the supervisor that originally acquired it. A settling supervisor that does not yet hold the parent reassigns holdership to itself before firing the parent's own terminal resolution (see `concept:cancel-siblings`'s multi-supervisor scope for the sibling-cancellation side of this same boundary).
- For terminal children resolved through natural settlement or sibling-cancellation, the row is preserved by the promote transition and participates in the parent's aggregation counter. Children swept up by the descendant-cancel walk are preserved the same way but do not bump a parent's counter — their immediate parent is itself being torn down in the same walk, so there is no live aggregation left to feed. The descendant-cancel walk skips all non-active rows, so committed-durable children preserve the durable-Commit contract (no force-Abandon undoes a successful promotion) and committed-subgraph + abandoned rows aren't candidates for re-cancellation either.
```

### Amend decision: release-distribution

```markdown
---
decision: release-distribution
---

# Distribution channels

## Choice

Four channels: container-registry images with SBOM and provenance attestations, the protocols npm package, Go modules consumed from a full checkout, and GitHub Releases carrying prebuilt CLI archives built by goreleaser for Linux and macOS on Intel and ARM, each archive accompanied by a published SBOM. The CLI ships as prebuilt archives only: Windows is unsupported (the CLI transitively embeds Unix-only system calls), and network `go install` is unsupported (the workspace's modules are wired with local-path redirects that resolve only inside a full checkout).

## Rationale

Multiple consumption patterns need multiple channels — images for deployment, npm for protocol consumers, Go modules for embedders, prebuilt archives for CLI users without a Go toolchain. Naming the two non-channels in the Choice keeps each from being rediscovered as a bug: the Unix-only matrix and the go-install gap are consequences of deliberate choices (system-call usage, workspace module layout), not oversights.

## Alternatives

- A Homebrew tap — deferred, not rejected: broader macOS/Linux reach at the cost of a standing extra repo to maintain, with no requester yet.
- Publishing the workspace's sub-modules as independently versioned Go modules so `go install` works — rejected: a packaging overhaul far larger than CLI distribution itself, motivated by nothing current.
```

## Work items

- **Apply the five corpus deltas** (issues `breakpoint-overlay-visibility-across-matchers`, `proxy-same-identity-register-displacement`, `instance-key-idempotent-create-uncarried`, and the corpus halves of `subclaim-realized-write-semantics` and `cli-archive-sboms-after-first-release`): copy each delta body into place verbatim. The three behaviors the new breakpoint / host-agent-proxy / instance invariants describe are already implemented and test-pinned; those three are corpus-side only.
- **Sub-claim realized-write-semantics inheritance** (issue `subclaim-realized-write-semantics`): sub-claim rows carry the parent's realized write semantics from insert, so a later acquisition whose scope overlaps an active fan-out sub-claim receives a normal coexistence evaluation instead of failing with a misleading "holder open still in flight" error. Exercised end-to-end by a test in the ordinary suites.
- **CLI-archive SBOMs** (issue `cli-archive-sboms-after-first-release`): the formal-release chain publishes an SBOM alongside each of the four CLI platform archives on the GitHub release, matching the attestation posture the image channel already has.
- **Upstream lint-conflict report** (issue `src-tag-script-violates-plumbline-lint`): file a report with the tool suites' maintainer stating that the workspaces suite's canonical `tools/image-src-tag.sh` payload fails the plumbline comment-hygiene lint and carries a citation tag that resolves to no artifact here, and that this project holds a lint-ignore entry as a temporary bridge to be removed when a lint-clean payload ships. No corpus change.
- **Expression normalization** — repairs that change no commitment, executed directly, each recorded in the completion report for after-the-fact veto:
  - Frontmatter across all three catalogs: strip `status:` fields, empty `aliases: []` lists, and any path-form `references:`.
  - Story bodies: reduce to the `## Story` section alone wherever the canonical sentence already exists (or is determined by the rules) and every commitment in the removed prose survives — restated by the sentence, already stated by a concept or decision, or pure restatement. A story where the reduction fails that test is left untouched and handled by the restructure-proposals item below.
  - Delete the `_retired/` directories under `concepts/`, `stories/`, and `decisions/` (git history is the archive).
  - Regenerate the three catalog TOCs against the final catalogs, with header text naming sprint execution as the refresh mechanism.
  - The five delta files above end exactly as their deltas state; this item makes no further change to them.
- **Story restructure proposals as issues**: review every story against the canonical story rules and file one intake issue (kind `sprint`) per commitment-shaped restructure: a story carrying two distinct user-outcomes (split), stories that are one outcome expressed per-surface (collapse, surface choice to a decision), and prescriptive story content whose only honest home is a new or amended decision. No corpus edit rides this item; the issues carry the proposals to the next planning ceremony.
- **Intent-ledger disposition** — issues only, no corpus or code edits: for each claim in each dossier's Net position, Required behaviors, Intentional absences, and Corrections-and-restorations sections across all 77 dossiers, disposition it as (a) already committed to by the corpus, (b) settled by a delta above or by an issue already in the intake or its history (dedupe by slug), (c) superseded within the ledger's own record, or (d) none of those — file an intake issue (kind `sprint`) capturing the divergence, the evidence tier, and the candidate resolutions (align the corpus, change the code, or retire the intent claim). A dossier's Conflicts-needing-human-ruling section files one issue per unresolved conflict. The completion report records the per-dossier counts for each disposition class, so "fully dispositioned" is a set of counts, not an assurance.
- **Ingestion stamp** (depends on the disposition item): append one line to the ledger's `README.md` at `.ok-planner/history/intent/` recording that this sprint dispositioned it, so a future reader knows the ledger is consumed history rather than an unprocessed queue.

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
