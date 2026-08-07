# Sprint: Ruled-intake drain — race-gate retirement and eleven verified fixes

## Intent

Drain the ruled issue intake. No single theme: the race-detector
retirement's corpus consequences, a registration-time validation gap,
two proxy trust-posture gaps, four config-surface fixes, two
corpus-alignment survivors of the design-log sweep, and a
docs-projection direction. Every item enters with an owner ruling
already made; nothing here is open design.

Promoted issues:
`race-suite-ceilings-are-performance-assertions`,
`retry-backoff-values-unvalidated`,
`bare-string-closed-value-sets-untyped`,
`resolve-target-agent-bypasses-identity-overrides`,
`allow-paths-env-var-absent`,
`claude-agent-declared-tags-inert`,
`max-retries-without-progress-vestigial`,
`compose-demo-scripts-unrunnable-when-vendored`,
`proxy-missing-control-api-ca`,
`proxy-unsustainable-mtls-posture`,
`design-log-contradictions-sweep`,
`empty-struct-doc-comments-in-projected-references`.

## Corpus deltas

### Retire decision: race-gate-split

Delete `.ok-planner/design/decisions/race-gate-split.md`. The
mechanism it chose — a single-iteration race slice in the everyday
gate plus a repeated race gate at release — no longer exists anywhere
in the build; the project's standing rule now excludes the race
detector from every gate. Drop the `race-gate-split` row from the
decisions TOC, and delete the retired decision's implementation-audit
record `.ok-planner/audits/decisions/race-gate-split.md` with it (the
audit corpus holds one file per live decision; git history preserves
the record).

### Amend decision: release-chain

```markdown
---
decision: release-chain
---

# Shared release chain

## Choice

Lint → license lint → build the core images → build the bundled-service images → run the full test suite → scan the built images → push the images.

## Rationale

Comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set.

## Alternatives

- Tests before image builds (the conventional order) — rejected: the scenario suites consume the locally-built image set, so the images must exist first.
- Separate bespoke chains for the formal and dev release flows — rejected: two chains drift; both flows share this one.
```

### Amend decision: race-injection-hooks

```markdown
---
decision: race-injection-hooks
---

# Deterministic race injection at defended seams

## Choice

The runtime's deterministic injection-hook pattern (a post-commit hook a test can use to force a precise interleaving) extends to deterministic injection tests at the defended concurrency seams: the acquire-unavailable abandon path, the folded ownership-bail path, the held-claim aggregate check-and-fire, and the orphan-reaper vs in-flight-terminal overlap.

## Rationale

These are designed defenses against inherent multi-replica collisions; deterministic forcing proves the defense and pins it against refactors — strictly stronger than probabilistic race-detector luck.

## Alternatives

- Relying on the race detector alone — rejected: probabilistic; a green run proves nothing, and a scheduler-dependent interleaving can survive any finite repetition budget unexercised.
- Sleep-based timing tests to provoke the interleavings — rejected: nondeterministic verdicts, which the project's test discipline forbids outright.
```

### New decision: host-agent-proxy-enrollment

```markdown
---
decision: host-agent-proxy-enrollment
---

# The proxy enrolls in the deployment trust domain and pins its control-API trust anchor

## Choice

Under mutual-TLS peer auth the host-agent proxy enrolls with the control API through the same enrollment machinery every bundled service uses, renews its short-lived leaf automatically, and serves the supervisor-facing protocols on a listener that requires and verifies client certificates against the deployment CA — separate from the agent-facing listener, which keeps its `decision:host-agent-proxy-tls` posture (server TLS toward agents, no client certificates). Independently of peer-auth mode, the proxy's outbound control-API clients accept the same CA-bundle trust anchor bundled services read (`RIMSKY_CONTROL_API_CA`), honored whenever the control-API URL is HTTPS.

## Rationale

`decision:peer-auth-mtls` commits every operator-deployed standing service to enrollment, and the proxy is one; a hand-provisioned static certificate with a short-lived-leaf TTL and a listener that never verifies clients cannot satisfy that commitment, and manual re-minting on the leaf cadence is not an operable posture. Splitting the serving legs keeps the agent-facing hop non-mutual — the agent is per-user session tooling, per `decision:host-agent-proxy-tls` — while making the supervisor-facing hop genuinely mutual. The control-API trust anchor is unconditional because the proxy's control-API calls run in every peer-auth mode: a deployment whose control API serves a private-CA certificate must be able to run the proxy without turning on mutual TLS.

## Alternatives

- A long-lived certificate carve-out for the proxy — rejected: abandons the uniform short-lived-leaf posture for exactly one service.
- Sanctioning plaintext on the proxy's peer entries under mutual-TLS peer auth — rejected: one plaintext internal hop contradicts the trust domain's every-leg claim.
- System trust store only for the outbound control-API client — rejected: locks private-CA deployments out of running the proxy at all.
```

