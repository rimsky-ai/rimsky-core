# Sprint: Audit gap drain

## Intent

This sprint drains the whole issue intake — the thirteen gaps the periodic
implementation audit found and the verifier made ruling-ready. There is no
unifying theme and none should be read into it: the items span operator
environment naming, shutdown semantics, transport encoding, metrics shape,
security posture, build-time enforcement, and one event-log correctness bug.
What they share is only their provenance.

Three of the rulings move the corpus: the in-process executor decision's
Choice mis-states the mechanism it governs, the anonymous-mode story's
"every action" promise has one deliberate exception it never named, and the
graceful-shutdown decision's single-value premise was never true in the tree.
The other ten leave the corpus as written and bring the code to it.

A fourth corpus delta comes from reconciling work that landed outside any
sprint: a dedicated lifecycle-subscriber conformance subcommand now exists,
which the conformance concept denied. The corpus catches up to it, and the
flag that used to serve as the escape hatch is deleted rather than
deprecated.

Issues promoted into this sprint:

- `sensor-object-store-memory-backend-always-registered`
- `executor-host-port-envs-inconsistently-prefixed`
- `inproc-executor-client-goroutine-channel`
- `stale-recovery-duplicate-work-started-events`
- `enroll-route-rejects-anonymous-identity`
- `plumbline-lint-not-enforced-in-ci`
- `license-lint-ignores-third-party-imports`
- `peer-auth-mtls-forward-legs-not-tied-to-switch`
- `no-per-run-lifecycle-state-endpoint`
- `named-lock-metric-separate-family-not-label`
- `production-shutdown-grace-is-30s-not-5s`
- `http-json-bridges-not-uniformly-protojson`
- `mcp-catalog-missing-four-http-routes`

## Corpus deltas

### Amend decision: inproc-eventstream

```markdown
---
decision: inproc-eventstream
---

# Unary in-process executor call

## Choice

The in-process executor client is a unary call into the runtime executor
package's handler interface: one invocation, one Outcome (or error)
returned to the caller, with no event stream, no receive loop, and no
end-of-stream signal. The invocation is bridged through a goroutine and a
buffered result channel for exactly two purposes — so the caller's context
cancellation is honored even when a handler blocks, and so a handler panic
is recovered rather than taking down the process that hosts every other
role alongside it.

## Rationale

Matches `decision:executor-unary-rpc`: the executor protocol's Execute call
is unary, so the in-process transport mirrors the gRPC and HTTP-bridge
transports exactly, with no transport-specific casing at the dispatch call
site. Sync utility executors (counter, loop) and async-style ones alike
simply return their one Outcome; there is nothing to stream.

The goroutine bridge is isolation machinery rather than transport shape.
In-process execution shares an address space with the scheduler,
supervisor, and control API, so an unrecovered handler panic or an
uncancellable blocking handler would take all of them down with it — a cost
the out-of-process transports do not carry, and the reason this one
transport pays for a bridge the others have no use for.

## Alternatives

- A goroutine-plus-channel event stream mirroring a server-streaming
  Execute call — rejected: the executor protocol itself is unary, so a
  streaming in-process transport would need to fabricate a receive loop the
  other two transports don't have, reintroducing exactly the concurrency
  and error-surfacing complexity a unary protocol avoids.
- A bare synchronous call on the caller's goroutine with no bridge at all —
  rejected: it is simpler, but it gives up both cancellation of a blocking
  handler and panic isolation, which in a single-process deployment means
  one misbehaving executor can hang or crash every role at once.
```

### Amend story: anonymous-mode-bootstrap

```markdown
---
story: anonymous-mode-bootstrap
---

# Fresh deployment opens then locks down

## Story

As an operator bringing up a fresh rimsky deployment on a dev machine, I
can use it without minting credentials first — anonymous mode is open and
every operator control-plane action succeeds, with machine service
enrollment the one deliberate exception — and the moment I mint the first
admin key, anonymous mode closes and subsequent unauthenticated requests
are refused, so that I can experiment freely on first run and lock down the
moment I'm ready.
```

### Amend decision: graceful-shutdown

