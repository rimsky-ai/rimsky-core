# Completion report: intake drain and concept-catalog repair

Sprint: `2026-08-21-intake-drain-and-concept-repair.md`

# Certification — intake drain and concept-catalog repair

Status: certified with issues promoted

## Outcomes delivered

- Lifecycle subscribers can no longer block or veto a control-plane transition: every delivery runs after commit, staged in a durable outbox inside the transition's transaction, drained per subscriber stream in order and retried until it lands (`decision:lifecycle-fanout-after-commit`, `decision:lifecycle-subscriber-at-least-once-delivery`). A latent bug this surfaced is fixed: no subscriber had ever received `on_template_deregistered`.
- The concept catalog is repaired to the authoring template: 74 concepts hold What it is / Purpose / Boundaries only, the removed Invariants content lives in 30 new and 11 amended decisions, one new story, and 16 gap tests; the three TOCs, `CLAUDE.md`, and the citation grammar follow.
- The agent-to-proxy hop serves TLS in every posture, with a persisted zero-config CA, a renewing leaf, CA pinning on the agent, and one explicit insecure switch (`decision:host-agent-proxy-tls`).
- A blob-backend mismatch refuses on every read path; a data-processing promotion writes its claim-terminal lineage record once, after commit, carrying the version; lineage records cover computation only (`decision:blob-backend-mismatch-read-refused`, `decision:promotion-lineage-record-after-commit`, `decision:lineage-records-computation-only`).
- Sequenced cascade mode dispatches a receiver's rounds from one sender in arrival order, proven at the executor under a contested burst (`story:sequenced-preserves-cascade-rounds`).
- The supervisor's tuning lives in the one configuration file; the second file and its variable are gone from code, images, and both compose paths (`concept:rimsky-yml`).
- The three understating executors declare every attribute they read and registration treats a bundled schema as closed, with a read-only escape for author-declared outputs (`decision:expected-attributes-schema-closed`).
- The remote one-shot run terminates on quiescence, and a rootless template is refused up front instead of reporting a false success (`story:one-shot-to-terminal`).
- The deployment owns the template hash: the validation route returns it, the compose planner resolves through it (`decision:template-identity-deployment-canonical`, `story:compose-lifecycle`).
- The message envelope carries the sender subject, the ledger's node column names a cascade send's node, MCP `message_send` requires the idempotency key and authorizes before validating, and `params_redact` is gone with instance params returned as written.
- Every declared event kind has a writer and a fitness check enforces it; eight new kinds emit at their transitions (`story:event-log-read`, `decision:event-log-kind-enum`).
- Sixteen gap tests prove properties the removed Invariants sections only stated; the full regression (`make test-all`: lint, all images, every suite including the services stack) exits 0 with 6110 passes.

## Divergences

Each entry is named here for after-the-fact veto; the full entries, kept under their identifiers, follow in the executor's `## Divergences` section below. F1, F5, D5, and D50 name the issues they were promoted into; F2, F3, F4, F6 were overturned by the architect with the built readings standing; D19's refutation (C4) and the casing reversal (C17/C27) are recorded on their entries.

D1 — Release notes under `releases/` keep their numbered-invariant references.
D2 — Discovery scaffolding under `.ok-planner/design/_discover/` keeps its numbered-invariant references.
D3 — Repaired `decision:blessed-invariant-annotations`, which the sprint carries no delta for.
D4 — The TOC regeneration kept the authored summaries of untouched artifacts.
F1 — The delete route drops an instance-terminated delivery the poll loop has not yet made. PROMOTED: `.ok-planner/issues/2026-08-21-222949-instance-delete-drops-undelivered-lifecycle-events.md`.
D5 — Lifecycle and producer-verb delivery failures are logged, not written to the event log. PROMOTED: `.ok-planner/issues/2026-08-21-225241-event-log-domain-for-peer-delivery-health.md`.
D6 — The access-denied audit-row gap test already existed.
D7 — `state_transition` is emitted beside each state write, not from one funnel.
D8 — `concept:claim-producer` lists no alias, and the live second name is `store`, not the dropped `claim-store`.
D9 — The `docs/` release documents still describe `params_redact`.
D10 — The value-free rendering covers `enum` as well as the three families the work item names.
D11 — Message-body validation gained the same rendering.
D12 — Fixed a Stage 2 regression that left the `lib/foundation` module uncompilable.
D13 — Fixed a racing verdict in `TestTemplateErrorPolicy/release_and_requeue_abandons_claim_and_re_acquires`.
D14 — The lifecycle reconciler drains from the source of truth, not from the ledger alone.
D15 — The terminator was renamed rather than joined by a second loop.
D16 — `TestLifecycleReconciler_RowFoundRPCSucceedsRowDeleted` counts its two verbs instead of every call.
D17 — `TestEventLog_SettlingRunWritesStateTransitionAndAttributesCommitted` reads its rows by reason, not by position.
D18 — The postgres column staging a promotion's lineage record is `BYTEA`, not `JSONB`.
D19 — A failed deferred commit result now fails the outbox delivery instead of logging.
F2 — The sub-graph caller keeps its lineage records although it invokes no executor. OVERTURNED: reading (a) stands, and the tree already holds it.
D20 — Each transition stages its lifecycle deliveries, rather than a reconciler reading them back from the source of truth.
D21 — The delete route purges an instance's staged deliveries along with its ledger rows.
D22 — The signal completeness check leaves the held transition unconstrained.
D23 — The sequenced ordering rule holds a round at the gate rather than reordering the dispatch queue.
F3 — Rimsky still delivers a superseded lifecycle event, rather than converging the subscriber on the template's current state. OVERTURNED: reading (a) stands, and the tree already holds it.
D24 — The positive lineage gate also excludes a run whose executor never answered.
D25 — The proxy publishes its locally generated CA root through the log, and through a file when the operator names a path.
D26 — The agent verifies against the system roots when no root is pinned.
D27 — The switch is one variable both binaries read, and the old TLS opt-in is deleted.
D28 — The `docs/` release documents still describe the second configuration file and the old TLS opt-in.
D29 — The proxy always runs its peer-facing listener, so the peer protocols leave the agent-facing port.
D30 — The lifecycle outbox became append-only rather than taking a fresh position on re-stage, and a retention window now bounds it.
D31 — The sequenced probe takes the receiver run alone and derives both sides in SQL.
D32 — A receiver with several senders waits on any of them.
F4 — The expected-attributes contract covers what the executor reads, and http-node's open attribute passthrough is gone. OVERTURNED on all three questions: the built readings stand, and the tree already holds them.
D33 — Registration reads an executor schema as closed unless it declares `additionalProperties: true`.
D34 — Each optional attribute in the three schemas carries the default its code applies.
D35 — Two CLI tests that keyed on the terminal stamp were retargeted rather than deleted.
D36 — The compose planner fails when the deployment returns no canonical hash.
D37 — The split-scope disjointness check uses the producer's own conflict relation where it has one.
D38 — The async-callback backoff check reads elapsed time, and the project's own suite no longer runs it.
D39 — The bundled verifiers gained the park and cancel stub probes.
D40 — `expect_status: []` no longer means "match nothing" in http-node.
D41 — `rimsky compose run` keeps the wake gate it has; only the `rimsky run` verb refuses a rootless template.
F5 — The lifecycle outbox takes a retention window, and the window is off until an operator names one. PROMOTED: `.ok-planner/issues/2026-08-21-222949-lifecycle-outbox-retention-narrows-at-least-once.md`.
D42 — The session-resume registration test carries the example's template shape rather than parsing the example file.
F6 — A closed executor schema admits a property the template marks read-only when the executor leaves its outputs open. OVERTURNED: reading (c) stands, and the tree already holds it.
D43 — The events standard's emit channel is the structured log, and the event log is a product surface.
D44 — The log-kind repair is scoped to the two files the findings name.
D45 — A passed error keeps the completing terminal kind and carries its error class.
D46 — A quoted JSON document keeps its structure even when it carries a substitution directive.
D47 — `rimsky compose run` warns per instance and still applies the manifest.
D48 — The scenario harness takes the proxy CA root as a required argument.
D49 — The host-agent stops repeating one unchanged connect error at the maximum backoff.
D50 — REVERSAL RULED: the upper-case log kinds return to the tree's lower form. PROMOTED: `.ok-planner/issues/2026-08-21-235520-structured-log-kind-case-convention.md`.
D51 — The sending node goes in the ledger's own node column, and `MessageSentPayload` keeps only the type.
D52 — The two readers of `additionalProperties` answer different questions, so an absent key means opposite things.
D53 — The transition check classifies held by its reason rather than abstaining.
D54 — Deleting a template row removed the ledger row its own deregister delivery reads.
D55 — The canary terminates through the verb rather than through the database.
D56 — A staged lifecycle delivery says when it is skipped.
D57 — The SQLite lock-path check derives its population from the struct.
D58 — `CLAUDE.md` named three citation tags where the config declares five.
D59 — A staged delivery reports its success once, before the ledger branch splits.
D60 — A delivery attempt reclaims the ledger row of a peer that is no longer registered.
D61 — The eleven kinds are lower case again, and the two born in this gate take the same idiom.
D62 — The MCP tool lets the route refuse the caller, and renames the refusal afterwards.
D63 — The promoted issue states the counts I measured, not the ones the finding carried.

## Findings fixed

- Sprint alignment (the corpus-change judge): 8 findings — the missing round records, two veto reviews fixed (D5's event-shape half, compose-run silence), one refuted (D19's barrier), the kind-casing round, the canary overstatement, and the eleven-kind follow-on (promoted with the convention issue). Deltas verified byte-identical twice.
- The mechanical floor: clean on every round (annotation integrity, plumbline lint, catalog TOCs).
- Code review (cold, 450 files read in full): 15 findings — among them the `RawJSON` YAML defects, the error-policy pass lineage record, the not-yet-valid CA, the delivered-event blind spot, the orphaned ledger row, and the stale issue premise. All fixed and verified; final sweep DRY.
- Test suites: 2 findings — the canary hang (a harness assumption plus the never-delivered `on_template_deregistered` product bug) and the MCP auth-before-arguments parity break. Both fixed; final `make test-all` exit 0.
- ok-workspaces discipline sweep: clean (no mutable tags, no fixed compose identity, run-tag path intact).
- Loop accounting: 0 repeats subtracted, 1 reversal ruled (C17/C27).

## The finding ledger

The `## Certification ledger` section below holds the table: 32 rows, all settled — 24 fixed, 1 refuted, 1 reversal-ruled, 6 promoted (two rows share an issue).

## Dissolved

None.

## Issues promoted

All verified by `/verify-issues` this run; each awaits your ruling (none was answered by the corpus, none generated):

- `issue:instance-delete-drops-undelivered-lifecycle-events` (architect-confirmed fork, F1/C6/C12) — recommended: stage `instance_terminated` into the outbox at termination so delete cannot destroy an owed delivery.
- `issue:lifecycle-outbox-retention-narrows-at-least-once` (architect-confirmed fork, F5/C10) — recommended: keep the opt-in window, amend the decision to state the bound, pair it with the staleness signal.
- `issue:event-log-domain-for-peer-delivery-health` (architect-confirmed fork, C2/C3) — recommended: a lifecycle diagnostics route now plus stall/recover edge kinds for peer delivery generally.
- `issue:structured-log-kind-case-convention` (architect-confirmed at the reversal, C30/C27) — recommended: ratify the lower dotted form as the structured-log convention.
- `issue:peer-readiness-gate-is-generic` (filed at planning, verified this run) — recommended: a readiness question in the peer protocol gating instance creation and first dispatch, from the 2026-08-08 sketch.

The close-out offer: archive this sprint (with its report, ledger file, delta sidecar, and the eighteen promoted-issue receipts) and commit the work — both on your word alone.

---

## Stages

- Stage 1 — done — Corpus: "Concept catalog repaired" (all concept, decision, and story deltas; three TOCs; `CLAUDE.md` paragraph; `citation-grammar.md` row; numbered-invariant sweep) and "Surface intent amended".
- Stage 2 — done — Lifecycle and event log: "Lifecycle fan-out after commit"; "Every declared event kind has a writer"; gap tests: event-log access-denied mode field; claim-producer event-log terminal row independent of producer ack.
- Stage 3 — done — Messages and template surfaces: "Sender-subject on the envelope"; "MCP message-send requires the idempotency key"; "`params_redact` deleted"; "Substitution errors carry no value bytes".
- Stage 4 — done — Persistence and lineage: "Blob-backend mismatch refuses everywhere"; "Promotion lineage record after commit"; "Lineage records computation only"; gap tests: advisory-lock keys distinct; atomic-staging Release drops staging as Abandon does.
- Stage 5 — done — Runtime and scheduler: "Sequenced mode dispatches rounds in arrival order"; gap tests: auto-terminal racing terminal writer; breakpoint resume overlay non-persistence; breakpoint overlay joins the bag; cascade-graph read surface; cascade walk creates no frame; fan-out clones never aggregate onto parent; frames in arrival order per instance; every node-run transition emits exactly one signal.
- Stage 6 — done — Configuration and deployment: "One configuration file"; "Agent-to-proxy hop is TLS in every posture"; gap test: gate module list equals workspace module list.
- Stage 7 — done — Executors, conformance, and CLI: "Expected-attributes schema as a closed contract"; "Remote one-shot run terminates on quiescence"; "The deployment owns the template hash"; gap tests: conformance uniformity skip for pick-policy producers; executor conformance failed-callback retry; split-scope conformance rejects overlapping partitions.
- Stage 8 — done — Finish the completion report. The build ran seven stages and four fix-only rounds (the owner chose the fourth at the cap); the standing reviewer closed all 44 findings against the tree and its final ledger is empty. Claimed forks F1-F6 and divergences D1-D42 stand under `## Divergences` for the gate.
- Stage 9 — pending — Run `/certify-work .ok-planner/sprints/2026-08-21-intake-drain-and-concept-repair.md`.
- Stage 10 — pending — Walk the presentation with the owner.
- Stage 11 — pending — Offer archive-and-commit.

## Work done

### Stage 1 — Corpus: concept catalog repaired, surface intent amended

Applied every corpus delta from the sidecar verbatim. Copied the 74 concept bodies over `.ok-planner/design/concepts/`, the 41 sidecar decision bodies over `.ok-planner/design/decisions/` (11 amendments overwrote, 30 were new), and the new story into `.ok-planner/design/stories/`. Verified each kind with `diff -r` against the sidecar: no difference. Every concept file now carries the template's four sections and no Invariants section; the section census reads 74 What it is, 74 Purpose, 74 Boundaries, 19 Aliases, and nothing else.

Regenerated the three catalog TOCs in their existing format. Rewrote all 74 concept rows from the new bodies, with each row's aliases read from the file's frontmatter; no concept row mentions Invariants. Wrote fresh decision rows for the 42 touched decisions and kept the authored summaries of the 225 untouched ones, for 267 rows. Added the new story's row, for 122 rows. Each catalog's slug set now equals the file set on disk.

Copied the sidecar's surface intent over `.ok-planner/surface/surface.md` verbatim.

Rewrote the "Load-bearing safety properties" paragraph of `CLAUDE.md`: the properties live in decisions and stories under `.ok-planner/design/`, and tests under `test/scenarios/` and the ordinary suites prove them. Repointed the same file's "Architecture, concepts, invariants" pointer at the design corpus. Removed the `invariant:` row from the Kinds table in `.claude/rules/citation-grammar.md`.

Swept the live tree for numbered-invariant references. Repaired five sentences in `README.md`, two in `.claude/rules/rules.md`, and the failure message in `test/plumbline/numbered_invariant_test.go`, and reworded one log line in `test/plumbline/service_port_precedence_test.go`. Every remaining hit is a plain English use or a symbol name that cites no numbered entry.

Checked the annotations and the lint. All 2681 `@concept:` / `@story:` / `@decision:` annotations in the tree resolve to a live artifact; the only unresolved slug is `no-such-concept` inside a fenced example in `docs/examples/clean-lint.md`, which the lint's own documentation carries as a deliberate example. `.ok-plumbline/bin/plumbline .` exits 0.

Tests run: `go test ./test/plumbline/ -run 'TestNoNumberedInvariantReferences|TestEveryListeningBundledServiceResolvesItsPortThroughTheSharedPrecedence'` and `go test ./tools/rulesdoc/ ./tools/license-check/`, both green.

Paths touched: `.ok-planner/design/concepts/` (74 files), `.ok-planner/design/decisions/` (42 files), `.ok-planner/design/stories/template-validate-without-registering.md`, `.ok-planner/design/concepts.md`, `.ok-planner/design/decisions.md`, `.ok-planner/design/stories.md`, `.ok-planner/surface/surface.md`, `CLAUDE.md`, `README.md`, `.claude/rules/rules.md`, `.claude/rules/citation-grammar.md`, `test/plumbline/numbered_invariant_test.go`, `test/plumbline/service_port_precedence_test.go`.

### Stage 2 — Lifecycle and event log

**Lifecycle fan-out after commit.** Moved every lifecycle-subscriber delivery outside the transaction that performs the transition. The four template routes — register, deregister, deploy, undeploy — now commit first and fan out afterwards through `fanOutTemplateEventAfterCommit`; a subscriber's error is logged and never returns a 500. Instance creation fans out through `fanOutInstanceEventAfterCommit` and no longer fails the request on a subscriber error. The frame engine's settled-scope fan-out was running inside the frame-end transaction: `closeSettledFrameScopeTree` now returns the closed scope ids and `transitionFrameEnd` fans out after the commit. `FanOutTemplateEvent`, `FanOutInstanceEvent`, and the run-scope fan-out no longer abort on the first failing peer; each records the failure per peer, leaves that peer's ledger row unadvanced, and continues. The delete route performs no synchronous fan-out at all; the polling terminator alone delivers instance-terminated, and it now treats a per-peer failure as a retryable tick failure.