### Amend concept: error-policy

Full new body — two changes against the live file: the
policy-evaluation-cursor invariant grows the emitted-counter
statement, and a new invariant closes the retry-backoff vocabularies.

```markdown
---
concept: error-policy
aliases:
  - error-types policy chain
---

# Error policy

## What it is

A template-level error-routing surface that maps each error class to one of four runtime actions: `retry`, `give_up`, `pass`, or `release_and_requeue`. The runtime looks up the per-class action at terminal-error dispatch. The node-level `MaxRetries` cap bounds the total number of retries across a single dispatch (counted across all error classes that occur in that dispatch); when retries exhaust the cap, the runtime synthesizes a give-up settling the run as failed.

Error routing is the decision surface for operator-side error handling: every operator-side error variant — an executor-emitted failure or a runtime acquisition failure — arrives carrying an error class and is matched against the per-class action map. A reserved class-name namespace covers runtime acquisition failures (one synthetic class for unavailable claims, another for unclassified producer faults); operators wanting retry-on-acquire declare a policy keyed by the relevant class. A producer may name a more specific class on an acquisition failure (declared in its capabilities vocabulary); the policy lookup for acquisition failures falls back from the exact producer-declared class to the synthetic family class. Without any matching entry the lookup returns nil and the runtime gives up with an unknown-class reason — fail-fast is the default; retry is opt-in. Infra-class dispatch faults bypass this map entirely and are governed instead by `concept:terminal-resolution`'s supervisor-side retry cap (see Invariants).

Error-class keys are range-checked at registration against the union of the declared vocabularies a key may legitimately come from: the node's executor's declared error classes, the runtime-synthesized classes (including the reserved acquisition-failure family), and the declared error classes of every claim producer reachable from the node's claims (producers advertise their vocabulary in the capabilities handshake; declaring nothing remains legal). A declared vocabulary entry may end in a trailing wildcard, covering every class beneath its prefix — the same trailing-wildcard convention `concept:signal` fixes for subscription targets. When at least one peer vocabulary is known, a key attributable to no declared vocabulary registers as an advisory warning, never a hard rejection — the validator must accept whatever the runtime is able to route, and undeclared peer vocabularies must not lock operators out of their own routing. When no peer declares any vocabulary at all, there is nothing to range-check against, and the key registers silently, with no warning.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics, lets the platform uniformly bound runaway retry loops, and treats executor `Error{class}` and runtime acquisition failure under one chain.

## Boundaries

Owns: the closed action vocabulary, the per-class action lookup (covering both executor-emitted failures and acquisition failures), the per-dispatch retry-budget cap (`MaxRetries`), the closed retry-backoff vocabularies and their registration gate.

Does NOT own:
- The signal type-path taxonomy (lives in `concept:signal`).
- Cascade firing (lives in `concept:cascade`).
- Terminal-resolution stitching from terminal event to producer verb (lives in `concept:terminal-resolution`).

Adjacent: `signal`, `frame`, `terminal-resolution`.

## Invariants

- The per-node `MaxRetries` cap on operator-side errors (errors routed through the per-class action map) defaults to disabled (unbounded retries); an explicit positive value enables it. Infra-class errors — runtime-synthesized dispatch faults that occur before an executor error is possible, owned by `concept:terminal-resolution` — skip the per-class action map entirely; no operator-declared policy for the class is consulted. They retry under an internal supervisor cap that defaults to 10, except that an operator-declared node-level `MaxRetries` overrides the supervisor default whenever set.
- The release-and-requeue action releases the run's acquired claim handles and re-enqueues the run for a fresh acquire on the next dispatch: a claim outside held-subgraph coordination is abandoned immediately, firing its producer's abandon verb directly; a claim under held-subgraph coordination instead marks the run's holder failed and defers the abandon verb to `concept:auto-terminal`'s resolution. The plain retry action preserves claims and re-invokes the executor in place.
- The pass action settles the run with a fresh color (treating the error as benign for downstream cascade purposes). The per-class action map is a flat lookup; a class mapped to pass always passes — there is no per-attempt action-chain advance.
- The give-up action settles the run with a failed color.
- The retry action is in-place on the existing node-run row (see `decision:in-place-retry`): the runner sleeps the policy's retry delay and re-attempts the same operation — re-invoking the executor against the same dispatch context for an executor-emitted error, or re-attempting claim acquisition for an acquisition-failure error. Claims already held stay acquired, the persisted attribute bag stays unchanged, the dispatch id is preserved, and the node-run stays in its pre-retry state across the loop — `running` for an executor retry, `stale` for an acquisition retry. No new node-run row is created for retry; only the per-dispatch retry counter advances.
- The policy-evaluation cursor is a single per-dispatch retry counter, persisted on the node-run row (see `concept:node-run`). A new node-run for the same node starts at zero — the retry budget is per-dispatch, not cross-dispatch. Emitted error and retry signals report this counter: the terminal-error payload's `attempt` and `retries_so_far` fields both carry the counter's value at emission — the number of retries completed before the signal — and the transient-retry signal's type-path embeds the same value.
- The retry-backoff vocabularies are closed and range-checked at registration: a `retry_backoff.kind` or `retry_backoff.jitter` value outside the declared vocabulary refuses registration as a hard error, the same shape as the closed action-vocabulary check — never a silent fallback at retry time.
- The reserved acquisition-failure namespace lets operators declare policies keyed by an acquisition-failure class. The acquisition-failure policy lookup resolves in fallback order: the exact producer-declared class first (when the producer named one on the failure), then the synthetic family class for that failure kind (one for unavailable claims, another for unclassified producer faults). Without any matching entry the runtime gives up with an unknown-class reason (fail-fast; retry is opt-in). The fallback affects policy lookup only; the emitted signal carries the most specific class.
- The retry → `MaxRetries` cap → synthesized give-up chain is the sanctioned escalation for a transiently unreachable peer. A dispatch aimed at a service that is temporarily offline — a developer's agent behind the proxy, an executor mid-restart — surfaces as an ordinary executor error and walks this chain; there is no separate "peer offline" alerting mechanism, and settling the run as failed when the cap exhausts is the deliberate threshold-then-escalate shape.
```