```markdown
---
decision: graceful-shutdown
---

# Graceful shutdown with a hardcoded grace per path

## Choice

On interrupt, terminate, run-timeout expiry, or natural completion,
shutdown is polite-then-forceful: new dispatches stop first, in-flight
dispatches and spawned children receive a polite terminate signal, and
anything still running when the grace expires stops holding the shutdown —
spawned child processes are hard-killed, and in-flight dispatches the
supervisor is still waiting on are left to their own timeouts while
shutdown proceeds — before the remaining surfaces close and the process
exits (the most-recent-run pointer updating per `decision:artifact-layout`
on the way out).

The grace is hardcoded at two values by path: five seconds where the CLI
supervises child processes it spawned itself, and thirty seconds on the
deployed paths — the container entrypoint, the standalone role processes,
and the supervisor's wait for in-flight dispatches to drain. Every path,
without exception, treats a second interrupt as escalation to hard exit:
immediate hard kill, best-effort close.

## Rationale

The two windows serve different populations. A CLI-spawned child is a local
process an operator is watching in a terminal; five seconds is long enough
for a well-behaved one to unwind and short enough that a misbehaving one
never holds the session. A deployed process is draining dispatches carrying
real work whose loss costs more than the wait, so thirty seconds gives the
drain a genuine chance to complete.

Neither window is configurable, because the operator need a knob would
serve — "let me out now" — is answered by the second-signal escalation
instead. That is why the escalation is universal rather than a property of
one path: it is the escape hatch that makes fixed graces tolerable.

## Alternatives

- One hardcoded grace across every path — rejected: a five-second value
  cuts live deployed dispatches to serve a local-tooling responsiveness
  need, and a thirty-second value makes an operator wait on a local child
  that has already stopped mattering. The two populations genuinely differ.
- A configurable grace period — rejected: a knob whose only real use is
  immediate exit, which the second-signal escalation already provides
  without configuration.
- Wait indefinitely for in-flight work to unwind — rejected: a single
  misbehaving executor blocks the operator's exit.
```

### Amend concept: conformance

```markdown
---
concept: conformance
---

# Conformance

## What it is

A per-protocol conformance subcommand family on the CLI — one subcommand per
protocol — over a shared conformance library in the protocols module (one
sub-package per protocol). Third-party service implementers run a conformance
subcommand against their service endpoint; Go service authors can also invoke
the underlying library from a Go test without going through the CLI.

- Executor conformance — gRPC transport only (no HTTP+JSON bridge); the
  fail-closed stub-mode gate, scenario include/skip filters, and the
  observability check flag (see Invariants). Nine registered scenarios
  spanning the happy path, async handoff and restart survival, cancellation,
  attribute and terminal-tag round-trips, scratch park-and-resume, park
  emission, and malformed input.
- Stub-mode probe — its own subcommand, specific to the executor protocol,
  gRPC transport only; issues one Execute RPC and asserts the stub-mode
  response shape (see Invariants for the shared stub-mode signature). Spins up
  a callback receiver so async-handoff executors can complete the probe via
  the callback path.
- Claim-producer conformance over gRPC — the standard battery: capabilities,
  first- and second-open, cross-open uniformity, the split-scope and
  scope-conflict checks (including a raw-wire fallback probe against
  `supports=false` declarations — see Invariants), the terminal-verb checks
  (Commit, Abandon, Release, each with a repeat-call idempotency check), and
  the staged-async serialization check. An optional observability/
  retention-probe flag mirrors the executor subcommand's.
- Blob-backend conformance via in-process construction — ten checks
  (round-trip at two sizes, range read plus an out-of-bounds range probe,
  delete-then-read and delete-then-range-read, an empty-payload round-trip, a
  handle-shape check, idempotent delete, concurrent writes), run against each
  concrete backend (memory / filesystem / pg-largeobject) through the
  conformance library's reduced backend interface.
- DataProcessing-mix-in conformance — capabilities plus the candidate
  lifecycle (begin / commit / abandon, with idempotency and abandon-exclusion
  checks), version/partition/schema list smoke tests, and a concurrent-writes
  check.
- Publisher-protocol conformance — capabilities plus subscribe,
  list-subscriptions, idempotent-subscribe, message-push, unsubscribe, and
  idempotent-unsubscribe.
- Validation-mix-in conformance — per-role happy-path and unknown-role checks
  for every role, plus a malformed-input check for the executor role.
- Lifecycle-subscriber conformance over gRPC — a sanity pass issuing every
  template- and instance-lifecycle notification the protocol defines, each
  against synthetic identifiers, asserting the subscriber accepts all of them.

The conformance library lives in the protocols module; each subcommand is a
thin wrapper (parse flags, dial endpoint, invoke library, format output,
exit). The conformance surface ships inside the single rimsky binary.

## Purpose

A third-party implementer runs the per-protocol conformance subcommand against
their service endpoint. Pass/fail validates wire compatibility without forcing
the implementer to import internal Go test code. The runner logic lives in an
importable Go library, so Go service authors can also invoke the same suite
from their own Go tests against an in-process or testcontainers-hosted target.

## Boundaries

Owns: the conformance library, the per-protocol conformance subcommand
handlers, the shared fixture packages, and the stub-mode probe. Does NOT own:
in-repo unit tests (those live with the source), or the in-repo scenario
harness. Adjacent: `executor`, `claim-producer`, `blob-backend`, `publisher`,
`data-processing`, `validation`, `lifecycle-subscriber`.

## Invariants

- Every protocol carrying a conformance suite reaches it through exactly one
  CLI entry point: its own subcommand. No protocol's suite is reachable as a
  flag on another protocol's subcommand.
- The executor conformance subcommand always issues an in-process stub-mode
  probe before running scenarios, and the gate is fail-closed: a failed or
  negative probe stops the run before any scenario executes, unless the
  operator passes an explicit allow-live override. Under the override,
  stub-requiring scenarios skip rather than fail. Refusing a live endpoint is
  the default; there is no opt-in strict flag.
- The stub-mode signature — the stub-probe request flag, the stub
  attributes-delta response shape, and the sibling park- and cancel-probe
  flags — is defined once, in a shared definition in the conformance library
  that every issuing and asserting site imports: the conformance scenarios and
  the stub-capable in-tree executors alike. The signature is additionally
  documented as the contract a non-Go "stub-conformant" executor reproduces;
  it is not a wire-protocol field.
- The conformance surface is part of the all-targets build (compile-time
  dependency on the protocols module, carried by the rimsky binary).
- The uniformity check is silently skipped (rather than failed) for
  pick-policy producers whose consecutive opens return non-byte-equal scopes.
- The memory blob backend's startup-time unified-only gate is bypassed in the
  blob-backend conformance subcommand by running it under the unified process
  role.
- A claim producer that declares `supports=false` on the split-scope or
  scope-conflict capability is additionally probed on the raw wire, bypassing
  the conformance client's own capability short-circuit — this catches a
  fabricated success or a broken byte-equal implementation that the
  client-side fallback would otherwise mask.
```