Paths: `lib/control/controlapi/lifecycle.go`, `lib/control/controlapi/templates.go`, `lib/control/controlapi/instances.go`, `lib/control/controlapi/instance_terminator.go`, `lib/graph/frame/engine.go`, `lib/runtime/lifecycle_fanout.go`.

Tests: `lib/control/controlapi/lifecycle_fanout_after_commit_test.go` proves a failing subscriber leaves the template registered and the instance created, and that the poll tick delivers run-scope-terminal before instance-terminated. `lib/control/controlapi/delete_instance_no_synchronous_fanout_test.go` proves the delete route calls no subscriber. Three tests that proved the removed veto behaviour are gone.

**Every declared event kind has a writer.** Emit sites landed for the eight kinds the sprint names, and for `error`, which the fitness check also found had no writer. `state_transition` is written at all fifteen node-run state writes the product performs, through `AppendStateTransitionEvent`. `claim_acquired` rides the post-acquisition audit for a producer-backed claim; `claim_held` and `claim_resolved` ride the release path's held and non-held branches. `attributes_committed` and `no_op_commit` land at the completing terminal. `error` records the error policy's resolved action, its class, and its retry delay. `work_rejected` records a dispatch bag the executor schema refuses. `message_sent` lands in `EnqueueMessage`, the one funnel every send passes through, so its dependency interface now names the event table.

The auth audit path consumed raw kind strings and re-parsed them at the write boundary, which `decision:event-log-kind-enum` forbids. The five `auth.Event*` values are now typed kinds and `insertEvent` takes a typed kind, so an unknown kind is unrepresentable rather than parsed and rejected. The test that proved the parse rejection is gone with the parse.

Paths: `lib/runtime/state_transition_event.go`, `lib/runtime/event_emitters.go`, `lib/runtime/runner_terminal.go`, `lib/runtime/runner_terminal_park.go`, `lib/runtime/runner_terminal_release.go`, `lib/runtime/runner_error_policy.go`, `lib/runtime/runner_acquire_postcommit.go`, `lib/runtime/runner_dispatch.go`, `lib/runtime/fanout_dispatch.go`, `lib/runtime/wake_parked.go`, `lib/runtime/instance_kill.go`, `lib/runtime/state_propagation.go`, `lib/runtime/held_cascade_defer.go`, `lib/runtime/message_delivery.go`, `lib/runtime/scheduler/pure_cascade.go`, `lib/foundation/auth/audit.go`, `lib/control/controlapi/audit.go`, `lib/control/controlapi/audit_read.go`.

Tests: `test/plumbline/event_kind_emit_sites_test.go` is the fitness check — it reads all 44 kinds the enum declares and fails naming any with no emit site. `test/scenarios/event_log_kind_writers_test.go` drives each transition and reads the row back by kind.

**Gap test — the event log's terminal row records rimsky's settlement decision independent of the producer's acknowledgement.** Written at `lib/runtime/claim_terminal_event_independence_test.go`: a producer that refuses to acknowledge its commit still leaves exactly one terminal row naming rimsky's outcome. The row lands in the settling transaction; the producer verb is only enqueued there and dispatched after the commit.

**Bug fixed.** `StartScheduler` cancelled its auth-sweep loop on shutdown but never waited for it, so a sweep in flight could write to a closed sqlite database and leave the test's temp directory non-empty. `Shutdown` now joins the loop.

Paths: `lib/control/config/scheduler.go`.

Tests run: `go build ./...`, `go vet ./...`, `go test ./lib/... ./test/plumbline/ ./test/scenarios/ -count=1`, and `.ok-plumbline/bin/plumbline .` — all green.

### Stage 3 — Messages and template surfaces

**Reviewer's stage-1 findings.** Annotated `TestTemplateValidate_RejectsButDoesNotPersist` with `@story: template-validate-without-registering` (S1-1). Widened the numbered-invariant guard's separator class to `[\s#:-]*` so it catches `invariant:4` and `invariant:4-claimant-guarded-release`, and added `TestNumberedInvariantPatternCatchesCitationForms`, which feeds the guard one fixture per citation form and three prose strings that cite no number (S1-2). Recorded the claim-producer alias call as D8 (S1-3). Corrected the Stage 1 decision-row counts to 42 touched and 225 untouched (S1-4).

**Sender-subject on the envelope.** The message envelope now carries the sender-subject. Migration 043 adds `sender_subject` to `rimsky_messages` in both backends. `MessageRow` and `EnqueueMessageRequest` carry the field; both drivers write it on insert and read it on all four selects. The control API stamps it on every send — the api-key id for an operator send, the anonymous sentinel for an anonymous-mode send, the subscription id for a publisher send — and `EnqueueMessage` refuses a non-empty subject on an instance send, so an instance send that carries one fails at the enqueue instead of persisting. The message DTO, the frame projection's message attribution, and the CLI's message read all carry it.

The idempotency dedup discriminator is now an explicit four-value enum. `persistence.DedupSenderKind{Operator,Publisher,Instance,Anonymous}` declares it, `MessageIdempotencyRow.ValidateSenderKind` closes it, and both drivers call that validation at `InsertOrLookup`. The cascade send path writes `instance` and the control API writes `anonymous` through those constants, so the two enums are separate declarations in code, as the decision says.

Paths: `lib/foundation/persistence/postgres/migrations/043-message-sender-subject.sql`, `lib/foundation/persistence/sqlite/migrations/043-message-sender-subject.sql`, `lib/foundation/persistence/messages.go`, `lib/foundation/persistence/message_idempotencies.go`, `lib/foundation/persistence/frames.go`, `lib/foundation/persistence/postgres/{messages,frames,message_idempotencies}.go`, `lib/foundation/persistence/sqlite/{messages,frames,message_idempotencies}.go`, `lib/control/controlapi/messages.go`, `lib/control/controlapi/frames.go`, `lib/runtime/message_delivery.go`, `lib/runtime/runner_send_message.go`, `cmd/rimsky/cli/client_messages.go`, `cmd/rimsky/cli/messages.go`.

Tests: `TestMessageEnvelope_OperatorSendNamesTheSendingKey` sends under a known api-key and reads the key id back from the envelope and from the listing. The persistence conformance suite gained `MessagesSenderSubjectRoundTrips`, which proves both drivers return an operator send's subject and an instance send's empty subject through Get, ListPendingForInstance, and List. `MessageIdempotency/InsertOrLookup` gained the `instance` and `anonymous` cases and a refusal case for an unknown kind.

**MCP message-send requires the idempotency key.** The tool schema marks `idempotency_key` required. `mcp.Catalog` gained an explicit `IdempotencyKeyRequired` set, wired for `message_send` alone; a call with no key returns a tool error naming the argument and never reaches the route, so no key is minted for the caller. Other write tools keep their minting; this sprint does not settle them.

Paths: `lib/control/controlapi/mcp/catalog.go`, `lib/control/controlapi/mcp_route.go`.

Tests: `TestMCPMessageSend_OmittedIdempotencyKeyFailsNamingTheArgument` proves the tool error names the argument and that the keyless call enqueues nothing. `TestBuiltinSchemas_MessageSendDescriptorAdvertisesTypeNoRetiredKindOrTarget` now expects the three-member required set.

**`params_redact` deleted.** Removed the spec field, the redactor and its sentinel, both redactor test files, and the call-site plumbing: `toInstanceItem` returns the stored params, and the listing's per-template redact cache and the `instanceRedact` fail-closed lookup are gone. The scenario harness no longer emits the key.

Paths: `lib/foundation/spec/template.go`, `lib/control/controlapi/instances.go`, `lib/control/controlapi/redact.go` (deleted), `lib/control/controlapi/redact_test.go` (deleted), `lib/control/controlapi/instance_redact_fail_closed_test.go` (deleted), `test/support/scenario/harness.go`.

Tests: `TestInstanceParamsComeBackAsWrittenOnEveryReadSurface` creates an instance with nested params and proves the instance read and the instance listing both return them as written.