### Amend concept: terminal-resolution

One change against the live file: a new invariant sanctions the
administrative-kill record-only fallback (the body is otherwise
verbatim the live file; the full body appears here because the
artifact changes).

```markdown
---
concept: terminal-resolution
aliases:
  - executor-terminal-spine
---

# Terminal resolution

## What it is

The end-to-end spine that takes a single executor Outcome off the wire and converges it onto exactly four decisions: (1) what canonical signal type-path to emit (and the verdict's `tags` set as the discriminator that subscribers CEL-filter against), (2) whether the node-run row settles — state-transitioned and its dispatch entry removed from the queue — or the dispatch retries in place with the node-run row and its queue entry both preserved (see `decision:in-place-retry`), (3) what producer verb (`Commit` / `Abandon` / nothing) to fire on every acquired claim, (4) when to delete the persisted claim-handle rows claimant-guarded. Four stages stitched across the runtime. The same four-stage spine handles the executor error outcome and runtime acquisition failure uniformly — acquisition-failure routes through the operator's `error_types:` chain via the producer-declared class else the synthetic acquisition class (see `concept:error-policy`).

> **Vocabulary note.** "Terminal" is not a wire-protocol term. The wire layer carries a single Outcome with one of four variants (success, error, park, await-async-callback); the unary RPC returns the Outcome directly. The word "terminal" is reserved for two narrower senses: (a) the state-machine sense — the `concept:node-run` terminal states and the unified claim-handle resolution decision-engine entry point; and (b) this concept's name as the convergence-spine umbrella. The internal terminal-kind classification is a supervisor-internal categorization, not a wire shape.

1. **Wire to internal terminal kind** — the executor-outcome reader maps each of the wire's four Outcome variants to its own internal terminal-kind: success to completion, error to error, park to park, await-async-callback to async-accepted. A nil or otherwise unrecognized Outcome synthesizes a fifth internal terminal-kind, infra, so every dispatch path resolves to a terminal-kind even when the wire contract is violated. The settling verdict's tag set rides into the cascade-walk's CEL filter on the tag set (see `concept:terminal-tag`).
2. **Dispatch on terminal kind** — the terminal-application step routes the four kinds (completion, error, infra, park) to their per-kind handlers and increments a per-class terminal-verdict counter. Acquisition failure (pre-dispatch) routes through the acquire-unavailable handler into the same Stage-3 entry point via the producer-declared class else the synthetic acquisition class.
3. **Resolution** — produces the canonical resolution tuple of signal, dispatch disposition, and color per `concept:error-policy`. Runs the operator's per-class error-policy chain when the terminal kind is an error or when an acquisition-failure class (the producer-declared class else the synthetic acquisition class) is in play. For completion, park, await-async-callback, and infra the resolution is fixed by the terminal kind — no operator-configurable policy chain.
4. **Claim-handle resolution** — the lock-release step walks the dispatch's acquired locks. A named-lock acquisition → claimant-guarded handle delete only. A non-held claim → the unified claim-handle resolution directly, with an active-terminal source. A held claim → mark the persisted claim-holder row + check-and-fire; if the holding subgraph is complete, that engine computes the aggregate outcome (any failed → Abandon; else Commit) and calls the unified claim-handle resolution with a held-terminal source. The verify-before-run bail (the supervisor discovers post-commit that another supervisor stole the dispatch and unwinds the acquisition it just opened) also calls the unified claim-handle resolution, with an ownership-bail source — under that source the engine enqueues Abandon and deletes the handle row claimant-guarded, emitting no signal (admin path). The unified claim-handle resolution records the disposition and enqueues the producer verb on the terminal-verb outbox, then resolves the persisted claim-handle row claimant-guarded, all inside the settlement transaction — the single audited disposition-then-enqueue site for all three sources. The verb itself is a notification of the already-made decision, never a decision point.

### Terminal-verb outbox

Producer terminal verbs (Commit / Abandon / Release) are delivered from a durable, ordered, per-producer outbox rather than dialed inside the settlement transaction. The claim-handle disposition is decided and durably recorded at settlement, before any producer is dialed; the undelivered verb survives as a persistent row — across frames, runs, restarts, and the instance's own termination or deletion (its lifetime is tied to the producer relationship, not the instance) — and a background dispatcher delivers it with retry-with-backoff, paced by the injected clock and independent of future demand. The cascade never waits on a producer ack. Delivery is strictly ordered per (producer, claim scope): a failed or not-yet-due head-of-line row blocks later verbs on the same scope while unrelated scopes and producers proceed, and a new Open against a scope first drains that scope's undelivered terminals (delivering them through the same path) before the producer is asked to open — a recovering producer processes its queue in order and never guesses. Delivery is at-least-once; the producer-side idempotency invariant on terminal verbs makes redelivery safe. Per-attempt deadlines are connection hygiene only. Undelivered-terminals-per-producer is a diagnostics surface: visibility is rimsky's job, remediation the operator's. A Commit delivery whose response carries a version identifier or fan-out child producer metadata applies those effects post-delivery.

One carve-out sits outside the unified engine: the acquire-unavailable handler. It runs *before* dispatch, when the acquisition attempt returns the unavailable sentinel. It enqueues Abandon for already-Open'd partial claims on the terminal-verb outbox and routes through the error path with the producer-declared class else the synthetic acquisition class for state-machine + queue mutation. The carve-out exists because the acquisition tx has already rolled back — the persisted claim-handle rows are gone, so there is no claimant-guarded delete to fold into the unified engine, and folding it anyway would force the engine to grow a no-rows mode that dilutes its single audited disposition-then-enqueue promise.

### Terminal kind → emitted signal → producer verb

| Terminal kind | Emitted signal | Active-claim verb | Held-claim aggregate |
|---|---|---|---|
| Completion | Success terminal | Commit | Commit if all completed |
| Error | Per-class error terminal (give-up or pass paths) or per-class transient retry signal | Abandon on give-up; preserved on retry | Abandon if any failed |
| Infra | Per-class transient infra retry signal below the infra retry cap; per-class error terminal at the cap | Abandon on give-up; preserved on retry | mark failed + check |
| Park | Park terminal (time-wake at resume-at) | none — claims retained | none — claims retained |
| Await-async-callback (transient) | Await-async transient signal | none — no settling verb on first pass | none — callback's eventual terminal drives verb emission |
| Acquisition failure (pre-dispatch) | Per-class error terminal (producer-declared class else the synthetic acquisition class) | Abandon partial-acquired (outbox enqueue — the single carve-out outside the unified engine) | n/a |
| Verify-before-run race (orphaned-claim bail) | (no signal — admin path) | Abandon (via the unified engine, ownership-bail source: enqueue then claimant-guarded delete) | n/a |

## Purpose

The four constituent concepts each describe one stage; none on its own makes visible how an `Errored` event from an executor ends up calling `Abandon` on a claim-producer several steps later. This concept threads the spine so a reader can trace a single terminal event from the wire through to the producer verb and the claim-handle row deletion.

## Boundaries

Owns: the four-stage flow as one coherent narrative, the kind→signal-type-path→verb table, the convergence-point story (two convergence points: the per-acquired-lock fan-out at lock release, and the per-claim-handle producer-verb site at the unified claim-handle resolution). Does NOT own: any stage's internals (those are the constituent concepts). Adjacent: `concept:executor`, `concept:signal`, `concept:terminal-tag`, `concept:error-policy`, `concept:auto-terminal`, `concept:claim-handle`, `concept:parked-state`.

## Invariants

- The unary `Execute` RPC returns exactly one Outcome carrying one of four variants — Success / Error / Park / AwaitAsyncCallback.
- Every kind except await-async-callback flows through the terminal-application step; a `retry` disposition loops in place on the dispatch's already-acquired locks without releasing them, while `give_up`, `pass`, and `release_and_requeue` dispositions all end in the lock-release step for the dispatch's acquired locks (see `concept:error-policy`). Park's resolution is fixed rather than policy-driven (see Stage 3) and its claims are retained rather than routed to the lock-release step.
- The unified claim-handle resolution is the single audited site that records the disposition, enqueues the producer `Commit` / `Abandon` verb on the terminal-verb outbox, *and* resolves the persisted claim-handle row claimant-guarded (invariant 4), all in one settlement transaction. Its source kinds are active-terminal, held-terminal, and ownership-bail — all three converge here. The ownership-bail source deletes the row (the acquisition is unwound, not resolved) and emits no signal.
- Undelivered terminal verbs are durable outbox rows delivered at-least-once with clock-injected retry backoff, in strict per-(producer, claim-scope) order; a new Open against a scope queues behind — and first drains — that scope's undelivered terminals. Verb delivery failure never rewrites a recorded disposition.
- Administrative instance kill resolves claims through the same outbox path; when kill-time resolution cannot reach it — the producer absent from the registry, or the resolution call itself failing — the claim's disposition is recorded with no producer verb enqueued, so the kill always lands. Record-only resolution is the sanctioned fallback for the administrative kill path alone; ordinary settlement never bypasses the outbox.
- The acquire-unavailable handler is the single carve-out outside the unified claim-handle resolution: its acquisition transaction has already rolled back, so no claim-handle rows exist and only the outbox Abandon enqueue fires against the producer's partial opens.
- The retry-loop cap at Stage 3 short-circuits before policy lookup. A per-class pass action in the operator's error-policy chain settles the run as cleanly-resolved and ends the dispatch without retry — bypassing the cap by design.
- The await-async-callback outcome re-enters the spine through the callback path; the final terminal event produced there feeds back into the terminal-application step.
```