### Amend decision catalog: two TOC lines

The decision catalog's one-sentence entries for the two amended decisions no
longer describe them. Replace those two bullets in place, leaving every other
catalog line untouched:

```markdown
- `graceful-shutdown` — Shutdown is polite-then-forceful: stop new dispatches, politely terminate in-flight work, then stop waiting when the hardcoded grace expires — five seconds where the CLI supervises children it spawned, thirty seconds on deployed paths; a second interrupt escalates to immediate hard exit.
```

```markdown
- `inproc-eventstream` — A unary in-process executor call returning one Outcome, bridged through a goroutine and buffered channel solely for caller-cancellation and panic isolation — no event stream, no receive loop.
```

The catalog entries for `concept:conformance` and
`story:anonymous-mode-bootstrap` still describe their amended artifacts and
are left alone.

## Work items

- **Delete the executor subcommand's lifecycle-check flag.** Realizes
  `concept:conformance`. The lifecycle-subscriber suite is reachable only
  through its own subcommand; the deprecated alias flag on the executor
  conformance subcommand is removed outright, not left deprecated, so no
  protocol's suite hangs off another protocol's subcommand.

- **Gate the object-store sensor's in-memory backend behind an explicit
  enable.** Realizes `decision:object-store-watching-model`. The bundled
  object-store sensor's shipped capabilities schema advertises only the
  filesystem backend; the in-memory backend registers only when an
  environment variable explicitly enables it, and the sensor's own tests and
  local throwaway dev set that variable. An operator running a stock image
  cannot select a store that forgets its watch state on restart.

- **Unify the bundled executors' host/port environment variable names on the
  unprefixed form.** Realizes `decision:operator-env-namespaced-per-service`.
  All four bundled executors read the same unprefixed listen host and port
  variables; the three that currently read per-service-prefixed variants are
  renamed to match, with no compatibility aliases (pre-v1). Per-service
  prefixing remains for behavior-specific knobs such as allowlists, which is
  what the decision's namespacing rule is actually for.

