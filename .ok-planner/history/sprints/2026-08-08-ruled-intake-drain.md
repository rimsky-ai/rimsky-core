# Sprint: ruled intake drain — examples removal, payload typing, load-independent suite

## Intent

Drains the ruled issue intake. No single theme: the examples module is removed
entirely, every structured JSON payload rimsky writes becomes proto-declared and
generated, the test suite's verdict stops depending on machine load, and a batch
of unrelated defects across the CLI, the control API, the mTLS bootstrap, and the
runtime is fixed.

Promoted issues: `auth-login-stores-unread-api-key`,
`version-verb-missing-from-help`, `root-help-common-flags-claim-false`,
`root-help-command-lines-misstate-surface`,
`action-registry-descriptions-contradict-handlers`,
`action-registry-omits-preconditions`,
`whoami-mounted-but-absent-from-action-registry`,
`stubmode-doc-misdescribes-probe-signature`,
`stub-executor-declares-tags-it-never-echoes`,
`suite-verdict-is-load-dependent`,
`permissive-surface-buildability-unpromised`,
`three-signal-payload-fields-have-no-emit-site`,
`typed-event-oneof-is-aspirational`,
`events-proto-payloads-disagree-with-emitters`,
`claim-producer-ref-data-never-reaches-open`,
`error-type-reason-template-never-substituted`,
`retry-backoff-numerics-unvalidated`,
`three-data-processing-surfaces-broken`,
`scratch-silent-loss-and-cannot-be-cleared`,
`no-way-to-export-deployment-ca-root`,
`proxy-agent-hop-tls-asserted-but-plaintext`,
`plaintext-enrollment-hop-silently-accepted`,
`publisher-declared-kinds-unenforced`,
`terminal-event-precedes-producer-ack`.

Retired in planning: the nine examples-README issues, dissolved by the module's
removal — `atomic-staging-readme-release-semantics-wrong`,
`publisher-readme-templatespec-cannot-register`, `executor-readme-three-defects`,
`loop-counter-readme-retired-subscription-model`,
`data-processing-readme-rimsky-yml-prevents-start`,
`claimproducer-readme-falsifier-cannot-fail`,
`lifecyclesubscriber-readme-error-blocking-claim-false`,
`validation-readme-misdescribes-when-validator-runs`,
`examples-readme-apache-claim-and-guarantee-table`. Three findings underneath
them that concern rimsky rather than the examples were pulled out as their own
issues before retirement; two are promoted above and
`lifecycle-subscribers-can-block-without-authorization` stays open for a later
sprint (see `sketch:2026-08-08-subscriber-readiness-gating`).

Out-of-band corpus movement since the last close is ratified here: two artifacts
were edited mid-drain by a verifier running under a withdrawn contract, and both
edits are correct against the tree.

## Corpus deltas

Each delta below names the artifact and the section to replace. Sections not
named are unchanged. Text is final form — copy it into place verbatim.

### Ratify concept: delegation

The invariant describing an unknown delegate target already reads correctly on
disk, matching a registration-time check the validator has carried since 20 July.
No text changes; this entry records the authorization the mid-drain edit lacked.

### Ratify decision: event-log-kind-enum

The Choice's "three-class taxonomy" already reads correctly on disk — the signal
taxonomy declares exactly three top-level kinds. No text changes; this entry
records the authorization the mid-drain edit lacked.

### Amend concept: module-layout — remove the Examples module bullet