### Amend concept: inertness

One change against the live file: invariant §24 adds the receipt-time
body-schema validation as a sanctioned read site.

```markdown
---
concept: inertness
aliases:
  - inert bytes
---

# Inertness (cross-cutting discipline)

## What it is

A uniform discipline applied across two overlapping lists.

**Carrier streams the discipline governs:** claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, attribute values, message payloads, scratch (per `concept:executor`), executor error payloads. Each stream is "inert" in rimsky — rimsky neither inspects nor interprets the bytes beyond a narrowly defined set of read sites.

**Read-site sub-disciplines** distinguish how strict the rule is per stream:

- **Byte-opaque inertness** — rimsky treats the bytes as meaningless outside the enumerated sanctioned sites below; it does not log, format, validate beyond schema gates, or otherwise interpret them. Applies to: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, scratch. Rimsky reads them only at substitution-leaf extraction (which may walk to a named path within the bytes), at the byte-equality conflict comparison (claim scope), at blob-spill movement between the inline column and the backend (blob content), or for transport into the executor's wire (claim payload, address); each owning concept enumerates its own stream's exact sites.
- **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) and for the precisely-enumerated sanctioned read sites below, but does NOT inspect values to make routing or validation decisions outside those sites. Applies to: attribute values, message payloads, executor error payloads. Rimsky reads them only at the sanctioned read sites; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, indexes, attaches to traces, or includes them in error messages, except as the sanctioned sites below require. Two sanctioned sites traverse for content-based matching rather than plain transport: the shared matcher evaluator performs primitive-equality comparison against a single named attribute path, with no traversal beyond that path; node-subscription payload predicates evaluate a CEL expression over the emitted signal payload, spanning all three structurally-inert streams (attribute-delta, message-body, and error-payload fields, each validated against the receiver's schema at registration). A downstream owning concept may compute a value hash strictly to keep inert bytes out of a derived record it owns (for example, the lineage record's attribute-bag hash) — this sanctions that one derived-record use, not hashing generally.

## Purpose

Rimsky is a project-agnostic substrate. Logging, normalizing, or otherwise inspecting carrier bytes would couple rimsky to the carrier's semantics. The discipline keeps rimsky narrow: the bytes go in one side and come out the other unchanged, except at the precisely-named substitution leaf and transport boundary.

The same leveling discipline extends beyond bytes to vocabulary: anything executor-specific lives behind the executor protocol, and rimsky's core, scheduler, and persistence surfaces carry no executor-specific fields, tables, or terms — executor-private state (session material, resume context, checkpoints) rides the generic opaque carriers, chiefly scratch (see `concept:executor`).

## Boundaries

Owns: the cross-cutting "don't inspect" rule, the enumerated sanctioned read sites, the per-stream invariant annotations, and the two-sub-discipline taxonomy. Does NOT own: any one of the streams individually (each has its own concept and schema home). Adjacent: `concept:claim`, `concept:claim-scope`, `concept:blob-backend`, `concept:attribute` (substitution is the sanctioned exception), `concept:message`, `concept:executor`.

## Invariants

Three invariants codify the discipline:

- **§20** — claim payload, address, and claim scope are byte-opaque inert (carried on the claim-result value type).
- **§21** — blob content (carried by the blob-backend interface) is byte-opaque inert; executor error payloads are structurally inert.
- **§24 (message-inertness)** — message payloads are inert. Read only at the substitution leaf (resolving the trigger message), at persistence-layer fetches that surface message rows (single or list), at delivery time, when the message-receiver-node's attribute bag is populated from the message body under the same structural-inertness discipline that governs attribute values, and at the receipt-time body-schema validation, where the one shared ledger-insertion chokepoint validates the body bytes against the declared type's body-schema (see `concept:message-schema`) before any row lands. The message delivery path also touches envelope routing fields (type, sender, sender_kind, frame_id, instance_id, cancelled, delivered_at, received_at).

Sanctioned read sites are precisely enumerated by the per-stream owning concepts: each owner concept names the sites where the discipline permits a read. Read shapes vary by site — verbatim extraction (a substitution-leaf read returns the resolved value unchanged), equality comparison (the claim-scope conflict predicate, the shared matcher evaluator), schema validation (the message receipt gate), or wire transport (executor dispatch) — but no site inspects a value for a purpose beyond its own narrow contract, and no site logs, formats, or includes the value in an error message. The scratch carrier permits persistence when scratch arrives attached to a settling outcome and copy onto the subsequent re-dispatch (there is no mid-dispatch scratch write channel, per `concept:executor`); bytes remain opaque throughout.

## Auth audit log: verbatim request bodies

Auth audit-log rows store the request body verbatim, sanctioned by inertness (see `concept:event-log`). This is a deliberate policy choice, not a consequence of the inertness discipline itself: control-plane request bodies are treated as carrying no secrets, since the one sensitive value in an auth-relevant exchange — the API key — travels in the auth header per `concept:control-api` / `concept:api-key` and is never stored. Verbatim params make the audit log materially more useful for forensic queries without violating inertness.
```