**Substitution errors carry no value bytes.** Attribute schema validation no longer hands the library's message through. `attribute.ValueFreeValidationError` walks the validation-error tree and renders each leaf as its instance path plus the constraint it broke; for a value-bearing keyword it stops there, and for the rest it keeps the library's message, which names property names, lengths, and counts rather than values. The value-bearing set is `const`, `enum`, `format`, `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, and `multipleOf` — every v5 keyword whose message formats a value, whether the instance's or the schema's. `ErrSchemaValidation.Cause` holds the rendered error, so unwrapping returns that text and never the library's.

Paths: `lib/graph/attribute/validate.go`, `lib/graph/node/message_body_validate.go`.

Tests: `lib/graph/attribute/validation_error_omits_value_test.go` proves a failing const constraint names the path and the constraint and embeds neither the declared nor the supplied value, drives the same assertion over the numeric, format, and enum constraints, and proves a missing required property still names the property.

**Bugs fixed.** The `lib/foundation` module did not compile: `auth/audit_test.go` still used the five `auth.Event*` values as strings after Stage 2 typed them. The test compared each value to the kind it is now defined as, so it proved nothing; the round-trip half is already covered in `lib/foundation/events/kinds_test.go` over every operational kind. I removed the test. A root-module `go build ./...` does not reach the other three modules, which is how the break survived Stage 2; `make lint` runs all four and caught it.

`TestTemplateErrorPolicy/release_and_requeue_abandons_claim_and_re_acquires` failed once under a full-package run and passed alone. It waited on the node reaching `fresh` and then read the producer's calls, but the settling transaction commits before the producer's Commit verb dispatches, so under load the read raced the call. It now waits on the Commit reaching the stub through `awaited.Until`, the project's outcome-class wait.

Paths: `lib/foundation/auth/audit_test.go`, `test/scenarios/template_error_policy_e2e_test.go`, `lib/control/controlapi/audit_test.go` (removed `requireLoggedError`, dead once the redactor tests went).

Tests run: `go build ./...`, `go vet ./...`, `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`, `go test ./test/scenarios/ ./test/scenarios/messages/ -count=1`, the persistence conformance suite against both drivers, `cd lib/foundation && go test ./... -count=1`, `make lint`, and `.ok-plumbline/bin/plumbline .` — all green. `TestCtxDemo` fails for want of `RIMSKY_IMAGE_TAG` and this run's images, which is the harness's stated precondition and not this work.


### Fix round — the reviewer's stage-2 findings

**S2-1 — lifecycle delivery is at-least-once again.** Stage 2 moved the template-scope and instance-created fan-out after the commit. Nothing then drained a failed delivery: the poll loop covered instance-terminated and run-scope terminals alone. `InstanceTerminator` is now `LifecycleReconciler`, and its tick runs two passes. The first is the terminated-instance drain it already ran. The second reconciles the ledger against the source of truth. It walks every template and redelivers the event that template's current state names. It walks every live instance and redelivers instance-created. It walks each subscriber's ledger rows for a template-scope row whose template is gone, and delivers the deregistration. Each pass calls the existing fan-out, which skips a peer whose row already reads the target state, so a fully delivered scope costs one ledger read per peer and no RPC. `dispatchTemplateDeregisteredForPeer` is the peer-level deregistration delivery, mirroring the instance-terminated fallback.

Paths: `lib/control/controlapi/lifecycle_reconciler.go` (renamed from `instance_terminator.go`), `lib/control/controlapi/lifecycle_reconciler_drain.go`, `lib/control/controlapi/lifecycle.go`, `lib/control/config/controlapi.go`, `lib/control/controlapi/lifecycle_reconciler_test.go` (renamed), `lib/control/controlapi/app_test.go`.

Tests: `TestTemplateRegister_FailingSubscriberLeavesTemplateRegistered` and `TestInstanceCreate_FailingSubscriberLeavesInstanceCreated` now drive a tick after the subscriber recovers and prove the retry delivers once and advances the ledger. `TestLifecycleReconciler_RedeliveredEventIsNotSentAgainOnALaterTick` proves the row the first tick wrote stops the second.

**S2-2 — both gate writes emit `state_transition`.** `evaluateOneGate` and `routeSubstitutionFailureAtGate` write pending to stale and emitted nothing. Both now append the transition with `cascade.ReasonGateCleared`, the one reason that maps pending to stale. The satisfied-gate site loads the receiver node for its instance id, as the failure site already did. D7's count reads seventeen.

Paths: `lib/runtime/gate_evaluator.go`, `test/scenarios/event_log_kind_writers_test.go`.

**S2-3 — a missing node row is refused, not written as a zero instance id.** `cancelInFlightRunTreeChild` left `instanceID` zero when the node row was absent, and the event table's foreign key then aborted the cancel-siblings transaction. The function now returns `errNodeRowMissingForRun`, naming the node and the run, so a store that lost the row fails legibly instead of through a constraint.

Paths: `lib/runtime/state_propagation.go`.

**S2-4 — the discarded struct-conversion error is logged.** `emitErrorPolicyApplied` and `emitWorkRejected` dropped both the `structpb.NewStruct` error and the details it carried. Both now build the struct through `detailsAsStruct`, which logs the failure with the node, the run, and the reason, and lets the event record the transition without the details.

Paths: `lib/runtime/event_emitters.go`.

**S2-5 — the emit-site check requires the kind to reach an Append.** The check counted any `events.KindX()` occurrence, so a switch arm or a filter entry satisfied it. It now parses every product file and walks from each `Events().Append` call back to the kind it writes, resolving a named value, a returning function, and a wrapper's parameter through its call sites. All 44 declared kinds still pass. `TestEmitSiteScanCountsOnlyKindsThatReachAnAppend` runs the scan over synthetic sources and proves a constructor named only in a switch arm and a comparison counts for nothing.

Paths: `test/plumbline/event_kind_emit_sites_test.go`.

**S2-6 — the run-scope fan-out no longer takes a transaction.** `RunScopeTerminalFanout`, `FrameRunScopeTerminalFanout`'s closure, and `FanOutRunScopeEvent` all carried a `tx` that every product call site passed as nil; only a test reached the non-nil branch. The parameter is gone from all three and `withOptionalFanOutTx` with it, so `decision:lifecycle-fanout-after-commit` holds by type rather than by call-site discipline. The test that wrapped the call in a transaction now calls it directly.

Paths: `lib/graph/frame/engine.go`, `lib/runtime/lifecycle_fanout.go`, `lib/runtime/child_execution.go`, `lib/runtime/lifecycle_fanout_test.go`.

**S2-7 — F1 names both event kinds.** The delete route purges the run-scope ledger rows as well as the instance-scope rows, so it drops an undelivered `OnRunScopeTerminal` on the same terms as `OnInstanceTerminated`. F1 now names both.

Tests run: `go build ./...`, `go vet ./...` across all four modules, `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`, `cd lib/foundation && go test ./...`, `go test ./test/scenarios/ ./test/scenarios/lifecycle/ ./test/scenarios/messages/ ./test/scenarios/forensics/ ./test/scenarios/frame_resolution/ -count=1`, `make lint`, and `.ok-plumbline/bin/plumbline .` — all green apart from `TestCtxDemo`, which wants `RIMSKY_IMAGE_TAG` and this run's images. The whole `test/scenarios/...` tree exhausted the machine's docker capacity twice and the run was killed both times; the gate runs that regression.


### Stage 4 — Persistence and lineage

**Reviewer's stage-3 findings.** Dropped the MCP bridge's silent key minting: `mcp.Catalog.Invoke` sets `Idempotency-Key` only when the caller supplied one, so no surface mints a key on the caller's behalf (S3-1). `TestCatalogInvoke_OmittedIdempotencyKeyMintsNoKeyForTheCaller` proves two keyless calls reach the route with no header. Checked S3-2 against the tree: the file still carries `TestKeyRevokedReasonEnumClosed`, and it passes. D12 named the removal loosely, so it now names the one test that went and the one that stands. Set the `minimum` and `exclusiveMinimum` subcases to a bound of 999999 and a value of 4242, so each subcase asserts the absence of its own value (S3-3). Routed `validateAgainstSchema`, the third render site, through `attribute.ValueFreeValidationError`, so a template's declared-defaults bag reports the path and the constraint and never the value (S3-4). Added the two missing message tests: `TestMessageEnvelope_PublisherSendNamesTheSubscription` reads the subscription id back off a publisher send's envelope, and `TestEnqueueMessage_ValidatesShape` now proves an instance send carrying a sender-subject is refused by name and persists no row, and that the same send with an empty subject succeeds (S3-5).

Paths: `lib/control/controlapi/mcp/catalog.go`, `lib/control/controlapi/mcp/catalog_invoke_test.go`, `lib/graph/attribute/validation_error_omits_value_test.go`, `lib/graph/node/template_validator_attrschema.go`, `lib/graph/node/template_validator_attrschema_test.go`, `lib/control/controlapi/idempotency_sender_subject_test.go`, `lib/runtime/message_delivery_test.go`.

**Blob-backend mismatch refuses everywhere.** `persistence.CheckBlobBackendMatches` is now the one refusal, and all four read paths call it: the two driver attribute-row readers, `CarryForwardBag`, and the dispatch scratch load (the last routed through it in the stage-4 fix round, below). Carry-forward was the path that fell back. It read a handle on another backend as an unspilled row and carried the inline column forward, and a spilled row's inline column is empty. It now returns the mismatch error and writes nothing, so `SnapshotBagForNewRun` fails the transaction and the new run gets no bag at all. Both drivers' duplicate `blobBackendName` helpers are gone, and so is the duplicated message.

Paths: `lib/foundation/persistence/blob_spill.go`, `lib/foundation/persistence/postgres/node_attributes.go`, `lib/foundation/persistence/sqlite/node_attributes.go`.

Tests: `TestSnapshotBagCarryForwardRefusesAMismatchedBackend` seeds a spilled row, rewrites its backend name, and proves the carry-forward errors naming both backends and leaves the new run's attribute row absent.

**Promotion lineage record after commit.** A data-processing promotion's claim-terminal record is now written once, after the producer's commit response, carrying the version that response returns. Settlement builds the record as before and stages it on the promotion's producer-verb outbox row (`pending_lineage_record`, migration 044 in both drivers); the outbox's commit delivery decodes it, stamps `version_id` from the commit result, and writes it. A settlement with no candidate handle keeps writing at settlement, unchanged. `applyDeferredCommitResult` now returns its error instead of logging it, so a failed version write or lineage write leaves the outbox row for the next attempt rather than dropping it.

Paths: `lib/foundation/persistence/producer_verb_outbox.go`, `lib/foundation/persistence/{postgres,sqlite}/producer_verb_outbox.go`, `lib/foundation/persistence/{postgres,sqlite}/migrations/044-producer-verb-outbox-pending-lineage.sql`, `lib/runtime/terminal_decision_forensics.go`, `lib/runtime/terminal_decision.go`, `lib/runtime/producer_verb_outbox.go`.

Tests: `TestResolveClaimHandleTerminal_PromotionLineageRecordWaitsForTheCommitResponseAndCarriesTheVersion` proves the ledger holds no claim-terminal row at settlement, then exactly one carrying `v-42` after the outbox flush. The persistence conformance suite's outbox round-trip now covers the staged record on both drivers.

**Lineage records computation only.** The leaf-run terminal kind is a closed family of four constants — `complete`, `errored`, `park`, `subgraph_call` — and `WriteLeafRunLineage` refuses any other value, so `pure_cascade` and `handler_pass` are unrepresentable. The scheduler's pure-cascade settlement writes no record. Every runner emit site goes through `emitLeafRunLineageForAcq`, which drops the write for a run that invoked no executor: a node with no executor, and an acquire-phase disposition, which `runAcquireErrorPolicy` marks. A post-executor pass disposition records `complete`. The sub-graph caller's site stays ungated, because `concept:lineage-record` owes that record (see F2).

Paths: `lib/runtime/lineage_writer.go`, `lib/runtime/runner_acquire.go`, `lib/runtime/runner_acquire_error_policy.go`, `lib/runtime/runner_error_policy.go`, `lib/runtime/runner_terminal.go`, `lib/runtime/runner_terminal_park.go`, `lib/runtime/subgraph_dispatch.go`, `lib/runtime/scheduler/pure_cascade.go`.

Tests: `TestPassThroughNodeSettlementWritesNoLineageRecord` drives a hub with no executor into a downstream worker, waits on the worker's record, and proves the hub has none. `TestLeafRunRecordCreation_RefusesATerminalKindOutsideTheClosedFamily` feeds the writer the four members and three non-members.

**Gap test — the pinned advisory-lock keys are pairwise distinct.** `TestPinnedAdvisoryLockKeysArePairwiseDistinct` reads the five pinned values in the client-server driver — the scheduler-tick key, the migration key, and the three transaction-scoped key classes — and fails naming the pair that collides. `TestPinnedAdvisoryLockFilesArePairwiseDistinct` does the same for the embedded driver's two lock files and the database file beside them.

Paths: `lib/foundation/persistence/postgres/advisory_lock_keys_test.go`, `lib/foundation/persistence/sqlite/advisory_lock_paths_test.go`.

**Gap test — Release drops an uncommitted staging as Abandon does.** `TestAtomicStaging_ReleaseDropsUncommittedStagingExactlyAsAbandonDoes` stages two claims over one canonical schema, abandons one and releases the other, and proves both staging schemas and both reservations are gone and the canonical view still holds its pre-stage row.

Paths: `lib/services/claim_producers/postgres/store/atomic_staging_test.go`.

Tests run: `go build ./...`, `go vet ./...` across the modules, `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`, `cd lib/foundation && go test ./... -count=1`, `go test ./test/scenarios/ ./test/scenarios/lineage/ ./test/scenarios/forensics/ -count=1`, `cd lib/services && go test ./claim_producers/postgres/store/`, `make lint`, and `.ok-plumbline/bin/plumbline .` — all green apart from `TestCtxDemo`, which wants `RIMSKY_IMAGE_TAG` and this run's images.


### Fix round — the reviewer's lifecycle-reconciler findings

**FR-1, FR-4, FR-5 — lifecycle delivery is a transactional outbox.** The reconcile pass listed every template and every active instance on the two-second tick and called the fan-out on each. A steady state therefore cost a scan of the whole control plane, and a scan that outran the tick budget never finished, because pagination restarted at the first page on the next tick. Rimsky now stages each delivery instead. `rimsky_lifecycle_outbox` (migration 045, both drivers) holds one row per subscriber and event, and each row carries the payload its delivery needs. The four template routes and the instance-create route stage their rows inside the transaction that performs the transition. The fan-out then runs after the commit and deletes each row as its subscriber answers. The reconciler's pass is now one targeted query, `ListPending` over the outbox. A fully delivered control plane costs one query that returns no rows, and undelivered work alone bounds the drain. The three source-of-truth walks are gone, and `FanOutTemplateEvent` with them.

Delivery runs in staged order per subscriber and scope, and stops at the first failure. A subscriber therefore sees a scope's events in the order they happened. That settles FR-4: a failed `template_registered` followed by a deploy is delivered as registration then deploy, rather than converged onto the template's current state (see F3). It settles FR-5 too. An instance whose creation never reached its subscriber keeps its staged row, so the reconciler delivers the creation and the terminated drain then delivers the termination, whether or not the instance is still active.

**FR-2 — the reconcile pass is damped like the terminated drain.** `recordFailure` and `clearFailure` now key on a string. Every failure the reconciler logs routes through them: the staged-delivery list, each staged delivery, and the terminated-instance list. A persistently failing peer therefore logs once and then every tenth attempt.

**FR-3 — a missing node row at the gate is refused, not swallowed.** `appendGateClearedTransition` returned nil when the node row was absent, committing the pending-to-stale transition with no `state_transition` row. It now returns `errNodeRowMissingForRun`, naming the node and the run, as `cancelInFlightRunTreeChild` already does for the same condition.

**FR-6 — `strconv.Itoa` replaces the hand-rolled `itoa`.**

Paths: `lib/foundation/persistence/lifecycle_outbox.go`, `lib/foundation/persistence/tables.go`, `lib/foundation/persistence/{postgres,sqlite}/lifecycle_outbox.go`, `lib/foundation/persistence/{postgres,sqlite}/migrations/045-lifecycle-outbox.sql`, `lib/foundation/persistence/conformance/{lifecycle_outbox.go,conformance.go}`, `lib/control/controlapi/lifecycle_outbox.go`, `lib/control/controlapi/lifecycle.go`, `lib/control/controlapi/lifecycle_reconciler.go`, `lib/control/controlapi/lifecycle_reconciler_drain.go`, `lib/control/controlapi/templates.go`, `lib/control/controlapi/instances.go`, `lib/runtime/gate_evaluator.go`, `test/plumbline/event_kind_emit_sites_test.go`.

Tests: the persistence conformance suite gained `LifecycleOutboxDeliversInStagedOrder`, which proves the staged order, the in-place replacement of a re-staged event, and both deletes on each driver. `lib/control/controlapi/lifecycle_test.go` carries the template fan-out's behaviour on the staged path: every subscriber hears a registration once, a replay calls no subscriber, one failing subscriber leaves only its own delivery owed and takes it on the next pass, and a deregistration deletes every ledger row. `TestTemplateEvents_AFailedPredecessorIsDeliveredBeforeItsSuccessor` proves F3's reading. `TestLifecycleReconciler_InstanceThatTerminatesBeforeItsCreationLandsGetsBothEvents` proves FR-5.

### Stage 5 — Runtime and scheduler

**Sequenced mode dispatches rounds in arrival order.** The gate evaluator treated `sequenced` as a no-op. A receiver's queued rounds therefore became claimable in whatever order the runtime evaluated their gates, and the advanced-sibling guard then serialised them behind whichever round advanced first. `applyCascadeModeRule` now returns a hold as well as a drop, and the sequenced arm holds a round while an older cascade round of the same receiver in the same run scope is still unsettled. The held round is re-evaluated when its predecessor settles, through the pending-sibling sweep `drainWaitSetOnSettled` already runs. `HasEarlierUnsettledCascadeRun` is the new targeted query, implemented on both drivers.

Paths: `lib/runtime/gate_evaluator.go`, `lib/foundation/persistence/nodes.go`, `lib/foundation/persistence/{postgres,sqlite}/nodes.go`.

Tests: `TestSequencedMode_ABurstOfRoundsBecomesClaimableInArrivalOrder` seeds three queued rounds, evaluates the newest gate first, and proves only the oldest becomes claimable, then that each successor becomes claimable as its predecessor settles. The persistence conformance suite gained `HasEarlierUnsettledCascadeRun`, which proves the query sees the older round, sees nothing before the first sequence, and never reads across run scopes.

**Gap test — a held-holder transition skips a run another terminal writer already settled.** `TestHeldHolderTransition_SkipsARunAnotherTerminalWriterAlreadySettled` seeds two holders with fully resolved portfolios: the one still held settles to fresh, and the one a killer already failed stays failed, with no error.

Paths: `lib/runtime/auto_terminal_settled_holder_test.go`, `lib/runtime/hard_dep_cascade_export_test.go`.

**Gap tests — the resume overlay.** `TestResumeOverlayAppliesToOneDispatchAndNeverBecomesAnInstanceOverride` proves the overlay reaches the dispatch that hit the breakpoint, leaves the instance's attribute overrides empty, and leaves no trace on the node's next dispatch. `TestResumeOverlayJoinsTheBagALaterBreakpointMatchesOn` installs a second breakpoint whose matcher reads an attribute value only the overlay supplies, and proves it hits.

Paths: `test/scenarios/breakpoints/resume_overlay_is_one_shot_test.go`.

**Gap test — the cascade-graph surface registers reads only.** `TestCascadeGraphSurfaceRegistersReadsOnly` walks the router `observability.Routes` builds and fails on any method other than GET or HEAD. It checks all 18 routes.

Paths: `test/plumbline/cascade_graph_read_surface_test.go`.

**Gap test — the cascade walk creates no frame.** `TestCascadeWalkCreatesNoFrame` drives a three-node chain through several self-cascade rounds and proves the instance's frame count equals the number of messages the frame engine picked up, while the walk created more cascade-driven runs than there are frames.

Paths: `test/scenarios/cascade_walk_creates_no_frame_test.go`.

**Gap test — a fan-out clone's writeback stays on the clone.** `TestFanOutCloneWritebacksNeverAggregateOntoTheParent` gives three partitions three distinct values for one key and proves the parent's attribute bag carries none of them.

Paths: `test/scenarios/fanout_clone_writeback_stays_on_the_clone_test.go`.

**Gap test — frames start in arrival order.** `TestFramesStartInTheArrivalOrderOfTheirMessages` holds the first frame's dispatch at the executor, posts two more messages, releases, and proves the frames started in the order their messages arrived.

Paths: `test/scenarios/frames_start_in_arrival_order_test.go`.

**Gap test — every node-run transition names exactly one signal.** `TestEveryNodeRunTransitionNamesExactlyOneSignal` parses every product call to `Nodes().UpdateState` under `lib/runtime`, resolves a state held in a local variable to the states assigned to it, and asserts each transition into `fresh`, `failed`, or `parked` names a settling signal type while a transition into `stale` or `running` names none. The state write takes exactly one signal-type argument, so no site can name two. It checks all 16 sites.

Paths: `test/plumbline/node_run_transition_signal_test.go`.

Tests run: `go build ./...`, `go vet ./...` across the modules, `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`, `cd lib/foundation && go test ./... -count=1`, `go test ./test/scenarios/ ./test/scenarios/breakpoints/ -count=1`, `go run ./tools/wallclock-lint`, `make lint`, and `.ok-plumbline/bin/plumbline .` — all green apart from `TestCtxDemo`, which wants `RIMSKY_IMAGE_TAG` and this run's images.


### Fix round — the reviewer's stage-4 findings

**S4-1 — the lineage gate is a positive flag set where the executor is invoked.** `acquisition.AcquirePhase` is gone; `acquisition.ExecutorInvoked` replaces it, set at the one dispatch site that calls `client.Execute` and at the async-callback path that reconstructs an acquisition for a run already dispatched. `invokedExecutor` now reads that flag alone. Neither path the finding names sets the flag: `applyAttributeFailure` after acquisition, and `routeSubstitutionFailureAtGate`. Neither writes a leaf-run record. See D24 for the paths the positive gate also excludes.

Paths: `lib/runtime/runner_acquire.go`, `lib/runtime/runner_acquire_error_policy.go`, `lib/runtime/runner_dispatch.go`, `lib/runtime/callback.go`, `lib/runtime/subgraph_exit_normal_leaf_run_test.go`.

Tests: `lib/runtime/lineage_pre_dispatch_failure_test.go` drives each path over a real driver and proves the run settles to failed and the ledger holds no leaf-run row for it.

**S4-2 — a promotion with no outbox writes its record at settlement.** `emitTerminalForensics` now stages the record only when a producer-verb outbox exists. With no outbox it writes the record immediately, versionless, on the path every other terminal takes. A record can no longer vanish between a settlement that stages it and a dispatcher that never runs.

**S4-3 — a failed marshal falls back to the direct write.** The same function writes the record at settlement when `json.Marshal` fails, and logs the failure as the reason. `pending` holds bytes only when the staging succeeded, so the two branches never both run.

Paths: `lib/runtime/terminal_decision_forensics.go`.

**S4-4 — the lineage write and the outbox delete share one transaction.** `DispatchOnce` no longer deletes the row after `deliverRow` returns. `deliverProducerVerb` takes a finalize callback, and `applyDeferredCommitResult` runs it inside the transaction that writes the version and the lineage record. The dispatcher passes the row's delete as that callback, so a failed delete rolls back the record with it. The barrier path passes its own delete the same way and drops its separate call. A non-Commit verb runs the callback in its own transaction.

Paths: `lib/runtime/producer_verb_outbox.go`.

Tests: `TestProducerVerbDispatch_ARetriedDeliveryLeavesOneClaimTerminalRecord` refuses the first delete, proves the pass writes no record and keeps the row, then advances the clock past the backoff and proves the retry leaves exactly one record carrying the version and an empty outbox.

**S4-5 — D19 now states the barrier coupling and why I accepted it.** See D19.

**S4-6 — the advisory-lock key test derives its population from the source.** `TestPinnedAdvisoryLockKeysArePairwiseDistinctWithinTheirLockSpace` parses `advisory_locker.go`, reads the key constants the const block declares, walks every lock call, and sorts each key into its Postgres lock space by the SQL the call carries: `pg_advisory_lock($1)` and its unlock are the session-scoped space, `pg_advisory_xact_lock($1, hashtext($2))` the transaction-scoped class space. It compares within each space and reports the two populations it checked. It fails on a key no lock call reaches, and on a lock call naming a constant the file does not declare.

Paths: `lib/foundation/persistence/postgres/advisory_lock_keys_test.go`.

**S4-7 — the scratch load calls the shared refusal.** `loadScratchIntoAcquisition` routes through `persistence.CheckBlobBackendMatches`, so the missing-backend and mismatched-backend cases now read the one message. `TestLoadScratchIntoAcquisition_FailsRatherThanHandOverAFalseEmptyState` asserts against that message. The stage-4 text above now says four paths.

Paths: `lib/runtime/runner_acquire_helpers.go`, `lib/runtime/runner_acquire_scratch_load_test.go`.

**S4-8 — the inner Warn is gone.** `applyDeferredCommitResult` returns its error and logs nothing; `DispatchOnce` logs the delivery failure once, with the backoff it applied.

Tests run: `go build ./...`, `go vet ./...` across the modules, `go test ./lib/runtime/... -count=1`, `go test ./test/scenarios/lineage/ ./test/scenarios/forensics/ -count=1`, and `cd lib/foundation && go test ./persistence/postgres/ -run TestPinnedAdvisoryLockKeys` — all green.

### Stage 6 — Configuration and deployment

**One configuration file.** The supervisor's tuning moved into the unified configuration file under a `supervisor:` section: `supervisor_id`, `concurrency`, `liveness_interval_ms`, `claim_poll_interval_ms`, and a `callback:` block with `host`, `port`, `advertise_host`, and `advertise_port`. `config.SupervisorSection` carries it on `RimskyConfig`, and the unified loader parses it, keeping the callback port a pointer so an explicit `0` still means an operating-system-assigned port. `RunSupervisor` reads `rimskyCfg.Supervisor` and no longer reads a path from the environment; `loadSupervisorYAML` and the second file are gone. The advertise host keeps `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST`, which still wins over the YAML value.

Both compose run paths write one file. `WriteSyntheticRimskyYAML` takes the callback port and emits the supervisor block itself; `WriteSyntheticSupervisorYAMLWithCallbackPort` is gone, and neither run directory carries a `supervisor.yml`. The `rimsky` image drops `RIMSKY_SUPERVISOR_CONFIG`, the all-in-one image bakes one file, and `dockerfiles/all-in-one.supervisor-config.yml` is deleted with its tuning merged into `dockerfiles/all-in-one.rimsky.yml`. The services harness renders the supervisor block into the rimsky.yml it hands every container, in both the all-in-one and the split topology, and its split path no longer writes a second file. The variable that named the second file appears nowhere in the tree.

Paths: `lib/control/config/supervisor.go`, `lib/control/config/claim_producers.go`, `lib/control/launch/supervisor.go`, `lib/control/launch/supervisor_test.go`, `cmd/rimsky/cli/compose/synthetic_config.go`, `cmd/rimsky/cli/compose/run.go`, `cmd/rimsky/cli/compose/template_run.go`, `cmd/rimsky/cli/compose/synthetic_config_test.go`, `cmd/rimsky/cli/compose/launcher_test.go`, `cmd/rimsky/cli/compose/launcher_internal_test.go`, `dockerfiles/Dockerfile.rimsky`, `dockerfiles/Dockerfile.all-in-one`, `dockerfiles/all-in-one.rimsky.yml`, `dockerfiles/all-in-one.supervisor-config.yml` (deleted), `lib/services/test/harness/rimsky.go`, `lib/services/test/harness/rimsky_split.go`, `CLAUDE.md`.

Tests: `lib/control/config/supervisor_section_test.go` loads the section from a unified file, proves the environment expansion reaches `callback.advertise_host`, and proves an explicit `port: 0` and an omitted `port` come back different. `TestWriteSyntheticRimskyYAML_CarriesTheSupervisorSectionWithTheCallbackPort` proves the compose run writes one file carrying the block and no `supervisor.yml` beside it. `TestSyntheticSupervisorDefaultsMatchTheBakedAllInOneFile` loads the baked all-in-one config and compares its supervisor tuning to the CLI's constants, so a local run and the image cannot diverge.

**Agent-to-proxy hop is TLS in every posture.** `proxyServerCredentials` returns a struct naming the credentials, their source, and the CA root to publish. It picks in order: the insecure switch, the operator-mounted keypair, the enrollment leaf, and — when none of those settle it — a leaf under a CA the proxy generates at startup. The proxy publishes that root: it logs the PEM, and writes it to `RIMSKY_PROXY_LOCAL_CA_FILE` when the operator names a path. The agent's dial defaults to TLS. With `RIMSKY_AGENT_TLS_CA` set it verifies the pinned root against the fixed peer server name every rimsky peer dial uses, which the minted leaf and the enrollment leaf both carry; without one it verifies against the system roots. `RIMSKY_HOST_AGENT_INSECURE`, read by both binaries, is the only way to plaintext; `RIMSKY_AGENT_TLS` and `rimsky agent start --tls` are gone, and `--insecure` replaces the flag. The proxy now always runs its peer-facing listener as well, so the peer-service protocols leave the agent-facing port (see D29).

Paths: `cmd/rimsky-host-agent-proxy/tls.go`, `cmd/rimsky-host-agent-proxy/config.go`, `cmd/rimsky-host-agent-proxy/main.go`, `cmd/rimsky-host-agent-proxy/serving.go`, `cmd/rimsky-host-agent-proxy/tls_test.go`, `cmd/rimsky-host-agent-proxy/agent_hop_tls_test.go`, `lib/runtime/hostagent/tls.go`, `lib/runtime/hostagent/config.go`, `lib/runtime/hostagent/tls_test.go`, `cmd/rimsky/cli/agent.go`, `test/scenarios/host_agent_harness_test.go`, `test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go`, `test/scenarios/host_agent_cli_autostart_test.go`, `test/scenarios/host_agent_control_plane_demo_test.go`, `test/scenarios/host_agent_negative_auth_test.go`, `test/scenarios/host_agent_latebind_rejects_unsupported_protocols_test.go`, `test/scenarios/host_agent_per_run_scope_isolation_test.go`, `test/fixtures/demos/host-agent-control-plane-demo.sh`, `cmd/rimsky-host-agent-proxy/serving_test.go`, `lib/runtime/hostagent/spawn_test.go`, `lib/runtime/hostagent/connect_register_ack_test.go`, `lib/runtime/hostagent/mtls_test.go`, `tools/env-registry/registry.md`, `CLAUDE.md`.

Tests: `TestZeroConfigAgentHopCarriesTheKeyInsideTLSUnderThePublishedRoot` boots the agent-facing listener on the zero-config credentials, dials it with the published root pinned, and proves the proxy registers the agent. `TestAgentHopRefusesAPlaintextDialWhileTheProxyServesTLS` and `TestAgentHopRunsPlaintextWhenBothEndsCarryTheInsecureSwitch` prove the switch binds both ends. `TestAgentTransportCredentialsDialTLSByDefault`, `TestAgentTransportCredentialsPlaintextOnlyBehindTheInsecureSwitch`, and `TestAgentTransportCredentialsRejectAnUnreadableCARoot` cover the agent's side. The host-agent scenarios now run the whole spawn-dispatch-reap path over the zero-config TLS hop: the proxy publishes its root to a temp file and every agent pins it.

**Gap test — the gate's module list equals the workspace's.** `TestBuildLintAndTestGatesCoverEveryWorkspaceModule` reads `go.work`'s `use` list, parses the `Makefile` into targets and recipes, walks each gate's prerequisite closure, and derives the module directory each `go build ./...`, `golangci-lint run`, and `$(GOTEST_GUARD)` invocation runs in. It compares the three sets to the workspace list and names the difference. It checks all four modules.

Paths: `test/plumbline/module_layout_test.go`.

Tests run: `go build ./...` and `go vet ./...` across all four modules, `go test ./lib/control/... ./lib/runtime/... ./cmd/... ./test/plumbline/ -count=1`, `go test ./test/scenarios/ -run 'HostAgent|CLIRunService|Anonymous' -count=1`, `cd lib/foundation && go test ./persistence/ ./persistence/postgres/ ./persistence/sqlite/ -count=1`, `go run ./tools/env-registry`, `make lint`, and `.ok-plumbline/bin/plumbline .` — all green apart from `TestCtxDemo`, which wants `RIMSKY_IMAGE_TAG` and this run's images.

Three test fixtures moved with the default. `connectAgentToFakeProxy`, the shared entry point every spawn, dispatch, and reap test goes through, sets the insecure switch, because its fake proxy is plaintext; `connect_register_ack_test.go` and `mtls_test.go` set it on the two configs they build by hand. `TestSingleServing_PeerAuthNoneKeepsOneServer` asserted the shape D29 retires, so it is now `TestSplitServing_HoldsWithPeerAuthOff` and proves the split holds with peer-auth off: the agent listener carries HostAgent alone and the peer-facing listener carries the peer-service protocols.

### Fix round — the reviewer's stage-5 findings

**S5-1 — the lifecycle outbox is append-only.** The staging upsert kept the first staging's `seq`, so a deploy, an undeploy, and a re-deploy delivered as deploy then undeploy and left the subscriber's last-heard state wrong for good. `Stage` is now a plain insert, migration 045 drops the unique key in both drivers, and `Delete` becomes `DeleteBySeq`. A re-staged event therefore lands after the events staged between the two stagings.

Paths: `lib/foundation/persistence/lifecycle_outbox.go`, `lib/foundation/persistence/{postgres,sqlite}/lifecycle_outbox.go`, `lib/foundation/persistence/{postgres,sqlite}/migrations/045-lifecycle-outbox.sql`, `lib/control/controlapi/lifecycle_outbox.go`.

Tests: the persistence conformance case now stages deploy, undeploy, deploy and proves the scope holds three rows in that order with their own payloads, and that `DeleteBySeq` removes the named row. `TestTemplateEvents_ARestagedEventArrivesAfterTheEventsStagedBetweenIt` proves the delivery order and the subscriber's final ledger state at the control-API level.

**S5-4 — the drain reads one head per stream.** `ListPending` returned the hundred oldest rows table-wide, so one dead subscriber's backlog filled every batch and starved every other stream. `ListOldestPendingPerStream` returns the oldest pending row per `(claim_producer_name, scope_kind, scope_id)` — `DISTINCT ON` on postgres, a `MIN(seq)` group on the embedded driver — ordered by `seq`. The drain delivers those heads, then queries again, and stops when a pass delivers nothing or the tick's delivery cap is reached, so a stream advances round-robin.

Paths: `lib/control/controlapi/lifecycle_reconciler_drain.go`.

Tests: `TestLifecycleReconciler_ABlockedStreamDoesNotStarveAnother` stages a hundred and twenty deliveries for a failing subscriber and one for a healthy one, and proves the healthy one lands on the same tick.

**S5-2 — the sequenced probe is scoped to the round's sender and to queued states.** `HasEarlierUnsettledCascadeRun` filtered on node and run scope alone and matched five states, so a round from one sender waited behind another sender's round and a parked or held round stalled every later round. `HasEarlierQueuedRoundFromSameSender` takes the receiver run and derives both sides in SQL: it joins the receiver's wait-set rows to their sender runs, and looks for an earlier cascade round of the same receiver, in `pending` or `stale`, whose own sender run belongs to one of those sender nodes.

Paths: `lib/foundation/persistence/nodes.go`, `lib/foundation/persistence/{postgres,sqlite}/nodes.go`, `lib/runtime/gate_evaluator.go`.

Tests: the persistence conformance case seeds two sender nodes and four receiver rounds and proves the probe sees an older queued round of the same sender, ignores another sender's rounds, ignores another run scope, and stops seeing a round that settled or parked.

**S5-3 — the scenario contests the dispatch and the gate test carries what it cannot show.** `TestSequencedPreservesCascadeRounds` now holds b's first dispatch at the stub, waits for a's later rounds to queue two more b-runs behind it, releases, and asserts the executor saw the rounds in arrival order. `lib/runtime/sequenced_cascade_order_test.go` is now `TestSequencedMode_ARoundWaitsOnItsOwnSenderNotOnAnother`, which seeds two senders and proves a round from one sender clears its gate while an older round from the other stays pending — the one property the scenario cannot show.

Paths: `test/scenarios/sequenced_preserves_cascade_rounds_test.go`, `lib/runtime/sequenced_cascade_order_test.go`.

**S5-5 — the lifecycle scenario passes.** `go test ./test/scenarios/lifecycle/ -count=1` is green on the outbox path; it needed no change.

### Stage 7 — Executors, conformance, and CLI

**Expected-attributes schema as a closed contract.** Registration now treats an advertised schema as closed unless the executor declares `additionalProperties: true`. `validateCompositionAgainstExecutor` reads `!executorSchemaAllowsExtensions`, the same test the read-only rule beside it already used. The three named executors declare every attribute their code reads. verifier-http adds `body`, `timeout_ms`, `expected_status`, and `class_field` beside `url`; verifier-shape-checks adds `rows` beside `checks`; http-node moves from `{"type":"object"}` to `url`, `method`, `headers`, `body`, `expect_status`, `error_class_field`, `stub_probe`, and the read-only `stub`. Each optional key carries the default its code applies, so a template that leaves it out still registers. All three schemas declare their outputs read-only. http-node's open attribute passthrough is gone (see F4), and its `expect_status` guard now ignores an empty list rather than treating it as "match nothing".

Paths: `lib/graph/node/template_validator_attrschema.go`, `lib/services/executors/verifier-http/identity.go`, `lib/services/executors/verifier-shape-checks/identity.go`, `lib/services/executors/http-node/identity.go`, `lib/services/executors/http-node/server.go`, `lib/services/executors/http-node/README.md`, `lib/protocols/conformance/stubmode/stubmode.go`.

Tests: `lib/services/test/plumbline/expected_attributes_schema_test.go` is the fitness check. It parses each bundled executor package, collects every string-constant read of the dispatch attributes bag — following the stub-mode helpers the package calls — and fails when a read key is undeclared, when a declared input is read nowhere, and when the code reads the whole bag. `TestValidateAttributesSchema_UndeclaredPropertyRejectedWithoutAnExplicitClosedFlag` proves registration refuses a misspelt attribute under a schema that says nothing about extensions and under one that closes them explicitly. `TestExecute_RequestBodyComesFromTheBodyAttributeAlone` proves http-node's upstream body carries the `body` attribute and nothing else.

**Remote one-shot run terminates on quiescence.** `waitAndCleanup` polled `terminated_at`, a stamp nothing sets, so a remote `rimsky run --no-keep` never returned. `cli.InstanceQuiescence` is now the one gate — no running frame, no pending message — and the compose path's `instanceIsIdle` calls it too. The remote run polls it, terminates the instance through the control API, classifies the outcome, and cleans up.

Paths: `cmd/rimsky/cli/quiescence.go`, `cmd/rimsky/cli/run.go`, `cmd/rimsky/cli/compose/wait.go`, `cmd/rimsky/cli/run_test.go`, `cmd/rimsky/cli/run_exit_codes_test.go`.

Tests: `TestRemoteOneShotRunTerminatesItsInstanceOnQuiescenceAndReturns` drives the real verb against a live control API and returns; under the old gate it never returned. The exit-code fake now models frames and messages and fails the run that reaches a quiescent instance without terminating it.

**The deployment owns the template hash.** The validation route returns the canonical hash it already computed as `template_hash`. `compose.ResolveTemplateThroughDeployment` posts each manifest template to that route and takes the hash from the answer, and `ComputePlan` resolves through it. The client resolver applies the aggregation-policy default alongside the frame-resolution defaults.

Paths: `lib/control/controlapi/templates.go`, `cmd/rimsky/cli/client_templates.go`, `cmd/rimsky/cli/compose/resolver.go`, `cmd/rimsky/cli/compose/plan.go`, `cmd/rimsky/cli/internal/clitest/{server,state}.go`.

Tests: `TestComposeManifestNamingANodeByKindAliasAppliesAndReconciles` applies a manifest whose template names a node by kind alias and proves the second plan is empty. With the planner back on the local hash the same test fails, so it discriminates.

**Gap test — the uniformity check is skipped for a pick-policy producer.** `TestRun_Uniformity_SkippedForAPickPolicyProducerWhoseOpensReturnDifferentScopes` drives a producer whose consecutive opens return different scopes and proves both opens pass while the result set carries no uniformity row at all.

Paths: `lib/protocols/conformance/claimproducer/runner_uniformity_test.go`.

**Gap test — the executor battery reads the retry cadence.** The kit's callback receiver records every delivery attempt. The new `async_callback_retry_backoff` scenario refuses three attempts, lets the fourth through, and then reads the attempt times: the gaps must never shrink and the last must be at least twice the first. The in-tree stub executor now retries on a widening cadence, which is the reference behaviour it should have shown all along.

Paths: `lib/protocols/conformance/executor/callback_receiver.go`, `lib/protocols/conformance/executor/scenarios/async_callback_retry_backoff.go`, `lib/protocols/conformance/executor/scenarios/scenarios_test.go`.

Tests: `async_callback_retry_backoff_internal_test.go` feeds `checkRetryBackoff` synthetic timestamps and proves it rejects a fixed cadence, a tightening cadence, and too few attempts, and accepts a widening one.

**Gap test — the split-scope battery rejects overlapping partitions.** `checkSubScopesDisjoint` compares every pair of returned sub-scopes: byte-equal scopes overlap outright, and where the producer supports it the check asks the producer's own conflict relation. Three tests cover the byte-equal overlap, the producer-reported conflict, and the honest split.

Paths: `lib/protocols/conformance/claimproducer/runner.go`, `lib/protocols/conformance/claimproducer/runner_splitscope_test.go`, `lib/foundation/locks/storetest/fake_conformance_test.go`.

**Bugs fixed.** The bundled verifiers failed the conformance kit's park and cancel probes: neither answered `probe_park` or `probe_cancel`. `lib/services/internal/stubprobe` now holds the one implementation, http-node delegates to it, and both verifiers call it in stub mode. All three executors now pass every applicable check. `TestInstallSecondSignalEscalator_HardExitsAndKillsChildren` sampled the killed child's liveness once and failed under load; it now waits on the child's death. The foundation's claim-producer fake returned sub-scopes with no scope data at all, which hands every clone the parent's whole scope; each partition now carries its own.

Paths: `lib/services/internal/stubprobe/stubprobe.go`, `lib/services/executors/verifier-http/executor.go`, `lib/services/executors/verifier-shape-checks/{server,validation}.go`, `cmd/rimsky/cli/compose/shutdown_test.go`, `lib/foundation/locks/storetest/fake_conformance_test.go`.

Tests run: `go build ./...` and `go vet ./...` across all four modules; `go test ./lib/... ./test/plumbline/ -count=1`; `cd lib/foundation && go test ./... -count=1`; `cd lib/protocols && go test ./... -count=1`; `cd lib/services && go test ./... -count=1`; `go test ./cmd/... -count=1`; `go test ./test/scenarios/ ./test/scenarios/lifecycle/ -count=1`; `go run ./tools/wallclock-lint`; `make lint`; `.ok-plumbline/bin/plumbline .`; and `rimsky conformance executor --endpoint <executor> --transport grpc` against http-node, verifier-http, and verifier-shape-checks, each 7 passed and 0 failed. The suites that boot images fail for want of `RIMSKY_IMAGE_TAG` and this run's images, which is the harness's stated precondition.


### Fix round — the reviewer's stage-6 and stage-7 findings

**S6-1 — the zero-config agent-facing leaf renews itself.** The proxy minted its local CA and a 24-hour leaf once at startup and pinned the leaf into `tls.Config.Certificates`, so every agent dial failed a day after the proxy started. `localLeafHolder` now holds the generated CA and the current leaf, and the listener serves through `tls.Config.GetCertificate`. Each handshake asks the holder for the leaf; the holder re-issues from the retained CA once the leaf passes `peerauth.RenewalDeadline`, the same two-thirds-of-life rule the mutual-TLS posture renews on. The published root does not change, so an agent that pinned it at startup keeps verifying every renewed leaf. `proxyServerCredentials` now takes a clock function rather than one timestamp, so the holder reads the time at every handshake.

Paths: `cmd/rimsky-host-agent-proxy/tls.go`, `cmd/rimsky-host-agent-proxy/main.go`, `cmd/rimsky-host-agent-proxy/tls_test.go`, `cmd/rimsky-host-agent-proxy/agent_hop_tls_test.go`.

Tests: `TestLocalLeafHolderReissuesTheLeafBeforeItExpires` drives a controlled clock and proves the holder serves one leaf inside its lifetime and a different one past the renewal deadline. `TestLocalLeafHolderRenewsUnderTheCARootItPublished` verifies a renewed leaf against the root the proxy published at startup, at the peer server name every rimsky peer dial uses.

**S6-2 — `CLAUDE.md` names the section the advertise host lives in.** The "Supervisor callback hostname" bullet said "Set `callback.advertise_host` in YAML". Stage 6 moved the key under the unified file's `supervisor:` section, and the decoder ignores an unknown top-level block in silence, so an operator following that sentence got no error and no advertise host. The bullet now names `cfg:supervisor.callback.advertise_host`, says the decoder ignores a top-level `callback:` block, and names `cfg:supervisor.callback.host` as the fallback. The "Shipped default ports" bullet now names `cfg:supervisor.callback.port`.

Paths: `CLAUDE.md`.

**S6-3 — publishing the CA root is a rename, not a truncate-and-write.** `publishLocalCARoot` called `os.WriteFile`, so a reader could pin a half-written PEM and a failed write left the superseded root standing. `replaceCARootFile` writes a temp file in the same directory, flushes it, sets its mode, and renames it over the target, so a reader sees the old root or the new one. On failure the publish removes whatever stands at the path. An agent that pins a superseded root fails every dial. An agent that finds no root reports the missing file.

Paths: `cmd/rimsky-host-agent-proxy/main.go`, `cmd/rimsky-host-agent-proxy/publish_ca_root_test.go`.

Tests: `TestPublishLocalCARootReplacesASupersededRootWholeAndLeavesNoTempFile` proves the published file holds the current root alone and the directory holds no temp residue. `TestPublishLocalCARootDropsTheSupersededRootWhenItCannotWriteTheCurrentOne` proves a failed publish leaves nothing at the path.

**S7-1 — the project's suite no longer reads a stub's retry cadence.** See D38. `async_callback_retry_backoff` stays in the kit. It leaves the project test's `Only` list. `TestARefusedAsyncCallbackIsRetriedUntilTheReceiverAccepts` proves the retry on events alone.

Paths: `lib/protocols/conformance/executor/scenarios/scenarios_test.go`, `lib/protocols/conformance/executor/scenarios/async_callback_retry_test.go`.

**S7-2 — the claude-agent schema is closed.** Its schema carried `"additionalProperties": true`, so template registration accepted any undeclared attribute key on a claude-agent node. It now reads `{"readOnly": true}`, which closes its inputs and leaves its author-defined outputs open; verifier-http, verifier-shape-checks, and http-node keep `false`, because they know their outputs and declare them. See F6. The executor reads seven top-level keys — `cwd_from_claim_producer`, `cwd`, `model`, `system_prompt`, `user_prompt`, `session_token`, and `cli` — and the schema declares exactly those; `cli.mcp_servers` and `cli.expose_env` are properties of the declared `cli` object, not attribute keys of their own, so closing the top level leaves them reachable. `schemaAdmitsUndeclaredKeys` is the new assertion in `TestBundledExecutorSchemasDeclareEveryAttributeTheyRead`: it fails any bundled schema that admits an undeclared key.

Paths: `lib/services/executors/claude-agent/expected_attributes_schema.json`, `lib/services/test/plumbline/expected_attributes_schema_test.go`.

**S7-3 — `rimsky run` refuses a template nothing will drive.** A template with no structural root gets no wake message. The run then found no running frame on its first quiescence poll, terminated the instance, reported success, and exited zero having executed nothing. Both paths of the verb now read `TemplateHasStructuralRoot` before they create the instance and refuse when it answers no, naming the template and what to do. `cli.NoStructuralRootError` is the one message both paths raise. The remote path still admits `--keep`, which hands the instance to the operator to drive; the self-host path already rejects `--keep`, so it refuses outright. See D41 for `rimsky compose run`.

Paths: `cmd/rimsky/cli/structural_root.go`, `cmd/rimsky/cli/run.go`, `cmd/rimsky/cli/compose/template_run.go`, `cmd/rimsky/cli/run_test.go`.

Tests: `TestRunRunRemote_RefusesATemplateNothingWillDrive` proves the run exits 2, names the missing structural root, and creates no instance. `TestRunRunRemote_KeepsARootlessTemplateForTheOperatorToDrive` proves `--keep` still creates the instance.

**S7-4 — a retention window bounds the lifecycle outbox.** See F5. `LifecycleOutboxTable.DeleteOlderThan` lands in both drivers, `runtime.SweepLifecycleOutbox` runs it inside a transaction, and the scheduler's tick calls it beside the lineage and message-idempotency sweeps. `retention.lifecycle_outbox_trailing` carries the window, defaults to zero, rejects a negative value, and issues no delete at zero.

Paths: `lib/foundation/persistence/lifecycle_outbox.go`, `lib/foundation/persistence/{postgres,sqlite}/lifecycle_outbox.go`, `lib/foundation/persistence/conformance/{lifecycle_outbox.go,conformance.go}`, `lib/runtime/sweep_lifecycle_outbox.go`, `lib/runtime/retention_sweeps.go`, `lib/runtime/scheduler/scheduler.go`, `lib/control/config/claim_producers.go`, `lib/control/config/retention_test.go`, `lib/runtime/sweep_lifecycle_outbox_test.go`.

Tests: the persistence conformance suite gained `LifecycleOutboxDropsRowsPastTheRetentionCutoff`, which proves both drivers delete the row staged before the cutoff and keep the one staged after it. `TestSweepLifecycleOutboxDeletesTheRowsOlderThanTheTrailingWindow` proves the cutoff arithmetic and that the sweep opens one transaction; `TestSweepLifecycleOutboxKeepsEveryRowWhenTheOperatorDisablesIt` proves a zero window issues no delete. The config tests cover the default, the explicit zero, and the negative refusal.

**Bug fixed — a YAML template could not declare a message body schema, a claim data blob, or a publisher config.** `MessageSchema.BodySchema`, `NodeClaimProducerRef.Data`, and `PublisherSpec.Config` were `json.RawMessage`, which the YAML decoder cannot fill from a YAML mapping. Every path that reads a template file — `rimsky template register`, `rimsky template lint`, `rimsky run`, `rimsky compose` — failed with `cannot unmarshal !!map into json.RawMessage`, The shipped demo fixture `test/fixtures/demos/cascade-send-demo-template.yaml` is one of the files that failed. I found the bug while writing the S7-3 test. `spec.RawJSON` replaces `json.RawMessage` on those three fields: it encodes and decodes as raw JSON on the JSON surface, exactly as before, and on the YAML surface it converts a mapping or a sequence to JSON and keeps a string as written. `node.RawJSON` aliases it for the graph layer.

Paths: `lib/foundation/spec/rawjson.go`, `lib/foundation/spec/rawjson_test.go`, `lib/foundation/spec/template.go`, `lib/foundation/spec/graphs.go`, `lib/graph/node/template.go`, `lib/graph/node/message_body_validate.go`, `lib/control/config/publisher_reconciler_test.go`, `lib/runtime/validation_pipeline_inertness_test.go`, `test/support/scenario/harness_util.go`, `test/scenarios/sensor/lifecycle_start_stop_test.go`, `test/scenarios/idempotent_mode_substitution_failure_routes_first_test.go`.

Tests: `TestTemplateSpecReadsAMessageBodySchemaWrittenAsYAML` loads the shape the demo fixture uses. `TestRawJSONKeepsAJSONStringAsWritten`, `TestRawJSONRoundTripsThroughYAMLAndJSON`, and `TestRawJSONOmitsAnAbsentValue` cover the two surfaces and the omitted value.

Tests run: `go build ./...` and `go vet ./...` across all four modules; `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`; `cd lib/foundation && go test ./... -count=1`; `cd lib/protocols && go test ./... -count=1`; `cd lib/services && go test ./test/plumbline/ ./executors/... ./internal/... -count=1`; `go test ./test/scenarios/lifecycle/ ./test/scenarios/sensor/ -count=1`; `go test ./test/scenarios/ -run 'IdempotentMode|RemoteOneShot|TemplateErrorPolicy' -count=1`; `make lint`; `go run ./tools/wallclock-lint`; and `.ok-plumbline/bin/plumbline .` — all green. `TestCtxDemo` and the services suites that boot images fail for want of `RIMSKY_IMAGE_TAG` and this run's images, which is the harness's stated precondition.


### Fix round — the reviewer's round-1 findings

**FX1-1 — F5 states what the sweep drops, and the sweep is off by default.** F5's reasoning claimed the reconciler re-derives template-scope and instance-created deliveries from the source of truth. D20 removed those walks: the tick is `drainStagedLifecycleDeliveries` over the outbox and `drainTerminatedInstances` over the terminated instances, so `instance_terminated` alone has a second source. `retention.lifecycle_outbox_trailing` now defaults to zero, and the rewritten F5 names what a positive window costs.

Paths: `lib/control/config/claim_producers.go`, `lib/control/config/retention_test.go`.

Tests: `TestRetentionDefaultsWhenAbsent` proves an unconfigured deployment sweeps nothing. `TestRetentionExplicitValuesHonored` proves an operator's `72h` reaches the sweep.

**FX1-2 — a closed schema admits a template-named output.** See F6 and D42. claude-agent declares `"additionalProperties": {"readOnly": true}`. `executorSchemaAllowsExtensions` reads that form as closed and the new `executorSchemaAllowsReadOnlyExtensions` reads it as outputs-open. `validateCompositionAgainstExecutor` admits an undeclared node property the template marks `readOnly: true`, `CheckEffectiveAttributesSchema` treats that property as an output, so it needs no `source:` and no `default:`, and it does not fail the rule that the executor is authoritative on its outputs. An undeclared writable property is still refused. The fitness check reads the new form as closed. `docs/examples/claude-agent-session-resume.md` marks `turn` and `recall` read-only in both its agent nodes. The round's `rootlessSpec` fixture declares `url`, an attribute http-node reads, rather than the undeclared `reason` a real deployment would refuse.

Paths: `lib/graph/node/template_validator_attrschema.go`, `lib/graph/node/template_validator_attrschema_test.go`, `lib/services/executors/claude-agent/expected_attributes_schema.json`, `lib/services/test/plumbline/expected_attributes_schema_test.go`, `lib/services/test/plumbline/claude_agent_template_outputs_test.go`, `docs/examples/claude-agent-session-resume.md`, `cmd/rimsky/cli/run_test.go`.

Tests: `TestSessionResumeTemplateRegistersWithItsAgentOutputsDeclared` registers the example's node against the real claude-agent schema. `TestClaudeAgentTemplateStillRefusesAMisspeltInput` and `TestClaudeAgentTemplateRefusesAnUndeclaredWritableProperty` prove the misspelling check survives. `TestValidateAttributesSchema_ReadOnlyExtensionsAdmitATemplateNamedOutput` drives the three property shapes at the graph layer, `TestExecutorSchemaAllowsExtensions_ReadOnlyExtensionsDoNotOpenTheInputBag` proves the two readings differ.

**FX1-3 — the proxy keeps the CA it published.** `newLocalLeafHolder` generated a CA per process, so a proxy restart published a new root and every running agent's dials failed until the agent restarted. The holder now writes the CA certificate to `RIMSKY_PROXY_LOCAL_CA_FILE` and its key to that path plus `.key` at mode 0600, and loads both on the next start. It mints a CA only when it finds no kept one. It warns and mints a fresh CA when a kept one does not load. With no path named it mints a CA per process and logs that every agent must re-pin. `replaceFileAtomically` carries the temp-file-and-rename write S6-3 introduced, and now writes the root and the key alike. `publishLocalCARoot` keeps the log line, and the holder owns both files.

Paths: `cmd/rimsky-host-agent-proxy/tls.go`, `cmd/rimsky-host-agent-proxy/main.go`, `cmd/rimsky-host-agent-proxy/config.go`, `cmd/rimsky-host-agent-proxy/publish_ca_root_test.go`, `cmd/rimsky-host-agent-proxy/tls_test.go`, `CLAUDE.md`.

Tests: `TestLocalLeafHolderReusesTheCAItPublishedOnAnEarlierRun` builds a second holder over the same path and verifies its leaf against the root the first holder published. It also proves the published root does not change. `TestLocalLeafHolderKeepsItsCAKeyPrivate` proves the two file modes. `TestLocalLeafHolderMintsAFreshCAWhenTheKeptOneDoesNotLoad` proves the proxy replaces a corrupt key. `TestPersistLocalCADropsBothFilesWhenItCannotWriteTheRoot` proves a failed persist fails the startup and leaves neither file behind.

**FX1-4 — `spec.RawJSON` keeps a YAML scalar's type.** The string branch stored any scalar verbatim, so `data: prod` became invalid JSON and the quoted strings `"123"` and `"true"` became a JSON number and a JSON boolean — a type change on bytes rimsky passes through uninspected. The branch now keeps the text verbatim only when it opens with `{` or `[`, and refuses that text when it does not parse as JSON, naming the line. Every other scalar goes through `json.Marshal`, so a quoted string stays a JSON string and a bare number stays a JSON number.

Paths: `lib/foundation/spec/rawjson.go`, `lib/foundation/spec/rawjson_test.go`.

Tests: `TestRawJSONEncodesABareScalarAsJSON` proves `prod` lands as `"prod"`. `TestRawJSONKeepsAQuotedScalarsType` covers the quoted and the bare form of a number and of a boolean. `TestRawJSONRefusesAStringThatOpensAJSONDocumentAndDoesNotParse` proves the refusal names the failure.

Tests run: `go build ./...` and `go vet ./...` across all four modules; `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`; `cd lib/foundation && go test ./... -count=1`; `cd lib/protocols && go test ./... -count=1`; `cd lib/services && go test ./test/plumbline/ ./executors/... ./internal/... -count=1`; `go test ./test/scenarios/lifecycle/ ./test/scenarios/sensor/ -count=1`; `go test ./test/scenarios/ -run 'IdempotentMode|RemoteOneShot|TemplateErrorPolicy' -count=1`; `make lint`; `go run ./tools/wallclock-lint`; and `.ok-plumbline/bin/plumbline .` — all green apart from the image-booting suites, which want `RIMSKY_IMAGE_TAG`.


### Fix round — the reviewer's round-2 finding

**FX2-1 — `pki.LoadCA` proves the key belongs to the certificate.** `LoadCA` parsed a certificate and a key and returned them together without comparing them, so a key that does not belong to the certificate loaded without an error. The proxy reaches that state: `persistLocalCA` writes the key and then the root, so a process killed between the two writes leaves a fresh key beside the previous root, and the next start logged the reuse line and issued leaves no pinned agent could verify. A root restored from a backup without its key reaches the same state, and `lib/control/config/peer_auth.go` loads the deployment CA through the same function.

`LoadCA` now compares the parsed key's public half to the certificate's and refuses a mismatch, naming the certificate. It also refuses a certificate that is not a CA and one that has expired, because neither signs a leaf anything accepts. It now takes the time it judges expiry against, as `GenerateCA` and `IssueLeaf` already do; `ensureDeploymentCA` passes its clock and the proxy passes its own. `loadPersistedLocalCA` already treats a load failure as mint-fresh-and-republish, so the proxy recovers on the next start, and the deployment CA fails at startup with a message that names the cause.

No write order avoids the crash window. Writing the root first leaves the previous key beside the new root instead. The check at load is what makes the state recoverable.

Paths: `lib/foundation/pki/ca.go`, `lib/foundation/pki/ca_test.go`, `lib/control/config/peer_auth.go`, `cmd/rimsky-host-agent-proxy/tls.go`, `cmd/rimsky-host-agent-proxy/publish_ca_root_test.go`.

Tests: `TestLoadCARefusesAKeyThatDoesNotBelongToTheCertificate` loads one CA's certificate with another CA's key and proves the error names the mismatch. `TestLoadCARefusesAnExpiredCertificate` and `TestLoadCARefusesALeafCertificate` cover the other two refusals. The leaf test passes the leaf certificate with its own key, so only the CA check can refuse it, and it proves the error names that condition. `TestLocalLeafHolderMintsAFreshCAWhenTheKeptPairDoesNotMatch` leaves a stranger's key beside the published root, then proves the proxy republishes a different root and serves a leaf that verifies against it.

Tests run: `go build ./...` and `go vet ./...` across all four modules; `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`; `cd lib/foundation && go test ./... -count=1`; `cd lib/protocols && go test ./... -count=1`; `cd lib/services && go test ./test/plumbline/ ./executors/... ./internal/... -count=1`; `go test ./test/scenarios/ -run 'PeerAuth|Mtls|MTLS|HostAgent|RemoteOneShot' -count=1`; `make lint`; `go run ./tools/wallclock-lint`; and `.ok-plumbline/bin/plumbline .` — all green apart from `TestCtxDemo`, which wants `RIMSKY_IMAGE_TAG`.


### Fix round — the reviewer's round-3 finding

**FX3-1 — `TestLoadCARefusesALeafCertificate` never reached the branch it named.** The test passed the leaf certificate with a nil key. `LoadCA` failed at the key parse and returned before the `IsCA` check, so the test proved nothing about a leaf certificate. The test now passes the leaf's own key. The key parses, the key-belongs-to-certificate check passes, and only the CA check can refuse the certificate. The test asserts the error names "not a CA". The builder mutation-checked the branch: removing the `IsCA` refusal fails the test.

Paths: `lib/foundation/pki/ca_test.go`.

Tests: `TestLoadCARefusesALeafCertificate` proves `LoadCA` refuses a leaf certificate and names the condition.

Tests run: `go build ./...` and `go vet ./...` across all four modules; `go test ./lib/... ./cmd/... ./test/plumbline/ -count=1`; `cd lib/foundation && go test ./... -count=1`; `cd lib/protocols && go test ./... -count=1`; `cd lib/services && go test ./test/plumbline/ ./executors/... ./internal/... -count=1`; `make lint`; `go run ./tools/wallclock-lint`; and `.ok-plumbline/bin/plumbline .` — all green apart from `TestCtxDemo`, which wants `RIMSKY_IMAGE_TAG`.

### Fix round 4 — the reviewer's ledger came back empty

The owner chose the fourth round at the review cycle cap. The standing reviewer read the round-3 fix and closed FX3-1 against the tree. It raised no new finding, so its ledger is empty. The claimed forks F1-F6 stand in `## Divergences` for the gate. The build is code complete at four fix-only rounds.

## Divergences

D1 — Release notes under `releases/` keep their numbered-invariant references.

`releases/v0.7.0.md` cites `invariant:identity-bound-dry-run-mode-floor`, and four release notes use the word in prose. Each note states what shipped at one past version, so rewriting it falsifies the record. The sweep's excluded set names the archive directories rather than these files, so the call is mine: I left them as written and treat them as records, like `.ok-planner/history/`.

D2 — Discovery scaffolding under `.ok-planner/design/_discover/` keeps its numbered-invariant references.

Roughly thirty `_discover/` entries cite "Blessed invariants §3" and similar numbers from a catalog that no longer exists. The authoring rules exempt `_discover/` as point-in-time scaffolding, and rewriting thirty bootstrap notes goes beyond this work item. I left them as written.

D3 — Repaired `decision:blessed-invariant-annotations`, which the sprint carries no delta for.

Its Choice said safety properties are documented in the concept doc that owns them. The concept deltas remove exactly that home, so the decision named a place that no longer exists. I rewrote the body to say a property is recorded in the decision or story that owns it, proven by a test, and cited from code by the ordinary annotation, and added the rejected alternative of keeping the properties in the concept catalog. The commitment — no dedicated tag, no numbered catalog — is unchanged.

D4 — The TOC regeneration kept the authored summaries of untouched artifacts.

A TOC row is an authored one-line summary, not a mechanical slice of the body: only 65 of 237 decision rows matched the first sentence of their Choice, and every story row is a restatement of the body's title. Regenerating all 389 rows from a formula would replace working summaries with worse ones for artifacts this sprint does not touch. I regenerated every concept row, because every concept body changed, and the 42 touched decision rows and the one new story row, and left the rest.

F1 — The delete route drops an instance-terminated delivery the poll loop has not yet made. PROMOTED: `.ok-planner/issues/2026-08-21-222949-instance-delete-drops-undelivered-lifecycle-events.md`.

The build read the delivery window as bounded by the poll interval. There is no window: nothing stages the instance-terminated event ahead of the reconciler's tick, so the instance's lifecycle ledger rows are the only record that the delivery is owed, and the delete removes them. A subscriber hears nothing at all. The one-shot run verb, told not to keep its instance, terminates and deletes in consecutive calls while the reconciler polls every two seconds, so a deployment loses the event on a shipped path, not in a rare race.

The fork is genuine. Every remaining option decides product intent that the sprint and the corpus leave open: whether a subscriber hears about an instance the control plane has already removed, whether an unreachable subscriber may hold an instance in place against a delete, or whether a delete ends what rimsky owes that instance's subscribers. Closing it also builds mechanism no sprint authorized — staging the event inside the terminate transaction, or a new refusal on the delete route. Reading (a) stands as the tree's answer until the owner rules.

D5 — Lifecycle and producer-verb delivery failures are logged, not written to the event log. PROMOTED: `.ok-planner/issues/2026-08-21-225241-event-log-domain-for-peer-delivery-health.md`.

The events standard is met at both sites: each now emits a kind and fields on the structured log, per D43 and D44. Which operator surface carries a stalled peer delivery is a separate question, and the corpus leaves it open. `concept:event-log` names the control API a writer and admits an entry belonging to no instance, so nothing bars a kind here; its Purpose says an operator asks the ledger rather than reading process output, and process output is today the only place a stalled lifecycle delivery appears. Against that, the reconciler retries every two seconds and the trace-retention window is off by default, so a per-attempt kind writes tens of thousands of rows a day for one unreachable subscriber. Reasonable owners diverge over which surface owes the condition, so the fork is genuine. The sprint authorizes a new kind where an observable transition has none, and a caught error on a retry loop is not a transition, so this sprint authorizes no kind here either way. Reading (b) stands as the tree's answer until the owner rules.

D6 — The access-denied audit-row gap test already existed.

The sprint lists a gap test for an access-denied row's mode field being empty before mode evaluation and populated after. `TestGate_IdentityDenialRowCarriesNoMode` and `TestGate_PermissionDeniedRowCarriesRequestedMode` in `lib/control/controlapi/permission_denied_mode_test.go` already prove both halves and pass. I added no duplicate; I annotated both with `@concept: event-log` so the link to the corpus is explicit.

D7 — `state_transition` is emitted beside each state write, not from one funnel.

The node-run state column has seventeen product write sites and the persistence interface offers no read of a run's current state, node, and instance together. Rather than add one to the interface and implement it in both drivers, each site calls `AppendStateTransitionEvent` with the prior state it already knows. Every product transition emits exactly one row; the persistence conformance suites, which write states directly, emit none.

D8 — `concept:claim-producer` lists no alias, and the live second name is `store`, not the dropped `claim-store`.

The sidecar's body drops the `aliases: claim-store` entry the old file carried. `store` is a live second name for the claim producer: `concept:service-address-book` calls the routing entry "each claim-producer store name", `concept:supervisor` resolves "a run's executor and store names" at dispatch, and roughly 2695 `store` identifiers under `lib/` and `cmd/` name the same thing. `claim-store` appears nowhere live. Two candidates settle it: restore `claim-store`, or list `store`. I judge `store` right — an alias records a name readers meet, and `store` is the name the corpus and the code use. I did not edit the file: contract item 1 compares each concept to the sidecar verbatim, so the alias belongs in a delta, not in this build. The file stays as the sidecar wrote it.

D9 — The `docs/` release documents still describe `params_redact`.

`docs/templates.md`, `docs/http-api.md`, `docs/capabilities.md`, and `docs/concepts.md` document the deleted field. Each opens with a `/document` provenance stamp naming the release it describes, and the planner's rules make such a document a record: it goes stale, it files nothing, and the next `/document` run revises it. I left all four as written.

D10 — The value-free rendering covers `enum` as well as the three families the work item names.

The item names the numeric, format, and const constraints. In this library the `const` message formats the schema's declared value, not the instance's, and the `enum` message is built the same way from the schema's permitted values. Treating const as in scope and enum as out would cover half of one shape, so I covered both. No other v5 keyword formats a value: the rest carry property names, lengths, counts, indices, and type names.

D11 — Message-body validation gained the same rendering.

A message payload is structurally inert on the same terms as an attribute value, and `ValidateMessageBody` compiled its own schema and handed the library's error straight into `MessageBodySchemaViolation`. That is the same leak the work item names, at a second site, so I routed it through the same function rather than leave a known leak standing.

D12 — Fixed a Stage 2 regression that left the `lib/foundation` module uncompilable.

Stage 2 typed the five `auth.Event*` values as `events.Kind`; `lib/foundation/auth/audit_test.go` still used them as strings. `go build ./...` from the repo root covers the root module alone, so the break survived. I removed one test from that file, `TestAuditEventKindsMatchTypedOperationalKinds`, rather than repair it: the values are now defined as the kinds it compared them to, and `lib/foundation/events/kinds_test.go` already proves the wire round-trip over every operational kind. The file's other test, `TestKeyRevokedReasonEnumClosed`, stands untouched and still proves the `key_revoked` reason enum holds exactly `"manual"` and `"rotation_grace"` and that `"expired"` is not a member.

D13 — Fixed a racing verdict in `TestTemplateErrorPolicy/release_and_requeue_abandons_claim_and_re_acquires`.

The test waited on the worker node reaching `fresh` and then asserted the producer had recorded one Commit. The settling transaction commits before the Commit verb dispatches, so the assertion could run before the call arrived; it failed once in a full-package run and passed alone. The test now waits on the Commit reaching the stub before it reads the call list.

D14 — The lifecycle reconciler drains from the source of truth, not from the ledger alone.

The finding offers a drain "over un-advanced lifecycle rows". A first delivery that fails writes no row at all, so a ledger-only scan cannot see it. That is the compounding case the finding names: a failed instance-created leaves no instance-scope row. The reconciler therefore reads templates and live instances and asks the existing fan-out to reconcile each against the ledger, and reads the ledger only for the one case the source of truth cannot show: a template-scope row whose template is gone.

D15 — The terminator was renamed rather than joined by a second loop.

The finding offers a separate periodic reconciler or a widened terminator. I widened it: two loops would each need their own poll interval, failure cadence, and shutdown, and the second pass reuses the first's fan-out helpers. `InstanceTerminator` no longer names what the type does, so it is `LifecycleReconciler`, and its tests moved with it.

D16 — `TestLifecycleReconciler_RowFoundRPCSucceedsRowDeleted` counts its two verbs instead of every call.

The test asserted the fixture's peer saw exactly two calls in a tick. The fixture seeds a deployed template with no template-scope ledger row, so the new drain correctly delivers `OnTemplateDeployed` as a third call. The assertion now counts the two verbs the test is about, one each.

D17 — `TestEventLog_SettlingRunWritesStateTransitionAndAttributesCommitted` reads its rows by reason, not by position.

The test read the last row the event list returned and expected the settling transition. The list returns rows newest first, so the last row is the oldest, and the gate transition S2-2 adds is now the oldest row for that node. Position was never the property the test meant. It now indexes the node's transition rows by reason and asserts both: the gate clearing writes pending to stale, and the settling terminal writes running to fresh.

D18 — The postgres column staging a promotion's lineage record is `BYTEA`, not `JSONB`.

`JSONB` normalizes key order and whitespace, so the staged bytes come back reordered and the two drivers disagree on what they stored. The record is staged, not queried: nothing reads inside it between the settlement and the commit response, and the commit path decodes it into `ClaimTerminalRecord`. `BYTEA` beside the embedded driver's `BLOB` gives both drivers one byte-exact contract, which the conformance suite now asserts.

D19 — A failed deferred commit result now fails the outbox delivery instead of logging.

`applyDeferredCommitResult` logged and swallowed every error, including a failed `SetVersionID`. The lineage record now travels the same path, so swallowing an error loses the record: the dispatcher deletes the outbox row and nothing writes it. The function now returns the error, so the row stays and the next attempt re-issues `Commit`. The outbox already retries `Commit` on a transport failure, so producers already owe idempotent commits.

The same error now also propagates through the claim-scope barrier, so a failed lineage write fails an `Open` on a claim scope that conflicts with the undelivered terminal's. The barrier skips every non-conflicting row, so an unrelated claim opens as before. I accepted the coupling rather than making the barrier tolerant of a lineage-only failure. The barrier exists to guarantee that a conflicting terminal is fully delivered before a new `Open` on that scope, and a promotion whose ledger row is missing is not fully delivered. Tolerance has no coherent shape either: a tolerant barrier that still deletes the outbox row loses the record for good, and one that does not delete has not cleared. The barrier delivers inside the caller's transaction, so the failed `Open` rolls back with the record and the next attempt re-issues both.

F2 — The sub-graph caller keeps its lineage records although it invokes no executor. OVERTURNED: reading (a) stands, and the tree already holds it.

`decision:lineage-records-computation-only` settles this against itself. Its Choice says a leaf-run record's terminal kind is one of a closed family of four, and `subgraph_call` is one of the four; under readings (b) and (c) that kind is never written and the family has three live members. Its Rationale limits records to runs that computed something, and a sub-graph caller's delegation computed something; the caller's records connect the graph across the call. Its rejected alternative describes the excluded case as "a node that computed nothing", which a caller is not. `concept:lineage-record` then says outright that a caller produces two records for one dispatch. No reasonable owner reads the three named settlements as reaching the caller.

I made no edit. Widening the decision's first sentence to name delegation is option (c), which changes what the decision claims, so it is not mine to write. Whether that sentence should say so is a text question for the audit, not an intent fork.

D20 — Each transition stages its lifecycle deliveries, rather than a reconciler reading them back from the source of truth.

FR-1 offers three shapes: a slower cadence for the reconcile pass, a targeted query, or a cursor carried across ticks. Only a targeted query makes the steady-state cost proportional to undelivered work, and a targeted query over the existing ledger cannot see the case D14 named: a first delivery that fails writes no row, so nothing marks it owed. I therefore made the ledger record the intent before the delivery. Each transition stages one outbox row per subscriber inside its own transaction, and the fan-out after the commit deletes each row as its subscriber answers. A row that remains names a delivery rimsky still owes, so the query is exact. Staging inside the transition also closes the window between the commit and the delivery: a process that dies there leaves the row behind for the reconciler. `decision:lifecycle-subscriber-at-least-once-delivery` describes the ledger as keyed by service, event type, and object, which the outbox's key is and the idempotency ledger's key is not; the idempotency ledger keeps its role as the record of the state each peer last heard.

D21 — The delete route purges an instance's staged deliveries along with its ledger rows.

The build read F1 as accepting a delivery window bounded by the poll interval, and treated the staged rows as the same work queue under a new name, so the delete purges them on the same terms. Leaving them behind would deliver events for an instance the operator has removed. F1 is now promoted to the intake, and the purge stands as the tree's answer until the owner rules on it.

D22 — The signal completeness check leaves the held transition unconstrained.

`concept:signal` names three kinds — terminal, transient, attribute-change — and its transient leaves are a retry, a caught error mid-retry, a release and requeue, a wait on an asynchronous callback, and a park. A transition into `held` is none of them. The code agrees only in part. The two held-claim sites name a settling signal, because it carries the deferred cascade to the holding subgraph's members. The fan-out dispatch site names none, because the parent's cascade fires at auto-terminal instead. Both are right for what they do, so the check asserts nothing about `held` and says so. It constrains `fresh`, `failed`, and `parked`, which must name a signal, and `stale` and `running`, which must not.

D23 — The sequenced ordering rule holds a round at the gate rather than reordering the dispatch queue.

The rule could live in the dispatcher, which would pick the oldest claimable round of a sequenced receiver. I put it in the gate evaluator, where the other three modes live: the gate is where `concept:cascade-mode` says the rule applies, and a round held there stays pending, so the dispatcher never sees it. The re-evaluation costs nothing new, because `drainWaitSetOnSettled` already sweeps a settled run's pending siblings in ascending sequence order.

F3 — Rimsky still delivers a superseded lifecycle event, rather than converging the subscriber on the template's current state. OVERTURNED: reading (a) stands, and the tree already holds it.

The corpus is not silent here. `decision:lifecycle-subscriber-at-least-once-delivery` says rimsky delivers each lifecycle event at least once, and `concept:lifecycle-subscriber` repeats it. Convergence delivers the missed registration zero times, which is the at-most-once alternative the decision rejects, so reading (b) needs a delta amending the decision before anyone may build it. A subscriber that provisions substrate at registration and configures it at deploy needs the registration whatever the template's current state is. Delivery keeps its order per subscriber and scope, so the subscriber sees the sequence rather than one stale event.

D24 — The positive lineage gate also excludes a run whose executor never answered.

S4-1 replaces `AcquirePhase` with `ExecutorInvoked`, set where the code calls the executor. That flips three more settlements to writing no leaf-run record: an unresolved executor, a failed dial, and a failed request build. Under the old negative gate each wrote one, because the node named an executor. `decision:lineage-records-computation-only` says a record is written at the terminal of a run that invoked an executor, and none of these three invoked one, so the positive gate reads the decision as written. A reader who wants those settlements has the audit log, which the decision names as their home.

D25 — The proxy publishes its locally generated CA root through the log, and through a file when the operator names a path.

`decision:host-agent-proxy-tls` says the proxy publishes the root for the agent to pin and does not say through what. A gRPC surface cannot carry it: the agent needs the root before the handshake, so fetching it over the same channel would be trust on first use, which is what pinning exists to avoid. The proxy therefore logs the PEM at startup — a public certificate, safe in a log, and reachable through `docker logs` for a containerized zero-config run — and writes it to `RIMSKY_PROXY_LOCAL_CA_FILE` when that variable names a path. A failed write warns and points at the log line rather than failing the boot, so the root is published either way.

D26 — The agent verifies against the system roots when no root is pinned.

The work item says the agent's dial defaults to TLS with CA pinning. A pinned root is a path the user supplies, so I had to settle what an unset path means. Refusing to start would break a deployment whose proxy carries a publicly trusted certificate, which is a legitimate posture and not one this sprint retires. The agent therefore dials TLS either way: with `RIMSKY_AGENT_TLS_CA` it verifies the pinned root against the fixed peer server name, and without it against the system roots and the host it dialed. A root the agent cannot read fails closed rather than falling back.

D27 — The switch is one variable both binaries read, and the old TLS opt-in is deleted.

The work item says plaintext lives behind an explicit switch set on both ends. One name serves both ends better than a pair that can disagree, so the proxy and the agent both read `RIMSKY_HOST_AGENT_INSECURE`, and `rimsky agent start` takes `--insecure`. `RIMSKY_AGENT_TLS` and `--tls` are gone rather than inverted: with TLS the default the opt-in has no meaning, and rimsky is pre-v1, so I deleted them instead of carrying a compatibility shim.

D28 — The `docs/` release documents still describe the second configuration file and the old TLS opt-in.

`docs/config.md`, `docs/operating.md`, `docs/images.md`, `docs/env-vars.md`, `docs/concepts.md`, and `docs/cookbook/journey-split-roles-postgres.md` document `RIMSKY_SUPERVISOR_CONFIG`, the baked `supervisor-config.yml`, and `RIMSKY_AGENT_TLS`. Each opens with a `/document` provenance stamp naming the release it describes, and the planner's rules make such a document a record: it goes stale, it files nothing, and the next `/document` run revises it. I left them as written, on the same reading as D9.

D29 — The proxy always runs its peer-facing listener, so the peer protocols leave the agent-facing port.

Serving the agent-facing listener with TLS broke every peer that dialed it in plaintext. The proxy used to register the peer-service protocols on the agent-facing server whenever peer-auth was off, and start the peer-facing listener only under mutual TLS, so with peer-auth off a plaintext peer and the agent shared one port. Making that port TLS made the plaintext peer unreachable, which the host-agent scenarios caught: the node stayed stale because the dispatch never reached the proxy. The options were to give the peer a way onto the TLS port, which peer-auth-off has no credential for, or to give the two hops the two listeners the concept already describes. I took the second. The proxy now always builds both servers and starts both listeners: the agent-facing one carries the host-agent protocol alone, and the peer-facing one at `RIMSKY_PROXY_PEER_GRPC_PORT` carries the peer-service protocols, with mutual TLS when the deployment enrolls and plaintext when it does not. A deployment that pointed its executor entry at the agent-facing port moves it to the peer-facing one. That is a configuration break, which pre-v1 admits, and it is what `concept:host-agent-proxy` already says the proxy is: one side facing the deployment, one facing the developer's machine.

I updated the sites that dialed the old shared port: the scenario fixture allocates both ports and points the executor entry at the peer-facing one, the late-bind rejection and per-run-scope reap tests dial the peer-facing listener, and the register probes dial the agent-facing listener over the published root. The demo script starts the proxy with both ports and a published CA root and passes `--tls-ca` to `rimsky agent start`.

D30 — The lifecycle outbox became append-only rather than taking a fresh position on re-stage, and a retention window now bounds it.

S5-1 offers two fixes: give the upsert a fresh ordering position on every stage, or make the table append-only. A fresh position keeps one row per event and coalesces a repeated event, so a subscriber that missed the first deploy never hears it. Append-only keeps each staging as its own row, which is what `decision:lifecycle-subscriber-at-least-once-delivery` promises and what F3 already built for the failed-predecessor case. A dead subscriber's backlog therefore grows with operator actions rather than staying bounded by the event vocabulary. S5-4's per-stream drain keeps that backlog from starving another stream, and the retention sweep F5 records lets an operator bound its size: the scheduler drops an outbox row older than `retention.lifecycle_outbox_trailing`, which is off unless the operator names a window.

D31 — The sequenced probe takes the receiver run alone and derives both sides in SQL.

The probe could take the receiver's node, run scope, sequence, and sender node ids as parameters, leaving the caller to resolve the senders. I gave it the receiver run id alone: one query joins the receiver's wait-set rows to their sender runs and looks for an earlier queued round of the same receiver whose sender run shares a sender node. The caller cannot pass a mismatched set, and the sender resolution lives beside the comparison that uses it.

D32 — A receiver with several senders waits on any of them.

`concept:cascade-mode` speaks of "one sender". A receiver run's wait set may name several — a subscription edge per sender, plus the upstream runs a force-refresh pulled in. I read the rule as covering each sender independently: a round holds while an older queued round shares at least one sender node with it. A round whose senders are disjoint from every older round's senders clears.

F4 — The expected-attributes contract covers what the executor reads, and http-node's open attribute passthrough is gone. OVERTURNED on all three questions: the built readings stand, and the tree already holds them.

The equality covers reads. `concept:executor` says registration rejects a node that sets an attribute the schema does not declare, and a read-only property sets nothing; it names an output the template reads back. Under the alternative, no template could ever reference an executor's output, which retires a shipped capability to enforce a sentence about inputs. No reasonable owner takes that.

The stub-mode signature is exempt both directions. Declaring seven conformance probe attributes in a shipped schema offers a template author keys that mean nothing outside a conformance run, and `_invalid` is read by presence, so a declared default would make every dispatch a malformed-shape probe. An executor must still be free to declare a probe attribute, because the harness's stub templates set one and registration would otherwise refuse the node. Both directions follow from the mechanism.

http-node's passthrough goes. `decision:expected-attributes-schema-closed` rejects an open attribute bag for a bundled executor by name in its alternatives, so keeping the passthrough needs a delta retiring that clause. Moving a template's fields under `body` is a configuration break, which the project's pre-v1 rule admits.

D33 — Registration reads an executor schema as closed unless it declares `additionalProperties: true`.

The undeclared-key check demanded an explicit `additionalProperties: false`, which no bundled executor set, so the check never fired. The read-only rule beside it already read the opposite default through `executorSchemaAllowsExtensions`, which returns false when the key is absent. I made the two agree on the decision's polarity: absent means closed. A schema that enumerates no properties at all stays permissive, which is what the scenario harness and the compose stub advertise.

D34 — Each optional attribute in the three schemas carries the default its code applies.

`CheckEffectiveAttributesSchema` refuses a property with no `source:`, no `default:`, and no read-only mark. Every property an executor schema declares reaches that check, so declaring `timeout_ms` without a default would fail every template that leaves it out. Each optional key therefore carries the default the executor already applies — `method: "GET"`, `class_field: "class"`, `timeout_ms: 60000` — and `url` on http-node carries an empty default, because a stub-mode dispatch never reads it and the live path already answers a missing url with `http/attribute_invalid`.

D35 — Two CLI tests that keyed on the terminal stamp were retargeted rather than deleted.

`TestRunRun_NoKeep` spawned a goroutine that stamped the fake's first live instance terminated, because nothing else would. The run now terminates its own instance, so the goroutine raced it and hung; the test asserts the exit code alone. `oneShotServer` keyed its behaviour on a `terminated` flag; it now models running frames and pending messages, and fails a run that reaches a quiescent instance without terminating it.

D36 — The compose planner fails when the deployment returns no canonical hash.

`decision:template-identity-deployment-canonical` says a client does not compute the hash. A fallback to the local hash would put the client back in the business of computing one whenever validation failed. The planner therefore returns an error naming the template and the deployment's validation errors, so `compose plan` reports an invalid template at plan time rather than at the register step.

D37 — The split-scope disjointness check uses the producer's own conflict relation where it has one.

Overlap has no general definition over opaque scope bytes. Byte-equal sub-scopes overlap outright, and a producer that supports `ScopesConflict` has already told rimsky what its scopes mean, so the check asks it. A producer without that verb is checked on byte equality alone, which catches the shape the foundation's own fake had: a split that returns no scope data at all, handing every clone the parent's whole scope.

D38 — The async-callback backoff check reads elapsed time, and the project's own suite no longer runs it.

`concept:executor` owes a retried callback a widening cadence, and cadence is measured in time. The kit keeps the `async_callback_retry_backoff` scenario, because a black-box executor's cadence has no other instrument: the check compares the gaps between the attempts the receiver recorded, no gap may be shorter than the one before it, and the last must be at least twice the first. A conformance run against a third-party executor is where that measurement belongs.

The project's own suite is a different matter. Stage 7 put the scenario in `TestUnaryProtocolScenarios_RunAgainstALiveExecutor`'s `Only` list, where its verdict compared the arrival gaps of an in-tree stub that sleeps 10ms, 30ms, and 90ms. A scheduling stall on a loaded machine inverts that ordering and reddens the run, which the testing standard forbids: a test's verdict never depends on elapsed time. The scenario is out of the `Only` list. `TestARefusedAsyncCallbackIsRetriedUntilTheReceiverAccepts` replaces it and reaches its verdict on events alone — it drains the receiver's refusal channel until the receiver has refused three attempts, lets the receiver accept, waits on the delivery, and asserts the attempt count. `checkRetryBackoff` keeps its own test over synthetic timestamps, so the cadence rule stays proven as a function of its fixture.

D39 — The bundled verifiers gained the park and cancel stub probes.

Running the conformance battery against verifier-http and verifier-shape-checks, as this stage's brief directs, showed each failing `cancel`, `park_emission`, and `scratch_park_round_trip`: neither answered `probe_park` or `probe_cancel`. Rimsky ships those executors and ships the battery, so the failure is a defect rather than a scope question. http-node already held both implementations, so I moved them to `lib/services/internal/stubprobe` and called it from all three rather than copy them twice.

D40 — `expect_status: []` no longer means "match nothing" in http-node.

The schema's default for `expect_status` is the empty list, which reads as "the executor decides". The code accepted any list, so an empty one replaced the 2xx default and failed every status. It now falls through to the default, which is the only reading under which the schema's own default works.

D41 — `rimsky compose run` keeps the wake gate it has; only the `rimsky run` verb refuses a rootless template.

S7-3 asks for the same reasoning on "the compose path that shares the gate". Three call sites read `TemplateHasStructuralRoot`. Two of them are the one-shot `rimsky run` verb — the remote path in `cmd/rimsky/cli/run.go` and the self-host path in `cmd/rimsky/cli/compose/template_run.go` — and both now refuse a template that declares no structural root, before they create an instance. The verb creates one instance from one template and waits for it to reach terminal, and a rootless template gets no wake message, so the verb cannot deliver what it promises.

The third site is `rimsky compose run`, which applies a manifest of many instances. A manifest may legitimately carry an instance a sensor or another instance's message drives, and that instance's template declares no structural root. Refusing the whole run would break that manifest, and skipping the instance in the terminal wait would change what the verb reports. I left that site as it stands. An operator who puts a rootless template in a manifest and expects `compose run` to drive it still gets an immediate success for that instance.

F5 — The lifecycle outbox takes a retention window, and the window is off until an operator names one. PROMOTED: `.ok-planner/issues/2026-08-21-222949-lifecycle-outbox-retention-narrows-at-least-once.md`.

The fork is genuine. `decision:lifecycle-subscriber-at-least-once-delivery` promises each event at least once without condition, and the window makes that promise conditional on an operator's configuration, so shipping the key changes what the corpus commits to. The sprint carries no work item for a retention key at all; the outbox itself arrived in this build, so the policy for a subscriber that never comes back is the owner's first ruling on it, not a fix in this round. Reasonable owners diverge over what to do when a subscriber stops answering: bound the table and accept the loss, leave it unbounded and surface the dead subscriber instead, or restore the re-derivation this build removed so a bound costs no delivery. The built reading stands as the tree's answer until the owner rules; at its zero default the deployment behaves exactly as the decision says.

D42 — The session-resume registration test carries the example's template shape rather than parsing the example file.

FX1-2 asks for a test that the session-resume example registers. `docs/examples/claude-agent-session-resume.md` opens with a `/document` provenance stamp, so it is a release snapshot the next `/document` run rewrites whole, and the sprint's step 5 says no test checks the existence of static text. A test that reads that file's fenced YAML would bind the ordinary suite to an artifact another ceremony regenerates. `TestSessionResumeTemplateRegistersWithItsAgentOutputsDeclared` therefore builds the example's node in Go and registers it. I repaired the example in the same change, so the file and the test agree today.

F6 — A closed executor schema admits a property the template marks read-only when the executor leaves its outputs open. OVERTURNED: reading (c) stands, and the tree already holds it.

The sentence that settles F4 settles this one: `concept:executor` says registration rejects a node that sets an attribute the schema does not declare, and a read-only property sets nothing. The decision's stated purpose is catching a misspelled input, and reading (c) keeps it whole — every input stays declared, and an undeclared writable property is still refused. claude-agent resolves its writeback bag from an argument the agent supplies, so it cannot enumerate its outputs; the template author enumerates them instead, as read-only properties in the node's attribute block. Reading (a) is the open bag the decision's alternatives reject, and reading (b) retires a shipped example and the cascade pattern it demonstrates to enforce a sentence about inputs. No reasonable owner takes either.

D43 — The events standard's emit channel is the structured log, and the event log is a product surface.

The gate read D5 and asked whether a lifecycle-delivery failure owes an event-log row. The events standard governs the structured events the code emits. It leaves library, transport, and wire format to the project. Rimsky's logging library is `log/slog`, and its structured-event kind is the dotted literal at the emitting site. The event log is a different artifact: an operator-facing durable ledger whose 44 operational kinds are a closed enum at the protocol layer, covering node-run, claim, message, and auth transitions inside an instance. I read the standard as satisfied by a conformant emit at the site, so I fixed the emits. Whether the ledger should also cover control-plane peer-delivery health is a product-intent question I kicked back to the architect.

D44 — The log-kind repair is scoped to the two files the findings name.

`lib/runtime/lifecycle_fanout.go` and `lib/runtime/producer_verb_outbox.go` carried prose in the kind, which the events standard forbids: an event is a kind plus structured fields, and prose lives in a field. Each site now emits a dotted kind in the tree's own lower-case idiom — `lifecycle_fanout.scope_lookup_failed`, `lifecycle_fanout.advisory_locker_missing`, `lifecycle_fanout.peer_delivery_failed`, `lifecycle_fanout.peer_transaction_failed`, `producer_verb_outbox.dispatch_pass_failed`, `producer_verb_outbox.record_attempt_failed`, `producer_verb_outbox.delivery_retry_scheduled` — with the prose moved to a `consequence` field. D61 settles the case. Roughly two hundred other log calls across the tree carry prose messages too. Converting them all is a repo-wide idiom sweep no sprint authorizes. It would also widen the diff far past this gate's scope. I fixed the sites under review and left the rest.

D45 — A passed error keeps the completing terminal kind and carries its error class.

`decision:lineage-records-computation-only` closes the leaf-run terminal family to four and does not say which one a passed error takes. The run settles Fresh, and every downstream reader treats it as a success. Naming it `errored` would contradict the record's own `state` field and inflate a failure count. The record now carries `error_class` on the completing kind instead, so a reader tells a passed error apart from a plain success without a new kind. The decision stays as written; saying which kind a passed error takes is a commitment for a sprint, not a repair.

D46 — A quoted JSON document keeps its structure even when it carries a substitution directive.

The finding asked for the JSON branch to fire only on a string that parses as JSON and carries no directive. That reading turns `config: '{"dsn": "{{params.dsn}}"}'` into a JSON string. The publisher config walk resolves a directive only at a leaf it can reach, so the directive would stop resolving. `spec.RawJSON` now takes the JSON branch whenever the text parses as JSON, keeps a directive-bearing string that does not parse as a JSON string, and refuses a `{`-opening string that neither parses nor carries a directive. The refusal the previous round added still stands.

D47 — `rimsky compose run` warns per instance and still applies the manifest.

D41 left a rootless instance in a manifest silently reported as a success. Refusing the run and dropping the instance from the terminal wait each decide what the verb owes a manifest that mixes driven and sensor-driven instances. This sprint settles neither. The verb now emits `compose.rootless_instance_not_woken`, naming the instance key, the instance id, the template hash, and what follows from it. It applies the manifest as before. `WakeCreatedInstances` carries the loop so a test drives it without booting a stack.

D48 — The scenario harness takes the proxy CA root as a required argument.

`TestProxyReconnectAfterAgentRestart` started its second agent through `startAgent` without the fixture's `proxyCAPath`, so the agent dialled the proxy against the system roots and retried an untrusted certificate for as long as the run allowed. `ProxyCAPath` was an optional field on `agentStartOptions`, which let one caller omit the pin and still compile. The path is now a positional parameter of `startAgent`, so a caller that omits it fails to compile. Every caller passes the root its proxy published.

D49 — The host-agent stops repeating one unchanged connect error at the maximum backoff.

The agent logged a warning on every failed connect. A stuck agent therefore emitted a line every ten seconds forever. Each repeat carries what the operator already read, and it buries the lines that carry new information. `reconnectLogGate` now logs each failure while the backoff is still growing, says once that it is going quiet, then stays silent while the same error repeats at the maximum backoff. A changed error and a successful connect both make it speak again. The retry itself is unchanged: the agent re-reads its CA file on every attempt, so a proxy that re-publishes its root still recovers the connection. This site also moved the prose out of the kind — `hostagent.connect_failed` — for the reason D44 gives.

D50 — REVERSAL RULED: the upper-case log kinds return to the tree's lower form. PROMOTED: `.ok-planner/issues/2026-08-21-235520-structured-log-kind-case-convention.md`.

C17 read the new kinds as breaking the events standard's naming rule. The standard's scan does not treat a lower-case dotted literal as a kind at all: it reports a wholly lower-case tree as zero kinds and zero violations, and it says that verdict settles nothing about conformance. The kinds C17 named were therefore not violating kinds, and rewriting them in the upper form split an idiom 113 sites hold uniformly. The uniformity rule forbids that state by name — one idiom per job, repo-wide, and a sweep in the same change when the idiom improves. C27 holds.

Finishing the sweep is not this gate's work. The gate certifies this change, and the 113 sites sit outside it. A check that fails a lower-case kind would enforce repo-wide a convention the corpus never declares, and one that contradicts `decision:event-log-kind-enum` for the ledger's own kinds. Which channel the events standard governs here is the owner's question, and it is now in the intake.

The fixer returns the eleven upper-case kinds to the lower form, in `lib/runtime/producer_verb_outbox.go`, `lib/runtime/lifecycle_fanout.go`, `lib/runtime/hostagent/run.go`, `lib/control/controlapi/lifecycle_outbox.go`, and `cmd/rimsky/cli/compose/run.go`, along with the literal `cmd/rimsky/cli/compose/wake_created_instances_test.go` waits on. D44's repair stands untouched: the prose stays in a `consequence` field, and only the case returns.

The fixer's reasoning for the upper form, which the ruling above supersedes:

The uniformity rule asks for one idiom per job and a sweep in the same change. A sweep of two hundred sites is a repo-wide migration no sprint authorizes, and this gate is not the place to run it. The alternative is to write every new kind lowercase until that sweep happens. That rule lets no new kind ever comply: each future kind meets the same argument, and the standard governs nothing. A standard adopted after the fact governs what the project writes now, so the nine follow it and the backlog stays a migration for a sprint.

One cost follows. `/events` reads a kind by its upper-case shape, so its inventory reports these nine and passes over the two hundred. The standard already says an empty inventory settles nothing about conformance, so nine reported kinds state the tree's position more honestly than zero.

D51 — The sending node goes in the ledger's own node column, and `MessageSentPayload` keeps only the type.

`emitMessageSent` wrote `req.Sender` into `source_node_id`. For an instance send that value is `instance:<instance id>`, so a reader filtering by node id matched nothing. Two fields beside it, `target_node_id` and `params`, were never assigned at all.

The event row already carries what the reader wants. `concept:event-log` says an entry raised by work inside an instance names the instance and the node that work belongs to, and `EventListFilter` filters on both. A payload field duplicating the node column would sit inside a JSON blob that no filter reads. So the sending node now travels on `EventAppendInput.NodeID`, and `MessageSentPayload` keeps `type` alone. `target_node_id` and `params` are reserved with it. A message names an instance, and delivery decides which nodes wake, so no target node exists at send. The body is author-defined and already sits on the message row, and copying it into the ledger would put an opaque blob in the audit record.

`MessageReceivedPayload` keeps `source_node_id` and `target_node_id` for the opposite reason. Its row's node column holds the target, so the source has nowhere else to go. Its `params` is reserved, because nothing sets it.

`EnqueueMessage` gained a sibling rather than a field. `EnqueueMessageFromNode` takes the sending node and refuses a zero one. `EnqueueMessage` serves the operator and publisher sends, and it names no node. Putting the node on `persistence.EnqueueMessageRequest` would have left a field the store ignores, and persisting it would add a column and a migration this finding does not ask for.

Writing the node column put the emit under the event table's foreign key on `node_id`. The cascade-send tests invented a bare uuid for the sending node, so they began failing that key. They seed a real node row now, through `seedSendNode`, which is what the runtime always passes.

D52 — The two readers of `additionalProperties` answer different questions, so an absent key means opposite things.

`schemaAdmitsUndeclaredKeys` in the bundled-schema fitness check now reads an absent key as admitting undeclared keys. JSON Schema says so, and the check's own failure message already ruled that form out. All four bundled schemas write the key, so the check stays green and now fails a bundled schema that omits it.

`executorSchemaAllowsExtensions` at the graph layer keeps reading an absent key as closed. It asks whether registration may admit an undeclared node attribute, and `decision:expected-attributes-schema-closed` says registration rejects one. Failing closed there enforces the contract on a third-party executor that never wrote the key. The fitness check asks a different question: whether a bundled executor declared the closed contract. A schema that says nothing has not declared it.

D53 — The transition check classifies held by its reason rather than abstaining.

`TestEveryNodeRunTransitionNamesExactlyOneSignal` carried an empty arm for held while its messages claimed completeness. The three held sites split cleanly on the reason the same call already passes. `ReasonHandlerHeld` settles the dispatch while a claim is held, and it names the settling signal. `ReasonFanoutDispatched` suspends a parent while its children run, and it names none. `heldRule` asserts both. A held transition entering on any other reason now fails as unclassified, so a new reason for entering held forces the discriminator to grow.

D54 — Deleting a template row removed the ledger row its own deregister delivery reads.

`templatesImpl.DeleteByHash` opened with `DELETE FROM rimsky_lifecycle_idempotencies WHERE scope_kind = 'template'`, in both backends. The deregister handler calls it and stages the deregister in one transaction, so by the time the post-commit fan-out ran, the ledger row was gone. `deliverStagedLifecycleRow` reads a nil ledger for a closing event as "this peer never heard the opening event" and drops the delivery. No subscriber has ever heard `OnTemplateDeregistered`.

Both backends now leave those rows alone. The deregister delivery already deletes each peer's row once that peer has heard the event, so the cleanup still happens, in the one place that knows the peer was told.

D55 — The canary terminates through the verb rather than through the database.

The three canary tests in `lifecycle_subscriber_callback_test.go` marked an instance terminated by calling `Instances().MarkTerminated` and then deleted the instance. The package's fourth test, `TestCanary_TemplateRegistrationAndRunAPass`, terminates no instance, and this change does not touch it. That worked while the delete route fanned out synchronously. This sprint moved the fan-out off the delete route: the terminate verb closes and fans out the run scopes, and the reconciler poll delivers instance-terminated from the rows a terminated instance still carries. A database write is not that transition. The delete then removed the rows the poll reads.

Each of the three now posts `/v1/instances/{id}/terminate`, waits for the callback it is about to assert, and deletes afterwards. They pin the same contract through the shipped path. They no longer assert that a delete leaves an undelivered event deliverable, which is the promoted issue `instance-delete-drops-undelivered-lifecycle-events` and the owner's to settle.

D56 — A staged lifecycle delivery says when it is skipped.

`deliverStagedLifecycleRow` had two branches that discard a staged delivery and emit nothing: a peer already at the target state, and a closing event for a peer holding no ledger row. The second one hid D54 for the whole build. Both now emit `lifecycle.staged_skipped` with the peer, the scope, the event, and the reason. A delivered row emits `lifecycle.staged_delivered` with the ledger disposition it reached. The events standard asks for an event at every branch taken on external input and every boundary crossed, and these are both.

The scenario harness gave its control API `shared.SilentLogger{}`, so no control-plane line reached a failing scenario's output. It now takes the same `SCENARIO_DEBUG` switch the supervisor already had, through one `scenarioDebugLogger` both call.

D57 — The SQLite lock-path check derives its population from the struct.

The check listed three paths by hand, so a fourth lock path would go unchecked. It now reflects over `advisoryLockerImpl`, collects every field whose name ends in `LockPath`, adds the database file, and asserts the set is pairwise distinct and that every field carries a path. A field the constructor leaves unset fails by name. The Postgres sibling walks the AST because its keys are constants. This one reads the values the constructor produced, because distinctness is a property of those values.

D58 — `CLAUDE.md` named three citation tags where the config declares five.

The file said the config declares `@concept:`, `@story:`, and `@decision:`, and told the reader to delete any other `@tag:` shape. The config also declares `@subject:` and `@practice:`, and the plumbline cheatsheet tells the same session to write `@practice:` citations. The two always-in-context files contradicted each other, and this one destroyed valid citations. `CLAUDE.md` now names five, says where each resolves, and scopes the delete clause to a shape the config does not declare. `.claude/rules/rules.md` listed the same three in its move-an-artifact rule; it names five now, because a moved practice file breaks its citations the same way.

D59 — A staged delivery reports its success once, before the ledger branch splits.

D56 put the delivered line in the branch that advances the ledger, so the two events that close it — template-deregistered and instance-terminated — reported nothing on success. Those are the two events D54 was about, and an operator watching them saw a line when a delivery was skipped and silence when one landed. The line now sits directly after the dispatch returns, so every delivery a peer accepts writes one.

The field changed with the placement. `ledger_state` named the state the delivery wrote, and a closing delivery writes none. The line carries `ledger_disposition` now: the new state, or `deleted`. A deletion is not a state, and the ledger's own constraint does not admit one.

D60 — A delivery attempt reclaims the ledger row of a peer that is no longer registered.

D54 stopped `DeleteByHash` purging template-scope ledger rows, and that left a gap. The unknown-subscriber branch logged, dropped the staged row, and returned. A peer that left the deployment therefore kept a ledger row naming a template that no longer exists. No retention sweep covers that table, so the row stayed forever.

The branch now deletes that peer's row, which is what `fanOutInstancePeer`'s fallback already does when the registry misses. Nothing will deliver this peer's event, so nothing needs the row. A peer that never heard an opening event has no row, and the delete is a no-op there.

D61 — The eleven kinds are lower case again, and the two born in this gate take the same idiom.

D50's ruling reverts the case. The nine literals that had earlier names take them back, so `lifecycle_fanout.scope_lookup_failed` and its siblings read as they did before this run touched them. This gate wrote two kinds in the upper form that have no earlier name. They become `lifecycle.staged_skipped` and `lifecycle.staged_delivered`. Each is distinct in meaning from the `lifecycle.staged_delivery_dropped` already beside them, which names a row rimsky cannot parse rather than one it chose not to deliver. The compose wake test waits on the reverted literal.

A sweep for `"[A-Z][A-Z0-9]*\.[A-Z]` over `lib/`, `cmd/`, `test/`, and `tools/` returns nothing, so no upper-case kind survives. The kinds this run never touched keep their names, under the promoted issue.

D62 — The MCP tool lets the route refuse the caller, and renames the refusal afterwards.

The required-key guard ran in `Catalog.Invoke` before the inner request, so a caller holding only `mcp:read` learned that `message_send` needs an `idempotency_key` and got a 400 where the HTTP route answers 403. The two transports disagreed. An unauthorized caller also learned an argument requirement for a tool it may not call.

The guard is gone. `Invoke` now sends the inner request with no `Idempotency-Key` header, and the route decides. The permission gate refuses an unauthorized caller with its own 403. An authorized caller reaches the handler's own check, which refuses a missing key before it touches any state. Both come back through the one path that already returns the route's status.

`decision:idempotency-key-header-universal` still asks the tool to name the argument rather than the header. `Catalog.errorBody` renames exactly that refusal: the tool requires a key, the caller supplied none, the route answered 400, and the body carries the route's header sentence. Every other error passes through as the route wrote it.

A second case sat behind the first. `TestMcpTransportParity`'s mutation probe called `message_send` with no `idempotency_key`. Once the authorization case passed, the probe reached the route, and the route refused it. The tool has required that argument since stage 3, and the probe stands for a real caller, so it supplies one.

Authorizing inside the catalog was the other option. It would have meant a second copy of the gate's judgment — targets, dry-run, and the audit row it emits on a denial — in a package that cannot import the gate. Letting the gate answer keeps one authority and one audit trail.

D63 — The promoted issue states the counts I measured, not the ones the finding carried.

C32 gave the lower-case population as 124. I counted 107 distinct kinds at 118 sites, and the issue states those. The difference is what counts as an emit. A regex for `.Error(` also matches testify's `require.NoError(t, err, "persistence.Open")`, and six literals reached the higher figure that way. I counted the message literal of a `Debug`, `Info`, `Warn`, or `Error` call on a logger, dropped the testify receivers, and added the three kinds `warnFanOut` carries as an argument. Every one sits in product code. No test emits a kind.

Two claims in the issue needed the same treatment. The sweep paragraph said eight kinds are named in tests, and ten are. The history said one function held both forms three lines apart. No commit holds that state, so I could not confirm it. The issue now names what I can check: `lib/control/controlapi/lifecycle_outbox.go` emitted one form from each of two adjacent helpers.

The issue also no longer claims a wholly lower-case tree. Eight literals carry a capitalised segment, and the standard's scan would flag every one, so the "zero kinds, zero violations" reading holds for the 107 and not for the tree as a whole. Five of the eight name a Go function for error context rather than an event. Whether those are kinds is the same question the issue asks, so it names them and leaves them to the owner.

## Certification ledger

The loop ran seven rounds and ended at round 7, the first in which neither the fixer nor the architect edited a file: the reviewer reported DRY over a complete 450-file sweep, the alignment judge reported clean, the mechanical producers passed, the full `make test-all` regression exited 0 (6110 passes), and no row stands open. Repeats subtracted: 0. Reversals ruled: 1 (C17/C27).

| id | site | producer | round entered | outcome | repeats | rounds touched | note |
|---|---|---|---|---|---|---|---|
| C1 | completion report — fix rounds 3–4 / FX3-1 unrecorded | alignment | 1 | fixed 1 | 0 | 1 | round records added |
| C2 | `lib/runtime/lifecycle_fanout.go` / `lifecycle_reconciler.go` — delivery failure logs, no event (D5 veto) | alignment | 1 | promoted `issues/2026-08-21-225241-event-log-domain-for-peer-delivery-health.md` | 0 | 1 | emit-shape fixed (D43/D44); event-log domain promoted; D5 rewritten |
| C3 | `lib/runtime/producer_verb_outbox.go::DispatchOnce` — retry path emits no event | alignment | 1 | promoted `issues/2026-08-21-225241-event-log-domain-for-peer-delivery-health.md` | 0 | 1 | emit-shape fixed; same issue as C2 |
| C4 | D19 — lineage-write failure fails claim Open (veto) | alignment | 1 | refuted | 0 | 1 | barrier touches conflicting scopes only; architect re-ran the reproduction; D19 wording refined |
| C5 | D41 — compose run silently succeeds on a rootless template (veto) | alignment | 1 | fixed 1 | 0 | 1 | compose run names each undriven instance (D47) |
| C6 | F1 — delete drops undelivered lifecycle events | alignment | 1 | promoted `issues/2026-08-21-222949-instance-delete-drops-undelivered-lifecycle-events.md` | 0 | 1 | architect confirmed; no bounded window; D21 corrected |
| C7 | F2 — sub-graph caller keeps lineage records | alignment | 1 | fixed 1 | 0 | 1 | overturned; reading (a) stands, tree unchanged |
| C8 | F3 — deliver superseded event vs converge | alignment | 1 | fixed 1 | 0 | 1 | overturned; delivery stands per the at-least-once decision |
| C9 | F4 — expected-attributes contract shape | alignment | 1 | fixed 1 | 0 | 1 | overturned on all three questions; built readings stand |
| C10 | F5 — lifecycle-outbox retention window | alignment | 1 | promoted `issues/2026-08-21-222949-lifecycle-outbox-retention-narrows-at-least-once.md` | 0 | 1 | architect confirmed; zero default stands |
| C11 | F6 — read-only extensions on a closed schema | alignment | 1 | fixed 1 | 0 | 1 | overturned; reading (c) stands |
| C12 | `cmd/rimsky/cli/run.go:349,359` + `instances.go:585-598` — terminate→delete drops instance_terminated | code review | 1 | promoted `issues/2026-08-21-222949-instance-delete-drops-undelivered-lifecycle-events.md` | 0 | 0 | folded into C6's ruling |
| C13 | `lib/foundation/spec/rawjson.go:57-64` — refuses a whole-value `{{…}}` directive | code review | 1 | fixed 1 | 0 | 1 | directive strings stored as JSON strings (D46) |
| C14 | `lib/runtime/runner_error_policy.go:214-219` — pass records complete + blank error class | code review | 1 | fixed 1 | 0 | 1 | option (b): complete + ErrorClass carried (D45) |
| C15 | `lib/foundation/pki/ca.go:106-124` — LoadCA accepts a not-yet-valid CA | code review | 1 | fixed 1 | 0 | 1 | NotBefore arm added |
| C16 | `test/scenarios/host_agent_*` — `TestProxyReconnectAfterAgentRestart` loops forever: agent distrusts the restarted proxy's certificate | test suites | 1 | fixed 1 | 0 | 1 | harness pin required + log gate (D48/D49); 16 scenarios pass |
| C17 | nine new log kinds were lowercase against the standard's `SUBSYSTEM.NOUN.VERB` | alignment | 2 | reversal-ruled | 0 | 2 | the rename was wrong: a lowercase literal is not a kind to the standard's scan; upper case undeclared for the slog channel; D50 rewritten |
| C18 | `lib/runtime/message_delivery.go:101-102` — `source_node_id` carried `instance:<id>` | code review | 2 | fixed 3 | 0 | 1 | node travels on the ledger node column via `EnqueueMessageFromNode` (D51) |
| C19 | `events.proto::MessageSentPayload` — dead declared fields | code review | 2 | fixed 3 | 0 | 1 | three fields + `MessageReceivedPayload.params` reserved; proto-gen run (D51) |
| C20 | `test/plumbline/node_run_transition_signal_test.go:56` — `NodeStateHeld` silently exempt | code review | 2 | fixed 3 | 0 | 1 | Held classified by transition reason (D53) |
| C21 | `lib/services/test/plumbline/expected_attributes_schema_test.go:103-105` — absent `additionalProperties` waved through | code review | 2 | fixed 3 | 0 | 1 | absent key fails; polarity call recorded (D52) |
| C22 | `lib/runtime/hostagent/reconnect_log_gate_test.go:26-28` — watchdog-justified failure message | code review | 2 | fixed 3 | 0 | 1 | operator-facing reason; D49 swept too |
| C23 | `test/scenarios/canary` — lifecycle callback never arrives | test suites | 3 | fixed 4 | 0 | 1 | harness terminated via DB write, now via the verb (D55); real bug: `DeleteByHash` purged the ledger so `OnTemplateDeregistered` never delivered — fixed both backends (D54); skip/delivered events added (D56); canary 3.9s green |
| C24 | `lib/foundation/persistence/sqlite/advisory_lock_paths_test.go` — hand-listed population | code review | 3 | fixed 4 | 0 | 1 | population reflected from the struct (D57); mutation-checked |
| C25 | `CLAUDE.md:44` — three citation tags named, five declared | code review | 3 | fixed 4 | 0 | 1 | five tags named in both rule files; delete clause scoped (D58) |
| C26 | `lifecycle_outbox.go` — delivered event skipped the ledger-closing branch | code review | 4 | fixed 5 | 0 | 1 | emit moved before the branch, `ledger_disposition` field (D59) |
| C27 | eight `UPPER.DOTTED` kinds against 105 lowercase — two dialects | code review | 4 | fixed 5 | 0 | 2 | all reverted to the lowercase idiom; zero upper-dotted literals remain |
| C28 | `lifecycle_outbox.go` — unknown-subscriber branch orphaned the ledger row | code review | 4 | fixed 5 | 0 | 1 | ledger row reclaimed on the branch, with a test (D60) |
| C29 | D55 overstated the canary change | alignment | 4 | fixed 5 | 0 | 1 | D55 names the three |
| C30 | eleven sprint-new lowercase kinds in the new reconciler/outbox files | alignment | 4 | promoted `issues/2026-08-21-235520-structured-log-kind-case-convention.md` | 0 | 0 | lowercase stands per the C27 ruling; convention promoted |
| C31 | MCP `message_send` validated arguments before authorization | test suites | 5 | fixed 5 | 0 | 1 | pre-flight guard removed; the route authorizes first; parity test green (D62) |
| C32 | `issues/2026-08-21-235520-structured-log-kind-case-convention.md` — Problem stated the reverted split as present fact | code review | 5 | fixed 6 | 0 | 1 | restated from the tree with measured counts (D63) |