- **Apply the amended in-process executor decision.** Realizes
  `decision:inproc-eventstream`. The corpus delta above is copied into place;
  the in-process client's goroutine-and-channel bridge stays exactly as it
  is. No code change — this item is the delta's application.

- **Stop the liveness-recovery sweep from double-emitting work-started.**
  Realizes `story:work-completed-emitted`. A dispatch that goes quiet past
  its deadline, is swept back to acquisition-eligible, and is re-acquired
  emits exactly one work-started event across the whole episode, matching its
  one work-completed. The emission is gated on the row's recovery
  disposition. A scenario test asserts the singleton count across the
  liveness-recovery path, sitting alongside the existing in-place-retry and
  park-resume singleton tests.

- **Apply the amended anonymous-mode story and prove its exception.**
  Realizes `story:anonymous-mode-bootstrap`. The corpus delta above is copied
  into place; the enrollment handler's rejection of an identity with no real
  key id stays as it is. A test drives a zero-key deployment with mutual-TLS
  peer auth configured and asserts both halves of the amended promise:
  operator control-plane actions succeed, and service enrollment is refused.

- **Make CI run the plumbline lint.** Realizes `decision:coding-style`. The
  CI workflow sets the vendored lint binary's path so the existing
  purpose-built test executes there instead of self-skipping, and a tree
  carrying a comment-hygiene or citation-resolution violation fails CI.

- **Teach the license check to classify third-party dependencies.** Realizes
  `decision:licensing-enforced-by-license-lint` and
  `decision:licensing-dual-apache-agpl`. A committed, curated
  module-to-license allowlist covers the Apache-surface modules' dependency
  closure; the existing checker consults it and fails when an
  Apache-licensed package imports a copyleft-incompatible module. An
  unclassified module fails the build until someone classifies it — the
  check is fail-closed on unknowns, not silent.

- **Make `peer_auth: mtls` a single flip.** Realizes
  `story:peer-auth-mtls-mutual`, `decision:peer-auth-mtls`, and
  `concept:peer-auth`. Setting mutual-TLS peer auth implies required TLS on
  every internal peer entry unless that entry explicitly overrides it, and
  the control API's own inbound listener serves TLS under this mode, sourcing
  its server certificate from the same deployment certificate authority the
  mode already stands up. An end-to-end bundled-service test exercises the
  one-flip posture — configuring only the peer-auth mode and the CA, no
  per-peer TLS keys — and proves the forward dispatch legs and the control
  API port are all mutually authenticated. The dormant harness option built
  for this posture gains its first real call site.

- **Build the per-run lifecycle state endpoint.** Realizes
  `decision:node-state-retired-from-operator-api`. An operator holding a
  single run id can read that run's current lifecycle state, in flight or
  terminal, through the control API, with its own authorization action. Its
  MCP tool registers in the same change. This item and the MCP parity item
  below interact: whichever lands second must satisfy the other's coverage.

- **Fold named-lock acquisitions into the claim-acquisition metric family.**
  Realizes `decision:named-lock-metric` and `story:named-lock-metric`. Named
  lock acquisitions are counted in the existing claim-acquisition counter
  family under a distinguishing label rather than a family of their own; the
  separate family is removed, not deprecated. The story's existing test
  assertions move to the folded shape in the same change.

- **Apply the amended shutdown decision and add production second-signal
  escalation.** Realizes `decision:graceful-shutdown`. The corpus delta above
  is copied into place, and the deployed shutdown paths — the container
  entrypoint and the standalone role processes — gain second-signal handling
  so a second interrupt or terminate forces immediate hard exit, matching
  what the CLI-supervised path already does. The thirty-second deployed
  drain window itself is unchanged.

- **Convert the remaining HTTP bridges to protojson in both directions.**
  Realizes `decision:protojson-gateway`. The claim-producer/lifecycle-
  subscriber bridge and the claude-agent executor's standalone execute bridge
  both decode requests and encode responses through protojson; their
  hand-written body structs are deleted, on the server and client sides
  alike. The conformance kit's client moves in the same change so the
  certified wire shape and the served wire shape cannot diverge.

- **Close the MCP catalog's parity gap and lock it shut.** Realizes
  `decision:mcp-http-parity` and `story:mcp-transport`. Every registered
  control-API action has an MCP counterpart — the two instance frame reads,
  the observability read, and service enrollment included — with the
  observability wildcard route exposed as a single tool taking a path-suffix
  argument. A test enumerates the action registry and fails when any entry
  lacks a tool or resource, so a future action cannot ship without one.

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