### Amend concept: message

One change against the live file: the payload-inertness invariant
enumerates all four sanctioned read sites, matching invariant §24.

```markdown
---
concept: message
---

# Message

## What it is

A typed envelope whose arrival at an instance enqueues on the instance's message queue. When a message is picked up from the queue by the frame engine — either because the instance is idle at pickup time, or once the instance's running frame settles — that message opens the next frame. The envelope's type selects an entry from the instance's template message-schema registry; an undeclared type is refused at receipt with an unknown-type response. Persisted in the message ledger on receipt; delivered to the receiver at frame open, one message per frame. Cascade-sent, operator-sent, and publisher-sent messages traverse the same enqueue-then-pickup path.

The envelope carries an identity, the target instance, the typed body (inert), a receipt timestamp, and sender attribution (a sender identifier plus a sender-kind discriminator that distinguishes the three origin classes — operator, publisher, instance). Receivers are decided by subscription to the message type as a node-type. Every message type — both the author-declared types and the runtime-implicit empty type — materializes one message-receiver-node in `rimsky_nodes` at instance creation (`node_type = <message-type>`, empty executor, no `subscribes:`, no `source:` directives); each delivery creates a node-run for that node directly in `stale` state with the message body as its bag, dispatched via the empty-executor `pure_cascade` settle path, with the standard cascade walker propagating `terminal/success` to subscribers. The empty-message type is reserved-for-runtime per `concept:message-schema` and its receiver-node is materialized alongside the author-declared receivers; structural roots subscribe to it via the runtime-injected edges keyed by sender=`""` per `decision:subscription-edges-only-from-explicit-block`. There is no envelope-side routing field.

## Idempotency

The message-send endpoint requires an idempotency key on every request; requests without one are refused. Rimsky computes a dedup tuple over the target instance, the requester's identity (distinct callers with the same key never replay each other), and the supplied key, then writes a dedup-ledger entry under uniqueness; on conflict the handler returns the original message identity, with the replay distinguished from a fresh insert at the transport-status layer (response body shape is identical). Dedup records expire on a configurable trailing window swept under the scheduler-tick advisory lock. See `decision:message-sender-kind-discriminator` for the relationship between the dedup-layer sender-kind discriminator and the envelope-side sender-kind.

The idempotency feature is universal — operator retries, publisher sends, and lifecycle handlers all use the same idempotency-key surface.

## Boundaries

Owns: the envelope shape and the message ledger; the one-message-per-frame delivery rule; the materialization of one message-receiver-node per message type at instance creation (both author-declared types and the runtime-implicit empty type); the delivery-time creation of a node-run for that node (creation reason `message_delivery`, bag = message body); the dead-letter path for a message whose message-receiver-node is missing (not merely subscriber-less): delivery returns without creating a run or dispatching, and writes a dead-letter audit-event row so the miss leaves a ledger trace; the universal idempotency-key dedup ledger; the registry lookup gate on receipt. Does NOT own: the type registry itself (see `concept:message-schema`); the instance-scoped message queue and its coalesce mode (see `concept:instance`); cascade walks within a frame (see `concept:cascade`); the frame creation mechanics (see `concept:frame`); the publisher's substrate state (see `concept:publisher` / `concept:publisher-subscription`); the send-node's dispatch (see `concept:message-sender-node`); the dispatch and settlement of the message-receiver-node itself (those are the standard `concept:node-run` mechanics). Adjacent: `concept:frame`, `concept:instance`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:message-schema`, `concept:message-sender-node`, `concept:node`, `concept:node-run`.

