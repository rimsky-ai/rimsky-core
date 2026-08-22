---
closed: 4a01bda4
---

# Sprint: intake drain and concept-catalog repair

## Intent

This sprint has no single theme. It drains the intake — every open issue carried a ruling — and it repairs the concept catalog to the concept authoring rules: every concept file is rewritten to the template's four sections, its Invariants section removed, and each removed entry lands where the rules put it — deleted where a test or the code carries it, in a decision where it argues a choice, in a story where it promises a user outcome, or as a test where the code behaves as claimed and nothing proves it.

Issues promoted into this sprint:

- `lifecycle-subscribers-can-block-without-authorization`
- `surface-intent-bundled-service-http-surfaces`
- `surface-intent-core-metrics-endpoint`
- `agent-proxy-hop-tls-optional-in-code`
- `blob-backend-mismatch-read-errors`
- `sequenced-rounds-dispatch-out-of-order`
- `supervisor-requires-second-config-file`
- `promotion-version-id-never-reaches-lineage`
- `lineage-terminal-kinds-and-pass-through-exclusions`
- `instance-terminated-fires-from-delete-request`
- `no-instance-terminal-promotion-exists`
- `advertised-attribute-schemas-understate-accepted-keys`
- `params-redact-covers-one-of-six-surfaces`
- `two-event-kinds-are-filterable-but-never-emitted`
- `envelope-has-no-sender-subject-and-dedup-admits-instance`
- `mcp-bridge-makes-idempotency-key-optional`
- `compose-template-hash-depends-on-deployment-configuration`
- `concept-catalog-carries-non-definitional-content`

Issues retired at this ceremony (recorded in `history/issues/`): `in-frame-sweep-cutoff-is-structural`, `node-run-allocation-return-value-load-bearing`, `cli-api-key-exception-list-omits-ephemeral-run-self-host`.

Issue filed by this ceremony, left open: `peer-readiness-gate-is-generic`.

## Corpus deltas

Every body is in the sidecar `2026-08-21-intake-drain-and-concept-repair-deltas/`, one file per artifact. A heading below names the operation and the target; the body is in the sidecar at `<kind>s/<slug>.md`.

### Decisions

### Amend decision: event-log-kind-enum
body: in the sidecar
### Amend decision: host-agent-proxy-tls
body: in the sidecar
### Amend decision: idempotency-key-header-universal
body: in the sidecar
### Amend decision: launch-config-injection
body: in the sidecar
### Amend decision: message-sender-kind-discriminator
body: in the sidecar
### Amend decision: secret-at-rest-posture
body: in the sidecar
### Amend decision: send-as-node-kind
body: in the sidecar
### Amend decision: subscription-reconciler
body: in the sidecar
### Amend decision: substitution-grammar-closed
body: in the sidecar
### Amend decision: substitution-grammar-fallback-routing
body: in the sidecar
### Amend decision: termination
body: in the sidecar
### New decision: auth-audit-log-verbatim-bodies
body: in the sidecar
### New decision: blob-backend-mismatch-read-refused
body: in the sidecar
### New decision: breakpoint-matcher-executor-scope-permissive
body: in the sidecar
### New decision: byte-equal-conflict-default
body: in the sidecar
### New decision: expected-attributes-schema-closed
body: in the sidecar
### New decision: fanout-attribute-merge-rejected
body: in the sidecar
### New decision: fanout-parallelism-cap-per-process
body: in the sidecar
### New decision: held-claim-poison-propagation
body: in the sidecar
### New decision: host-agent-late-bind-schema-check-deferred
body: in the sidecar
### New decision: host-agent-path-resolution-anchored-to-agent-cwd
body: in the sidecar
### New decision: host-agent-port-assignment-no-handshake
body: in the sidecar
### New decision: host-agent-proxy-error-vocabulary-reuse
body: in the sidecar
### New decision: host-agent-proxy-uniform-routing-identity
body: in the sidecar
### New decision: lifecycle-fanout-after-commit
body: in the sidecar
### New decision: lifecycle-subscriber-at-least-once-delivery
body: in the sidecar
### New decision: lineage-identity-hashed-not-raw
body: in the sidecar
### New decision: lineage-records-computation-only
body: in the sidecar
### New decision: mounting-subscriptions-accepted-for-message-send
body: in the sidecar
### New decision: multi-protocol-service-distinct-handler-per-protocol
body: in the sidecar
### New decision: no-cross-frame-attribute-caching
body: in the sidecar
### New decision: no-force-fresh-trigger-if-missing-flags
body: in the sidecar
### New decision: post-frame-review-over-frame-blocking-park
body: in the sidecar
### New decision: promotion-lineage-record-after-commit
body: in the sidecar
### New decision: sensor-emission-permanent-drop-vs-transient-retry
body: in the sidecar
### New decision: subgraph-closure-no-free-upstream-reference
body: in the sidecar
### New decision: substitution-per-field-arity-one-to-one
body: in the sidecar
### New decision: template-identity-deployment-canonical
body: in the sidecar
### New decision: template-registration-validation-unconditional
body: in the sidecar
### New decision: write-semantics-reader-lease-forbidden
body: in the sidecar
### New decision: write-semantics-three-level-structure
body: in the sidecar