Delete the fifth bullet in **What it is** (the one beginning "**Examples module**
(under the repo root, alongside the lib group)") in full. No replacement bullet.

### Amend concept: module-layout — "What it is" opening sentence

Replace the first sentence of **What it is** with:

> The Go workspace ties four modules into one build.

### Amend concept: module-layout — Boundaries

Replace the parenthetical listing the workspace in **Boundaries** with:

> the four-module workspace (protocols, foundation, services, root)

### Amend concept: module-layout — the workspace-wide verification invariant

Replace the final bullet of **Invariants** with:

> - Verification is workspace-wide: the build / lint / test gate covers every module in the workspace — root, foundation, protocols, and services. A root-module-only test sweep silently skips the sibling modules and is not a complete validation pass.

### Amend concept: module-layout — Licensing boundary

Replace the **Licensing boundary** section body with:

> A per-directory licensing mapping across two surfaces, enforced by a build-step license check with longest-prefix-match-wins. The permissive surface is the protocols module; the copyleft surface is everything else rimsky ships (the foundation, graph, runtime, and control layers, the bundled services, the binaries, the dev tooling, and the test-support scaffolding). The protocols module is a closed permissive island in the Go import graph: it imports only the standard library plus its pinned runtime dependencies, and nothing copyleft reaches a consumer who depends on it. The copyleft surface is dual-licensed — the copyleft license is the default, and a commercial alternative is available for organizations that prefer not to take on the copyleft obligations. The dual track applies only to the copyleft surface; the permissive island has no commercial track. The Postgres test-container helper is part of the copyleft test-support scaffolding (not a public module). The specific license identifiers and the commercial-alternative arrangement are owned by `decision:licensing-dual-apache-agpl`. Repo-organization concern; not a runtime noun. The check is build-step enforcement, not runtime.

### Amend decision: licensing-dual-apache-agpl — Choice

Replace the **Choice** section body with:

> Per `concept:module-layout`, dual-track licensing across two surfaces: a permissive open-source license covers the protocols module (the surface external implementers link against); a strong-copyleft license with a commercial alternative covers everything else (the foundation module, graph layer, runtime layer, control layer, the bundled services, the binaries group, the test group, the tools group).

### Amend decision: licensing-dual-apache-agpl — Rationale

Replace the **Rationale** section body with:

> Permissive surface for everything an external implementer links against to build a peer; copyleft for the orchestrator itself, with a commercial alternative so organizations that prefer not to take on copyleft obligations on modified or derivative work, or on network-delivered services, can use the orchestrator under negotiated terms.

### Amend decision: licensing-dual-apache-agpl — Alternatives

Replace the **Alternatives** section body with:

> - One permissive license across the whole repo — rejected: forfeits the copyleft lever on the orchestrator core and with it the commercial-licensing alternative.
> - Copyleft across the whole repo — rejected: external implementers could not depend on the protocol definitions without taking on copyleft obligations, defeating the integration surface's purpose.

### New story: permissive-peer-build

```markdown
---
story: permissive-peer-build
---

# Service author builds a peer without copyleft obligations

## Story

As a service author who cannot take on copyleft obligations, I can build and ship a working rimsky peer whose only rimsky dependency is the permissively-licensed protocols module — implementing a protocol, running against a real rimsky stack, and exchanging the protocol's verbs — so that integrating with rimsky does not place my own service under copyleft.
```

### Amend decision: testing-scenario-based-e2e — Choice

Replace the **Choice** section body with:

> End-to-end via the test group's scenarios directory + the services module's scenarios test directory driving the assembled product; persistence tests use an integration-test container helper to boot real backends. Harness wait helpers are poll-until-success: they block until the awaited state appears, and they report the expected-versus-observed state descriptively when the run is cut short. The suites run with no per-package time ceiling; hang detection lives in the test guard, which watches the runner's event stream and kills a run only when no test has completed for a long interval.

### Amend decision: testing-scenario-based-e2e — Rationale

Replace the **Rationale** section body with:

> Real-stack integration tests against the load-bearing safety properties documented in the concept catalog. Unbounded waits keep the verdict a function of the code alone (see `decision:test-wallclock-lint-ratchet`), and a progress-based hang backstop preserves that property where an elapsed-time ceiling cannot: a per-package timeout is an aggregate budget covering every test in the package, so one test blocking longer under load consumes the budget belonging to the rest and the verdict becomes load-dependent. A correct suite emits completions continuously at any load, so a no-progress interval never binds; a hung run emits nothing and still dies loudly.

### Amend decision: testing-scenario-based-e2e — Alternatives

Replace the **Alternatives** section body with:

> - Deadline-bounded poll helpers that fail the test on expiry — rejected: the deadline is a verdict input, the exact idiom the testing rules ban.
> - A per-package elapsed-time ceiling as the hang backstop — rejected: it is an aggregate budget sized to total runtime, so machine load changes which tests are killed.

### Amend concept: event-log — Invariants

Replace the second bullet of **Invariants** with:

> - The `payload` is rimsky's own JSON — readable by rimsky for the dashboard and audit consumers — and its shape is declared in the events proto, one message per kind, covering both operational and signal-class rows. Rimsky constructs every payload from the generated type; a payload is never assembled as an untyped map, so a declared field with no writer and a written key with no declaration are both unrepresentable. Fields whose shape belongs to someone else — an executor's error data, a template author's opaque blob — are declared as bytes and pass through uninspected (see `concept:inertness`).

### Amend concept: control-api — Invariants

Replace the **Every operation is auth-gated** bullet with:

> - **Every operation carries an auth posture, and the action registry records it.** Three postures exist: the health probe requires no token and is an unauthenticated infrastructure path (it returns success while persistence is available and non-success when it is not — persistence availability is the one dependency it checks); the identity-echo route requires a valid token but no permission, because a permission on it could only ask whether a key may learn its own name and a denial would be unlearnable; every other operation requires a token and a permission. The action registry lists every mounted route, and each entry records which posture applies and whether the route is mounted unconditionally or only when its dependency is configured. A mounted route absent from the registry is a wiring bug. A permission naming an ungated action is refused at grant time — an accepted grant that can never be consulted is worse than an unmapped route.

### Amend concept: publisher — Invariants

Replace the first bullet of **Invariants** with:

> - Publishers are advertised in the publisher service registry of `concept:rimsky-yml`. Their declared protocol membership must include the publisher protocol. A template declaring a publisher of a kind the peer does not advertise in its capabilities handshake is refused at registration, the same way a template naming an undeclared executor or an unknown claim-producer error class is refused.

### Amend concept: claim-producer — Invariants

Append to **Invariants**:

> - A terminal event in the event log records rimsky's settlement decision, not the producer's acknowledgement of it. The event and the outbox row that will carry the verb are written in the same transaction, and delivery happens after that transaction commits, so an event may stand before — or without — the producer having heard. What guarantees the producer eventually hears is the outbox's at-least-once delivery, not the event.

## Work items

- **Remove the examples module.** Delete the `examples/` tree in full — the protocol reference directories, the demo shell scripts and their template YAML, the module manifest. Drop it from the workspace definition, from every Makefile target that builds, lints, or tests it, and from the three test-image builds that read its Dockerfiles. Update the license-check's per-directory mapping so the permissive surface is the protocols module alone. Realizes the `module-layout` and `licensing-dual-apache-agpl` deltas.

- **Prove the permissive surface is buildable.** A minimal peer that depends on the protocols module alone, implements a protocol, and is exercised against a real rimsky stack — replacing what the examples' cross-stack proofs were incidentally demonstrating. Carries `@story: permissive-peer-build`. Makes `story:permissive-peer-build` true.

- **Move every rimsky-authored structured payload into the events proto and construct from generated types.** Move the signal payload shapes (retry, park, attribute-changed, await-async) into `events.proto` alongside the operational payload messages, regenerate, and replace all hand-built payload maps — 37 sites in non-test code — with construction from the generated types. Change the signal payload field's type so an untyped map no longer satisfies it. Opaque fields (an executor's error payload, a template author's blob) are declared as bytes and stay uninspected. Resolves the declared-versus-emitted mismatches in both directions: write the retry cap (in scope at the caller) and the previous attribute value (computed by the diff and currently discarded); delete `discarded_claims`, which has no writer and no settled meaning; reconcile the operational payloads' field lists — the keys lock-acquired, lock-released and the orphan reaper actually write, the declared fields none of them write, the hardcoded-empty field name on template-resolution-failed, and the violations list whose emitter always writes one aggregated element. Realizes the `event-log` delta.

- **Record the payload construction convention in the project rules.** A section in `.claude/rules/rules.md`: a structured JSON payload rimsky authors is declared in the events proto and built from the generated type, never assembled as a map; a payload whose shape belongs to a peer or a template author is declared as bytes and passes through uninspected. Not a corpus artifact — a coding convention.

- **Make the suite's verdict independent of machine load.** Run the suites with no per-package time ceiling and move hang detection into the test guard: consume the runner's JSON event stream and kill only when no test has completed for a long interval, reporting that distinctly from an ordinary failure. Establish whether the stream emits reliably enough during a single long-running test to separate slow from hung; if it does not, have the guard report a saturated environment as an inconclusive run rather than naming arbitrary tests. Route every read-after-action assertion in the scenario suites through the harness's blocking wait — six of the seven files that drive a node run directly read state immediately afterward with no wait. Apply the concurrency throttle the services and examples targets already used to the remaining targets. Correct the "Tests Are Deterministic" section of `.claude/rules/rules.md`, which asserts the suite-level timeout is load-independent. Realizes the `testing-scenario-based-e2e` delta.

- **Test the delegate-target check.** The template validator rejects a delegate naming an undeclared sub-graph, and no test exercises it — its file covers eleven sibling cases (unknown entry, unknown exit, delegate cycles, and the rest) and not this one. Add the missing case.

- **Fix the CLI's root help.** Add the `version` verb and its two flag spellings. Retitle the common-flags block to name the control-API verbs it actually covers, list `--key` and `--output`, and name the five families that parse their own flags (auth, agent, conformance, `compose run`, `ctx use|add|rm|current`). Correct the three understated command lines: `run` accepts a named template and self-hosts when no endpoint resolves; the delete verb takes an id or a key; key creation requires a role only without a role file, and also accepts a role file, an expiry, and repeatable grant patches.

- **Correct the action registry and complete it.** Rewrite the six descriptions their handlers contradict: terminate is a terminal-only cleanup that refuses otherwise, reset clears the failure marker with no state transition, the parked read lists parked node-runs rather than the wait-set, the breakpoint read returns all hits since the cursor, undeploy refuses while any instance is active, and rotation preserves the name while issuing a new key id. Tighten the kill and lineage descriptions. Add the three omitted preconditions — the required idempotency header, the required frame parameter, the producer-side release and active-holder refusal — give the wait-set tool a schema declaring its required argument, and drop the retired `reason` argument from the parked-node tool schema. Add the identity-echo route to the registry with its posture and the reason a permission on it could mean nothing; record the two conditionally-mounted routes' mounting condition. Refuse a grant naming an ungated action. Check while drafting whether keys can be edited after creation, or permissions written in other shapes, along paths that validate separately from the key-creation check. Realizes the `control-api` delta.

- **Make `auth login` mean something.** Resolve the api-key from the stored context after the flag and the environment variable, matching how endpoint resolution already falls through. Add a test asserting a command run after login carries the key — the existing test asserts only the write.

- **Correct the stub-mode package doc and pull its literals in.** Rewrite the cancel probe's bullet to its real four-step contract, the async probe's to the capability check it is, and the tag probe's to name its declared-tags gate. Add the two cancel acknowledgement ids and the malformed-shape marker as constants in the stub-mode package and repoint all six duplicate sites, so the doc's "defined once" claim becomes true rather than deleted.

- **Make the in-tree stub executor echo its declared tags.** It advertises five tags and never reads the tags attribute, so the conformance tag round-trip would fail if anyone pointed the runner at it. Echo the requested tag on the success outcome.

- **Check a publisher's declared kind at registration.** Add a publisher hook to the validator's registry-hook set alongside the executor and claim-producer lookups, and refuse a template naming a kind the peer does not advertise. The peer side is already built and already enforced — a publisher advertises its kinds and the conformance suite fails one that serves an unadvertised kind. Realizes the `publisher` delta.

- **Carry a claim's data blob through to acquisition.** A template's `data:` blob is forwarded to the producer for approval at registration, verbatim, and then never sent when the claim opens — the claim spec has no field for it and neither does the open request. Thread it through so the producer receives at acquisition what it was asked to approve.

- **Rename `reason_template` to `reason`.** Nothing substitutes placeholders into it and nothing will; the evaluator copies it to the outcome verbatim. The name is the defect.

- **Validate the retry-backoff numerics.** The kind and jitter vocabularies are closed at registration and the two numbers beside them are unchecked. Reject a negative base delay, a zero base delay, and a ceiling below the base.

- **Route per-candidate metadata into the parent writeback.** A data-processing producer returns a metadata blob with each per-child commit; rimsky decodes it into a field nothing reads. Surface it through the existing parent writeback path. Both bundled producers populate it.

- **Fail the dispatch when scratch cannot be read back.** Four paths in the scratch loader log a warning and dispatch with empty scratch — a failed database read, a spilled blob with no backend configured, a backend mismatch, and a failed blob read. An executor cannot distinguish that from a genuine first run. Fail the dispatch rather than hand over a false empty state.

- **Serve the deployment CA root from the control API, unauthenticated.** Enrolling under mutual TLS requires the CA root to verify the control API, and there is no route and no CLI subcommand that provides it — the documented procedure is selecting it out of a database table by hand. Add the route.

- **Use the proxy's enrolled certificate on the agent hop.** Three design artifacts state the agent-to-proxy link is secured by a certificate from the deployment CA; the proxy serves it in plaintext unless an operator mounts an unrelated certificate by hand. Under mutual TLS, serve the agent-facing listener with the leaf the proxy already enrolls for.

- **Refuse an unencrypted enrollment address.** The enrollment client checks four combinations of address scheme and pinned CA root, refuses three, and accepts the fourth — a plaintext address with no CA pinned — with no error, warning, or log line, after the api-key and a generated private key have already left the process. Refuse it before the request goes out.

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
   path as its argument, clear this task list just before the
   presentation (complete or remove every remaining entry, so a
   stale list does not linger past the run), walk the presentation,
   offer archive-and-commit — so the ceremony is a
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