## Invariants

- Two external send sites and one internal: operator API (the message-send endpoint with operator-origin sender attribution), publisher sends (the same endpoint with publisher-origin sender attribution plus a publisher-subscription capability token), and cascade-send (a message-sender node's dispatch, with instance-origin sender attribution naming the dispatching instance). All three paths land in the same ledger and follow the same delivery rules. The instance-origin sender attribution is unambiguously cascade-send; the runtime synthesizes no envelopes.
- One message per frame. At each frame open, exactly one message on the instance's queue is picked up and delivers; the rest stay pending on the queue (or, under `message_queue_mode=coalesce` per `concept:instance`, are cancelled at receipt so only the newest remains).
- Administrative instance termination cancels every still-pending message on the instance's queue inside the terminate transaction; a terminated instance rejects new messages, and nothing queued before the kill ever opens a frame.
- Type lookup at receipt: a message whose type is not declared in the target template's message-schema registry is refused with an unknown-type response; loud miss, not silent dead-letter. Every template's declared-types set carries an implicit empty-type entry seeded at registration, so empty-typed messages pass receipt under the same uniform check.
- A message cancelled by `message_queue_mode=coalesce` (see `concept:instance`) is never delivered even if it was the frame's triggering message when cancellation raced the delivery sweep: delivery excludes cancelled messages, so a coalesce-cancelled trigger never spawns a receiver run.
- Delivery at frame boundary: for every message type (author-declared or the runtime-implicit empty type), the runtime creates a node-run for the corresponding message-receiver-node directly in `stale` state (`creation_reason: message_delivery`), populates the run's attribute bag from the message body before settle, and the scheduler dispatches via the empty-executor `pure_cascade` settle path; the run settles `fresh` emitting `terminal/success` with an empty `attributes_delta` — the body already reached the bag via the pre-settle population, not via the terminal/success payload; nodes subscribing to the message-receiver-node stale-mark via the standard cascade walker; the message's delivery timestamp and frame reference populate. Structural roots subscribe to the empty-type receiver via the runtime-injected edges under sender=`""`; that is the sole mechanism that wakes them from an empty-message send.
- Payload is inert (see invariant: 24). Read only at the substitution leaf, at persistence-layer fetches that surface message rows, at delivery-time population of the message-receiver-node's attribute bag, and at the receipt-time body-schema validation at the ledger-insertion chokepoint (see `concept:message-schema`).
- Publisher requests are capability-checked at the existing publisher-subscription validation: rimsky validates that the publisher-subscription is in the active or mounting state for the target instance.
- The message-body substitution directive is sugar for the node-attribute substitution directive against the message-receiver-node — both resolve through the same lookup against the same `rimsky_node_attributes` row. The only difference is a registration-time check that the named type is declared in the template's message-schema registry, where the node-form requires the name to be declared as a node-type. The substitution-ref coverage check of `decision:substitution-ref-coverage-required` treats the two directives identically.
```

### Amend concept: publisher

One change against the live file: the message-inertness invariant line
acknowledges the sanctioned read sites instead of claiming a
no-inspection pipe.

```markdown
---
concept: publisher
---

# Publisher

## What it is

A publisher is a peer service that publishes messages into rimsky. Publishers implement the publisher protocol (four verbs: a capabilities handshake, subscribe, unsubscribe, and list-subscriptions) and POST message envelopes to the universal operator message-send endpoint, identifying themselves as publishers and presenting a per-subscription capability token.

Publishers are peer-services in the same trust perimeter as executors and claim-producers: out-of-process, addressed at startup via the publisher service registry in `concept:rimsky-yml`, and exclusively responsible for their own state and HA posture.

A publisher service is a provider of broadcasters: one service process serves many instances, and each subscription provisions a logical, per-instance broadcaster within it, parameterized by the instance's resolved config — the per-instance analogue of how an executor provides per-node-run execution.

## Purpose

To give rimsky a uniform way to accept inbound messages from peer services — sensors, schedulers, change-data-capture pipes — without each implementation needing its own bespoke deposit route. The publisher protocol is the single message-send surface for peer services; operators only ever fire messages via the universal message-send endpoint.

## Boundaries

Owns: the protocol surface, the peer client, the rimsky-side dispatch helpers, and the capability check on the universal message-send surface.

Does NOT own: the publisher's substrate (cron clock, HTTP endpoint, object-store, etc.), per-publisher state persistence (each publisher owns its own state DB; see `concept:sensor`), the message envelope shape (that's `concept:message`), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher-subscription` (the rimsky↔publisher binding lifecycle), `concept:sensor` (one class of publisher implementation), `concept:message` (the envelope shape), `concept:claim-producer` and `concept:executor` (peer-service siblings with their own protocols), `concept:replica` (publisher replica posture).

## Invariants

- Publishers are advertised in the publisher service registry of `concept:rimsky-yml`. Their declared protocol membership must include the publisher protocol.
- The subscribe verb carries the message type the publisher will stamp on every sent envelope; the subscribe surface carries no receiver-routing field — delivery routes by message type against node-subscription edges. The publisher persists the type and copies it onto each sent message envelope.
- Send-time messages identify the sender as a publisher and present the per-subscription capability token. Rimsky derives the sender name from the publisher-subscription row; the request's declared sender is ignored for trust.
- Mounting-to-active reconciliation, its retry cadence, and the failed-state contract are owned by `concept:publisher-subscription`.
- Replicas are not coordinated by rimsky. Single-replica is the durable posture per `concept:replica`.
- invariant: message-inertness — payload bytes flow from publisher → message envelope → consumer's substitution leaf uninspected outside the sanctioned read sites (the receipt-time body-schema validation among them; see `concept:inertness`).
```