### Concepts

The sprint rewrites every concept file to the template's four sections and removes its Invariants section. One heading per file; every body is in the sidecar.

### Amend concept: advisory-lock
body: in the sidecar
### Amend concept: anonymous-mode
body: in the sidecar
### Amend concept: api-key
body: in the sidecar
### Amend concept: asset
body: in the sidecar
### Amend concept: atomic-staging
body: in the sidecar
### Amend concept: attribute
body: in the sidecar
### Amend concept: auto-terminal
body: in the sidecar
### Amend concept: blob-backend
body: in the sidecar
### Amend concept: breakpoint
body: in the sidecar
### Amend concept: cancel-siblings
body: in the sidecar
### Amend concept: cascade
body: in the sidecar
### Amend concept: cascade-graph
body: in the sidecar
### Amend concept: cascade-mode
body: in the sidecar
### Amend concept: child-execution
body: in the sidecar
### Amend concept: claim
body: in the sidecar
### Amend concept: claim-co-holdership
body: in the sidecar
### Amend concept: claim-handle
body: in the sidecar
### Amend concept: claim-lifetime
body: in the sidecar
### Amend concept: claim-producer
body: in the sidecar
### Amend concept: claim-scope
body: in the sidecar
### Amend concept: claim-tree
body: in the sidecar
### Amend concept: conformance
body: in the sidecar
### Amend concept: control-api
body: in the sidecar
### Amend concept: data-processing
body: in the sidecar
### Amend concept: delegation
body: in the sidecar
### Amend concept: discovery-cache
body: in the sidecar
### Amend concept: dry-run
body: in the sidecar
### Amend concept: error-policy
body: in the sidecar
### Amend concept: event-log
body: in the sidecar
### Amend concept: executor
body: in the sidecar
### Amend concept: fan-out
body: in the sidecar
### Amend concept: frame
body: in the sidecar
### Amend concept: graph
body: in the sidecar
### Amend concept: host-agent
body: in the sidecar
### Amend concept: host-agent-proxy
body: in the sidecar
### Amend concept: inertness
body: in the sidecar
### Amend concept: instance
body: in the sidecar
### Amend concept: lifecycle-subscriber
body: in the sidecar
### Amend concept: lineage
body: in the sidecar
### Amend concept: lineage-record
body: in the sidecar
### Amend concept: message
body: in the sidecar
### Amend concept: message-schema
body: in the sidecar
### Amend concept: message-sender-node
body: in the sidecar
### Amend concept: module-layout
body: in the sidecar
### Amend concept: named-lock
body: in the sidecar
### Amend concept: node
body: in the sidecar
### Amend concept: node-run
body: in the sidecar
### Amend concept: node-subscription
body: in the sidecar
### Amend concept: observability
body: in the sidecar
### Amend concept: orphan-reaper
body: in the sidecar
### Amend concept: parked-state
body: in the sidecar
### Amend concept: peer-auth
body: in the sidecar
### Amend concept: permission
body: in the sidecar
### Amend concept: persistence-database
body: in the sidecar
### Amend concept: publisher
body: in the sidecar
### Amend concept: publisher-subscription
body: in the sidecar
### Amend concept: rimsky
body: in the sidecar
### Amend concept: rimsky-yml
body: in the sidecar
### Amend concept: role-template
body: in the sidecar
### Amend concept: run-scope
body: in the sidecar
### Amend concept: sensor
body: in the sidecar
### Amend concept: service
body: in the sidecar
### Amend concept: service-address-book
body: in the sidecar
### Amend concept: signal
body: in the sidecar
### Amend concept: sub-graph
body: in the sidecar
### Amend concept: supervisor
body: in the sidecar
### Amend concept: tag
body: in the sidecar
### Amend concept: template
body: in the sidecar
### Amend concept: terminal-resolution
body: in the sidecar
### Amend concept: terminal-tag
body: in the sidecar
### Amend concept: transition-reason
body: in the sidecar
### Amend concept: validation
body: in the sidecar
### Amend concept: wait-set
body: in the sidecar
### Amend concept: write-semantics
body: in the sidecar