## Work items

Flat and unordered; real dependencies are stated where they exist.
Deltas with no work item (the race pair of amendments and the
retirement, the terminal-resolution, inertness, message, and publisher
alignments, and the error-policy emitted-counter statement) are
applied on their own — the code already realizes them.

- **Retry-backoff registration validation** (realizes the new
  error-policy invariant; issue `retry-backoff-values-unvalidated`).
  Template registration rejects a `retry_backoff.kind` or
  `retry_backoff.jitter` value outside the closed vocabularies as a
  hard error in the standard validation-error shape, mirroring the
  action-vocabulary check. Registration-time tests cover both fields,
  valid and invalid.

- **Named types for the two closed template vocabularies** (issue
  `bare-string-closed-value-sets-untyped`, owner-ruled). The template
  spec's `cascade_mode` and claim `intent:` fields become named string
  types with const blocks, matching the sibling closed-set fields;
  the claim intent type reuses the protocol layer's existing intent
  type unless cross-module reuse proves unwanted, in which case a
  spec-local mirror. The compiler-enumerated call sites are swept in
  the same change. The two registry-resolved `kind:` fields stay bare
  strings.

- **CLI target-agent guess honors the identity-file env override**
  (issue `resolve-target-agent-bypasses-identity-overrides`). The
  default-target-agent resolution used by instance create, run, and
  both compose paths consults the identity-file env override before
  falling back to the default path, matching the agent daemon's own
  precedence. No new flags. A test pins the precedence.

- **Spawn-allowlist env parity** (issue
  `allow-paths-env-var-absent`). The host-agent env loader gains
  `RIMSKY_AGENT_ALLOW_PATHS` (comma-separated path globs) following
  the file's existing env-override convention; unset stays open. A
  test pins the parsing and the flag/env precedence.

- **Drop claude-agent's inert declared-tags surface** (issue
  `claude-agent-declared-tags-inert`). Remove
  `RIMSKY_EXECUTOR_DECLARED_TAGS` and the declared-tags plumbing from
  the claude-agent executor; advertisement returns only when emission
  exists.

- **Drop the write-only no-progress tuning column** (issue
  `max-retries-without-progress-vestigial`). Remove
  `max_retries_without_progress` from the queue schema and its dead
  write path in both persistence backends (drop-and-recreate
  migrations are legal pre-v1). The read-side counter column that IS
  consumed stays.

- **Self-contained compose demos** (issue
  `compose-demo-scripts-unrunnable-when-vendored`). The stub executor
  and sample manifest the three compose demo scripts use move into
  the examples module; the scripts reference them locally and keep
  the existing binary-override env var, so a vendored examples copy
  runs them without the parent checkout.

- **Proxy control-API trust anchor** (realizes
  `decision:host-agent-proxy-enrollment`; issue
  `proxy-missing-control-api-ca`). The proxy's outbound control-API
  clients honor `RIMSKY_CONTROL_API_CA` (same name and file format as
  the bundled services) whenever the control-API URL is HTTPS. A test
  covers the anchored and unanchored paths.

- **Proxy enrollment and split serving** (realizes
  `decision:host-agent-proxy-enrollment`; issue
  `proxy-unsustainable-mtls-posture`; depends on the trust-anchor
  item only insofar as both touch the proxy's TLS surface — build in
  either order, reconcile at the shared config). Under mutual-TLS
  peer auth the proxy enrolls through the standard machinery, renews
  its leaf before expiry without restart, and serves the
  supervisor-facing protocols on a listener that requires and
  verifies client certificates against the deployment CA; the
  agent-facing listener keeps its existing posture. Covered by tests
  at the enrollment/renewal and client-verification seams.

- **Decisions TOC refresh** (rides the deltas). Drop the
  `race-gate-split` row, add a `host-agent-proxy-enrollment` row, and
  re-derive any row whose one-line summary the amendments above
  changed.

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