### Stories

### New story: template-validate-without-registering
body: in the sidecar

## Work items

A flat, unordered list. Each item names the outcome and the artifacts it makes true. Items marked **gap test** write one test for a property a concept's removed Invariants entry stated; the code behaves as stated, and nothing proves it. Where the test reveals the code does not behave as stated, the builder records the finding as a claimed fork in the completion report and builds the reading it judges most plausible.

### From the promoted issues

- **Lifecycle fan-out after commit.** Every lifecycle-subscriber delivery site runs after the transition it reports commits; a subscriber error is logged and left to the at-least-once ledger, and it never fails the request or rolls the transition back. The poll loop alone delivers the instance-terminated event; the delete route performs no synchronous fan-out. Tests: a failing subscriber leaves the transition committed and the request successful; a terminated instance's subscribers hear within the poll interval and the delete request does not wait on them. Makes true: `decision:lifecycle-fanout-after-commit`, `concept:lifecycle-subscriber`, `concept:control-api`.
- **Surface intent amended.** Copy the sidecar's `surface/surface.md` over `.ok-planner/surface/surface.md` verbatim: the bundled services' protocol, lifecycle, and observability bridges public; their admin listeners and the claude-agent executor's internal MCP server internal; the core roles' metrics endpoint public with its metric names and labels as surface.
- **Agent-to-proxy hop is TLS in every posture.** The host-agent proxy's agent-facing listener serves TLS always: from a mounted keypair, from its mutual-TLS enrollment leaf, or — in the zero-config posture — from a leaf under a CA the proxy generates locally and publishes for the agent to pin. The host-agent's dial defaults to TLS with CA pinning. Plaintext exists only behind an explicit insecure switch set on both ends; the switch is a `RIMSKY_*` variable (public under the surface intent's environment-variable rule). Tests cover the zero-config TLS path and the insecure switch. Makes true: `decision:host-agent-proxy-tls`, `concept:host-agent-proxy`, `concept:host-agent`, `concept:peer-auth`.
- **Blob-backend mismatch refuses everywhere.** Attribute carry-forward, like the attribute-row readers and the scratch load, returns an error naming the backend mismatch when a spilled row's handle names a backend other than the active one; no path substitutes the inline column. Test: carry-forward over a mismatched handle errors and delivers no empty bag. Makes true: `decision:blob-backend-mismatch-read-refused`, `concept:blob-backend`.
- **Sequenced mode dispatches rounds in arrival order.** In `sequenced` cascade mode a receiver's round from one sender dispatches only when no older round of that sender is still queued. Test: a burst of rounds reaches the executor in arrival order. Makes true: `story:sequenced-preserves-cascade-rounds`, `concept:cascade-mode`.
- **One configuration file.** The supervisor's tuning — concurrency, poll intervals, callback host and port — moves into the unified configuration file under a per-role section; the callback advertise-host keeps its existing environment variable; the supervisor, the images, both compose run paths, and the integration harness drop the second file and the variable that named it. Makes true: `concept:rimsky-yml`, `decision:launch-config-injection`.
- **Promotion lineage record after commit.** For a data-processing producer, rimsky writes the claim-terminal lineage record once, after the commit response, carrying the version identifier. Test: the record carries the version and the ledger holds one row. Makes true: `decision:promotion-lineage-record-after-commit`, `concept:lineage-record`, `concept:data-processing`.
- **Lineage records computation only.** Acquire-phase pass dispositions and pure-cascade settlements write no leaf-run lineage record; the terminal-kind family on a leaf-run record is the closed four. Test: a pass-through node's settlement writes no record. Makes true: `decision:lineage-records-computation-only`, `concept:lineage`, `concept:lineage-record`.
- **Remote one-shot run terminates on quiescence.** The remote one-shot run polls its instances' quiescence — no running frame, no pending message — through the control API, terminates them, and exits, on the same gate the self-hosting verbs use. Test: a remote run against a quiescent instance returns. Makes true: `decision:termination`, `story:one-shot-to-terminal`, `concept:instance`.
- **Expected-attributes schema as a closed contract.** The three bundled executors whose schemas understate their keys — verifier-http, shape-checks, http-node — declare every key they read; template registration rejects a node attribute key the executor's schema does not declare; a fitness test keeps each bundled schema equal to the keys its code reads. Makes true: `decision:expected-attributes-schema-closed`, `concept:observability`, `concept:executor`.
- **`params_redact` deleted.** The spec field, the redactor, its call-site plumbing, and its tests go; instance params are returned as written on every surface. Makes true: `decision:secret-at-rest-posture`, `concept:template`.
- **Every declared event kind has a writer.** Emit sites land for `claim_acquired`, `claim_held`, `state_transition`, `work_rejected`, `no_op_commit`, `claim_resolved`, `attributes_committed`, and `message_sent`, each at the transition it names; where an observable transition has no kind, the vocabulary gains one; a fitness check asserts every operational kind in the enum has at least one emit site. Tests: a filter on each kind returns rows after the transition. Makes true: `story:event-log-read`, `decision:event-log-kind-enum`, `concept:event-log`.
- **Sender-subject on the envelope.** The message envelope carries the sender-subject — the api-key of an operator send, the subscription of a publisher send, empty for an instance send — through the write paths, the read paths, and the responses; the idempotency discriminator admits `instance`, which the cascade path writes. Test: an operator send's envelope names the key. Makes true: `decision:message-sender-kind-discriminator`, `concept:message`.
- **MCP message-send requires the idempotency key.** The tool's schema marks the key required; an omitted key returns a tool error naming the argument; no key is minted for the caller. Test: a call without the key fails naming it. Makes true: `decision:idempotency-key-header-universal`.
- **The deployment owns the template hash.** The validation route returns the canonical hash it computes; the compose planner resolves each manifest template through it before planning; the client resolver applies the aggregation-policy default. Test: a manifest naming a node by kind alias applies and reconciles. Makes true: `decision:template-identity-deployment-canonical`, `story:compose-lifecycle`, `concept:template`.
- **Substitution errors carry no value bytes.** Schema-validation errors for numeric, format, and const constraints name the path and the constraint and never embed the attribute value. Test: a failing const constraint's error omits the value. Makes true: `concept:attribute`, `concept:inertness`.
- **Concept catalog repaired.** Copy every sidecar concept body over its file under `.ok-planner/design/concepts/`; add the new decisions and the new story from the sidecar; copy the amended decisions; regenerate the three catalog TOCs (`concepts.md`, `decisions.md`, `stories.md`); rewrite the "Load-bearing safety properties" paragraph of `CLAUDE.md` to say the properties live in decisions and stories and are proven by tests; remove the `invariant:` row from `.claude/rules/citation-grammar.md`; sweep every live file outside `.ok-planner/history/`, `.ok-planner/archive/`, `.ok-planner/audits/`, and `.ok-planner/sketches/` for a numbered-invariant reference and repoint or delete it. Makes true: every concept delta below, `issue:concept-catalog-carries-non-definitional-content`.

### Gap tests

- **gap test** — The pinned advisory-lock keys are pairwise distinct (`concept:advisory-lock`).
- **gap test** — Release of a claim whose staging was never committed drops the staging exactly as Abandon does (`concept:atomic-staging`).
- **gap test** — A held-holder transition that finds its run already settled by another terminal writer skips without error and leaves the first verdict standing (`concept:auto-terminal`).
- **gap test** — A resume overlay applies to the one dispatch that hit the breakpoint and never persists into the instance's attribute overrides (`concept:breakpoint`).
- **gap test** — A resume overlay joins the dispatch's effective attribute bag so a later breakpoint's matcher in the same dispatch sees it (`concept:breakpoint`).
- **gap test** — Every handler on the cascade-graph read surface is a read: a fitness check over the router's registered methods (`concept:cascade-graph`).
- **gap test** — The cascade walk never creates a frame (`concept:cascade`).
- **gap test** — Fan-out clones never aggregate their attribute writebacks onto the parent (`concept:child-execution`, `decision:fanout-attribute-merge-rejected`).
- **gap test** — The event log's terminal row records rimsky's settlement decision independent of the producer's acknowledgement (`concept:claim-producer`).
- **gap test** — The conformance runner's uniformity check is skipped, not failed, for a pick-policy producer whose consecutive opens return different scopes (`concept:conformance`).
- **gap test** — An access-denied audit row's mode field is empty for a denial raised before mode evaluation and populated after (`concept:event-log`).
- **gap test** — The executor conformance suite exercises a failed async-callback delivery and expects the executor to retry with backoff (`concept:executor`).
- **gap test** — The split-scope conformance suite rejects a producer whose partitions overlap (`concept:fan-out`).
- **gap test** — The scheduler picks frames up in arrival order per instance (`concept:frame`).
- **gap test** — The build, lint, and test gate's module list stays equal to the workspace's module list (`concept:module-layout`).
- **gap test** — Every node-run transition emits exactly one signal: a completeness check over the runtime's transition sites (`concept:signal`).

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
